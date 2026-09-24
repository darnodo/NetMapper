package entity_test

import (
	"context"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// collide is two chassis that agree on their MAC and disagree on their serial, which is the shape
// FR-007 describes: one kind shared, another contradicted.
func collide(t *testing.T) (*Lab, int64) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOSShaped("a", "S001", Both),
		"10.0.0.2": FakeOSShaped("b", "X001", Both),
	})
	return l, snapshotOf(l, l.Crawl(doc))
}

// T043, FR-007, US2-1 and SC-005: a contradicted identity is reported, never guessed. Both devices
// stay, and one finding names them and what they disagree on.
func TestWithinSnapshotCollisionIsReported(t *testing.T) {
	l, snap := collide(t)

	if keys := keysOf(l, snap); len(keys) != 2 {
		t.Fatalf("entities %v, want the contradicting component left unmerged", keys)
	}
	rows := l.Strings(`
		SELECT domain || ' ' || category || ' ' || severity || ' ' || (detail->>'conflict') || ' ' || (detail->>'kind')
		FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, snap)
	if len(rows) != 1 || rows[0] != "data_quality identity_conflict warning within_snapshot serial" {
		t.Fatalf("findings %v, want one naming the kind they contradict each other on", rows)
	}
	values := l.Strings(`
		SELECT jsonb_array_elements_text(detail->'values') FROM finding
		WHERE snapshot_id = $1 AND category = 'identity_conflict' ORDER BY 1`, snap)
	if len(values) != 2 || values[0] != "S001" || values[1] != "X001" {
		t.Errorf("values %v, want both serials", values)
	}
	keys := l.Strings(`
		SELECT jsonb_array_elements_text(detail->'keys') FROM finding
		WHERE snapshot_id = $1 AND category = 'identity_conflict' ORDER BY 1`, snap)
	if len(keys) != 2 {
		t.Errorf("keys %v, want the finding to name both entities", keys)
	}
}

// T044, FR-005, US2-2 and SC-003: the finding leads back to the observations behind each side, and
// through them to the raw output they were read from.
func TestCollisionCitesItsEvidence(t *testing.T) {
	l, snap := collide(t)

	n := l.Int(`
		SELECT count(DISTINCT o.id)
		FROM finding f
		JOIN finding_evidence fe ON fe.finding_id = f.id
		JOIN observation o ON o.snapshot_id = fe.snapshot_id AND o.id = fe.observation_id
		JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
		WHERE f.snapshot_id = $1 AND f.category = 'identity_conflict' AND o.fact_family = 'identity'`, snap)
	if n != 2 {
		t.Errorf("the finding reaches %d identity observations with raw output, want both sides", n)
	}
}

// T045, FR-023 and research R6: a claim group set matching two known devices attaches to the lowest
// key and reports it. Re-attaching two keys is an operator's decision, never the system's guess.
func TestMatchingTwoDevicesIsReported(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("a", "S001"),
		"10.0.0.2": FakeOS("b", "S002"),
	})
	first := snapshotOf(l, l.Crawl(doc))
	known := keysOf(l, first)
	if len(known) != 2 {
		t.Fatalf("entities %v, want two devices known before the swap", known)
	}

	// One chassis now carrying the first device's serial and the second device's MAC.
	swapped := FakeOSShaped("a", "S001", Shape{Serial: true})
	swapped.CLI["display version"] += "MAC: " + MAC("S002") + "\n"
	delete(l.Net.Devices, Addr("10.0.0.2"))
	l.Net.Devices[Addr("10.0.0.1")] = swapped
	second := snapshotOf(l, l.Crawl(doc))

	keys := keysOf(l, second)
	if len(keys) != 1 || keys[0] != known[0] {
		t.Fatalf("entities %v, want it attached to the lowest known key %q", keys, known[0])
	}
	rows := l.Strings(`
		SELECT detail->>'conflict' FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, second)
	if len(rows) != 1 || rows[0] != "across_devices" {
		t.Fatalf("findings %v, want one across_devices collision", rows)
	}
	named := l.Strings(`
		SELECT jsonb_array_elements_text(detail->'keys') FROM finding
		WHERE snapshot_id = $1 AND category = 'identity_conflict' ORDER BY 1`, second)
	if len(named) != 2 {
		t.Errorf("keys %v, want the finding to name both devices it bridged", named)
	}
}

// T046, FR-015 and research R11: the collision findings are replaced with the entity set. A conflict
// that still holds is reported once however often the snapshot is resolved.
func TestCollisionFindingsAreReplaced(t *testing.T) {
	l, snap := collide(t)

	for range 3 {
		resolve(l, snap)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, snap); n != 1 {
		t.Errorf("%d collision findings after four resolutions, want one per live conflict", n)
	}
	if n := l.Int(`
		SELECT count(*) FROM finding_evidence fe
		WHERE NOT EXISTS (SELECT 1 FROM finding f WHERE f.id = fe.finding_id)`); n != 0 {
		t.Errorf("%d evidence rows left behind by a replaced finding", n)
	}
}

// T047, FR-015: a resolution replaces its own findings and nothing else. What the collector raised on
// the same snapshot is not its to remove.
func TestForeignFindingsSurviveAReResolution(t *testing.T) {
	l, snap := collide(t)
	ctx := context.Background()

	var id int64
	if err := l.DB.QueryRow(ctx, `
		INSERT INTO finding (snapshot_id, domain, category, severity, subject_ref, detail)
		VALUES ($1, 'data_quality', 'parse_failed', 'warning', '10.0.0.9', '{"family": "interfaces"}')
		RETURNING id`, snap).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec(ctx, `
		INSERT INTO finding_evidence (finding_id, snapshot_id, observation_id)
		SELECT $1, $2, o.id FROM observation o
		WHERE o.snapshot_id = $2 AND o.fact_family = 'identity' LIMIT 1`, id, snap); err != nil {
		t.Fatal(err)
	}

	resolve(l, snap)

	if n := l.Int(`SELECT count(*) FROM finding WHERE id = $1`, id); n != 1 {
		t.Errorf("the collector's finding was removed by a resolution")
	}
	if n := l.Int(`SELECT count(*) FROM finding_evidence WHERE finding_id = $1`, id); n != 1 {
		t.Errorf("%d evidence rows on the collector's finding, want it untouched", n)
	}
}
