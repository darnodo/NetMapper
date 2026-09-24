package jobrunner_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T016, FR-014, US1-6: a closed snapshot ends up judged without anyone asking, and a snapshot that
// closed while the judging side was down is judged once it comes back.
func TestSweepJudgesClosedSnapshots(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})
	ctx := context.Background()

	// The engine runs for the whole crawl, so the sweep judges the snapshot on its own.
	snap := int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, l.Crawl(Doc)))
	if n := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, snap); n != 1 {
		t.Fatalf("%d active judgements after a normal run, want 1", n)
	}

	// Now the case the sweep exists for: the snapshot is closed and unjudged, as it would be had
	// the engine died between closing it and writing its verdict.
	if _, err := l.DB.Exec(ctx, `DELETE FROM snapshot_judgement WHERE snapshot_id = $1`, snap); err != nil {
		t.Fatal(err)
	}
	if err := jobrunner.Tick(ctx, l.Engine); err != nil {
		t.Fatal(err)
	}
	rows := l.Strings(`
		SELECT classification || ' ' || active FROM snapshot_judgement WHERE snapshot_id = $1`, snap)
	if len(rows) != 1 || rows[0] != "published true" {
		t.Errorf("judgements after the sweep: %v, want one active published", rows)
	}
}
