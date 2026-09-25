package api

import (
	"fmt"
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T040, FR-008, FR-018, US3-2: the findings of a snapshot, each with its category, its subject and
// the observations it cites, and only those of the snapshot asked for.
func TestFindings(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	denied := FakeOS("sw2", "S002")
	denied.Transports = []string{"ssh"}
	denied.Reject = []string{"ssh-a"}
	denied.Evidence = "ssh: unable to authenticate, attempted methods [none password]"
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2"),
		Addr("10.0.0.2"): denied,
	}}
	l := NewLab(t, net)
	l.Crawl(Doc)
	first := l.Int(`SELECT max(id) FROM snapshot`)
	net.Devices[Addr("10.0.0.2")] = FakeOS("sw2", "S002")
	l.Crawl(Doc)
	second := l.Int(`SELECT max(id) FROM snapshot`)
	c := newClient(t, l)

	fs := list(c.json(fmt.Sprintf("/v1/findings?snapshot=%d", first), 200)["findings"])
	var hit map[string]any
	for _, f := range fs {
		if f["category"] == "credential_denied" {
			hit = f
		}
	}
	if hit == nil {
		t.Fatalf("findings %v, want a credential_denied", fs)
	}
	if hit["domain"] != "compliance" || hit["subject_ref"] != "target:10.0.0.2" || hit["confidence"] != "direct" {
		t.Errorf("finding %v", hit)
	}
	for _, f := range []string{"severity", "state", "detail"} {
		if _, ok := hit[f]; !ok {
			t.Errorf("finding has no %s", f)
		}
	}
	for _, f := range list(c.json(fmt.Sprintf("/v1/findings?snapshot=%d", second), 200)["findings"]) {
		if f["category"] == "credential_denied" {
			t.Errorf("snapshot %d answered with a finding of %d: %v", second, first, f)
		}
	}
}
