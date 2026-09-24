package jobrunner_test

import (
	"context"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// FR-025: a cancelled run leaves no pending task, keeps what it collected, closes its snapshot
// and ends cancelled.
func TestCancel(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "secret")
	sw2 := FakeOS("sw2", "S002", "p1 sw3 10.0.0.3", "p2 sw4 10.0.0.4")
	sw2.Block = map[string]bool{"display neighbours": true}
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2", "p2 sw5 10.0.0.5"),
		Addr("10.0.0.2"): sw2,
	}})
	job := l.Start(Doc)
	stopE := l.RunEngine()
	defer stopE()
	stopC := l.RunCollector(l.Collector("c1"))
	for deadline := time.Now().Add(10 * time.Second); !slices.Contains(l.Net.Commands(), "10.0.0.2 display neighbours"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("sw2 never reached")
		}
	}
	before := l.Outcomes(job)
	if err := jobrunner.Cancel(context.Background(), l.Operator, job); err != nil {
		t.Fatal(err)
	}
	stopC() // the blocked step is abandoned; its lease expires
	if s := l.Wait(job, "cancelled", "succeeded"); s != "cancelled" {
		t.Fatalf("job %s", s)
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND state IN ('pending', 'claimed')`, job); n != 0 {
		t.Errorf("%d tasks left pending or claimed", n)
	}
	after := l.Outcomes(job)
	for _, o := range before {
		if !slices.Contains(after, o) {
			t.Errorf("lost %q", o)
		}
	}
	if len(before) == 0 {
		t.Error("nothing was collected before the cancel")
	}
	if n := l.Int(`SELECT count(*) FROM snapshot s JOIN job j ON j.snapshot_id = s.id WHERE j.id = $1 AND s.state = 'closed' AND s.closed_at IS NOT NULL`, job); n != 1 {
		t.Error("snapshot not closed")
	}
}
