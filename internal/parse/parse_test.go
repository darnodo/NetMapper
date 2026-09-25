package parse

import (
	"reflect"
	"testing"

	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/transport"
)

func registry(t *testing.T) *pack.Registry {
	r, err := pack.Load("../../packs/_base", "../pack/testdata/fakeos")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func impl(t *testing.T, r *pack.Registry, family string) pack.Impl {
	im := r.Implementations("fakeos", family, "1.0")
	if len(im) != 1 {
		t.Fatalf("%s: %d implementations", family, len(im))
	}
	return im[0]
}

func TestInterfaces(t *testing.T) {
	r := registry(t)
	out := "p1 up up 1500 AABB.CC00.0101 uplink to core\np2 down weird 9000 aa:bb:cc:00:01:02\n"
	status, rows, err := Parse(r, "fakeos", "interfaces", impl(t, r, "interfaces"), [][]byte{[]byte(out)})
	if status != Collected || err != nil {
		t.Fatalf("%s %v", status, err)
	}
	want := []map[string]any{
		{"name": "port1", "admin_state": "up", "oper_state": "up", "mtu": int64(1500), "mac": "aa:bb:cc:00:01:01", "description": "uplink to core"},
		{"name": "port2", "admin_state": "down", "oper_state": "other", "mtu": int64(9000), "mac": "aa:bb:cc:00:01:02"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v", rows)
	}
}

func TestOutcomes(t *testing.T) {
	r := registry(t)
	im := impl(t, r, "neighbours")
	if s, _, _ := Parse(r, "fakeos", "neighbours", im, [][]byte{[]byte("\n  \n")}); s != Empty {
		t.Errorf("blank output: %s", s)
	}
	// The pack's empty lines say "nothing here"; any other line is drift.
	if s, _, err := Parse(r, "fakeos", "neighbours", im, [][]byte{[]byte("No neighbours\n\n")}); s != Empty || err != nil {
		t.Errorf("declared empty output: %s %v", s, err)
	}
	if s, _, _ := Parse(r, "fakeos", "neighbours", im, [][]byte{[]byte("No neighbours\nNeighbour table v2\n")}); s != ParseFailed {
		t.Errorf("empty line plus drift: %s", s)
	}
	if s, _, err := Parse(r, "fakeos", "neighbours", im, [][]byte{[]byte("Neighbour table v2\n---\n")}); s != ParseFailed || err == nil {
		t.Errorf("drift: %s %v", s, err)
	}
	// Six columns: local port, name, address type, address, chassis id, far-end port. The last is what
	// 004 added, and it is the one the parser leaves alone: a far end's spelling cannot be
	// canonicalised until that device's platform is known, which is the graph projector's job.
	s, rows, err := Parse(r, "fakeos", "neighbours", im, [][]byte{[]byte("p1 sw2 ip4 10.0.0.2 aa:bb:cc:00:00:02 p7\np2 sw3 mac AABB.CCDD.EEFF aa:bb:cc:00:00:03 -\n")})
	if s != Collected || err != nil || rows[0]["protocol"] != "lldp" || rows[0]["local_interface"] != "port1" {
		t.Fatalf("neighbours: %s %v %v", s, rows, err)
	}
	// The local port is canonical, p1 becoming port1; the far end's is kept exactly as reported, and a
	// "-" means the report named no port at all.
	if rows[0]["remote_interface"] != "p7" {
		t.Errorf("far-end spelling %v, want it kept verbatim", rows[0]["remote_interface"])
	}
	if _, ok := rows[1]["remote_interface"]; ok {
		t.Errorf("a report naming no far-end port still carries one: %v", rows[1])
	}
	// The address keeps its type; a MAC is normalised, an IP is left as given.
	if rows[0]["remote_mgmt_address_type"] != "ipv4" || rows[0]["remote_mgmt_address"] != "10.0.0.2" ||
		rows[1]["remote_mgmt_address_type"] != "mac" || rows[1]["remote_mgmt_address"] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("typed addresses: %v", rows)
	}
}

func TestWalkAndMerge(t *testing.T) {
	r := registry(t)
	// Two walks joined on name: the second one also returns the name column to join on.
	im := pack.Impl{
		Steps:   []pack.Step{{Walk: "1.3.6.1.2.1.2.2.1.2"}, {Walk: "1.3.6.1.2.1.2.2.1"}},
		Map:     map[string]string{"name": "1.3.6.1.2.1.2.2.1.2", "mtu": "1.3.6.1.2.1.2.2.1.4"},
		MergeOn: []string{"name"},
	}
	names := transport.EncodeVarbinds([]transport.Varbind{{OID: "1.3.6.1.2.1.2.2.1.2.1", Value: "p1"}, {OID: "1.3.6.1.2.1.2.2.1.2.2", Value: "p2"}})
	mtus := transport.EncodeVarbinds([]transport.Varbind{{OID: "1.3.6.1.2.1.2.2.1.2.1", Value: "p1"}, {OID: "1.3.6.1.2.1.2.2.1.4.1", Value: "1500"}})
	s, rows, err := Parse(r, "fakeos", "interfaces", im, [][]byte{names, mtus})
	want := []map[string]any{{"name": "port1", "mtu": int64(1500)}, {"name": "port2"}}
	if s != Collected || err != nil || !reflect.DeepEqual(rows, want) {
		t.Errorf("%s %v %v", s, rows, err)
	}
	if s, _, _ := Parse(r, "fakeos", "interfaces", im, [][]byte{[]byte("[]"), []byte("null")}); s != Empty {
		t.Errorf("empty walk: %s", s)
	}
}
