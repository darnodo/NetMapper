package collector_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// A lease short enough for a test to wait it out.
var shortLease = strings.Replace(Doc, "lease: 2s", "lease: 600ms", 1)

// waitCommand waits until the fake has received cmd.
func waitCommand(t *testing.T, n *fake.Network, cmd string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if slices.Contains(n.Commands(), cmd) {
			return
		}
	}
	t.Fatalf("%s never sent", cmd)
}

// US3-1, FR-012, SC-004: a worker stopped mid-task gives the task back through its lease; a
// second worker finishes the run with the same result and no row written twice.
func TestResume(t *testing.T) {
	for _, c := range []struct{ name, block, kind string }{
		{"mid scrape", "display interfaces", "scrape"},
		{"between find steps 2 and 3", "display neighbours", "find"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env(t)
			sw1 := FakeOS("sw1", "S001", "p1 sw2 10.0.0.2")
			sw1.Block = map[string]bool{c.block: true}
			l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw1, Addr("10.0.0.2"): FakeOS("sw2", "S002")}})
			job := l.Start(shortLease)
			stopE := l.RunEngine()
			defer stopE()

			stop1 := l.RunCollector(l.Collector("c1"))
			waitCommand(t, l.Net, "10.0.0.1 "+c.block)
			stop1() // the blocked step never finishes: nothing is written for it
			sw1.Block = nil

			stop2 := l.RunCollector(l.Collector("c2"))
			defer stop2()
			if s := l.Wait(job, "succeeded", "failed"); s != "succeeded" {
				t.Fatalf("job %s", s)
			}
			if n := l.Int(`SELECT count(*) FROM (SELECT 1 FROM observation GROUP BY target, fact_family HAVING count(*) > 1) d`); n != 0 {
				t.Errorf("%d (target, family) pairs written twice", n)
			}
			if got := l.Outcomes(job); len(got) != 6 {
				t.Errorf("outcomes %v", got)
			}
			got := l.Strings(`SELECT state || ' ' || claimed_by || ' ' || attempts FROM task WHERE job_id = $1 AND kind = $2 AND host(target) = '10.0.0.1'`, job, c.kind)
			if len(got) != 1 || got[0] != "done c2 2" {
				t.Errorf("interrupted task: %v", got)
			}
		})
	}
}
