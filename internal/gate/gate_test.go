package gate_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/darnodo/NetMapper/internal/gate"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func oneDevice(t *testing.T) *Lab {
	env(t)
	return NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})
}

// T017, FR-009/FR-012: judging again writes a new active verdict, keeps the superseded one
// readable, and reproduces the same figures.
func TestRejudgeSupersedesAndReproduces(t *testing.T) {
	l := oneDevice(t)
	snap := snapshotOf(l, l.Crawl(Doc))

	before := l.Strings(`
		SELECT classification || ' ' || baseline_devices || ' ' || carried_over
		FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, snap)

	if _, err := gate.Judge(context.Background(), l.Engine, snap); err != nil {
		t.Fatal(err)
	}

	rows := l.Strings(`
		SELECT classification || ' ' || baseline_devices || ' ' || carried_over || ' ' || active
		FROM snapshot_judgement WHERE snapshot_id = $1 ORDER BY id`, snap)
	if len(rows) != 2 {
		t.Fatalf("%d judgements, want 2: the superseded one must stay readable", len(rows))
	}
	if !strings.HasSuffix(rows[0], " false") || !strings.HasSuffix(rows[1], " true") {
		t.Errorf("judgements %v, want the older inactive and the newer active", rows)
	}
	for i, r := range rows {
		if got := strings.TrimSuffix(strings.TrimSuffix(r, " true"), " false"); got != before[0] {
			t.Errorf("judgement %d reads %q, want the same figures as before: %q", i, got, before[0])
		}
	}
	if n := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, snap); n != 1 {
		t.Errorf("%d active judgements, want 1", n)
	}
}

// T018, FR-001: a snapshot that has not closed is never judged, since an unfinished run would be
// compared against a finished one.
func TestOpenSnapshotIsRefused(t *testing.T) {
	l := oneDevice(t)
	job := l.Start(Doc) // no collector, no engine: the snapshot stays open
	snap := int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job))

	_, err := gate.Judge(context.Background(), l.Engine, snap)
	if err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("error %v, want one saying the snapshot is not closed", err)
	}
	if n := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1`, snap); n != 0 {
		t.Errorf("%d judgements written for an open snapshot, want none", n)
	}
}

// T019, research R12: nothing stops two engines from running, so the safe outcome has to be the
// default one. The partial unique index is what makes it so.
func TestConcurrentJudgementsLeaveOneActive(t *testing.T) {
	l := oneDevice(t)
	snap := snapshotOf(l, l.Crawl(Doc))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = gate.Judge(context.Background(), l.Engine, snap)
		}()
	}
	wg.Wait()

	if errs[0] != nil && errs[1] != nil {
		t.Errorf("both judgements failed: %v, %v", errs[0], errs[1])
	}
	if n := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, snap); n != 1 {
		t.Errorf("%d active judgements after two engines judged at once, want 1", n)
	}
}
