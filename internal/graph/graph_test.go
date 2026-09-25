package graph_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/darnodo/NetMapper/internal/graph"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// snap is the snapshot of the lab's single crawl.
func snap(l *Lab) int64 {
	return int64(l.Int(`SELECT id FROM snapshot ORDER BY id LIMIT 1`))
}

// dump is everything a projection wrote, as sorted text: what SC-005 compares between two runs.
func dump(l *Lab) []string {
	return l.Strings(`
		SELECT 'if ' || e.device_key || ' ' || i.canonical_name || ' ' || i.source || ' ' ||
		       coalesce(i.oper_state, '-') || ' ' || coalesce(i.mac, '-')
		  FROM interface i JOIN entity e ON e.id = i.entity_id
		UNION ALL
		SELECT 'alias ' || e.device_key || ' ' || i.canonical_name || ' ' || a.spelling || ' ' || a.source
		  FROM interface_alias a JOIN interface i ON i.id = a.interface_id
		  JOIN entity e ON e.id = i.entity_id
		UNION ALL
		SELECT 'edge ' || g.name || ' ' || g.confidence || ' ' || g.attributes::text FROM edge g
		UNION ALL
		SELECT 'evidence ' || g.name || ' ' || ev.side FROM edge g
		  JOIN edge_evidence ev ON ev.edge_id = g.id
		ORDER BY 1`)
}

// T028, edge case, FR-018: a snapshot with an entity set but no interface and no neighbour
// observation is recorded as projected, not left pending.
//
// The spec's edge case says it "projects to nothing", and that is true of everything those two
// families feed: no interface, no link. It is not true of the has_address edges, which FR-013 builds
// from the entity set and not from a fact family, so a device that answered still gets one. Recorded
// as divergence 1 in quickstart.md.
func TestEntitySetWithNothingToProject(t *testing.T) {
	mute := FakeOS("sw1", "S001")
	mute.Stall = map[string]bool{"display interfaces": true, "display neighbours": true}
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): mute})

	if n := l.Int(`SELECT count(*) FROM entity`); n != 1 {
		t.Fatalf("%d entities, want one", n)
	}
	if n := l.Int(`SELECT count(*) FROM interface`); n != 0 {
		t.Errorf("%d interfaces, want none", n)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'`); n != 0 {
		t.Errorf("%d links, want none", n)
	}
	got := l.Strings(`SELECT interfaces || ' ' || edges FROM projection WHERE snapshot_id = $1`, snap(l))
	if len(got) != 1 || got[0] != "0 1" {
		t.Errorf("projection row %v, want one saying 0 interfaces and the single has_address edge", got)
	}
}

// T029, edge case, FR-001: a closed snapshot with no entity set is not projected, and asking for it
// by name says so rather than failing as a runtime error.
func TestSnapshotWithoutEntitySet(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})
	ctx := context.Background()
	for _, q := range []string{"DELETE FROM projection", "DELETE FROM entity", "DELETE FROM resolution"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); !errors.Is(err, graph.ErrNotResolved) {
		t.Errorf("err %v, want ErrNotResolved", err)
	}
	if n := l.Int(`SELECT count(*) FROM projection`); n != 0 {
		t.Error("a snapshot with no entity set was projected anyway")
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, 99999); !errors.Is(err, graph.ErrNotFound) {
		t.Errorf("err %v, want ErrNotFound", err)
	}
}

// T030, edge case, FR-018: projecting again produces a complete new set, with no remnant of the old
// one and never two sets.
func TestReprojectReplaces(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})
	ctx := context.Background()
	before := dump(l)

	// A parser fix that drops a port. There is no re-parse path yet, so the test edits the parsed
	// rows directly; the projector cannot tell the difference, which is the point of FR-023.
	if _, err := l.DB.Exec(ctx, `
		UPDATE observation SET parsed = parsed - 1
		WHERE fact_family = 'interfaces' AND status = 'collected'`); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}

	after := dump(l)
	if slices.Equal(before, after) {
		t.Fatal("the new set is identical to the old one: the fixture changed nothing")
	}
	if n := l.Int(`SELECT count(*) FROM interface WHERE canonical_name = 'port2'`); n != 0 {
		t.Error("port2 survived a projection that no longer produces it")
	}
	if n := l.Int(`SELECT count(*) FROM projection WHERE snapshot_id = $1`, snap(l)); n != 1 {
		t.Errorf("%d projection rows, want exactly one", n)
	}
	if n := l.Int(`SELECT interfaces FROM projection WHERE snapshot_id = $1`, snap(l)); n != 1 {
		t.Errorf("the projection row says %d interfaces, want 1", n)
	}
}

// T031, edge case, research R12: the same snapshot projected twice at once ends with one set, never
// two half-written ones.
func TestConcurrentProjections(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
	one := dump(l)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = graph.Project(context.Background(), l.DB, l.Registry, snap(l))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Errorf("concurrent projection: %v", err)
		}
	}
	if got := dump(l); !slices.Equal(got, one) {
		t.Errorf("after four concurrent projections the set differs:\n got %v\nwant %v", got, one)
	}
}

// T032, FR-017, SC-005: the same snapshot, the same parse generation and the same entity set produce
// the same interfaces and the same edges every time.
func TestReproducible(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p2 unmanaged - - -"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
	want := dump(l)
	if len(want) == 0 {
		t.Fatal("the fixture projected nothing, so this proves nothing")
	}
	for i := range 3 {
		if _, err := graph.Project(context.Background(), l.DB, l.Registry, snap(l)); err != nil {
			t.Fatal(err)
		}
		if got := dump(l); !slices.Equal(got, want) {
			t.Fatalf("run %d differs:\n got %v\nwant %v", i+2, got, want)
		}
	}
}

// T033, FR-019, research R13: projection reads only the observations of the active parse generation.
func TestActiveParseGenerationOnly(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})
	ctx := context.Background()
	if n := l.Int(`SELECT count(*) FROM interface`); n == 0 {
		t.Fatal("nothing was projected to begin with")
	}
	// A new generation with no observation of its own: everything collected belongs to the old one.
	if _, err := l.DB.Exec(ctx, `UPDATE parse_generation SET active = false WHERE snapshot_id = $1`, snap(l)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec(ctx, `
		INSERT INTO parse_generation (snapshot_id, active) VALUES ($1, true)`, snap(l)); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT count(*) FROM interface`); n != 0 {
		t.Errorf("%d interfaces, want none: an inactive generation was read", n)
	}
}

// T037, FR-020, Principle II: projection modifies nothing in the collected zone, and no entity,
// registry row or operator decision either.
func TestProjectionWritesNothingItDoesNotOwn(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
	sum := func() []string {
		return l.Strings(`
			SELECT 'observation ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || status || fact_family || coalesce(parsed::text, '') AS x FROM observation) o
			UNION ALL
			SELECT 'raw ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT observation_id || command || encode(hash, 'hex') AS x FROM observation_raw) r
			UNION ALL
			SELECT 'claim ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || kind || value || strength AS x FROM identifier_claim) c
			UNION ALL
			SELECT 'entity ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || device_key || weak::text || attributes::text AS x FROM entity) e
			UNION ALL
			SELECT 'device ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT perimeter_name || key || weak::text AS x FROM device) d
			UNION ALL
			SELECT 'decision ' || count(*) FROM entity_decision
			ORDER BY 1`)
	}
	before := sum()
	if _, err := graph.Project(context.Background(), l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if after := sum(); !slices.Equal(before, after) {
		t.Errorf("projection changed what it does not own:\nbefore %v\nafter  %v", before, after)
	}
}

// T065, US3-3, FR-013: each address a device entity answered on is recorded as its own kind of edge,
// so a later consumer can ask what answers at an address without reading identifier claims.
func TestAddressEdges(t *testing.T) {
	// The neighbour entry points at the box's own second address, which is how discovery reaches it:
	// the crawl finds it, identifies the same serial, ends that task a duplicate, and resolution
	// records both addresses on the one entity. The self-report produces no cable, because its two
	// endpoint references are equal.
	sw1 := FakeOS("sw1", "S001", "p1 self 10.0.0.2 p1 "+MAC("S001"))
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): sw1,
		Addr("10.0.0.2"): sw1,
	})
	if n := l.Int(`SELECT count(*) FROM entity`); n != 1 {
		t.Fatalf("%d entities, want one", n)
	}
	if n := l.Int(`SELECT jsonb_array_length(attributes -> 'targets') FROM entity`); n != 2 {
		t.Fatalf("the entity carries %d addresses, want the two it answered on", n)
	}
	got := l.Strings(`SELECT from_ref || ' ' || to_ref || ' ' || confidence
		FROM edge WHERE type = 'has_address' ORDER BY 1`)
	want := []string{
		"dev:" + key("S001") + " addr:10.0.0.1 direct",
		"dev:" + key("S001") + " addr:10.0.0.2 direct",
	}
	if !slices.Equal(got, want) {
		t.Errorf("address edges %v, want %v", got, want)
	}
	// Each one is cited by the identity observation collected on that very address.
	ev := l.Strings(`SELECT g.to_ref || ' ' || host(o.target) || ' ' || o.fact_family
		FROM edge g JOIN edge_evidence ev ON ev.edge_id = g.id
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		WHERE g.type = 'has_address' ORDER BY 1`)
	wantEv := []string{"addr:10.0.0.1 10.0.0.1 identity", "addr:10.0.0.2 10.0.0.2 identity"}
	if !slices.Equal(ev, wantEv) {
		t.Errorf("address evidence %v, want %v", ev, wantEv)
	}
}

// T071, FR-018, FR-020, research R9: re-projecting replaces its own disagreement findings, leaving
// none duplicated and none stale, and leaves alone the findings the collector and the resolver raised.
func TestFindingReplacement(t *testing.T) {
	broken := FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002"))
	broken.CLI["display interfaces"] = "this is not an interface table\n"
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): broken,
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p2 "+MAC("S001")),
	})
	ctx := context.Background()

	others := l.Strings(`SELECT category || ' ' || subject_ref FROM finding
		WHERE category <> 'link_disagreement' ORDER BY 1`)
	if len(others) == 0 {
		t.Fatal("the fixture raised no finding from anything but the projector, so this proves nothing")
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category = 'link_disagreement'`); n != 1 {
		t.Fatalf("%d disagreements after the first projection, want one", n)
	}

	for range 3 {
		if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
			t.Fatal(err)
		}
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category = 'link_disagreement'`); n != 1 {
		t.Errorf("%d disagreements after four projections, want still one", n)
	}
	if n := l.Int(`SELECT count(*) FROM finding f
		LEFT JOIN finding_evidence fe ON fe.finding_id = f.id
		WHERE f.category = 'link_disagreement' AND fe.finding_id IS NULL`); n != 0 {
		t.Error("a disagreement finding lost its evidence when it was replaced")
	}
	after := l.Strings(`SELECT category || ' ' || subject_ref FROM finding
		WHERE category <> 'link_disagreement' ORDER BY 1`)
	if !slices.Equal(others, after) {
		t.Errorf("findings raised elsewhere changed:\nbefore %v\nafter  %v", others, after)
	}
}

// T072, SC-008, FR-023: deleting every projected row and recomputing restores the same interfaces and
// the same edges, from the stored observations and the entity set alone, with no device contacted.
// This is what proves Principle II for this feature, so it runs the whole sequence.
func TestWipeAndRecompute(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p2 unmanaged 10.0.0.9 eth0 aa:bb:cc:99:99:99"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
	ctx := context.Background()
	want := dump(l)
	if len(want) == 0 {
		t.Fatal("the fixture projected nothing, so this proves nothing")
	}
	opens := len(l.Net.Opens())

	for _, q := range []string{
		"DELETE FROM edge_evidence", "DELETE FROM edge",
		"DELETE FROM interface_alias", "DELETE FROM interface_evidence", "DELETE FROM interface",
		"DELETE FROM projection",
	} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if got := dump(l); len(got) != 0 {
		t.Fatalf("the wipe left %v", got)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if got := dump(l); !slices.Equal(got, want) {
		t.Errorf("after the wipe and recompute:\n got %v\nwant %v", got, want)
	}
	if n := len(l.Net.Opens()); n != opens {
		t.Errorf("%d sessions opened during a recomputation, want none beyond the %d of the crawl", n-opens, opens)
	}
}

// T085, FR-008: an edge records when its own evidence was collected, not when the entity it hangs off
// was. A device reached on two addresses answered on each at its own moment, and each `has_address`
// edge cites exactly one of those observations.
func TestAddressEdgeCarriesItsOwnEvidenceRange(t *testing.T) {
	sw1 := FakeOS("sw1", "S001", "p1 self 10.0.0.2 p1 "+MAC("S001"))
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw1, Addr("10.0.0.2"): sw1})
	if n := l.Int(`SELECT jsonb_array_length(attributes -> 'targets') FROM entity`); n != 2 {
		t.Fatalf("the entity carries %d addresses, want two for this to mean anything", n)
	}
	// The two identity observations must not have landed on the same instant, or the assertion below
	// would hold even with the entity-wide range this test exists to reject.
	if n := l.Int(`SELECT count(DISTINCT collected_at) FROM observation WHERE fact_family = 'identity'`); n != 2 {
		t.Fatalf("%d distinct collection times, want the two addresses answered at different moments", n)
	}

	got := l.Strings(`SELECT g.to_ref || ' ' || (g.first_seen = o.collected_at) || ' ' ||
		(g.last_seen = o.collected_at)
		FROM edge g JOIN edge_evidence ev ON ev.edge_id = g.id
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		WHERE g.type = 'has_address' ORDER BY 1`)
	want := []string{"addr:10.0.0.1 true true", "addr:10.0.0.2 true true"}
	if !slices.Equal(got, want) {
		t.Errorf("address edge ranges %v, want each to match the observation it cites, %v", got, want)
	}
}

// T086, spec Assumptions: a snapshot the coverage gate quarantined is projected like any other.
// Whether to trust the result stays the consumer's call, the way resolution already treats it.
//
// The behaviour holds today only because the gate records its verdict in `snapshot_judgement` and
// leaves `snapshot.state` at `closed`, which is what the projector's sweep selects on. Nothing else
// pins that, so a later change moving a verdict onto the snapshot row would silently stop projecting
// quarantined snapshots. This is the test that would catch it.
func TestQuarantinedSnapshotIsProjected(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	// A baseline of two devices, then a run that reaches one: 50% coverage, quarantined under the
	// default thresholds, the same shape internal/gate builds its own quarantine fixture from.
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}}
	l := NewLab(t, net)
	l.Crawl(Doc)
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001")
	l.Crawl(Doc)

	second := int64(l.Int(`SELECT id FROM snapshot ORDER BY id DESC LIMIT 1`))
	verdict := l.Strings(`SELECT classification FROM snapshot_judgement
		WHERE snapshot_id = $1 AND active`, second)
	if len(verdict) != 1 || verdict[0] != "quarantined" {
		t.Fatalf("verdict %v, want quarantined for this test to mean anything", verdict)
	}

	if n := l.Int(`SELECT count(*) FROM projection p JOIN resolution r USING (snapshot_id)
		WHERE p.snapshot_id = $1 AND p.resolution_at = r.computed_at`, second); n != 1 {
		t.Error("a quarantined snapshot was not projected, or its projection is stale")
	}
	if n := l.Int(`SELECT count(*) FROM interface WHERE snapshot_id = $1`, second); n == 0 {
		t.Error("the quarantined snapshot has no interfaces")
	}
	// And the verdict changed nothing about what was projected: the snapshot is labelled, not locked.
	if n := l.Int(`SELECT count(*) FROM edge WHERE snapshot_id = $1 AND type = 'has_address'`, second); n == 0 {
		t.Error("the quarantined snapshot has no address edges")
	}
}
