package collector_test

import (
	"context"
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US2-4, FR-015: output that no longer matches its template is parse_failed, the raw output is
// still stored, and a data quality finding cites it.
func TestParseDrift(t *testing.T) {
	env(t)
	sw := FakeOS("sw1", "S001")
	drift := "Interface table (format 2)\nport1: up/up mtu=1500\n"
	sw.CLI["display interfaces"] = drift
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw}})
	job := l.Crawl(Doc)
	got := l.Outcomes(job)
	if len(got) != 3 || got[1] != "10.0.0.1 interfaces parse_failed" {
		t.Fatalf("%v", got)
	}
	var hash []byte
	l.DB.QueryRow(context.Background(), `SELECT r.hash FROM observation o JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
		WHERE o.fact_family = 'interfaces'`).Scan(&hash)
	if b, _ := l.Raw.Get(context.Background(), hash); string(b) != drift {
		t.Errorf("raw output not kept: %q", b)
	}
	f := l.Strings(`SELECT f.domain || ' ' || f.category || ' ' || f.severity || ' ' || (f.detail ->> 'family') || ' ' || o.status
		FROM finding f JOIN finding_evidence e ON e.finding_id = f.id
		JOIN observation o ON o.snapshot_id = e.snapshot_id AND o.id = e.observation_id`)
	if len(f) != 1 || f[0] != "data_quality parse_failed warning interfaces parse_failed" {
		t.Errorf("findings %v", f)
	}
	if s := l.Strings(`SELECT state FROM job WHERE id = $1`, job); s[0] != "succeeded" {
		t.Errorf("job %s", s[0])
	}
}
