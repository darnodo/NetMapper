package entity_test

import (
	"context"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T012, FR-002 and US1-1: two claim groups carrying the same strong identifier are one device,
// however many routes reached it, and the entity names both groups' claims as its evidence.
func TestSharedIdentifierIsOneEntity(t *testing.T) {
	sw := FakeOS("sw1", "S001")
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": sw, "10.0.0.11": sw})
	snap := snapshotOf(l, l.Crawl(doc))

	if keys := keysOf(l, snap); len(keys) != 1 {
		t.Fatalf("entities %v, want one device reached twice", keys)
	}
	n := l.Int(`
		SELECT count(DISTINCT c.observation_id)
		FROM entity e JOIN entity_claim ec ON ec.entity_id = e.id
		JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
		WHERE e.snapshot_id = $1`, snap)
	if n != 2 {
		t.Errorf("the entity cites %d observations, want both routes that reached it", n)
	}
}

// T013, FR-002 and US1-2: the grouping follows the chain and not only the direct pair. A shares a
// serial with C, C shares a chassis MAC with B, A and B share nothing at all.
func TestTransitiveChainIsOneEntity(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOSShaped("a", "S001", Shape{Serial: true}),
		"10.0.0.2": FakeOSShaped("c", "S001", Both),
		"10.0.0.3": FakeOSShaped("b", "S001", Shape{MAC: true}),
	})
	snap := snapshotOf(l, l.Crawl(doc))

	if keys := keysOf(l, snap); len(keys) != 1 {
		t.Fatalf("entities %v, want the chain closed into one", keys)
	}
	n := l.Int(`
		SELECT count(DISTINCT c.observation_id)
		FROM entity e JOIN entity_claim ec ON ec.entity_id = e.id
		JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
		WHERE e.snapshot_id = $1`, snap)
	if n != 3 {
		t.Errorf("the entity cites %d observations, want all three links of the chain", n)
	}
}

// T014, FR-008, US1-3 and SC-002: the observation of a task the crawl ended as a duplicate hangs off
// the entity of the task it duplicated, and the address it answered on stays readable there.
func TestDuplicateHangsOffTheDeviceItDuplicated(t *testing.T) {
	sw := FakeOS("sw1", "S001")
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": sw, "10.0.0.11": sw})
	snap := snapshotOf(l, l.Crawl(doc))

	if n := l.Int(`SELECT count(*) FROM observation WHERE snapshot_id = $1 AND parsed -> 0 ? 'duplicate_of_task'`, snap); n != 1 {
		t.Fatalf("%d duplicate observations, want the crawl to have caught one live", n)
	}
	targets := l.Strings(`
		SELECT jsonb_array_elements_text(attributes->'targets') FROM entity WHERE snapshot_id = $1 ORDER BY 1`, snap)
	if len(targets) != 2 || targets[0] != "10.0.0.1" || targets[1] != "10.0.0.11" {
		t.Errorf("targets %v, want both addresses the device answered on", targets)
	}
}

// T015, FR-003 and US1-4: a weak identifier never groups anything. Two chassis that happen to print
// the same hostname stay two devices.
func TestSharedHostnameDoesNotGroup(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOSShaped("sw", "S001", Both),
		"10.0.0.2": FakeOSShaped("sw", "S002", Both),
	})
	snap := snapshotOf(l, l.Crawl(doc))

	if keys := keysOf(l, snap); len(keys) != 2 {
		t.Errorf("entities %v, want two chassis that only share a name", keys)
	}
}

// T022, FR-003/FR-013 and research R15: grouped observations that disagree on a weak attribute
// resolve to the winning one's value, the observation the crawl did not mark a duplicate, and they
// do so on every recomputation.
func TestWeakAttributeTakesTheWinningObservation(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1":  FakeOSShaped("sw1", "S001", Both),
		"10.0.0.11": FakeOSShaped("sw1.lab.example", "S001", Both),
	})
	snap := snapshotOf(l, l.Crawl(doc))

	// What the winning observation calls the device, read from the collected zone alone.
	want := l.Strings(`
		SELECT c.value
		FROM identifier_claim c
		JOIN observation o ON o.snapshot_id = c.snapshot_id AND o.id = c.observation_id
		WHERE c.snapshot_id = $1 AND c.kind = 'hostname'
		  AND NOT (o.parsed -> 0 ? 'duplicate_of_task')`, snap)
	if len(want) != 1 {
		t.Fatalf("hostnames on the winning observation %v, want exactly one", want)
	}
	for i := range 2 {
		got := l.Strings(`SELECT attributes->>'hostname' FROM entity WHERE snapshot_id = $1`, snap)
		if len(got) != 1 || got[0] != want[0] {
			t.Fatalf("resolution %d: hostname %v, want the winning observation's %q", i, got, want[0])
		}
		resolve(l, snap)
	}
}

// T023, FR-019 and research R10: a replay writes a new parse generation over the same raw output.
// Resolution reads the active one only, so a superseded generation's claims never mix in.
func TestOnlyTheActiveParseGenerationIsRead(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Crawl(doc))
	ctx := context.Background()

	// A second generation reading the same device as something else entirely.
	var gen int64
	if _, err := l.DB.Exec(ctx, `UPDATE parse_generation SET active = false WHERE snapshot_id = $1`, snap); err != nil {
		t.Fatal(err)
	}
	if err := l.DB.QueryRow(ctx, `
		INSERT INTO parse_generation (snapshot_id, active) VALUES ($1, true) RETURNING id`, snap).Scan(&gen); err != nil {
		t.Fatal(err)
	}
	// On its own address: observation is unique on (snapshot, target, family, task), so 001's schema
	// cannot hold the same observation twice, once per generation.
	var obs int64
	if err := l.DB.QueryRow(ctx, `
		INSERT INTO observation (snapshot_id, parse_generation_id, task_id, collector_id, target,
		                         fact_family, status, parsed)
		SELECT $1, $2, o.task_id, o.collector_id, '10.0.0.77'::inet, 'identity', 'collected', o.parsed
		FROM observation o WHERE o.snapshot_id = $1 AND o.fact_family = 'identity' LIMIT 1
		RETURNING id`, snap, gen).Scan(&obs); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec(ctx, `
		INSERT INTO identifier_claim (snapshot_id, observation_id, kind, value, strength)
		VALUES ($1, $2, 'serial', 'S999', 'strong')`, snap, obs); err != nil {
		t.Fatal(err)
	}

	resolve(l, snap)
	if keys := keysOf(l, snap); len(keys) != 1 || keys[0] != "serial:S999" {
		t.Errorf("entities %v, want only the active generation's claim", keys)
	}
}
