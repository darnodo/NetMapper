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

// T071, FR-012/FR-024 and the edge case "kept apart by a split or a never-merge decision although
// they share a strong identifier": once an identifier is detached from a device it stops being a
// reason to call two claim groups one device, so they stay two entities under two distinct keys
// inside a single snapshot.
func TestSplitKeepsTwoEntitiesApartInOneSnapshot(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("a", "S001"),
		"10.0.0.2": nil,
	})
	delete(l.Net.Devices, Addr("10.0.0.2"))
	known := keysOf(l, snapshotOf(l, l.Crawl(doc)))
	if len(known) != 1 {
		t.Fatalf("entities %v, want the one device that was there", known)
	}

	// Another chassis, re-using that serial, answering beside the first.
	l.Net.Devices[Addr("10.0.0.2")] = FakeOSShaped("b", "S001", Shape{Serial: true})
	snap := snapshotOf(l, l.Crawl(doc))
	if got := keysOf(l, snap); len(got) != 1 || got[0] != known[0] {
		t.Fatalf("entities %v, want the shared serial to have made them one before the split", got)
	}

	decide(l, "split", []string{known[0]}, map[string]string{"kind": "serial", "value": "S001"})
	resolve(l, snap)

	got := keysOf(l, snap)
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("entities %v, want two under two distinct keys", got)
	}
	if got[0] != known[0] || got[1] != "serial:S001" {
		t.Errorf("entities %v, want %q kept and the detached serial naming a device of its own",
			got, known[0])
	}

	// And the decision is not undone by the resolution that applied it.
	resolve(l, snap)
	if again := keysOf(l, snap); len(again) != 2 || again[0] != got[0] || again[1] != got[1] {
		t.Errorf("entities %v then %v, want the split to hold", got, again)
	}
}

// T072, SC-006 and FR-012: a decision recorded once keeps applying to every later run of the
// perimeter, not only to a recomputation of the snapshot it was recorded against. Here a merge and a
// split are both still in force on a third run, with no further operator action.
func TestSplitAndMergeBothApplyOnALaterRun(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("a", "S001"),
		"10.0.0.2": FakeOS("b", "S002"),
		"10.0.0.3": nil,
	})
	delete(l.Net.Devices, Addr("10.0.0.3"))
	keys := keysOf(l, snapshotOf(l, l.Crawl(doc)))
	if len(keys) != 2 {
		t.Fatalf("entities %v, want the two chassis of the first run", keys)
	}

	// A third chassis re-using the first one's serial.
	l.Net.Devices[Addr("10.0.0.3")] = FakeOSShaped("c", "S001", Shape{Serial: true})
	second := snapshotOf(l, l.Crawl(doc))
	if got := keysOf(l, second); len(got) != 2 {
		t.Fatalf("entities %v, want the re-used serial to have merged into the first device", got)
	}

	decide(l, "merge", []string{keys[0], keys[1]}, nil)
	decide(l, "split", []string{keys[0]}, map[string]string{"kind": "serial", "value": "S001"})

	// A third run of the perimeter, nobody asked for anything.
	third := snapshotOf(l, l.Crawl(doc))

	got := keysOf(l, third)
	if len(got) != 2 {
		t.Fatalf("entities %v, want the merge to have made two chassis one and the split to have freed the third", got)
	}
	if got[0] != keys[0] {
		t.Errorf("first entity %q, want the merge to keep naming it %q", got[0], keys[0])
	}
	if got[1] != "serial:S001" {
		t.Errorf("second entity %q, want the detached serial to name its own device", got[1])
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, third); n != 0 {
		t.Errorf("%d collision findings, want none: both answers are already recorded", n)
	}
}

// A malformed decision must not be able to stop a perimeter resolving. Nothing but the owner may delete
// a decision, so a row the code cannot act on has to be skipped rather than fatal. The CHECK constraints
// reject these on the way in; this proves the reader survives one that predates them.
func TestMalformedDecisionsAreRejectedAndSurvivable(t *testing.T) {
	l, snap, keys := twoDevices(t)
	ctx := context.Background()

	// The database refuses them: an empty subjects array used to pass, because array_length('{}', 1) is
	// NULL and a NULL CHECK passes.
	for _, q := range []string{
		`INSERT INTO entity_decision (perimeter_name, kind, subjects, actor) VALUES ('lab', 'merge', '{}', 't')`,
		`INSERT INTO entity_decision (perimeter_name, kind, subjects, actor) VALUES ('lab', 'never_merge', '{}', 't')`,
		`INSERT INTO entity_decision (perimeter_name, kind, subjects, identifier, actor)
		 VALUES ('lab', 'split', '{}', '{"kind":"serial","value":"S"}', 't')`,
		`INSERT INTO entity_decision (perimeter_name, kind, subjects, actor)
		 VALUES ('lab', 'merge', ARRAY['a', NULL], 't')`,
	} {
		if _, err := l.DB.Exec(ctx, q); err == nil {
			t.Errorf("accepted a malformed decision: %s", q)
		}
	}

	// And one already in the table, written before the constraints existed, is skipped rather than fatal.
	if _, err := l.DB.Exec(ctx, `
		ALTER TABLE entity_decision DROP CONSTRAINT entity_decision_subject_count`); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec(ctx, `
		INSERT INTO entity_decision (perimeter_name, kind, subjects, actor)
		VALUES ('lab', 'merge', '{}', 'legacy')`); err != nil {
		t.Fatal(err)
	}
	decide(l, "merge", []string{keys[0], keys[1]}, nil)

	r := resolve(l, snap)
	if r.Entities != 1 {
		t.Errorf("%d entities, want the good decision applied and the malformed one ignored", r.Entities)
	}
}

// FR-013 and FR-011: two merges naming the same key both write the same entry, so the reduction has to
// read them in the order they were recorded. Reading them in map order gives a different answer per run.
func TestOverlappingMergesReduceInIDOrder(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("a", "S001"),
		"10.0.0.2": FakeOS("b", "S002"),
		"10.0.0.3": FakeOS("c", "S003"),
	})
	snap := snapshotOf(l, l.Crawl(doc))
	keys := keysOf(l, snap)
	if len(keys) != 3 {
		t.Fatalf("entities %v, want three devices", keys)
	}

	// Both merges name keys[2], so both write the same entry in the reduction. Only the third device
	// moves: the later decision governs, so it joins keys[1] and not keys[0].
	decide(l, "merge", []string{keys[0], keys[2]}, nil)
	decide(l, "merge", []string{keys[1], keys[2]}, nil)

	// Which entity absorbed the third device is the thing a map-ordered reduction flips on, so that is
	// what this asserts, over enough resolutions that a coin toss would show.
	cites := func(key string) int {
		return l.Int(`
			SELECT count(DISTINCT c.observation_id)
			FROM entity e JOIN entity_claim ec ON ec.entity_id = e.id
			JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
			WHERE e.snapshot_id = $1 AND e.device_key = $2`, snap, key)
	}
	for i := range 10 {
		resolve(l, snap)
		got := keysOf(l, snap)
		if len(got) != 2 || got[0] != keys[0] || got[1] != keys[1] {
			t.Fatalf("resolution %d: entities %v, want %q and %q", i, got, keys[0], keys[1])
		}
		if a, b := cites(keys[0]), cites(keys[1]); a != 1 || b != 2 {
			t.Fatalf("resolution %d: %q cites %d observations and %q cites %d, want 1 and 2: the later merge governs",
				i, keys[0], a, keys[1], b)
		}
	}
}

// FR-009 and FR-012: a never-merge names the keys the operator typed, and a later merge can rename one
// of them. The pair has to be canonicalised, or the never-merge silently retires.
func TestNeverMergeSurvivesAMergeOnItsSubject(t *testing.T) {
	// Two chassis agreeing on their MAC and disagreeing on their serial, plus a third device that will
	// be merged into one of them later.
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOSShaped("a", "S001", Both),
		"10.0.0.2": FakeOSShaped("b", "X001", Both),
		"10.0.0.3": nil,
	})
	delete(l.Net.Devices, Addr("10.0.0.3"))
	snap := snapshotOf(l, l.Crawl(doc))
	keys := keysOf(l, snap)
	if len(keys) != 2 {
		t.Fatalf("entities %v, want the collision to have left two", keys)
	}
	decide(l, "never_merge", []string{keys[0], keys[1]}, nil)
	resolve(l, snap)
	if got := keysOf(l, snap); len(got) != 2 {
		t.Fatalf("entities %v, want the never-merge holding before anything renames its subjects", got)
	}

	// The third device appears, and the operator merges it into one of the never-merge's subjects, which
	// renames that subject for every later resolution.
	l.Net.Devices[Addr("10.0.0.3")] = FakeOS("c", "S003")
	second := snapshotOf(l, l.Crawl(doc))
	var other string
	for _, k := range keysOf(l, second) {
		if k != keys[0] && k != keys[1] {
			other = k
		}
	}
	if other == "" {
		t.Fatalf("entities %v, want a third key to merge with", keysOf(l, second))
	}
	decide(l, "merge", []string{other, keys[0]}, nil)

	resolve(l, snap)
	if got := keysOf(l, snap); len(got) != 2 {
		t.Errorf("entities %v, want the never-merge still holding after its subject was renamed", got)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, snap); n != 0 {
		t.Errorf("%d collision findings, want the never-merge still suppressing it", n)
	}
}
