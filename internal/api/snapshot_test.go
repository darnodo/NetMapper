package api

import (
	"context"
	"encoding/hex"
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

// evictAsOwner runs the database part of an eviction the way `netmapper snapshot evict --yes` does,
// and with finish also its object store part, so the API sees an evicted snapshot or, without, one
// still being evicted.
func evictAsOwner(t *testing.T, l *Lab, snap int, finish bool) {
	t.Helper()
	ctx := context.Background()
	var outcome string
	if err := l.DB.QueryRow(ctx, `SELECT outcome FROM evict_snapshot($1, true)`, snap).Scan(&outcome); err != nil || outcome != "evicting" {
		t.Fatalf("evict %d: %s %v", snap, outcome, err)
	}
	if !finish {
		return
	}
	for _, h := range l.Strings(`SELECT encode(hash, 'hex') FROM eviction_object WHERE snapshot_id = $1`, snap) {
		b, _ := hex.DecodeString(h)
		if err := l.Raw.Delete(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{`DELETE FROM eviction_object WHERE snapshot_id = $1`, `SELECT finish_snapshot_eviction($1)`} {
		if _, err := l.DB.Exec(ctx, q, snap); err != nil {
			t.Fatal(err)
		}
	}
}

// T018, SC-006, FR-010, FR-011, US1-2: a request naming an evicted or evicting snapshot gets 410
// on every endpoint that takes ?snapshot=; without it the default skips them; the outputs the
// remaining snapshot shares with the evicted one still answer. Served as netmapper_api.
func TestEvictedSnapshot(t *testing.T) {
	l := standard(t)
	first := l.Int(`SELECT max(id) FROM snapshot`)
	l.Crawl(Doc)
	second := l.Int(`SELECT max(id) FROM snapshot`)
	c := newClient(t, l)

	gone := l.Int(`SELECT min(id) FROM observation WHERE snapshot_id = $1`, second)
	evictAsOwner(t, l, second, true)

	paths := []string{"/v1/devices", "/v1/devices/sw1", "/v1/interfaces/sw1/port1", "/v1/devices/sw1/facts/interfaces",
		"/v1/findings", fmt.Sprintf("/v1/observations/%d", gone), fmt.Sprintf("/v1/observations/%d/raw/0", gone)}
	for _, p := range paths {
		body := c.json(fmt.Sprintf("%s?snapshot=%d", p, second), 410)
		snap, _ := body["snapshot"].(map[string]any)
		if body["error"] != "snapshot_evicted" || snap["state"] != "evicted" || snap["evicted_at"] == nil {
			t.Errorf("%s?snapshot=%d: %v", p, second, body)
		}
	}
	if body := c.json("/v1/devices", 200); body["snapshot"].(map[string]any)["id"] != float64(first) {
		t.Errorf("default snapshot %v, want %d", body["snapshot"], first)
	}
	if body := c.json(fmt.Sprintf("/v1/observations/%d", gone), 404); body["error"] != "no_such_observation" {
		t.Errorf("an evicted observation without ?snapshot=: %v", body)
	}

	// Every output of the remaining snapshot is served, the ones it shared with the evicted one
	// included.
	shared := 0
	for _, row := range l.Strings(`SELECT r.observation_id || ' ' || r.step_id || ' ' || encode(r.hash, 'hex') FROM observation_raw r
		WHERE r.snapshot_id = $1`, first) {
		var obs int
		var step, h string
		fmt.Sscan(row, &obs, &step, &h)
		status, got := c.get(fmt.Sprintf("/v1/observations/%d/raw/%s", obs, step))
		b, _ := hex.DecodeString(h)
		want, err := l.Raw.Get(context.Background(), b)
		if status != 200 || err != nil || string(got) != string(want) {
			t.Errorf("raw %d/%s: %d, want 200 with the stored bytes (%v)", obs, step, status, err)
		}
		shared++
	}
	if shared == 0 {
		t.Fatal("the remaining snapshot has no raw output to read")
	}

	// A snapshot being evicted answers the same, with its state.
	l.Crawl(Doc)
	third := l.Int(`SELECT max(id) FROM snapshot`)
	evictAsOwner(t, l, third, false)
	if body := c.json(fmt.Sprintf("/v1/devices?snapshot=%d", third), 410); body["snapshot"].(map[string]any)["state"] != "evicting" {
		t.Errorf("evicting snapshot: %v", body)
	}

	// With every graph gone, the default answers as on a fresh install.
	evictAsOwner(t, l, first, true)
	if body := c.json("/v1/devices", 409); body["error"] != "not_projected" || body["snapshot"] != nil {
		t.Errorf("no snapshot left: %v", body)
	}
}

// T031, FR-009: every snapshot says whether it is pinned.
func TestSnapshotPinned(t *testing.T) {
	l := standard(t)
	snap := l.Int(`SELECT max(id) FROM snapshot`)
	c := newClient(t, l)
	if s := list(c.json("/v1/snapshots", 200)["snapshots"])[0]; s["pinned"] != false || s["evicted_at"] != nil {
		t.Errorf("new snapshot: %v", s)
	}
	if _, err := l.DB.Exec(context.Background(), `SELECT set_snapshot_pinned($1, true)`, snap); err != nil {
		t.Fatal(err)
	}
	if s := c.json(fmt.Sprintf("/v1/snapshots/%d", snap), 200)["snapshot"].(map[string]any); s["pinned"] != true {
		t.Errorf("pinned snapshot: %v", s)
	}
}

// T036, FR-003, FR-013, US3-1, US3-2: the tombstone answers with its times and verdict, and a later
// verdict shows that its baseline was evicted, with its figures unchanged.
func TestTombstoneAndBaseline(t *testing.T) {
	l, second := quarantined(t)
	first := l.Int(`SELECT min(id) FROM snapshot`)
	c := newClient(t, l)
	if j := c.json(fmt.Sprintf("/v1/snapshots/%d", first), 200)["judgement"].(map[string]any); j["baseline_state"] != nil {
		t.Errorf("a verdict without a baseline: baseline_state %v, want null", j["baseline_state"])
	}

	evictAsOwner(t, l, first, true)
	body := c.json(fmt.Sprintf("/v1/snapshots/%d", first), 200)
	s := body["snapshot"].(map[string]any)
	if s["state"] != "evicted" || s["evicted_at"] == nil || s["closed_at"] == nil || s["verdict"] == nil || body["judgement"] == nil {
		t.Errorf("tombstone %v", body)
	}
	j := c.json(fmt.Sprintf("/v1/snapshots/%d", second), 200)["judgement"].(map[string]any)
	if j["baseline_state"] != "evicted" || j["baseline_snapshot_id"] != float64(first) || j["coverage"] != 0.5 {
		t.Errorf("later verdict %v, want baseline %d shown evicted at 0.5", j, first)
	}
}
