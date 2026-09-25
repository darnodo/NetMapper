package graph_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// lab crawls a fake network and returns the settled lab. Every test in this package goes through a
// real crawl, so the observations the projector reads are the ones a collector actually writes.
func lab(t *testing.T, devices map[netip.Addr]*fake.Device) *Lab {
	t.Helper()
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: devices})
	l.Crawl(Doc)
	return l
}

// key is the device key resolution mints for a fakeos device: its lowest strong identifier, which is
// the chassis MAC, since "chassis_mac" sorts before "serial".
func key(serial string) string { return "chassis_mac:" + MAC(serial) }

// ports lists "<device key> <canonical name> <source>" for the whole snapshot.
func ports(l *Lab) []string {
	return l.Strings(`SELECT e.device_key || ' ' || i.canonical_name || ' ' || i.source
		FROM interface i JOIN entity e ON e.id = i.entity_id ORDER BY 1`)
}

// T018, US1-1, FR-002, FR-003, FR-006: a device's collected interfaces become one row each, under the
// canonical name the pack's naming rules produce, carrying the fields that were collected.
func TestDevicePorts(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})

	got := l.Strings(`SELECT i.canonical_name || ' ' || i.source || ' ' || i.admin_state || ' ' ||
		i.oper_state || ' ' || i.mtu || ' ' || i.mac || ' ' || coalesce(i.description, '-')
		FROM interface i ORDER BY 1`)
	want := []string{
		"port1 device up up 1500 " + MAC("S001") + " uplink",
		"port2 device down down 1500 " + MAC("S001") + " -",
	}
	if !slices.Equal(got, want) {
		t.Errorf("interfaces %v, want %v", got, want)
	}
	// The pack spells them p1 and p2 and its interface_names rule makes them port1 and port2. No Go
	// code here knows that, which is the whole of FR-003.
	if n := l.Int(`SELECT count(*) FROM interface WHERE canonical_name LIKE 'p_'`); n != 0 {
		t.Errorf("%d interfaces kept the device's own spelling as their name", n)
	}
}

// T019, US1-2, FR-004, SC-002: a port two sources spell differently appears once, and both spellings
// lead to it with the source each came from.
func TestNeighbourSpellingIsAnAlias(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})

	if n := l.Int(`SELECT count(*) FROM interface i JOIN entity e ON e.id = i.entity_id
		WHERE e.device_key = $1 AND i.canonical_name = 'port1'`, key("S001")); n != 1 {
		t.Fatalf("%d interfaces named port1 on sw1, want exactly one", n)
	}
	got := l.Strings(`SELECT a.spelling || ' ' || a.source
		FROM interface_alias a JOIN interface i ON i.id = a.interface_id
		JOIN entity e ON e.id = i.entity_id
		WHERE e.device_key = $1 AND i.canonical_name = 'port1' ORDER BY 1`, key("S001"))
	want := []string{"p1 neighbour", "port1 device"}
	if !slices.Equal(got, want) {
		t.Errorf("spellings of sw1 port1: %v, want %v", got, want)
	}
	// Each spelling names the observation that used it, and the two are different observations.
	obs := l.Strings(`SELECT DISTINCT a.observation_id::text
		FROM interface_alias a JOIN interface i ON i.id = a.interface_id
		JOIN entity e ON e.id = i.entity_id
		WHERE e.device_key = $1 AND i.canonical_name = 'port1'`, key("S001"))
	if len(obs) != 2 {
		t.Errorf("%d observations behind the two spellings, want two", len(obs))
	}
}

// T020, edge case, FR-005: a spelling that matches no naming rule still produces an interface, named
// by that spelling rather than by nothing.
func TestSpellingWithNoRule(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 mgmt0 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002"),
	})

	// The fakeos rule is ^p(\d+)$ -> port$1, so mgmt0 matches nothing and stays itself.
	if n := l.Int(`SELECT count(*) FROM interface i JOIN entity e ON e.id = i.entity_id
		WHERE e.device_key = $1 AND i.canonical_name = 'mgmt0'`, key("S002")); n != 1 {
		t.Errorf("%d interfaces named mgmt0 on sw2, want one", n)
	}
}

// T021, US1-3, FR-007, SC-003: from an interface, the observation it was collected from and through
// it the raw output and the time of collection are reachable.
func TestInterfaceEvidenceReachesRawOutput(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})

	got := l.Strings(`SELECT i.canonical_name || ' ' || o.fact_family || ' ' || r.command || ' ' ||
		(o.collected_at IS NOT NULL) || ' ' || (r.hash IS NOT NULL)
		FROM interface i
		JOIN interface_evidence ev ON ev.interface_id = i.id
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
		ORDER BY 1`)
	want := []string{
		"port1 interfaces display interfaces true true",
		"port2 interfaces display interfaces true true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("evidence chain %v, want %v", got, want)
	}
}

// T022, US1-4, FR-001: a device whose interfaces were never collected keeps its entity and simply has
// no interfaces, rather than the projection failing.
func TestDeviceWithoutInterfacesKeepsEntity(t *testing.T) {
	mute := FakeOS("sw1", "S001")
	mute.Stall = map[string]bool{"display interfaces": true}
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): mute})

	if n := l.Int(`SELECT count(*) FROM entity WHERE device_key = $1`, key("S001")); n != 1 {
		t.Fatalf("%d entities for sw1, want one", n)
	}
	if n := l.Int(`SELECT count(*) FROM interface`); n != 0 {
		t.Errorf("%d interfaces, want none", n)
	}
	if n := l.Int(`SELECT count(*) FROM projection`); n != 1 {
		t.Error("the snapshot was not recorded as projected")
	}
}

// T023, US1-5, FR-002: two devices that each have a port spelled the same way are two interfaces,
// because an interface belongs to exactly one device.
func TestSamePortNameOnTwoDevices(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// The neighbour entry is how discovery reaches sw2 at all; both devices then list a p1.
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002"),
	})

	got := l.Strings(`SELECT e.device_key FROM interface i JOIN entity e ON e.id = i.entity_id
		WHERE i.canonical_name = 'port1' ORDER BY 1`)
	want := []string{key("S001"), key("S002")}
	if !slices.Equal(got, want) {
		t.Errorf("owners of a port1: %v, want %v", got, want)
	}
}

// T024, edge case, FR-006: a device that reports a neighbour on a port it never listed among its own
// interfaces gets that port, and it is distinguishable from one the device described itself.
func TestPortRevealedByNeighbour(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// p9 is not in the fakeos interfaces output, which lists p1 and p2 only.
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p9 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002"),
	})

	got := ports(l)
	if !slices.Contains(got, key("S001")+" port9 neighbour") {
		t.Errorf("ports %v, want sw1 port9 marked neighbour", got)
	}
	if !slices.Contains(got, key("S001")+" port1 device") {
		t.Errorf("ports %v, want sw1 port1 still marked device", got)
	}
	// A port only a report revealed carries no operational facts, and that is not a failure.
	if n := l.Int(`SELECT count(*) FROM interface WHERE canonical_name = 'port9'
		AND (mtu IS NOT NULL OR oper_state IS NOT NULL)`); n != 0 {
		t.Error("a port no interfaces row described carries operational facts")
	}
}

// T025, edge case, research R2: a device reached on two addresses is one entity, and its interfaces
// are not duplicated per address.
func TestTwoAddressesDoNotDuplicatePorts(t *testing.T) {
	// The self-report is what makes discovery try the second address at all; without it the crawl
	// never reaches 10.0.0.2 and the test proves nothing.
	sw1 := FakeOS("sw1", "S001", "p1 self 10.0.0.2 p1 "+MAC("S001"))
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): sw1,
		Addr("10.0.0.2"): sw1, // the same box, answering twice
	})

	if n := l.Int(`SELECT count(*) FROM entity`); n != 1 {
		t.Fatalf("%d entities, want one: the crawl should have deduplicated the second address", n)
	}
	if n := l.Int(`SELECT jsonb_array_length(attributes -> 'targets') FROM entity`); n != 2 {
		t.Fatalf("the entity carries %d addresses, want the two it answered on", n)
	}
	got := ports(l)
	want := []string{key("S001") + " port1 device", key("S001") + " port2 device"}
	if !slices.Equal(got, want) {
		t.Errorf("ports %v, want %v", got, want)
	}
}

// T026, research R5: the same spelling arriving from two observations keeps the lowest observation
// id. Run with inputs that actually tie, which is what the constitution asks of a rule that settles a
// tie. The tie is real and ordinary: sw1 spells its own port1 that way in its interfaces list and
// again as the local end of its own neighbours row, so one spelling arrives from two observations.
func TestAliasTieBreak(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002"),
	})

	rows := l.Strings(`SELECT a.observation_id || ' ' || a.source
		FROM interface_alias a JOIN interface i ON i.id = a.interface_id
		JOIN entity e ON e.id = i.entity_id
		WHERE e.device_key = $1 AND i.canonical_name = 'port1' AND a.spelling = 'port1'`, key("S001"))
	if len(rows) != 1 {
		t.Fatalf("%d rows for one spelling of one port, want one: %v", len(rows), rows)
	}
	// Both observations belong to sw1, so both are the device naming its own port, and the lowest id
	// wins. The find writes neighbours before its scrape writes interfaces, so it is the lower one.
	lowest := l.Strings(`SELECT min(o.id) || ' device' FROM observation o
		JOIN task t ON t.job_id = o.task_id OR t.id = o.task_id
		WHERE o.status = 'collected' AND o.fact_family IN ('interfaces', 'neighbours')
		  AND o.target = '10.0.0.1'`)
	if rows[0] != lowest[0] {
		t.Errorf("alias cites %q, want the lowest of the two observations, %q", rows[0], lowest[0])
	}
}

// T027, edge case, research R14: an interface whose MAC is also a device's chassis MAC, which
// resolution used as a strong identifier, is an interface. It does not become an entity, and no edge
// mistakes it for one.
func TestInterfaceMACIsNotADevice(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{Addr("10.0.0.1"): FakeOS("sw1", "S001")})

	// FakeOS gives every interface the chassis MAC, which is exactly the collision this guards.
	if n := l.Int(`SELECT count(*) FROM interface WHERE mac = $1`, MAC("S001")); n != 2 {
		t.Fatal("the fixture no longer puts the chassis MAC on the interfaces")
	}
	if n := l.Int(`SELECT count(*) FROM entity`); n != 1 {
		t.Errorf("%d entities, want one: an interface MAC minted a device", n)
	}
	for _, ref := range l.Strings(`SELECT from_ref FROM edge UNION SELECT to_ref FROM edge`) {
		if strings.HasPrefix(ref, "unknown:") {
			t.Errorf("edge endpoint %q: an interface MAC was read as an unresolved device", ref)
		}
	}
}

// Code review, FR-004 and Principle I: an alias records who wrote a spelling and the observation that
// proves it, and those two always come from the same report. A spelling first seen in a neighbour's
// report and later written by the owner used to keep the neighbour's lower observation id while
// flipping the source to `device`, so the row claimed the owner had named its own port while citing
// evidence from another device entirely.
//
// The shape needed for it: a port the owner's interfaces recipe never listed, named by a neighbour
// first, under a spelling that is already canonical so both sides record the same string. The fakeos
// rule is `^p(\d+)$`, which leaves `port9` alone, so a report naming `port9` hides nothing.
func TestAliasSourceAndEvidenceAgree(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// sw1 is the seed, so its neighbours observation is written first and carries the lower id.
		// It names port9 on sw2, a port sw2's own interfaces output never lists.
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 port9 "+MAC("S002")),
		// sw2 then names that same port as the local end of its own report.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p9 sw1 10.0.0.1 p1 "+MAC("S001")),
	})

	got := l.Strings(`SELECT a.source || ' ' || host(o.target)
		FROM interface_alias a
		JOIN interface i ON i.id = a.interface_id
		JOIN entity e ON e.id = i.entity_id
		JOIN observation o ON o.snapshot_id = a.snapshot_id AND o.id = a.observation_id
		WHERE e.device_key = $1 AND i.canonical_name = 'port9' AND a.spelling = 'port9'`, key("S002"))
	if len(got) != 1 {
		t.Fatalf("%d alias rows for port9 on sw2, want one: %v", len(got), got)
	}
	// Whichever source won, the observation has to be one collected from the device that source names.
	if got[0] != "device 10.0.0.2" {
		t.Errorf("alias %q, want the owner's own naming cited by the owner's own observation", got[0])
	}
}
