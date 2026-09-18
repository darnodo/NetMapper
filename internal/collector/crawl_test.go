package collector_test

import (
	"net/netip"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func env(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
}

// US1-1: seed plus two neighbours that report each other and themselves: no loop, one
// observation per family per device, snapshot closed, job succeeded.
func TestCrawl(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw1 10.0.0.1", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1", "p2 sw3 10.0.0.3", "p3 sw2 10.0.0.2"),
		Addr("10.0.0.3"): FakeOS("sw3", "S003", "p1 sw2 10.0.0.2", "p2 sw1 10.0.0.1"),
	}})
	job := l.Crawl(Doc)
	want := []string{}
	for _, a := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		for _, f := range []string{"identity", "interfaces", "neighbours"} {
			want = append(want, a+" "+f+" collected")
		}
	}
	if got := l.Outcomes(job); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("outcomes:\n%s", strings.Join(got, "\n"))
	}
	if s := l.Strings(`SELECT j.state || ' ' || s.state FROM job j JOIN snapshot s ON s.id = j.snapshot_id WHERE j.id = $1`, job); s[0] != "succeeded closed" {
		t.Errorf("job and snapshot: %s", s[0])
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND kind = 'find'`, job); n != 3 {
		t.Errorf("%d find tasks, want 3", n)
	}
	if n := l.Int(`SELECT count(*) FROM snapshot WHERE closed_at IS NULL`); n != 0 {
		t.Error("snapshot left open")
	}
}
