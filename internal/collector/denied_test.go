package collector_test

import (
	"context"
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US2-2, FR-020 to FR-022: a device that answers and refuses every covering set is denied, and a
// compliance finding cites the observation whose raw output proves it answered.
func TestDeniedFinding(t *testing.T) {
	env(t)
	dev := FakeOS("sw1", "S001")
	dev.Transports = []string{"ssh"}
	dev.Reject = []string{"ssh-a"}
	dev.Evidence = "ssh: unable to authenticate, attempted methods [none password]"
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): dev}})
	job := l.Crawl(Doc)
	if got := l.Outcomes(job); len(got) != 1 || got[0] != "10.0.0.1 identity denied" {
		t.Fatalf("%v", got)
	}
	f := l.Strings(`SELECT f.domain || ' ' || f.category || ' ' || f.severity || ' ' || f.subject_ref || ' ' || o.status
		FROM finding f JOIN finding_evidence e ON e.finding_id = f.id
		JOIN observation o ON o.snapshot_id = e.snapshot_id AND o.id = e.observation_id`)
	if len(f) != 1 || f[0] != "compliance credential_denied high target:10.0.0.1 denied" {
		t.Fatalf("findings %v", f)
	}
	var hash []byte
	var command string
	l.DB.QueryRow(context.Background(), `SELECT r.hash, r.command FROM finding_evidence e
		JOIN observation_raw r ON r.snapshot_id = e.snapshot_id AND r.observation_id = e.observation_id`).Scan(&hash, &command)
	b, err := l.Raw.Get(context.Background(), hash)
	if err != nil || string(b) != dev.Evidence || command != "ssh.auth nm" {
		t.Errorf("evidence %q from %q, err %v", b, command, err)
	}
}
