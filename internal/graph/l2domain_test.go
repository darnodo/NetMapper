package graph_test

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/graph"
	"github.com/darnodo/NetMapper/internal/pack"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// l2Packs loads _base and a copy of fakeos with the VLAN recipes of testdata/fakeos-l2 added. The
// shared fakeos pack has no VLAN recipe on purpose (NoRecipeFamilies), so the copy is the only place
// they exist (research R9).
func l2Packs(t *testing.T) *pack.Registry {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fakeos")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(Root(), "internal", "pack", "testdata", "fakeos"))); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "fakeos-l2"))); err != nil {
		t.Fatal(err)
	}
	reg, err := pack.Load(filepath.Join(Root(), "packs", "_base"), dir)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// l2lab crawls a fake network whose devices may print VLAN data, and returns the settled lab.
func l2lab(t *testing.T, devices map[netip.Addr]*fake.Device) *Lab {
	t.Helper()
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: devices})
	l.Registry = l2Packs(t)
	l.Crawl(Doc)
	return l
}

// vlanned gives a fake device its VLAN output. An empty string leaves the command printing nothing,
// which the parser records as empty.
func vlanned(dev *fake.Device, vlans, switchport string) *fake.Device {
	dev.CLI["display vlans"] = vlans
	dev.CLI["display switchport"] = switchport
	return dev
}

// sw is switch n of a test network: name swN, address 10.0.0.N, serial S00N.
func sw(n int, vlans, switchport string, neighbours ...string) *fake.Device {
	return vlanned(FakeOS(fmt.Sprintf("sw%d", n), fmt.Sprintf("S00%d", n), neighbours...), vlans, switchport)
}

// cable is the neighbour entry for a cable from a local port to port peerPort of switch peer.
func cable(local string, peer int, peerPort string) string {
	return fmt.Sprintf("%s sw%d 10.0.0.%d %s %s", local, peer, peer, peerPort, MAC(fmt.Sprintf("S00%d", peer)))
}

// network builds the address map for switches 1 to len(devs).
func network(devs ...*fake.Device) map[netip.Addr]*fake.Device {
	m := map[netip.Addr]*fake.Device{}
	for i, d := range devs {
		m[Addr(fmt.Sprintf("10.0.0.%d", i+1))] = d
	}
	return m
}

// shorten turns a fakeos device key into dNN, so expectations stay readable: key("S001") is d01.
var shorten = strings.NewReplacer("chassis_mac:aa:bb:cc:00:00:", "d")

// domains lists every domain of the lab's first snapshot as
// "<key> <confidence> <label or -> <member>,... |<state> <edge>,...", members and links sorted.
func domains(l *Lab) []string {
	got := l.Strings(`
		SELECT d.key_id || ' ' || d.confidence || ' ' || coalesce(d.label, '-') || ' ' ||
		       (SELECT string_agg(e.device_key || ':' || m.local_vlan_id, ',' ORDER BY e.device_key, m.local_vlan_id)
		          FROM l2domain_member m JOIN entity e ON e.id = m.entity_id WHERE m.l2domain_id = d.id) || ' |' ||
		       coalesce((SELECT string_agg(k.state || ' ' || g.name, ',' ORDER BY g.name)
		          FROM l2domain_link k JOIN edge g ON g.id = k.edge_id WHERE k.l2domain_id = d.id), '')
		FROM l2domain d WHERE d.snapshot_id = (SELECT min(id) FROM snapshot)
		ORDER BY 1`)
	for i := range got {
		got[i] = shorten.Replace(got[i])
	}
	slices.Sort(got) // byte order, whatever the database collation
	return got
}

// link is the short name of the l1_link between port a of switch i and port b of switch j, i < j.
func link(i int, a string, j int, b string) string {
	return fmt.Sprintf("l1_link:if:d%02d/%s|if:d%02d/%s", i, a, j, b)
}

// hasDomain reports whether one line of domains starts with want.
func hasDomain(got []string, want string) bool {
	return slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, want) })
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("domains:\n got %q\nwant %q", got, want)
	}
}

// trunkPair is two switches cabled on p1, both declaring VLANs 1 and 10, with the given switchport
// lines.
func trunkPair(t *testing.T, sp1, sp2 string) *Lab {
	return l2lab(t, network(
		sw(1, "1 active\n10 active USERS\n", sp1, cable("p1", 2, "p1")),
		sw(2, "1 active\n10 active USERS\n", sp2, cable("p1", 1, "p1")),
	))
}

// T008, US1-1, FR-001: a trunk carrying VLAN 10 at both ends puts both switches in one VLAN 10
// domain, built from that cable; native VLAN 1 is untagged at both ends and joins too.
func TestL2TrunkSharesVLAN(t *testing.T) {
	l := trunkPair(t, "p1 trunk 1 1,10\n", "p1 trunk 1 1,10\n")
	p1 := link(1, "port1", 2, "port1")
	expect(t, domains(l),
		"1 complete - d01:1,d02:1 |joined "+p1,
		"10 complete USERS d01:10,d02:10 |joined "+p1)
}

// T008, US1-2: the same VLAN pruned at one end gives two domains of one switch, with no link.
func TestL2PrunedVLAN(t *testing.T) {
	l := trunkPair(t, "p1 trunk 1 1,10\n", "p1 trunk 1 1\n")
	p1 := link(1, "port1", 2, "port1")
	expect(t, domains(l),
		"1 complete - d01:1,d02:1 |joined "+p1,
		"10 complete USERS d01:10 |",
		"10 complete USERS d02:10 |")
}

// T009, US1-3: a VLAN carried sw1 to sw2 and sw2 to sw3 is one domain of three.
func TestL2ChainOfThree(t *testing.T) {
	v := "10 active\n"
	l := l2lab(t, network(
		sw(1, v, "p1 trunk 1 10\n", cable("p1", 2, "p1")),
		sw(2, v, "p1 trunk 1 10\np2 trunk 1 10\n", cable("p1", 1, "p1"), cable("p2", 3, "p1")),
		sw(3, v, "p1 trunk 1 10\n", cable("p1", 2, "p2")),
	))
	expect(t, domains(l), "10 complete - d01:10,d02:10,d03:10 |joined "+link(1, "port1", 2, "port1")+
		",joined "+link(2, "port2", 3, "port1"))
}

// T009, US1-4: two member cables of a port-channel trunking VLAN 10 put both switches in one domain,
// and both cables are its links.
func TestL2PortChannel(t *testing.T) {
	v := "10 active\n"
	sp := "p2 member pc10\np3 member pc10\npc10 trunk 1 10\n"
	l := l2lab(t, network(
		sw(1, v, sp, cable("p2", 2, "p2"), cable("p3", 2, "p3")),
		sw(2, v, sp, cable("p2", 1, "p2"), cable("p3", 1, "p3")),
	))
	expect(t, domains(l), "10 complete - d01:10,d02:10 |joined "+link(1, "port2", 2, "port2")+
		",joined "+link(1, "port3", 2, "port3"))
}

// T010, US1-5, FR-005: an access port in a VLAN its own device does not declare makes that device a
// member of nothing; the other side is a domain of one.
func TestL2AccessVLANDeclaredOnOneSide(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "30 active\n", "p4 access 30\n", cable("p4", 2, "p4")),
		sw(2, "1 active\n", "p4 access 30\n", cable("p4", 1, "p4")),
	))
	expect(t, domains(l), "1 complete - d02:1 |", "30 complete - d01:30 |")
}

// T010, US1-6, FR-004: a declared VLAN no cable carries is a complete domain of one, with no link.
func TestL2VLANNoCableCarries(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "10 active\n", "p1 trunk 1 10\n", cable("p1", 2, "p1")),
		sw(2, "10 active\n50 active SW2-ONLY\n", "p1 trunk 1 1-4094\n", cable("p1", 1, "p1")),
	))
	if !hasDomain(domains(l), "50 complete SW2-ONLY d02:50 |") {
		t.Errorf("no lone VLAN 50 domain in %q", domains(l))
	}
}

// T011, FR-002, FR-003: a VLAN sent untagged at one end and expected tagged at the other is not
// joined. sw2's native VLAN 1 is not in its active list, so it carries nothing untagged on p1.
func TestL2TaggedAgainstUntagged(t *testing.T) {
	l := trunkPair(t, "p1 access 10\n", "p1 trunk 1 10\n")
	got := domains(l)
	if hasDomain(got, "10 complete USERS d01:10,d02:10") {
		t.Errorf("VLAN 10 joined across a tagged and an untagged end: %q", got)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category = 'vlan_mismatch'`); n != 0 {
		t.Errorf("%d vlan_mismatch findings, want none: only one end carries anything untagged", n)
	}
}

// T011, FR-005: a cable to something no entity accounts for adds no member.
func TestL2NoMemberThroughUnknownEndpoint(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "10 active\n", "p1 trunk 1 10\n", "p1 unmanaged 10.0.0.9 eth0 aa:bb:cc:99:99:99"),
	))
	expect(t, domains(l), "10 complete - d01:10 |")
}

// T012, spec edge cases: a device whose vlans observation is empty is a member of nothing; a device
// whose interface_vlans is empty carries nothing on any cable and is alone, and complete, in each
// VLAN it declares.
func TestL2EmptyFamilies(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "", "", cable("p1", 2, "p1")),
		sw(2, "10 active\n20 active\n", "", cable("p1", 1, "p1")),
	))
	if got := l.Strings(`SELECT host(target) || ' ' || fact_family || ' ' || status FROM observation
		WHERE fact_family IN ('vlans', 'interface_vlans') ORDER BY 1`); !slices.Equal(got, []string{
		"10.0.0.1 interface_vlans empty", "10.0.0.1 vlans empty",
		"10.0.0.2 interface_vlans empty", "10.0.0.2 vlans collected",
	}) {
		t.Fatalf("observations %v: the fixture is not the case under test", got)
	}
	expect(t, domains(l), "10 complete - d02:10 |", "20 complete - d02:20 |")
}

// T013, FR-008, research R6: the label is the name every named member agrees on, and nothing when
// they disagree, including on a tie.
func TestL2Label(t *testing.T) {
	sp := "p1 trunk 1 10\n"
	cases := []struct{ name1, name2, want string }{
		{"A", "B", "-"}, // a tie on count: neither name wins
		{"", "A", "A"},  // an unnamed member does not disagree
		{"", "", "-"},   // nothing to agree on
		{"A", "A", "A"}, // agreement
	}
	for _, c := range cases {
		t.Run(c.name1+"/"+c.name2, func(t *testing.T) {
			l := l2lab(t, network(
				sw(1, "10 active "+c.name1+"\n", sp, cable("p1", 2, "p1")),
				sw(2, "10 active "+c.name2+"\n", sp, cable("p1", 1, "p1")),
			))
			expect(t, domains(l), "10 complete "+c.want+" d01:10,d02:10 |joined "+link(1, "port1", 2, "port1"))
		})
	}
	// Two members agree and one does not: still no label. A majority is not agreement.
	sp2 := "p1 trunk 1 10\np2 trunk 1 10\n"
	l := l2lab(t, network(
		sw(1, "10 active A\n", sp, cable("p1", 2, "p1")),
		sw(2, "10 active B\n", sp2, cable("p1", 1, "p1"), cable("p2", 3, "p1")),
		sw(3, "10 active A\n", sp, cable("p1", 2, "p2")),
	))
	if !hasDomain(domains(l), "10 complete - d01:10,d02:10,d03:10") {
		t.Errorf("a two-to-one majority produced a label: %q", domains(l))
	}
}

// pcChain is the port-channel pair of T009 with a third switch on sw2's p4, so one fixture holds a
// channel, a chain and a VLAN carried on part of it only.
func pcChain(t *testing.T) *Lab {
	sp := "p2 member pc10\np3 member pc10\npc10 trunk 1 10,20\n"
	return l2lab(t, network(
		sw(1, "10 active\n20 active\n", sp, cable("p2", 2, "p2"), cable("p3", 2, "p3")),
		sw(2, "10 active\n20 active\n30 active\n", sp+"p4 trunk 1 10,30\n",
			cable("p2", 1, "p2"), cable("p3", 1, "p3"), cable("p4", 3, "p1")),
		sw(3, "10 active\n30 active\n", "p1 trunk 1 10,30\n", cable("p1", 2, "p4")),
	))
}

// T014, SC-002, FR-004: every declared VLAN of a device whose vlans was collected is in exactly one
// membership.
func TestL2OneDeclaredVLANOneDomain(t *testing.T) {
	l := pcChain(t)
	declared := l.Int(`SELECT count(*) FROM observation o, jsonb_array_elements(o.parsed)
		WHERE o.fact_family = 'vlans' AND o.status = 'collected'`)
	members := l.Int(`SELECT count(*) FROM l2domain_member`)
	if declared != members || declared != 7 {
		t.Errorf("%d declared VLANs, %d memberships, want 7 of each", declared, members)
	}
}

// T014, SC-003: two devices share a domain if and only if a path of joined links connects them. On
// this fixture: VLAN 10 spans all three, 20 sw1 and sw2, 30 sw2 and sw3.
func TestL2SameDomainIffPath(t *testing.T) {
	l := pcChain(t)
	pc2, pc3, p4 := link(1, "port2", 2, "port2"), link(1, "port3", 2, "port3"), link(2, "port4", 3, "port1")
	expect(t, domains(l),
		"10 complete - d01:10,d02:10,d03:10 |joined "+pc2+",joined "+pc3+",joined "+p4,
		"20 complete - d01:20,d02:20 |joined "+pc2+",joined "+pc3,
		"30 complete - d02:30,d03:30 |joined "+p4)
}

// T015, research R2, FR-011: a VLAN declared by two observations of one entity keeps the row of the
// lower observation id. Run with inputs that tie: the copy has the same VLAN ID and another name, and
// another target only because (snapshot, target, family, task) is unique; the owner is found by task.
func TestL2RowTieBreak(t *testing.T) {
	l := trunkPair(t, "p1 trunk 1 1,10\n", "p1 trunk 1 1,10\n")
	ctx := context.Background()
	if _, err := l.DB.Exec(ctx, `
		INSERT INTO observation (snapshot_id, collected_at, collector_id, task_id, target, transport,
		                         platform, recipe_id, fact_family, status, detail, parse_generation_id, parsed)
		SELECT snapshot_id, collected_at, collector_id, task_id, '10.0.0.99', transport,
		       platform, recipe_id, fact_family, status, detail, parse_generation_id,
		       '[{"vlan_id": 10, "name": "OTHER", "status": "active"}]'
		FROM observation WHERE fact_family = 'vlans' AND host(target) = '10.0.0.1'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
			t.Fatal(err)
		}
		// sw1 now has USERS from the original and OTHER from the copy; the original wins, so the two
		// members still agree.
		if got := domains(l); !hasDomain(got, "10 complete USERS d01:10,d02:10") {
			t.Fatalf("the higher observation id won the tie: %q", got)
		}
	}
}

// T016, SC-004, FR-011: projecting again gives the same domains.
func TestL2Reproducible(t *testing.T) {
	l := pcChain(t)
	want := domains(l)
	for range 3 {
		if _, err := graph.Project(context.Background(), l.DB, l.Registry, snap(l)); err != nil {
			t.Fatal(err)
		}
		if got := domains(l); !slices.Equal(got, want) {
			t.Fatalf("re-projection changed the domains:\n got %q\nwant %q", got, want)
		}
	}
}

// T016, SC-007, FR-013: deleting every domain row and recomputing restores the same domains, with no
// device contacted.
func TestL2WipeAndRecompute(t *testing.T) {
	l := pcChain(t)
	ctx := context.Background()
	want := domains(l)
	opens := len(l.Net.Opens())
	for _, q := range []string{"DELETE FROM l2domain", "DELETE FROM projection"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if got := domains(l); len(got) != 0 {
		t.Fatalf("the wipe left %v", got)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if got := domains(l); !slices.Equal(got, want) {
		t.Errorf("after the wipe and recompute:\n got %q\nwant %q", got, want)
	}
	if n := len(l.Net.Opens()); n != opens {
		t.Errorf("%d sessions opened during a recomputation", n-opens)
	}
	if n := l.Int(`SELECT l2domains FROM projection`); n != len(want) {
		t.Errorf("projection.l2domains is %d, want %d", n, len(want))
	}
}

// T017, FR-009, SC-005: each membership names the observations it rests on, of the right family and
// collected for the member's own device; each link's edge has its own evidence.
func TestL2Evidence(t *testing.T) {
	l := pcChain(t)
	if n := l.Int(`SELECT count(*) FROM l2domain_member`); n == 0 {
		t.Fatal("no member: this proves nothing")
	}
	if bad := l.Strings(`
		SELECT e.device_key || ':' || m.local_vlan_id FROM l2domain_member m
		JOIN entity e ON e.id = m.entity_id
		JOIN observation v ON v.snapshot_id = m.snapshot_id AND v.id = m.vlans_observation_id
		JOIN observation s ON s.snapshot_id = m.snapshot_id AND s.id = m.ports_observation_id
		WHERE v.fact_family <> 'vlans' OR s.fact_family <> 'interface_vlans'
		   OR NOT host(v.target) = ANY (SELECT jsonb_array_elements_text(e.attributes -> 'targets'))
		   OR NOT host(s.target) = ANY (SELECT jsonb_array_elements_text(e.attributes -> 'targets'))
		   OR v.collected_at IS NULL OR s.collected_at IS NULL`); len(bad) != 0 {
		t.Errorf("memberships citing the wrong evidence: %v", bad)
	}
	if n := l.Int(`SELECT count(*) FROM l2domain_member WHERE ports_observation_id IS NULL`); n != 0 {
		t.Errorf("%d memberships lost their port evidence", n)
	}
	if n := l.Int(`SELECT count(*) FROM l2domain_link k
		WHERE NOT EXISTS (SELECT 1 FROM edge_evidence ev WHERE ev.edge_id = k.edge_id)`); n != 0 {
		t.Errorf("%d links whose edge has no evidence", n)
	}
}

// stalled makes a fake device stop answering the given commands, so their families are unreachable.
func stalled(dev *fake.Device, commands ...string) *fake.Device {
	dev.Stall = map[string]bool{}
	for _, c := range commands {
		dev.Stall[c] = true
	}
	return dev
}

// confidenceHolds checks the contract on every domain of the lab: partial exactly when the domain has
// an unchecked link, and no unchecked link between two members of its own domain, since an unchecked
// cable never joins anything (contracts/l2domain.md).
func confidenceHolds(t *testing.T, l *Lab) {
	t.Helper()
	if bad := l.Strings(`SELECT d.id::text FROM l2domain d
		WHERE (d.confidence = 'partial') <> EXISTS (
			SELECT 1 FROM l2domain_link k WHERE k.l2domain_id = d.id AND k.state = 'unchecked')`); len(bad) != 0 {
		t.Errorf("domains whose confidence does not match their unchecked links: %v", bad)
	}
	if bad := l.Strings(`SELECT g.name FROM l2domain_link k JOIN edge g ON g.id = k.edge_id
		WHERE k.state = 'unchecked'
		  AND EXISTS (SELECT 1 FROM l2domain_member m WHERE m.l2domain_id = k.l2domain_id AND m.entity_id = g.from_entity_id)
		  AND EXISTS (SELECT 1 FROM l2domain_member m WHERE m.l2domain_id = k.l2domain_id AND m.entity_id = g.to_entity_id)`); len(bad) != 0 {
		t.Errorf("unchecked links between two members of their own domain: %v", bad)
	}
}

// T022, US2-2, FR-007: a trunk whose far switch gave no VLAN data joins nothing. The near switch is
// alone in each VLAN it carries on that trunk, partial, with the trunk listed as unchecked; the far
// switch is in no domain.
func TestL2UncheckedFarEnd(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "1 active\n10 active\n", "p1 trunk 1 1,10\n", cable("p1", 2, "p1")),
		stalled(sw(2, "1 active\n10 active\n", "p1 trunk 1 1,10\n", cable("p1", 1, "p1")),
			"display vlans", "display switchport"),
	))
	p1 := link(1, "port1", 2, "port1")
	expect(t, domains(l), "1 partial - d01:1 |unchecked "+p1, "10 partial - d01:10 |unchecked "+p1)
	confidenceHolds(t, l)
}

// T022, clarified 2026-10-12: a switch whose declared VLANs were read but whose port data was not is
// still a member of each VLAN it declares. Its cables join nothing, every domain it is in is partial
// with all its cables unchecked, and the switches facing it are partial on the VLANs they carry
// towards it.
func TestL2PortsUnknown(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "10 active\n", "p1 trunk 1 10\n", cable("p1", 2, "p1")),
		stalled(sw(2, "10 active\n20 active\n", "p1 trunk 1 10\np2 trunk 1 20\n",
			cable("p1", 1, "p1"), cable("p2", 3, "p1")), "display switchport"),
		sw(3, "20 active\n", "p1 trunk 1 20\n", cable("p1", 2, "p2")),
	))
	l12, l23 := link(1, "port1", 2, "port1"), link(2, "port2", 3, "port1")
	expect(t, domains(l),
		"10 partial - d01:10 |unchecked "+l12,
		"10 partial - d02:10 |unchecked "+l12+",unchecked "+l23,
		"20 partial - d02:20 |unchecked "+l12+",unchecked "+l23,
		"20 partial - d03:20 |unchecked "+l23)
	if n := l.Int(`SELECT count(*) FROM l2domain_member m JOIN entity e ON e.id = m.entity_id
		WHERE e.device_key = $1 AND m.ports_observation_id IS NOT NULL`, key("S002")); n != 0 {
		t.Errorf("%d memberships of sw2 cite port evidence that was never collected", n)
	}
	confidenceHolds(t, l)
}

// T023, research R4, R5: a cable whose far port is not known (sw1 named the device, not the port, and
// sw2 never reported back) cannot be checked, so the VLANs sw1 carries on it are partial.
func TestL2PortlessFarEnd(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "10 active\n", "p1 trunk 1 10\n", "p1 sw2 10.0.0.2 - "+MAC("S002")),
		sw(2, "10 active\n", "p1 trunk 1 10\n"),
	))
	expect(t, domains(l),
		"10 complete - d02:10 |",
		"10 partial - d01:10 |unchecked l1_link:dev:d02|if:d01/port1")
	confidenceHolds(t, l)
}

// T023, US2-3: a domain with no cable at all has nothing unchecked in it, so it is complete.
func TestL2CompleteWithoutCables(t *testing.T) {
	l := l2lab(t, network(sw(1, "10 active\n", "p1 trunk 1 10\n")))
	expect(t, domains(l), "10 complete - d01:10 |")
	confidenceHolds(t, l)
}

// T024, contracts/l2domain.md: the confidence contract holds on a fixture where every cable is
// checked, not only on the partial ones above.
func TestL2ConfidenceInvariant(t *testing.T) {
	l := pcChain(t)
	if n := l.Int(`SELECT count(*) FROM l2domain WHERE confidence = 'complete'`); n == 0 {
		t.Fatal("no complete domain: this proves nothing")
	}
	confidenceHolds(t, l)
}

// mismatches lists the vlan_mismatch findings of the lab's first snapshot as
// "<domain> <severity> <subject> <detail>", device keys shortened.
func mismatches(l *Lab) []string {
	got := l.Strings(`SELECT domain || ' ' || severity || ' ' || subject_ref || ' ' || detail::text
		FROM finding WHERE category = 'vlan_mismatch' AND snapshot_id = (SELECT min(id) FROM snapshot)
		ORDER BY subject_ref`)
	for i := range got {
		got[i] = shorten.Replace(got[i])
	}
	return got
}

// T027, US3-1, FR-012: native 1 facing native 99 joins the two VLANs into one domain keyed 1, each
// switch keeping its own VLAN ID, and raises one finding naming both ports, both VLANs and the cable,
// with the observations of both ends behind it.
func TestL2MismatchNative(t *testing.T) {
	v := "1 active\n99 active\n"
	l := l2lab(t, network(
		sw(1, v, "p1 trunk 1 1,99\n", cable("p1", 2, "p1")),
		sw(2, v, "p1 trunk 99 1,99\n", cable("p1", 1, "p1")),
	))
	p1 := link(1, "port1", 2, "port1")
	expect(t, domains(l),
		"1 complete - d01:1,d02:99 |joined "+p1,
		"1 complete - d02:1 |",
		"99 complete - d01:99 |")
	want := []string{`compliance warning if:d01/port1 {"edges": ["` + p1 + `"], "ports": ["if:d01/port1", "if:d02/port1"], "vlans": [1, 99]}`}
	if got := mismatches(l); !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
	got := l.Strings(`SELECT o.fact_family || ' ' || host(o.target) FROM finding f
		JOIN finding_evidence fe ON fe.finding_id = f.id
		JOIN observation o ON o.snapshot_id = fe.snapshot_id AND o.id = fe.observation_id
		WHERE f.category = 'vlan_mismatch' ORDER BY 1`)
	if !slices.Equal(got, []string{"interface_vlans 10.0.0.1", "interface_vlans 10.0.0.2",
		"neighbours 10.0.0.1", "neighbours 10.0.0.2"}) {
		t.Errorf("evidence %v, want both ends' port data and both neighbour reports", got)
	}
}

// T027, US3-2: access VLAN 10 facing access VLAN 20 is the same case.
func TestL2MismatchAccess(t *testing.T) {
	v := "10 active\n20 active\n"
	l := l2lab(t, network(
		sw(1, v, "p4 access 10\n", cable("p4", 2, "p4")),
		sw(2, v, "p4 access 20\n", cable("p4", 1, "p4")),
	))
	if !hasDomain(domains(l), "10 complete - d01:10,d02:20 |joined "+link(1, "port4", 2, "port4")) {
		t.Errorf("domains %q, want one keyed 10 holding sw1:10 and sw2:20", domains(l))
	}
	if n := len(mismatches(l)); n != 1 {
		t.Errorf("%d findings, want one", n)
	}
}

// T028, US3-3: matching natives raise nothing.
func TestL2NoMismatchWhenNativesMatch(t *testing.T) {
	v := "10 active\n99 active\n"
	l := l2lab(t, network(
		sw(1, v, "p1 trunk 99 10,99\n", cable("p1", 2, "p1")),
		sw(2, v, "p1 trunk 99 10,99\n", cable("p1", 1, "p1")),
	))
	if got := mismatches(l); len(got) != 0 {
		t.Errorf("findings %q, want none", got)
	}
	if !hasDomain(domains(l), "99 complete - d01:99,d02:99") {
		t.Errorf("native 99 not joined: %q", domains(l))
	}
}

// T028, spec edge case: an untagged VLAN its own device does not declare forwards nothing, so there
// is nothing to join and nothing to report.
func TestL2MismatchUndeclared(t *testing.T) {
	l := l2lab(t, network(
		sw(1, "1 active\n10 active\n", "p1 trunk 1 1,10\n", cable("p1", 2, "p1")),
		sw(2, "10 active\n", "p1 trunk 99 10,99\n", cable("p1", 1, "p1")),
	))
	if got := mismatches(l); len(got) != 0 {
		t.Errorf("findings %q, want none", got)
	}
	if !hasDomain(domains(l), "1 complete - d01:1 |") {
		t.Errorf("sw1's native VLAN joined something: %q", domains(l))
	}
}

// T029, US3-5, clarified 2026-10-12: a native mismatch on a port-channel is one finding for the pair
// of channels, listing every member cable.
func TestL2MismatchPortChannel(t *testing.T) {
	v := "1 active\n99 active\n"
	m := "p2 member pc10\np3 member pc10\n"
	l := l2lab(t, network(
		sw(1, v, m+"pc10 trunk 1 1,99\n", cable("p2", 2, "p2"), cable("p3", 2, "p3")),
		sw(2, v, m+"pc10 trunk 99 1,99\n", cable("p2", 1, "p2"), cable("p3", 1, "p3")),
	))
	want := []string{`compliance warning if:d01/pc10 {"edges": ["` +
		link(1, "port2", 2, "port2") + `", "` + link(1, "port3", 2, "port3") +
		`"], "ports": ["if:d01/pc10", "if:d02/pc10"], "vlans": [1, 99]}`}
	if got := mismatches(l); !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

// T029, spec edge case: sw1's VLAN 1 reaches sw2's VLAN 99 untagged on p1, and sw2's VLAN 99 comes
// back to sw1's VLAN 99 tagged on p2. sw1 is then in one domain twice, once per local VLAN.
func TestL2MemberTwiceInOneDomain(t *testing.T) {
	v := "1 active\n99 active\n"
	l := l2lab(t, network(
		sw(1, v, "p1 trunk 1 1\np2 trunk 1 99\n", cable("p1", 2, "p1"), cable("p2", 2, "p2")),
		sw(2, v, "p1 trunk 99 99\np2 trunk 1 99\n", cable("p1", 1, "p1"), cable("p2", 1, "p2")),
	))
	expect(t, domains(l),
		"1 complete - d01:1,d01:99,d02:99 |joined "+link(1, "port1", 2, "port1")+",joined "+link(1, "port2", 2, "port2"),
		"1 complete - d02:1 |")
}

// T030, US3-4, FR-012: the finding is part of the projection's output, replaced with it rather than
// piled up, and belongs to its snapshot: once the mismatch is fixed, the next snapshot reports none
// and the first keeps its own.
func TestL2MismatchReplacedAndPerSnapshot(t *testing.T) {
	v := "1 active\n99 active\n"
	sw2 := sw(2, v, "p1 trunk 99 1,99\n", cable("p1", 1, "p1"))
	l := l2lab(t, network(sw(1, v, "p1 trunk 1 1,99\n", cable("p1", 2, "p1")), sw2))
	ctx := context.Background()
	for range 3 {
		if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
			t.Fatal(err)
		}
	}
	if n := l.Int(`SELECT count(*) FROM finding f WHERE category = 'vlan_mismatch'
		AND EXISTS (SELECT 1 FROM finding_evidence fe WHERE fe.finding_id = f.id)`); n != 1 {
		t.Fatalf("%d vlan_mismatch findings with evidence after four projections, want one", n)
	}

	sw2.CLI["display switchport"] = "p1 trunk 1 1,99\n"
	l.Crawl(Doc)
	got := l.Strings(`SELECT s.id::text || ' ' || count(f.id) FROM snapshot s
		LEFT JOIN finding f ON f.snapshot_id = s.id AND f.category = 'vlan_mismatch'
		GROUP BY s.id ORDER BY s.id`)
	if len(got) != 2 || !strings.HasSuffix(got[0], " 1") || !strings.HasSuffix(got[1], " 0") {
		t.Errorf("mismatches per snapshot %v, want one in the first and none in the second", got)
	}
}

// FR-014, Principle II: with VLAN data present and a mismatch to report, projection still changes
// nothing in the collected zone, no entity, no decision, no verdict, and no finding it did not raise.
// The 004 test of the same rule runs on the plain fakeos pack, which collects no VLAN family.
func TestL2WritesNothingItDoesNotOwn(t *testing.T) {
	v := "1 active\n99 active\n"
	broken := sw(1, v, "p1 trunk 1 1,99\n", cable("p1", 2, "p1"))
	broken.CLI["display interfaces"] = "this is not an interface table\n" // a parse_failed finding
	l := l2lab(t, network(broken, sw(2, v, "p1 trunk 99 1,99\n", cable("p1", 1, "p1"))))
	sum := func() []string {
		return l.Strings(`
			SELECT 'observation ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || status || fact_family || coalesce(parsed::text, '') AS x FROM observation) o
			UNION ALL
			SELECT 'claim ' || count(*) FROM identifier_claim
			UNION ALL
			SELECT 'entity ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || device_key || attributes::text AS x FROM entity) e
			UNION ALL
			SELECT 'decision ' || count(*) FROM entity_decision
			UNION ALL
			SELECT 'judgement ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || classification || active::text AS x FROM snapshot_judgement) j
			UNION ALL
			SELECT 'finding ' || count(*) || ' ' || coalesce(md5(string_agg(x, '|' ORDER BY x)), '')
			  FROM (SELECT id || category || subject_ref AS x FROM finding
			        WHERE category NOT IN ('link_disagreement', 'vlan_mismatch')) f
			ORDER BY 1`)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category NOT IN ('link_disagreement', 'vlan_mismatch')`); n == 0 {
		t.Fatal("no finding from the collector: the finding check proves nothing")
	}
	if len(mismatches(l)) != 1 {
		t.Fatal("no vlan_mismatch: the fixture does not exercise the finding writer")
	}
	before := sum()
	if _, err := graph.Project(context.Background(), l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if after := sum(); !slices.Equal(before, after) {
		t.Errorf("projection changed what it does not own:\nbefore %v\nafter  %v", before, after)
	}
}
