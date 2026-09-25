package api

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T018, FR-001a, FR-001b, SC-001: a device is reachable by its key, its hostname and an address it
// answered on, and every answer names the key it resolved to.
func TestDeviceByKeyHostnameAndAddress(t *testing.T) {
	c := newClient(t, standard(t))
	for _, name := range []string{key("S001"), "sw1", "10.0.0.1"} {
		d := c.json("/v1/devices/"+name, 200)
		if d["device_key"] != key("S001") {
			t.Errorf("%s resolved to %v, want %s", name, d["device_key"], key("S001"))
		}
	}
}

// T018, FR-001a, SC-001a, constitution tie-break rule: a hostname two devices share is refused with
// both named, never resolved to one. The tie is real: two fakeos devices with the same name and
// different serials, which resolution keeps apart because a hostname is weak.
func TestHostnameTieIsRefused(t *testing.T) {
	c := newClient(t, lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("dup", "S001", "p1 dup 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("dup", "S002", "p1 dup 10.0.0.1 p1 "+MAC("S001")),
	}))
	checkTie(t, c, "dup", []string{key("S001"), key("S002")})
}

// T018, FR-001a, research R10: an address two devices answered on is refused the same way. This is
// the shape a VRRP or HSRP virtual IP produces when the pair fails over between the find and scrape
// tasks: two boxes with contradicting serials answer on one address in one snapshot, resolution
// refuses to merge them, and both entities carry the address. The fake network shows one device per
// address whatever the transport, so the tie is inserted into the computed zone, which is rebuildable
// by design (Principle II). The rule under test is the interface's, not resolution's.
func TestAddressTieIsRefused(t *testing.T) {
	l := standard(t)
	if _, err := l.DB.Exec(context.Background(), `
		UPDATE entity SET attributes = jsonb_set(attributes, '{targets}', attributes->'targets' || '"10.0.0.1"')
		WHERE device_key = $1`, key("S002")); err != nil {
		t.Fatal(err)
	}
	checkTie(t, newClient(t, l), "10.0.0.1", []string{key("S001"), key("S002")})
}

func checkTie(t *testing.T, c *client, name string, want []string) {
	t.Helper()
	status, first := c.get("/v1/devices/" + name)
	if status != 409 {
		t.Fatalf("%s: %d %s, want 409", name, status, first)
	}
	body := c.json("/v1/devices/"+name, 409)
	var got []string
	for _, k := range body["candidates"].([]any) {
		got = append(got, k.(string))
	}
	if body["error"] != "ambiguous" || !slices.Equal(got, want) {
		t.Errorf("%s: %v, want ambiguous with candidates %v", name, body, want)
	}
	if _, ok := body["snapshot"].(map[string]any); !ok {
		t.Errorf("%s: the refusal does not say which snapshot was searched", name)
	}
	for range 5 {
		if _, again := c.get("/v1/devices/" + name); string(again) != string(first) {
			t.Fatalf("%s: answer changed between requests: %s then %s", name, first, again)
		}
	}
}

// T018, FR-018, edge case: a key that exists in an earlier snapshot and not in the one named is a
// plain absence, not an answer from the earlier snapshot.
func TestDeviceAbsentFromNamedSnapshot(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}}
	l := NewLab(t, net)
	l.Crawl(Doc)
	delete(net.Devices, Addr("10.0.0.2"))
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001")
	l.Crawl(Doc)
	second := l.Int(`SELECT max(id) FROM snapshot`)

	c := newClient(t, l)
	body := c.json(fmt.Sprintf("/v1/devices/%s?snapshot=%d", key("S002"), second), 404)
	if body["error"] != "no_such_device" {
		t.Errorf("%v, want no_such_device", body)
	}
	if snap, _ := body["snapshot"].(map[string]any); snap["id"] != float64(second) {
		t.Errorf("the 404 names snapshot %v, want %d", snap["id"], second)
	}
}

// T019, FR-001c, SC-001b: the list carries what a caller needs to pick a device, and no ports or
// edges.
func TestDeviceList(t *testing.T) {
	c := newClient(t, standard(t))
	devs := list(c.json("/v1/devices", 200)["devices"])
	var keys []string
	for _, d := range devs {
		keys = append(keys, d["device_key"].(string))
		for _, f := range []string{"device_key", "hostname", "platform", "targets", "weak", "confidence", "evidence"} {
			if _, ok := d[f]; !ok {
				t.Errorf("%v: no %s", d["device_key"], f)
			}
		}
		for _, f := range []string{"interfaces", "edges"} {
			if _, ok := d[f]; ok {
				t.Errorf("%v: the list carries %s", d["device_key"], f)
			}
		}
	}
	if len(devs) != 4 {
		t.Errorf("devices %v, want four", keys)
	}
}

// T019, FR-003, FR-005, FR-006, SC-003, US1-2, US1-4: what one device's answer says about its ports
// and its cables.
func TestDeviceAnswer(t *testing.T) {
	c := newClient(t, standard(t))

	sw1 := c.json("/v1/devices/sw1", 200)
	edges := map[string]map[string]any{}
	for _, e := range list(sw1["edges"]) {
		edges[e["to_ref"].(string)] = e
	}
	// Both ends agree on sw1-sw2 and the evidence has a row per side; only sw1 reports sw3.
	agreed := edges["if:"+key("S002")+"/port1"]
	if agreed == nil || agreed["confidence"] != "both_ends" || sides(agreed) != "from,to" {
		t.Errorf("sw1-sw2: %v, want both_ends with evidence from both sides", agreed)
	}
	oneSided := edges["if:"+key("S003")+"/port5"]
	if oneSided == nil || oneSided["confidence"] != "one_end" || sides(oneSided) != "from" {
		t.Errorf("sw1-sw3: %v, want one_end with evidence from one side", oneSided)
	}
	if a := edges["addr:10.0.0.1"]; a == nil || a["confidence"] != "direct" {
		t.Errorf("address edge: %v, want direct", a)
	}
	if sw1["weak"] != false || sw1["confidence"] != "strong" {
		t.Errorf("sw1: weak %v confidence %v, want strong", sw1["weak"], sw1["confidence"])
	}

	// sw2's port1 is spelled p1 by sw1's report and port1 by sw2 itself.
	sw2 := c.json("/v1/devices/sw2", 200)
	port1 := findPort(t, sw2, "port1")
	if port1["source"] != "device" || port1["confidence"] != "described" {
		t.Errorf("sw2 port1: %v, want described by its device", port1)
	}
	var spellings []string
	for _, a := range list(port1["aliases"]) {
		spellings = append(spellings, a["spelling"].(string))
	}
	if !slices.Contains(spellings, "p1") {
		t.Errorf("sw2 port1 aliases %v, want the neighbour's spelling p1 among them", spellings)
	}
	// A far end nobody accounts for is returned as the report named it, not dropped.
	unknown := false
	for _, e := range list(sw2["edges"]) {
		unknown = unknown || strings.HasPrefix(e["to_ref"].(string), "unknown:")
	}
	if !unknown {
		t.Errorf("sw2 edges carry no unknown far end: %v", sw2["edges"])
	}

	// sw3 never described port5; sw1's report revealed it.
	port5 := findPort(t, c.json("/v1/devices/sw3", 200), "port5")
	if port5["source"] != "neighbour" || port5["confidence"] != "revealed" {
		t.Errorf("sw3 port5: %v, want revealed by a neighbour", port5)
	}

	// sw4 prints no strong identifier.
	sw4 := c.json("/v1/devices/sw4", 200)
	if sw4["weak"] != true || sw4["confidence"] != "weak" {
		t.Errorf("sw4: weak %v confidence %v, want weak", sw4["weak"], sw4["confidence"])
	}
}

func findPort(t *testing.T, d map[string]any, name string) map[string]any {
	t.Helper()
	for _, i := range list(d["interfaces"]) {
		if i["canonical_name"] == name {
			return i
		}
	}
	t.Fatalf("%v has no %s: %v", d["device_key"], name, d["interfaces"])
	return nil
}

func sides(e map[string]any) string {
	var out []string
	for _, x := range e["evidence"].([]any) {
		if s := x.(map[string]any)["side"].(string); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

// T019, FR-001d: a device on a flat segment returns every edge on its port, uncapped.
func TestFlatSegmentIsUncapped(t *testing.T) {
	devices := map[netip.Addr]*fake.Device{}
	var nb []string
	for i := 2; i <= 7; i++ {
		addr := fmt.Sprintf("10.0.0.%d", i)
		serial := fmt.Sprintf("S%03d", i)
		nb = append(nb, fmt.Sprintf("p1 d%d %s p1 %s", i, addr, MAC(serial)))
		devices[Addr(addr)] = FakeOS(fmt.Sprintf("d%d", i), serial)
	}
	devices[Addr("10.0.0.1")] = FakeOS("hub", "S001", nb...)
	c := newClient(t, lab(t, devices))

	n := 0
	for _, e := range list(c.json("/v1/devices/hub", 200)["edges"]) {
		if e["type"] == "l1_link" && strings.HasPrefix(e["from_ref"].(string), "if:"+key("S001")+"/port1") {
			n++
		}
	}
	if n != 6 {
		t.Errorf("%d links on hub port1, want all six", n)
	}
}

// T020, FR-009, FR-017, FR-018, FR-019, research R9: which snapshot answers.
func TestSnapshotChoice(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})
	l.Crawl(Doc)
	l.Crawl(Doc)
	first, second := l.Int(`SELECT min(id) FROM snapshot`), l.Int(`SELECT max(id) FROM snapshot`)
	c := newClient(t, l)
	ctx := context.Background()

	if id := snapshotOf(c.json("/v1/devices", 200)); id != second {
		t.Errorf("default answered from %d, want the newest projected, %d", id, second)
	}

	// The sweep has not reached the newest yet: it is not the default, and naming it says so.
	if _, err := l.DB.Exec(ctx, `DELETE FROM projection WHERE snapshot_id = $1`, second); err != nil {
		t.Fatal(err)
	}
	if id := snapshotOf(c.json("/v1/devices", 200)); id != first {
		t.Errorf("default answered from %d, want %d while %d is unprojected", id, first, second)
	}
	named := c.json(fmt.Sprintf("/v1/devices?snapshot=%d", second), 409)
	if named["error"] != "not_projected" || snapshotOf(named) != second {
		t.Errorf("naming the unprojected snapshot: %v, want not_projected for %d", named, second)
	}

	// A projection built from an entity set since replaced is not a graph either.
	if _, err := l.DB.Exec(ctx, `UPDATE resolution SET computed_at = computed_at + interval '1 second'
		WHERE snapshot_id = $1`, first); err != nil {
		t.Fatal(err)
	}
	if body := c.json(fmt.Sprintf("/v1/devices?snapshot=%d", first), 409); body["error"] != "not_projected" {
		t.Errorf("stale projection: %v, want not_projected", body)
	}
	if body := c.json("/v1/devices", 409); body["error"] != "not_projected" || body["snapshot"] != nil {
		t.Errorf("no snapshot carries a graph: %v, want not_projected with no snapshot", body)
	}

	// An open snapshot carries no graph; an unknown one does not exist; a bad id is a bad request.
	l.Start(Doc)
	open := l.Int(`SELECT max(id) FROM snapshot WHERE state = 'open'`)
	if body := c.json(fmt.Sprintf("/v1/devices?snapshot=%d", open), 409); body["error"] != "not_projected" {
		t.Errorf("open snapshot: %v, want not_projected", body)
	}
	if body := c.json("/v1/devices?snapshot=999999", 404); body["error"] != "no_such_snapshot" {
		t.Errorf("unknown snapshot: %v", body)
	}
	if body := c.json("/v1/devices?snapshot=abc", 400); body["error"] != "bad_snapshot" {
		t.Errorf("bad snapshot: %v", body)
	}
}

func snapshotOf(body map[string]any) int {
	s, _ := body["snapshot"].(map[string]any)
	id, _ := s["id"].(float64)
	return int(id)
}

// T022: the interface endpoint returns the same port as the device answer, and the edges touching it.
// A port whose name carries a slash is reachable written as it is and escaped.
func TestInterfaceEndpoint(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)

	want := findPort(t, c.json("/v1/devices/sw2", 200), "port1")
	got := c.json("/v1/interfaces/sw2/port1", 200)
	if fmt.Sprint(got["interface"]) != fmt.Sprint(want) {
		t.Errorf("interface endpoint %v, want the device answer's %v", got["interface"], want)
	}
	if got["device_key"] != key("S002") || len(list(got["edges"])) == 0 {
		t.Errorf("interface answer %v, want sw2's key and the cable on port1", got)
	}
	if body := c.json("/v1/interfaces/sw2/port9", 404); body["error"] != "no_such_interface" {
		t.Errorf("unknown port: %v", body)
	}

	if _, err := l.DB.Exec(context.Background(),
		`UPDATE interface SET canonical_name = 'Ethernet1/1' WHERE canonical_name = 'port2'
		 AND entity_id = (SELECT id FROM entity WHERE device_key = $1)`, key("S002")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/interfaces/sw2/Ethernet1/1", "/v1/interfaces/sw2/Ethernet1%2F1"} {
		if body := c.json(path, 200); body["interface"].(map[string]any)["canonical_name"] != "Ethernet1/1" {
			t.Errorf("%s: %v", path, body)
		}
	}
}
