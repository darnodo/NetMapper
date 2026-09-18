package collector_test

import (
	"net/netip"
	"slices"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US2-1, US2-3, FR-009, FR-015: a silent address is unreachable; a device silent on SNMP but
// answering SSH is not a failure; a device matching no pack is unsupported/unknown_platform with
// a data quality finding, and the crawl goes on.
func TestOutcomes(t *testing.T) {
	env(t)
	sshOnly := FakeOS("sw6", "S006")
	sshOnly.Transports = []string{"ssh"}
	other := &fake.Device{
		CLI:  map[string]string{"display version": "OtherOS 9.1\n"},
		SNMP: map[string]string{"1.3.6.1.2.1.1.2.0": "1.3.6.1.4.1.12345.1", "1.3.6.1.2.1.1.1.0": "OtherOS 9.1"},
	}
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 silent 10.0.0.5", "p2 sw6 10.0.0.6", "p3 other 10.0.0.7", "p4 sw8 10.0.0.8"),
		Addr("10.0.0.6"): sshOnly,
		Addr("10.0.0.7"): other,
		Addr("10.0.0.8"): FakeOS("sw8", "S008"),
	}})
	job := l.Crawl(Doc)
	got := l.Outcomes(job)
	for _, want := range []string{
		"10.0.0.5 identity unreachable",
		"10.0.0.6 identity collected",
		"10.0.0.6 interfaces collected",
		"10.0.0.7 identity unsupported unknown_platform",
		"10.0.0.8 interfaces collected",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if n := l.Int(`SELECT count(*) FROM observation WHERE host(target) IN ('10.0.0.5', '10.0.0.7')`); n != 2 {
		t.Errorf("unreachable and unknown devices have %d observations, want identity only", n)
	}
	f := l.Strings(`SELECT f.domain || ' ' || f.category || ' ' || f.severity || ' ' || f.subject_ref || ' ' || o.fact_family || ' ' || o.status
		FROM finding f JOIN finding_evidence e ON e.finding_id = f.id
		JOIN observation o ON o.snapshot_id = e.snapshot_id AND o.id = e.observation_id`)
	if len(f) != 1 || f[0] != "data_quality unknown_platform warning target:10.0.0.7 identity unsupported" {
		t.Errorf("findings %v", f)
	}
	if n := l.Int(`SELECT count(*) FROM observation_raw r JOIN observation o ON o.snapshot_id = r.snapshot_id AND o.id = r.observation_id
		WHERE host(o.target) = '10.0.0.7'`); n == 0 {
		t.Error("unknown platform kept no evidence of what it answered")
	}
	if s := l.Strings(`SELECT state FROM job WHERE id = $1`, job); s[0] != "succeeded" {
		t.Errorf("job %s", s[0])
	}
}
