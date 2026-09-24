package entity_test

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/entity"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func env(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
}

// lab builds a network from address to device and returns the lab with a document seeding every
// address, so a test says what the network looks like and nothing else.
func lab(t *testing.T, devices map[string]*fake.Device) (*Lab, string) {
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{}}
	var targets []string
	for _, a := range slices.Sorted(maps.Keys(devices)) {
		net.Devices[Addr(a)] = devices[a]
		targets = append(targets, a)
	}
	l := NewLab(t, net)
	return l, strings.Replace(Doc, "targets: [10.0.0.1]", "targets: ["+strings.Join(targets, ", ")+"]", 1)
}

func snapshotOf(l *Lab, job int64) int64 {
	l.T.Helper()
	return int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job))
}

// keysOf lists the device keys of a snapshot's entity set, in order.
func keysOf(l *Lab, snap int64) []string {
	return l.Strings(`SELECT device_key FROM entity WHERE snapshot_id = $1 ORDER BY device_key`, snap)
}

// resolve runs the resolver as the engine does, which is how every test re-resolves.
func resolve(l *Lab, snap int64) entity.Result {
	l.T.Helper()
	r, err := entity.Resolve(context.Background(), l.Engine, snap)
	if err != nil {
		l.T.Fatal(err)
	}
	return r
}

// T017, US1-6: a snapshot that reached nothing resolves to zero entities and is still recorded as
// resolved, or the sweep would pick it up for ever (research R8).
func TestEmptySnapshotResolvesToNothing(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{}})
	snap := snapshotOf(l, l.Crawl(Doc))

	// The seed was attempted and nothing answered, so the snapshot holds an identity observation
	// that collected nothing. There is no claim in it to group.
	if n := l.Int(`SELECT count(*) FROM observation
	               WHERE snapshot_id = $1 AND fact_family = 'identity' AND status = 'collected'`, snap); n != 0 {
		t.Fatalf("%d collected identity observations, want a snapshot that reached nothing", n)
	}
	if n := l.Int(`SELECT count(*) FROM entity WHERE snapshot_id = $1`, snap); n != 0 {
		t.Errorf("%d entities, want none", n)
	}
	rows := l.Strings(`SELECT entities::text FROM resolution WHERE snapshot_id = $1`, snap)
	if len(rows) != 1 || rows[0] != "0" {
		t.Errorf("resolution rows %v, want exactly one saying 0 entities", rows)
	}
}

// T028, FR-001: an open snapshot is refused and nothing is written for it.
func TestOpenSnapshotIsRefused(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Start(doc)) // no collector, no engine: it stays open

	_, err := entity.Resolve(context.Background(), l.Engine, snap)
	if err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("error %v, want one saying the snapshot is not closed", err)
	}
	if n := l.Int(`SELECT count(*) FROM resolution WHERE snapshot_id = $1`, snap); n != 0 {
		t.Errorf("%d resolutions written for an open snapshot, want none", n)
	}
}

// T029, FR-025: the gate and the resolver are independent. A quarantined snapshot resolves like any
// other, and resolving gives no verdict of its own.
func TestVerdictDoesNotGateResolution(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Crawl(doc))

	// Force the verdict: what it says must change nothing here.
	if _, err := l.DB.Exec(context.Background(),
		`UPDATE snapshot_judgement SET classification = 'quarantined' WHERE snapshot_id = $1`, snap); err != nil {
		t.Fatal(err)
	}
	before := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1`, snap)

	if r := resolve(l, snap); r.Entities != 1 {
		t.Errorf("%d entities for a quarantined snapshot, want it resolved like any other", r.Entities)
	}
	if after := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1`, snap); after != before {
		t.Errorf("judgements %d then %d: resolving must write no verdict", before, after)
	}
}

// T024, FR-013 and SC-004: the same snapshot, the same generation, the same decisions, the same
// grouping. Entity ids are re-issued, so the comparison is on what a consumer reads.
func TestResolvingTwiceReproduces(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("sw1", "S001"),
		"10.0.0.2": FakeOS("sw2", "S002"),
	})
	snap := snapshotOf(l, l.Crawl(doc))

	read := func() []string {
		return l.Strings(`
			SELECT e.device_key || ' ' || e.weak || ' ' || coalesce(e.attributes->>'hostname', '') || ' ' ||
			       coalesce((SELECT string_agg(ec.identifier_claim_id::text, ',' ORDER BY ec.identifier_claim_id)
			                 FROM entity_claim ec WHERE ec.entity_id = e.id), '')
			FROM entity e WHERE e.snapshot_id = $1 ORDER BY e.device_key`, snap)
	}
	before := read()
	resolve(l, snap)
	if after := read(); !slices.Equal(before, after) {
		t.Errorf("entity set changed on a second resolution:\n before %v\n after  %v", before, after)
	}
}

// T030, FR-014/FR-020 and Principle II: resolving reads the collected zone and never writes to it.
func TestResolutionLeavesTheCollectedZoneAlone(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Crawl(doc))

	digest := func() []string {
		return l.Strings(`
			SELECT md5(string_agg(t, '|' ORDER BY t)) FROM (
				SELECT o.id || o.status || o.collected_at FROM observation o UNION ALL
				SELECT c.id || c.kind || c.value || c.strength FROM identifier_claim c UNION ALL
				SELECT r.observation_id || r.command || encode(r.hash, 'hex') FROM observation_raw r UNION ALL
				SELECT s.id || s.state || coalesce(s.closed_at::text, '') FROM snapshot s
			) AS rows(t)`)
	}
	before := digest()
	resolve(l, snap)
	if after := digest(); !slices.Equal(before, after) {
		t.Errorf("the collected zone changed: %v then %v", before, after)
	}
}

// T031, SC-008 and FR-020: losing every computed row costs a recomputation, never a re-crawl. The
// registry is rebuilt by replaying the snapshots in closing order, which is the order the sweep
// already uses.
func TestReplayRebuildsTheSameKeys(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("sw1", "S001"),
		"10.0.0.2": FakeOS("sw2", "S002"),
	})
	first := snapshotOf(l, l.Crawl(doc))
	second := snapshotOf(l, l.Crawl(doc))

	read := func() []string {
		return l.Strings(`SELECT snapshot_id || ' ' || device_key FROM entity ORDER BY snapshot_id, device_key`)
	}
	before := read()
	if len(before) != 4 {
		t.Fatalf("entities %v, want two per snapshot", before)
	}

	ctx := context.Background()
	for _, q := range []string{"DELETE FROM entity_claim", "DELETE FROM entity", "DELETE FROM resolution",
		"DELETE FROM device_identifier", "DELETE FROM device"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	resolve(l, first)
	resolve(l, second)

	if after := read(); !slices.Equal(before, after) {
		t.Errorf("replay gave different keys:\n before %v\n after  %v", before, after)
	}
}

// T031a, FR-006 and Principle I: an entity's freshness is the range its own evidence covers, not the
// moment it was resolved.
func TestEntityCarriesItsEvidenceTimes(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Crawl(doc))

	rows := l.Strings(`
		SELECT (e.first_seen = m.first) || ' ' || (e.last_seen = m.last) || ' ' || (e.last_seen < r.computed_at)
		FROM entity e
		JOIN resolution r ON r.snapshot_id = e.snapshot_id
		CROSS JOIN (SELECT min(collected_at) AS first, max(collected_at) AS last
		            FROM observation WHERE snapshot_id = $1 AND fact_family = 'identity') m
		WHERE e.snapshot_id = $1`, snap)
	if len(rows) != 1 || rows[0] != "true true true" {
		t.Errorf("first_seen/last_seen/before-resolution = %v, want the range of its own evidence", rows)
	}
}
