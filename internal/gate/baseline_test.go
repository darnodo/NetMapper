package gate_test

import (
	"context"
	"net/netip"
	"strconv"
	"testing"

	"github.com/darnodo/NetMapper/internal/gate"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func env(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
}

func snapshotOf(l *Lab, job int64) int64 {
	l.T.Helper()
	return int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job))
}

// T013: two runs of the same perimeter are compared against each other even though every run posts
// the configuration document whole and therefore inserts a fresh perimeter row. The test asserts
// the two perimeter ids really do differ, so it fails against an implementation that matches on id.
func TestBaselineMatchesPerimeterByName(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}})
	ctx := context.Background()

	first := snapshotOf(l, l.Crawl(Doc))
	second := snapshotOf(l, l.Crawl(Doc))

	ids := l.Strings(`
		SELECT DISTINCT (j.parameters->>'perimeter_id')
		FROM job j WHERE j.snapshot_id IN ($1, $2) ORDER BY 1`, first, second)
	if len(ids) != 2 {
		t.Fatalf("the two runs share a perimeter id (%v): this test cannot prove anything", ids)
	}

	if _, err := gate.Judge(ctx, l.Engine, first); err != nil {
		t.Fatal(err)
	}
	j, err := gate.Judge(ctx, l.Engine, second)
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineID == nil {
		t.Fatal("second snapshot found no baseline: perimeters are being matched by id, not by name")
	}
	if *j.BaselineID != first {
		t.Errorf("baseline %d, want %d", *j.BaselineID, first)
	}
	if j.Classification != gate.Published || j.CarriedOver != 2 || j.BaselineCount != 2 {
		t.Errorf("got %s %d/%d, want published 2/2", j.Classification, j.CarriedOver, j.BaselineCount)
	}

	stored := l.Strings(`
		SELECT classification || ' ' || coverage::float8 || ' ' || baseline_snapshot_id ||
		       ' ' || gate_version || ' ' || (thresholds->>'source')
		FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, second)
	if len(stored) != 1 {
		t.Fatalf("%d active judgements stored, want 1", len(stored))
	}
	if want := "published 1 " + itoa(first) + " 1 default"; stored[0] != want {
		t.Errorf("stored %q, want %q", stored[0], want)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A perimeter's first snapshot has nothing to compare against and is published with no coverage
// figure, rather than quarantined for a regression it cannot have had (FR-004).
func TestFirstSnapshotHasNoBaseline(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})

	j, err := gate.Judge(context.Background(), l.Engine, snapshotOf(l, l.Crawl(Doc)))
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineID != nil || j.Coverage != nil {
		t.Errorf("baseline %v coverage %v, want both unset", j.BaselineID, j.Coverage)
	}
	if j.Classification != gate.Published {
		t.Errorf("classification %s, want published", j.Classification)
	}
	if j.Breakdown["no_baseline"] != true {
		t.Errorf("breakdown %v, want no_baseline true", j.Breakdown)
	}
}

// T014, FR-015/FR-012: a snapshot is measured against the run before it, not against whichever run
// was judged most recently. Re-judging an old snapshot after newer ones closed must therefore find
// the same baseline it originally had, or a verdict would depend on the day it was computed.
func TestBaselineIsThePrecedingSnapshot(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})
	ctx := context.Background()

	first := snapshotOf(l, l.Crawl(Doc))
	second := snapshotOf(l, l.Crawl(Doc))
	third := snapshotOf(l, l.Crawl(Doc))

	baselineOf := func(snap int64) int64 {
		return int64(l.Int(`SELECT baseline_snapshot_id FROM snapshot_judgement
		                    WHERE snapshot_id = $1 AND active`, snap))
	}
	if got := baselineOf(second); got != first {
		t.Errorf("baseline of the middle snapshot is %d, want %d", got, first)
	}
	if got := baselineOf(third); got != second {
		t.Errorf("baseline of the newest snapshot is %d, want %d", got, second)
	}

	// The middle snapshot is judged again now that a newer one exists.
	j, err := gate.Judge(ctx, l.Engine, second)
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineID == nil || *j.BaselineID != first {
		t.Errorf("re-judged baseline %v, want %d: baselines are chosen by closing order, not by judging order",
			j.BaselineID, first)
	}
}
