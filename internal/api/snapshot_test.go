package api

import (
	"fmt"
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// quarantined crawls a baseline of two devices, then a run that reaches one: 50% coverage, quarantined
// under the default thresholds, as 004's TestQuarantinedSnapshotIsProjected builds it.
func quarantined(t *testing.T) (*Lab, int) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}}
	l := NewLab(t, net)
	l.Crawl(Doc)
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001")
	l.Crawl(Doc)
	second := l.Int(`SELECT max(id) FROM snapshot`)
	if v := l.Strings(`SELECT classification FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, second); len(v) != 1 || v[0] != "quarantined" {
		t.Fatalf("verdict %v, want quarantined for this test to mean anything", v)
	}
	return l, second
}

// T038, FR-007, US3-1: the snapshots, and one snapshot's judgement with what it was compared against.
func TestSnapshots(t *testing.T) {
	l, second := quarantined(t)
	l.Start(Doc)
	c := newClient(t, l)

	snaps := list(c.json("/v1/snapshots", 200)["snapshots"])
	if len(snaps) != 3 {
		t.Fatalf("snapshots %v, want three", snaps)
	}
	want := []string{"open false", "closed true", "closed true"}
	for i, s := range snaps {
		if got := fmt.Sprintf("%v %v", s["state"], s["has_graph"]); got != want[i] {
			t.Errorf("snapshot %v: %s, want %s (newest first)", s["id"], got, want[i])
		}
		if _, ok := s["verdict"]; !ok {
			t.Errorf("snapshot %v has no verdict key", s["id"])
		}
	}

	j := c.json(fmt.Sprintf("/v1/snapshots/%d", second), 200)["judgement"].(map[string]any)
	for _, f := range []string{"classification", "coverage", "baseline_snapshot_id", "baseline_devices",
		"carried_over", "reached", "thresholds", "breakdown", "gate_version", "computed_at"} {
		if _, ok := j[f]; !ok {
			t.Errorf("judgement has no %s: %v", f, j)
		}
	}
	if j["classification"] != "quarantined" || j["coverage"] != 0.5 || j["baseline_devices"] != float64(2) {
		t.Errorf("judgement %v, want quarantined at 0.5 against two devices", j)
	}
	open := l.Int(`SELECT max(id) FROM snapshot WHERE state = 'open'`)
	if body := c.json(fmt.Sprintf("/v1/snapshots/%d", open), 200); body["judgement"] != nil {
		t.Errorf("an open snapshot has a judgement: %v", body["judgement"])
	}
	if body := c.json("/v1/snapshots/999999", 404); body["error"] != "no_such_snapshot" {
		t.Errorf("unknown snapshot: %v", body)
	}
}

// T039, FR-019, FR-020, SC-009, US3-3: a quarantined snapshot is the default when it is the newest
// with a graph, and the answer says it is quarantined.
func TestQuarantinedSnapshotIsServed(t *testing.T) {
	l, second := quarantined(t)
	c := newClient(t, l)
	for _, path := range []string{"/v1/devices", "/v1/devices/sw1", "/v1/findings"} {
		snap := c.json(path, 200)["snapshot"].(map[string]any)
		if snap["id"] != float64(second) || snap["verdict"] != "quarantined" || snap["coverage"] != 0.5 {
			t.Errorf("%s: envelope %v, want snapshot %d, quarantined at 0.5", path, snap, second)
		}
	}
}
