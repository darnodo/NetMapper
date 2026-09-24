package collector_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/frontier"
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
		Addr("10.0.0.1"):    FakeOS("sw1", "S001", "p1 far 192.168.1.1", "p2 gw 10.0.0.254", "p3 byname -", "p4 macnb AABB.CCDD.EEFF"),
		Addr("192.168.1.1"): outside,
		Addr("10.0.0.254"):  outside,
	}})
	job := l.Crawl(Doc)

	// Refused when enqueued: inserted skipped, never claimed (attempts 0), so the dial guard below
	// is a second line, not the only one.
	got := l.Strings(`SELECT host(target) || ' ' || state || ' ' || skip_reason || ' ' || (parent_task_id IS NOT NULL) || ' ' || attempts
		FROM task WHERE job_id = $1 AND state = 'skipped' ORDER BY target`, job)
	want := []string{"10.0.0.254 skipped out_of_perimeter true 0", "192.168.1.1 skipped out_of_perimeter true 0"}
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
	// The name-only neighbour is known but not collected: its row stays in the neighbours
	// observation, with no address invented, and no task exists for it.
	if n := l.Int(`SELECT jsonb_array_length(parsed) FROM observation WHERE fact_family = 'neighbours'`); n != 4 {
		t.Errorf("neighbours rows %d, want 4", n)
	}
	// A neighbour that gives only a MAC is kept, typed, and not collected either.
	mac := l.Strings(`SELECT (r ->> 'remote_mgmt_address_type') || ' ' || (r ->> 'remote_mgmt_address') FROM observation, jsonb_array_elements(parsed) r
		WHERE fact_family = 'neighbours' AND r ->> 'remote_system_name' = 'macnb'`)
	if len(mac) != 1 || mac[0] != "mac aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC-only neighbour row %v", mac)
	}
	row := l.Strings(`SELECT r::text FROM observation, jsonb_array_elements(parsed) r
		WHERE fact_family = 'neighbours' AND r ->> 'remote_system_name' = 'byname'`)
	if len(row) != 1 || row[0] != `{"protocol": "lldp", "local_interface": "port3", "remote_chassis_id": "aa:bb:cc:ff:ff:ff", "remote_system_name": "byname"}` {
		t.Errorf("name-only neighbour row %v", row)
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE target_name IN ('byname', 'macnb')`); n != 0 {
		t.Error("a task was invented for a neighbour with no address")
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND kind = 'find'`, job); n != 3 {
		t.Errorf("%d finds, want seed plus two skipped", n)
	}
}

// SC-002, the dial guard on its own: a target outside the perimeter that reached the queue anyway
// (a bug upstream, a hand-written row) is skipped when claimed, and no packet reaches it.
func TestDialGuard(t *testing.T) {
	env(t)
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"):    FakeOS("sw1", "S001"),
		Addr("192.168.1.1"): FakeOS("far", "S099"),
		Addr("10.0.0.254"):  FakeOS("gw", "S098"),
	}})
	job := l.Start(Doc)
	for _, a := range []string{"192.168.1.1", "10.0.0.254"} {
		if err := frontier.Enqueue(context.Background(), l.Col, job, "find", Addr(a), "", "", 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	stopC, stopE := l.RunCollector(l.Collector("c1")), l.RunEngine()
	l.Wait(job, "succeeded")
	stopE()
	stopC()
	got := l.Strings(`SELECT host(target) || ' ' || state || ' ' || skip_reason FROM task WHERE job_id = $1 AND state = 'skipped' ORDER BY target`, job)
	if len(got) != 2 || got[0] != "10.0.0.254 skipped out_of_perimeter" || got[1] != "192.168.1.1 skipped out_of_perimeter" {
		t.Errorf("skipped %v", got)
	}
	for _, o := range l.Net.Opens() {
		if o.Addr != Addr("10.0.0.1") {
			t.Errorf("opened %v", o)
		}
	}
}
