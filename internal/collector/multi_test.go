package collector_test

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US3-3, FR-013: two collectors on one run share the work; no task is handled by both.
func TestTwoCollectors(t *testing.T) {
	env(t)
	devices := map[netip.Addr]*fake.Device{}
	for i := 1; i <= 8; i++ {
		var nb []string
		for j := 1; j <= 8; j++ {
			nb = append(nb, fmt.Sprintf("p%d sw%d 10.0.0.%d", j, j, j))
		}
		devices[Addr(fmt.Sprintf("10.0.0.%d", i))] = FakeOS(fmt.Sprintf("sw%d", i), fmt.Sprintf("S%03d", i), nb...)
	}
	// Slow devices keep both collectors busy.
	n := &fake.Network{Devices: devices, OnRun: func(netip.Addr, transport.Step) { time.Sleep(5 * time.Millisecond) }}
	l := NewLab(t, n)
	job := l.Start(Doc)
	c1, c2 := l.Collector("c1"), l.Collector("c2")
	c1.Workers, c2.Workers = 2, 2
	stop1, stop2, stopE := l.RunCollector(c1), l.RunCollector(c2), l.RunEngine()
	l.Wait(job, "succeeded")
	stopE()
	stop1()
	stop2()

	if got := l.Outcomes(job); len(got) != 24 {
		t.Errorf("%d outcomes, want 24", len(got))
	}
	if n := l.Int(`SELECT count(*) FROM (SELECT 1 FROM observation GROUP BY target, fact_family HAVING count(*) > 1) d`); n != 0 {
		t.Errorf("%d (target, family) pairs written twice", n)
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND attempts <> 1`, job); n != 0 {
		t.Errorf("%d tasks claimed more than once: %v", n, l.Strings(`SELECT kind || ' ' || host(target) || ' ' || attempts || ' ' || coalesce(last_error, '') FROM task WHERE job_id = $1 AND attempts <> 1`, job))
	}
	if got := l.Strings(`SELECT DISTINCT claimed_by FROM task WHERE job_id = $1 ORDER BY 1`, job); len(got) != 2 {
		t.Errorf("claimed_by %v, want both collectors", got)
	}
}
