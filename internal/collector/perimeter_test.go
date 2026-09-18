package collector_test

import (
	"net/netip"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func replace(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("%q not found", old)
	}
	return strings.Replace(s, old, new, 1)
}

// US1-3, FR-002, SC-002: neighbours outside the perimeter (not included, or excluded) become
// skipped find tasks, and no packet reaches them.
func TestNeighbourOutsidePerimeter(t *testing.T) {
	env(t)
	outside := FakeOS("far", "S099")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"):    FakeOS("sw1", "S001", "p1 far 192.168.1.1", "p2 gw 10.0.0.254", "p3 byname -"),
		Addr("192.168.1.1"): outside,
		Addr("10.0.0.254"):  outside,
	}})
	job := l.Crawl(Doc)

	got := l.Strings(`SELECT host(target) || ' ' || state || ' ' || skip_reason || ' ' || (parent_task_id IS NOT NULL)
		FROM task WHERE job_id = $1 AND state = 'skipped' ORDER BY target`, job)
	want := []string{"10.0.0.254 skipped out_of_perimeter true", "192.168.1.1 skipped out_of_perimeter true"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("skipped tasks %v", got)
	}
	for _, o := range l.Net.Opens() {
		if o.Addr == Addr("192.168.1.1") || o.Addr == Addr("10.0.0.254") {
			t.Errorf("opened %v", o)
		}
	}
	if n := l.Int(`SELECT count(*) FROM observation WHERE target IN ('192.168.1.1', '10.0.0.254')`); n != 0 {
		t.Errorf("%d observations for skipped targets", n)
	}
	// The name-only neighbour stays in the neighbours observation and queues nothing.
	if n := l.Int(`SELECT jsonb_array_length(parsed) FROM observation WHERE fact_family = 'neighbours'`); n != 3 {
		t.Errorf("neighbours rows %d, want 3", n)
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND kind = 'find'`, job); n != 3 {
		t.Errorf("%d finds, want seed plus two skipped", n)
	}
}
