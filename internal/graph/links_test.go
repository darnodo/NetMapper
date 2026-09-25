package graph_test

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/graph"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// links lists "<name> <confidence>" for every l1_link of the snapshot.
func links(l *Lab) []string {
	return l.Strings(`SELECT name || ' ' || confidence FROM edge WHERE type = 'l1_link' ORDER BY 1`)
}

// cabled is the fixture US2 is about: two switches reporting each other on port1.
func cabled(t *testing.T) *Lab {
	return lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
}

// T047, US2-1, FR-009, SC-004: two devices that each reported the other, naming the same pair of
// ports, give one link and not two, marked as agreed by both ends.
func TestAgreedLink(t *testing.T) {
	l := cabled(t)
	want := []string{"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 both_ends"}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want %v", got, want)
	}
	// Both ends are real interfaces, not references to something the projector could not attach.
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'
		AND (from_interface_id IS NULL OR to_interface_id IS NULL)`); n != 0 {
		t.Error("an agreed link has an endpoint that is not an interface")
	}
}

// T048, US2-2, FR-007, FR-008: the link cites the observation from each side, says which side each
// one is, and carries the first and last time its evidence was collected.
func TestAgreedLinkEvidence(t *testing.T) {
	l := cabled(t)
	got := l.Strings(`SELECT ev.side || ' ' || host(o.target) || ' ' || r.command
		FROM edge g JOIN edge_evidence ev ON ev.edge_id = g.id
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
		WHERE g.type = 'l1_link' ORDER BY 1`)
	want := []string{"from 10.0.0.1 display neighbours", "to 10.0.0.2 display neighbours"}
	if !slices.Equal(got, want) {
		t.Errorf("evidence %v, want one row per side %v", got, want)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'
		AND (first_seen IS NULL OR last_seen IS NULL OR first_seen > last_seen)`); n != 0 {
		t.Error("a link does not carry the range its evidence covers")
	}
}

// T049, US2-3, FR-012: a report naming the remote device only by a chassis identifier lands the link
// on the entity that identifier belongs to.
func TestLinkAttachesByChassis(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		// No address at all in sw2's report: the chassis identifier is the only handle it gives.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 - p1 "+MAC("S001")),
	})
	want := []string{"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 both_ends"}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want %v: the chassis identifier did not resolve", got, want)
	}
}

// T050, US2-3, FR-012: a report naming the remote device only by a management address lands the link
// on the entity that answered there.
func TestLinkAttachesByAddress(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		// Four fields: an address and a port, and no chassis identifier.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1"),
	})
	want := []string{"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 both_ends"}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want %v: the management address did not resolve", got, want)
	}
}

// T051, US2-4, FR-011: two devices cabled on more than one port give one link per cable, because a
// link is identified by the pair of interfaces and not by the pair of devices.
func TestTwoCablesBetweenTwoDevices(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p2 sw2 10.0.0.2 p2 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002",
			"p1 sw1 10.0.0.1 p1 "+MAC("S001"), "p2 sw1 10.0.0.1 p2 "+MAC("S001")),
	})
	want := []string{
		"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 both_ends",
		"l1_link:if:" + key("S001") + "/port2|if:" + key("S002") + "/port2 both_ends",
	}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want one per cable %v", got, want)
	}
}

// T052, FR-011, clarified 2026-09-25: one device reporting one cable under two discovery protocols
// gives one edge, which records both protocols and cites both reports. The fakeos recipe declares
// lldp on every row, so the second protocol is added to the parsed rows directly: the point under
// test is the projector's rule, not a pack's ability to speak CDP.
func TestOneCableTwoProtocols(t *testing.T) {
	l := cabled(t)
	ctx := context.Background()
	if _, err := l.DB.Exec(ctx, `
		UPDATE observation
		SET parsed = parsed || jsonb_build_array(jsonb_set(parsed -> 0, '{protocol}', '"cdp"'))
		WHERE fact_family = 'neighbours' AND status = 'collected' AND target = '10.0.0.1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}
	if got := links(l); len(got) != 1 {
		t.Fatalf("links %v, want one: the protocol became part of what identifies a cable", got)
	}
	got := l.Strings(`SELECT attributes ->> 'protocols' FROM edge WHERE type = 'l1_link'`)
	if len(got) != 1 || got[0] != `["cdp", "lldp"]` {
		t.Errorf("protocols %v, want both recorded on the one edge", got)
	}
}

// T053, research R6, R7: a link is oriented by putting the lower endpoint reference first, so it
// carries one name whichever end is read, and the database's own ordering constraint agrees.
func TestLinkOrientation(t *testing.T) {
	l := cabled(t)
	got := l.Strings(`SELECT (from_ref COLLATE "C" < to_ref COLLATE "C")::text || ' ' ||
		(name = type || ':' || from_ref || '|' || to_ref)::text
		FROM edge WHERE type = 'l1_link'`)
	if len(got) != 1 || got[0] != "true true" {
		t.Errorf("orientation %v, want the lower reference first and the name built from both", got)
	}
	// sw1 keys lower than sw2, and sw1's report is the one the lower reference comes from, but the
	// orientation must come from the references and not from which device happened to report.
	refs := l.Strings(`SELECT from_ref || ' ' || to_ref FROM edge WHERE type = 'l1_link'`)
	want := []string{"if:" + key("S001") + "/port1 if:" + key("S002") + "/port1"}
	if !slices.Equal(refs, want) {
		t.Errorf("references %v, want %v", refs, want)
	}
}

// T054, edge case, research R6: a port claiming to see itself is dropped rather than written, and the
// projection still succeeds. This is the tie the ordering rule has to be tested against: two endpoint
// references that are equal, which no ordering can put one before the other.
func TestPortReportingItself(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	})
	if got := links(l); len(got) != 0 {
		t.Errorf("links %v, want none: a port reported itself", got)
	}
	if n := l.Int(`SELECT count(*) FROM projection`); n != 1 {
		t.Error("the projection did not complete")
	}
	// The port itself is still there: only the cable was nonsense, not the interface.
	if n := l.Int(`SELECT count(*) FROM interface WHERE canonical_name = 'port1'`); n != 1 {
		t.Error("the port was dropped along with the report")
	}
}

// T055, edge case, research R10: three devices reporting each other on one port give one link per
// pair, none discarded, and no disagreement: each pair agrees with itself.
func TestSharedMedium(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p1 sw3 10.0.0.3 p1 "+MAC("S003")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002",
			"p1 sw1 10.0.0.1 p1 "+MAC("S001"), "p1 sw3 10.0.0.3 p1 "+MAC("S003")),
		Addr("10.0.0.3"): FakeOS("sw3", "S003",
			"p1 sw1 10.0.0.1 p1 "+MAC("S001"), "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
	})
	got := links(l)
	if len(got) != 3 {
		t.Errorf("%d links, want one per pair of the three: %v", len(got), got)
	}
	for _, s := range got {
		if !strings.HasSuffix(s, "both_ends") {
			t.Errorf("link %q, want every pair agreed", s)
		}
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category = 'link_disagreement'`); n != 0 {
		t.Errorf("%d disagreements raised on a shared segment, want none", n)
	}
}

// T056, FR-015, SC-006: a link whose two endpoints are still the same devices carries the same name
// in two consecutive snapshots of one perimeter, with nothing minted to make that true.
func TestLinkNameIsStableAcrossSnapshots(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}})
	l.Crawl(Doc)
	l.Crawl(Doc)

	if n := l.Int(`SELECT count(*) FROM snapshot`); n != 2 {
		t.Fatalf("%d snapshots, want two", n)
	}
	odd := l.Strings(`SELECT name FROM edge WHERE type = 'l1_link'
		GROUP BY name HAVING count(*) <> 2`)
	if len(odd) != 0 {
		t.Errorf("links not present in both snapshots: %v", odd)
	}
}

// T063, US3-1, FR-010: a report whose remote device no entity matches is still recorded, marked as
// known from one side, and carries the identifiers the report gave for the far end.
func TestUnresolvedFarEnd(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// 10.0.0.9 is inside the perimeter and answers nothing: it is found, tried and never resolved.
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 unmanaged 10.0.0.9 eth0 aa:bb:cc:99:99:99"),
	})
	got := l.Strings(`SELECT confidence || ' ' || to_ref FROM edge WHERE type = 'l1_link'`)
	want := []string{"one_end unknown:chassis_id=aa:bb:cc:99:99:99/eth0"}
	if !slices.Equal(got, want) {
		t.Fatalf("links %v, want %v", got, want)
	}
	attrs := l.Strings(`SELECT (attributes ->> 'remote_system_name') || ' ' ||
		(attributes ->> 'remote_mgmt_address') || ' ' || (attributes ->> 'to_spelling')
		FROM edge WHERE type = 'l1_link'`)
	if len(attrs) != 1 || attrs[0] != "unmanaged 10.0.0.9 eth0" {
		t.Errorf("far end %v, want what the report said about it", attrs)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND to_entity_id IS NOT NULL`); n != 0 {
		t.Error("a link with no matching entity was attached to one anyway")
	}
}

// T064, US3-2, FR-010: a report whose remote device did resolve, but which that device never reported
// back, connects the two and is still marked one-sided rather than agreed.
func TestResolvedFarEndThatNeverReportedBack(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002"), // reports nobody
	})
	want := []string{"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 one_end"}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want %v", got, want)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'
		AND from_entity_id IS NOT NULL AND to_entity_id IS NOT NULL`); n != 1 {
		t.Error("the link does not connect the two entities")
	}
}

// T066, US3-4, FR-016: a one-sided link that both ends report in a later snapshot is agreed there,
// and the earlier snapshot's record is unchanged.
func TestOneSidedThenAgreed(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	sw2 := FakeOS("sw2", "S002")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): sw2,
	}})
	l.Crawl(Doc)
	first := int64(l.Int(`SELECT id FROM snapshot ORDER BY id LIMIT 1`))

	// sw2 learns to report its neighbour, and the perimeter is crawled again.
	sw2.CLI["display neighbours"] = "p1 sw1 ip4 10.0.0.1 " + MAC("S001") + " p1\n"
	l.Crawl(Doc)

	name := "l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1"
	got := l.Strings(`SELECT (g.snapshot_id = $1) || ' ' || g.confidence
		FROM edge g WHERE g.name = $2 ORDER BY g.snapshot_id`, first, name)
	want := []string{"true one_end", "false both_ends"}
	if !slices.Equal(got, want) {
		t.Errorf("the link across two snapshots: %v, want %v", got, want)
	}
}

// T067, research R6: a report that says nothing at all about the far end yields the local port and no
// edge, because there is no endpoint to connect to and inventing one would be the opposite of FR-010.
func TestReportWithNoFarEnd(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 - - - -"),
	})
	if got := links(l); len(got) != 0 {
		t.Errorf("links %v, want none", got)
	}
	if n := l.Int(`SELECT count(*) FROM interface WHERE canonical_name = 'port2'`); n != 1 {
		t.Error("the local port of the report was not created")
	}
}

// T068, edge case, FR-012, clarified 2026-09-25: a chassis identifier that two entities both carry
// attaches the link to neither. Resolution produces that when it refuses to merge a component
// contradicting itself; the entity attributes are edited here rather than a whole contradicting crawl
// being built, because what is under test is the projector's rule.
func TestAmbiguousRemoteIdentifier(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// sw1's reports carry addresses, which is how discovery reaches the other two at all.
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p2 sw3 10.0.0.3 p1 "+MAC("S003")),
		// sw2 names sw1 by its chassis identifier alone, so that is the only handle it offers.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 - p1 "+MAC("S001")),
		Addr("10.0.0.3"): FakeOS("sw3", "S003"),
	})
	ctx := context.Background()
	before := l.Strings(`SELECT confidence FROM edge WHERE type = 'l1_link'
		AND from_ref = $1 AND to_ref = $2`,
		"if:"+key("S001")+"/port1", "if:"+key("S002")+"/port1")
	if len(before) != 1 || before[0] != "both_ends" {
		t.Fatalf("before the collision the cable is %v, want one agreed link", before)
	}

	// sw3 now claims sw1's chassis MAC too, so that identifier names two entities. Resolution produces
	// this shape when it refuses to merge a component contradicting itself; the attributes are edited
	// here rather than a whole contradicting crawl being built, because what is under test is the
	// projector's rule and not how the collision arose.
	if _, err := l.DB.Exec(ctx, `
		UPDATE entity SET attributes = jsonb_set(attributes, '{identifiers,chassis_mac}', to_jsonb($2::text))
		WHERE device_key = $1`, key("S003"), MAC("S001")); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Project(ctx, l.DB, l.Registry, snap(l)); err != nil {
		t.Fatal(err)
	}

	got := l.Strings(`SELECT confidence || ' ' || to_ref FROM edge WHERE type = 'l1_link'
		AND from_ref = $1 ORDER BY 1`, "if:"+key("S002")+"/port1")
	want := []string{"one_end unknown:chassis_id=" + MAC("S001") + "/p1"}
	if !slices.Equal(got, want) {
		t.Errorf("sw2's report after the collision: %v, want %v", got, want)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND from_ref = $1
		AND to_entity_id IS NOT NULL`, "if:"+key("S002")+"/port1"); n != 0 {
		t.Error("an ambiguous identifier was resolved to one of its candidates anyway")
	}
}

// T069, FR-014, SC-007, clarified 2026-09-25: two devices that name one port in common and a
// different port opposite it produce no invented link, keep each side's own report, and raise one
// disagreement.
func TestPortDisagreement(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		// sw2 agrees the cable reaches its own p1, and says the far end is sw1's p2, not p1.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p2 "+MAC("S001")),
	})
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND confidence = 'both_ends'`); n != 0 {
		t.Error("a link was invented that neither side described")
	}
	got := links(l)
	want := []string{
		"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 one_end",
		"l1_link:if:" + key("S001") + "/port2|if:" + key("S002") + "/port1 one_end",
	}
	if !slices.Equal(got, want) {
		t.Errorf("links %v, want each side's own report kept %v", got, want)
	}
	if n := l.Int(`SELECT count(*) FROM finding WHERE category = 'link_disagreement'`); n != 1 {
		t.Errorf("%d disagreements, want exactly one", n)
	}
}

// T070, FR-014, Principle I: the disagreement names both sides and what each said, and cites the
// observation behind each.
func TestDisagreementNamesBothSides(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p2 "+MAC("S001")),
	})
	got := l.Strings(`SELECT f.subject_ref || ' | ' || (f.detail -> 'devices')::text || ' | ' ||
		(f.detail -> 'reports')::text
		FROM finding f WHERE f.category = 'link_disagreement'`)
	if len(got) != 1 {
		t.Fatalf("%d findings, want one", len(got))
	}
	for _, want := range []string{
		"device:" + key("S001"), key("S002"),
		"if:" + key("S001") + "/port1", "if:" + key("S002") + "/port1",
		"if:" + key("S001") + "/port2",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("finding %q does not name %q", got[0], want)
		}
	}
	ev := l.Strings(`SELECT host(o.target) FROM finding f
		JOIN finding_evidence fe ON fe.finding_id = f.id
		JOIN observation o ON o.snapshot_id = fe.snapshot_id AND o.id = fe.observation_id
		WHERE f.category = 'link_disagreement' ORDER BY 1`)
	if !slices.Equal(ev, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Errorf("evidence %v, want the report from each side", ev)
	}
}

// T090, FR-011 as clarified 2026-09-25: one cable reported twice, with the far end's chassis
// identifier spelled two ways, is one edge. An unresolved endpoint is named by that identifier, so the
// reference has to be normalised the way the matcher normalises it to look for an entity, or the same
// box reads as two endpoints and one cable becomes two links.
//
// The lab found this: Arista reports a chassis MAC dotted, as `001c.7374.a126`, while the entity set
// holds `00:1c:73:74:a1:26`. Nothing was broken there, because one pack spells it one way; two
// protocols on one device spelling it differently is what this guards.
func TestUnresolvedFarEndIdentifierIsNormalised(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// One cable from port1 to the same unmanaged box, reported twice: colon form, then the dotted
		// form Arista prints. Same far-end port both times, so it is one cable by any reading.
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 box - eth0 aa:bb:cc:99:99:99", "p1 box - eth0 aabb.cc99.9999"),
	})
	got := links(l)
	want := []string{"l1_link:if:" + key("S001") + "/port1|unknown:chassis_id=aa:bb:cc:99:99:99/eth0 one_end"}
	if !slices.Equal(got, want) {
		t.Errorf("links %v, want the two spellings to name one cable %v", got, want)
	}
	// An address and a hostname are not MACs and must pass through as reported.
	l2 := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 box 10.0.0.9 eth0 -"),
	})
	if got := l2.Strings(`SELECT to_ref FROM edge WHERE type = 'l1_link'`); len(got) != 1 ||
		got[0] != "unknown:mgmt_address=10.0.0.9/eth0" {
		t.Errorf("to_ref %v, want the address kept as reported", got)
	}
}

// T094, FR-009: two reports of one cable produce one link even when only one end names the far-end
// port. `remote_interface` is optional in the fact family, and a device that omits it says "my port1
// faces that device, port unknown", which builds the endpoints `{dev:<key>, if:me/port1}` where the
// other end builds `{if:them/pX, if:me/port1}`. Those used to be two groups and two one-sided edges
// for one cable, against FR-009's "one link, not two, and marked as agreed by both ends".
func TestOneCableWhenOnlyOneEndNamesThePort(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		// The fourth field is "-", so sw2 names sw1 but no port on it.
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 - "+MAC("S001")),
	})
	want := []string{"l1_link:if:" + key("S001") + "/port1|if:" + key("S002") + "/port1 both_ends"}
	if got := links(l); !slices.Equal(got, want) {
		t.Errorf("links %v, want one agreed cable %v", got, want)
	}
	// The portless report is evidence for the same edge, from its own side.
	got := l.Strings(`SELECT ev.side || ' ' || host(o.target)
		FROM edge g JOIN edge_evidence ev ON ev.edge_id = g.id
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		WHERE g.type = 'l1_link' ORDER BY 1`)
	if !slices.Equal(got, []string{"from 10.0.0.1", "to 10.0.0.2"}) {
		t.Errorf("evidence %v, want one row per side", got)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND to_ref LIKE 'dev:%'`); n != 0 {
		t.Error("a dev: endpoint survived alongside the port-named one")
	}
}

// The other half of the rule: the fold happens only when one group qualifies. Two candidates mean the
// device claims two of its ports face one remote port, and picking one would invent a link neither
// side described, so the portless report keeps its own one-sided edge.
func TestPortlessReportIsNotFoldedWhenAmbiguous(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		// sw1 claims both of its ports face the same port of sw2, which is the contradiction.
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"), "p2 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 - "+MAC("S001")),
	})
	if got := links(l); len(got) != 3 {
		t.Errorf("%d links, want sw1's two claims plus sw2's unfolded report: %v", len(got), got)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND from_ref LIKE 'dev:%'`); n != 1 {
		t.Error("the ambiguous portless report was folded into one of the candidates anyway")
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND confidence = 'both_ends'`); n != 0 {
		t.Error("an agreed link was invented from an ambiguous fold")
	}
}

// T095: the near end's spelling is never recorded on an edge, because it cannot differ from the
// canonical name the reference already carries.
func TestEdgeCarriesNoNearSpelling(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 box 10.0.0.9 eth0 aa:bb:cc:99:99:99"),
	})
	if n := l.Int(`SELECT count(*) FROM edge WHERE attributes ? 'from_spelling'`); n != 0 {
		t.Error("an edge carries from_spelling, which can only ever repeat its own reference")
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE attributes ->> 'to_spelling' = 'eth0'`); n != 1 {
		t.Error("the far end's spelling, which is the one that can differ, was lost")
	}
}
