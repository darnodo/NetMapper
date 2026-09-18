package collector_test

import (
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// SC-006: a platform added as pack data alone (internal/pack/testdata/fakeos, no Go code) is
// discovered and collected, over SSH only, by the unchanged crawl.
func TestNewPlatformIsData(t *testing.T) {
	env(t)
	sw := FakeOS("sw1", "S001")
	sw.Transports = []string{"ssh"}
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw}})
	job := l.Crawl(Doc)
	got := l.Strings(`SELECT fact_family || ' ' || status || ' ' || platform FROM observation ORDER BY 1`)
	want := []string{"identity collected fakeos", "interfaces collected fakeos", "neighbours empty fakeos"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v", got)
	}
	if s := l.Strings(`SELECT state FROM job WHERE id = $1`, job); s[0] != "succeeded" {
		t.Errorf("job %s", s[0])
	}
	if s := l.Strings(`SELECT parsed -> 0 ->> 'transports_answered' FROM observation WHERE fact_family = 'identity'`); s[0] != `["ssh"]` {
		t.Errorf("transports_answered %s", s[0])
	}
}
