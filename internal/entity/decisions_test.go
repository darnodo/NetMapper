package entity_test

import (
	"context"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// decide records one decision as the operator does, which is the only role allowed to.
func decide(l *Lab, kind string, subjects []string, identifier any) int64 {
	l.T.Helper()
	var id int64
	err := l.Operator.QueryRow(context.Background(), `
		INSERT INTO entity_decision (perimeter_name, kind, subjects, identifier, actor, note)
		VALUES ('lab', $1, $2, $3, 'tester', 'test') RETURNING id`, kind, subjects, identifier).Scan(&id)
	if err != nil {
		l.T.Fatal(err)
	}
	return id
}

// twoDevices is two chassis with nothing in common, which is what a merge has to bring together.
func twoDevices(t *testing.T) (*Lab, int64, []string) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("a", "S001"),
		"10.0.0.2": FakeOS("b", "S002"),
	})
	snap := snapshotOf(l, l.Crawl(doc))
	keys := keysOf(l, snap)
	if len(keys) != 2 {
		t.Fatalf("entities %v, want two devices to decide about", keys)
	}
	return l, snap, keys
}

// T052, US3-1 and FR-009: a merge makes two keys one device, and the entity carries the first key the
// operator named. Nothing was edited in place: the decision is replayed.
func TestMergeMakesOneEntity(t *testing.T) {
	l, snap, keys := twoDevices(t)
	decide(l, "merge", []string{keys[0], keys[1]}, nil)

	resolve(l, snap)
	got := keysOf(l, snap)
	if len(got) != 1 || got[0] != keys[0] {
		t.Fatalf("entities %v, want one carrying %q", got, keys[0])
	}
	n := l.Int(`
		SELECT count(DISTINCT c.observation_id)
		FROM entity e JOIN entity_claim ec ON ec.entity_id = e.id
		JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
		WHERE e.snapshot_id = $1`, snap)
	if n != 2 {
		t.Errorf("the merged entity cites %d observations, want both devices' evidence", n)
	}
}

// T053, US3-2, FR-009 and FR-024: a never-merge keeps two devices apart under two distinct keys, and
// stops the collision being reported: the answer is already recorded.
func TestNeverMergeKeepsThemApartAndQuiet(t *testing.T) {
	l, snap := collide(t)
	keys := keysOf(l, snap)
	if len(keys) != 2 {
		t.Fatalf("entities %v, want the collision to have left two", keys)
	}
	decide(l, "never_merge", []string{keys[0], keys[1]}, nil)

	resolve(l, snap)
	got := keysOf(l, snap)
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("entities %v, want two under two distinct keys", got)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, snap); n != 0 {
		t.Errorf("%d collision findings, want none once the operator has answered", n)
	}
}

// T054, US3-3, FR-012, FR-024 and SC-006: a split detaches an identifier from a device, and the next
// resolution where that identifier is observed again does not put them back together.
func TestSplitSurvivesTheNextResolution(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("a", "S001")})
	first := snapshotOf(l, l.Crawl(doc))
	known := keysOf(l, first)

	// Another chassis that happens to carry the same serial and nothing else.
	l.Net.Devices[Addr("10.0.0.1")] = FakeOSShaped("b", "S001", Shape{Serial: true})
	second := snapshotOf(l, l.Crawl(doc))
	if got := keysOf(l, second); len(got) != 1 || got[0] != known[0] {
		t.Fatalf("entities %v, want the re-used serial to have matched %q before the split", got, known[0])
	}

	decide(l, "split", []string{known[0]}, map[string]string{"kind": "serial", "value": "S001"})
	resolve(l, second)

	got := keysOf(l, second)
	if len(got) != 1 || got[0] == known[0] {
		t.Fatalf("entities %v, want the detached serial to name a device of its own, not %q", got, known[0])
	}

	// And it stays detached: resolving again does not undo it.
	resolve(l, second)
	if again := keysOf(l, second); len(again) != 1 || again[0] != got[0] {
		t.Errorf("entities %v then %v, want the split to hold", got, again)
	}
}

// T055, US3-4 and FR-017: a decision recorded after a snapshot was resolved applies when it is
// resolved again, and the resolution row names the decisions it read.
func TestLateDecisionAppliesOnTheNextResolution(t *testing.T) {
	l, snap, keys := twoDevices(t)
	if n := l.Int(`SELECT decisions_applied FROM resolution WHERE snapshot_id = $1`, snap); n != 0 {
		t.Errorf("decisions_applied %d before any decision, want 0", n)
	}
	id := decide(l, "merge", []string{keys[0], keys[1]}, nil)

	// The set still describes the world before the decision until someone asks for it again.
	if got := keysOf(l, snap); len(got) != 2 {
		t.Errorf("entities %v, want recording a decision to change no entity on its own", got)
	}

	resolve(l, snap)
	if got := keysOf(l, snap); len(got) != 1 {
		t.Errorf("entities %v, want the decision applied", got)
	}
	if n := l.Int(`SELECT decisions_applied FROM resolution WHERE snapshot_id = $1`, snap); int64(n) != id {
		t.Errorf("decisions_applied %d, want the decision it read: %d", n, id)
	}
}

// T056, US3-5: a decision whose subjects appear in no snapshot being resolved is skipped without
// failing it, and stays available for the day they do appear.
func TestAbsentSubjectIsSkipped(t *testing.T) {
	l, _, keys := twoDevices(t)

	// A second run reaching only one of the two devices.
	delete(l.Net.Devices, Addr("10.0.0.2"))
	second := snapshotOf(l, l.Crawl(Doc))
	decide(l, "merge", []string{keys[0], keys[1]}, nil)

	r := resolve(l, second)
	if r.Entities != 1 {
		t.Errorf("%d entities, want the run that reached one device to resolve to one", r.Entities)
	}
	if n := l.Int(`SELECT count(*) FROM entity_decision`); n != 1 {
		t.Errorf("%d decisions, want the skipped one still recorded", n)
	}
}

// T057, FR-011: on the same pair of subjects the last decision recorded governs, and every earlier
// one stays readable so the history of the disagreement survives.
func TestLastDecisionOnAPairGoverns(t *testing.T) {
	l, snap, keys := twoDevices(t)

	decide(l, "merge", []string{keys[0], keys[1]}, nil)
	decide(l, "never_merge", []string{keys[0], keys[1]}, nil)
	resolve(l, snap)
	if got := keysOf(l, snap); len(got) != 2 {
		t.Errorf("entities %v, want the never-merge recorded last to govern", got)
	}

	decide(l, "merge", []string{keys[0], keys[1]}, nil)
	resolve(l, snap)
	if got := keysOf(l, snap); len(got) != 1 {
		t.Errorf("entities %v, want the merge recorded last to govern", got)
	}

	if n := l.Int(`SELECT count(*) FROM entity_decision`); n != 3 {
		t.Errorf("%d decisions, want every one of them still readable", n)
	}
}
