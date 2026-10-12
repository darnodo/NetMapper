# Feature Specification: Project layer 2 domains from VLAN data

**Feature Branch**: `009-l2domain-projection`

**Created**: 2026-10-12

**Status**: Clarified

**Input**: User description: "Implement GitHub issue darnodo/NetMapper#37 (third sub-issue of #34):
project `l2domain` rows from the collected VLAN data. An L2 domain is a connected component, per
VLAN, over the `l1_link` edges whose two ends carry that VLAN. The data model already defines
`l2domain`, `l2domain_member` (with `local_vlan_id`) and `l2domain_link`; none is built today.
Depends on the VLANs sub-issue (#35, feature 008). Does not need the MAC table. Rules to define:
when a link joins two devices in a VLAN domain, native VLAN mismatch, local VLAN per device, a
declared VLAN carried by no link, and what `confidence` means when only one end of a link reported
its VLANs. Open questions: whether a domain carries a name and from where, and how the read API
exposes domains. Testing: graph engine tests with fixture snapshots (two switches with a trunk
sharing VLAN 10, a VLAN pruned on the trunk, an access port in a VLAN that exists on one side only)."

## Decisions taken in this spec

- **A domain is a set of devices, per VLAN, joined by cables.** Two devices are in the same domain
  for a VLAN when a cable between them carries that VLAN at both ends, directly or through other
  devices. The cables are the `l1_link` edges the graph projector already builds; this feature adds
  no new way of finding a cable.
- **A port carries a VLAN tagged or untagged, and the two do not mix.** A trunk carries tagged every
  VLAN the device reports active on it, except its native VLAN. An access port carries its access
  VLAN untagged, and a trunk carries its native VLAN untagged. A cable joins a VLAN when both ends
  carry it tagged, or both carry it untagged. A VLAN tagged at one end and untagged at the other is
  not joined: on the wire, the untagged end's frames land in the other end's native VLAN, not in the
  VLAN with the same number.
- **A trunk carries what the device reports active, not what it allows.** Feature 008 records the
  device's own active list, which already leaves out VLANs not declared locally and VLANs the device
  restricts on that port. A shut trunk has no active list and carries nothing.
- **Port-channels are followed to their members.** Cables are seen on member ports and VLAN settings
  are on the channel (feature 008). A member port carries what its channel carries.
- **A device joins a domain through a declared VLAN.** Every VLAN declared on a device gives that
  device a membership, so a VLAN no cable carries is a domain of one device. A port carrying a VLAN
  the device does not declare does not make the device a member.
- **An untagged mismatch joins the two VLANs and is reported.** Added by clarification. When the two
  ends of a cable carry different VLANs untagged, frames cross from one to the other, so the two are
  one domain. Each member keeps its own VLAN ID, the domain's key is the lowest of them, and the
  cable is reported as a finding so the misconfiguration is not hidden inside a domain.
- **`local_vlan_id` is the VLAN ID on that device.** Feature 008 records no VLAN translation, so in
  this feature it differs from the domain's key only after an untagged mismatch. VNI domains
  (`key_type` `vni`) need VXLAN data (#33) and are not built here; the rules of this feature leave
  room for them.
- **A cable is used only when both ends have VLAN data.** Added by clarification. A device that could
  not be read has no VLAN data and is a member of no domain, so nothing is joined on the other end's
  word alone. The domain on the readable side records that one of its cables went unchecked.
- **Domains are not served through the read API yet.** Added by clarification. The projection builds
  them; serving them waits for #7, the same boundary feature 004 kept for edges.
- **A domain is labelled by the VLAN name its members agree on.** When every member that reports a
  name reports the same one, that name is the label. When they disagree, the domain has no label, and
  each member's own name stays readable from its `vlans` row. Picking one of several names would
  present a guess as a fact.
- **Domains are part of the projection, not a separate step.** They are computed with the
  interfaces and edges of a snapshot, replaced with them, and follow the same rebuild rules (feature
  004).

## Clarifications

### Session 2026-10-12

- Q: Does an untagged mismatch (native 1 facing native 99, or access 10 facing access 20) join the
  two VLANs, keep them apart, or both, and is it reported? → A: Join them into one domain, each
  member with its own `local_vlan_id`, the domain keyed by the lowest VLAN ID, and report a finding
  for the cable.
- Q: When one end of a cable reported its VLANs and the other device could not be read, is the cable
  used to join the domain? → A: No. The readable device's domain does not include the far device, and
  its confidence records that one of its cables was not checked at both ends.
- Q: Is serving domains through the read API part of this feature? → A: No, projection only. Serving
  waits for #7.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See which devices share a broadcast domain (Priority: P1)

An operator crawls a network with VLANs. For each VLAN, NetMapper now says which devices are in the
same broadcast domain, with the cables that join them, and splits a VLAN into several domains when
the same ID is used in places no cable connects.

**Why this priority**: this is the answer #34 was opened for. "These ports are in the same broadcast
domain" cannot be read from per-device VLAN lists, since VLAN 10 on two switches is only the same
domain if something carries it between them.

**Independent Test**: run the graph engine tests on fixture snapshots with no lab deployed: two
switches with a trunk carrying VLAN 10 give one domain of two members; the same VLAN pruned from the
trunk gives two domains of one member each.

**Acceptance Scenarios**:

1. **Given** two switches that both declare VLAN 10 and a trunk between them with VLAN 10 active at
   both ends, **When** the snapshot is projected, **Then** there is one VLAN 10 domain with both
   switches as members and the trunk's edge as its link.
2. **Given** the same two switches with VLAN 10 pruned from the trunk at one end, **When** the
   snapshot is projected, **Then** there are two VLAN 10 domains, one per switch, and neither has a
   link.
3. **Given** a chain of three switches where VLAN 10 is carried sw1 to sw2 and sw2 to sw3, **When**
   the snapshot is projected, **Then** one VLAN 10 domain holds all three.
4. **Given** two switches joined by a port-channel whose members each have a cable, and the
   port-channel trunks VLAN 10, **When** the snapshot is projected, **Then** both switches are in one
   VLAN 10 domain and every member cable is a link of that domain.
5. **Given** a switch whose access port is in VLAN 30, facing a switch whose port is also in access
   VLAN 30 but where VLAN 30 is not declared, **When** the snapshot is projected, **Then** the first
   switch is a VLAN 30 domain of one member and the second switch is in no VLAN 30 domain.
6. **Given** a VLAN declared on a switch and carried by none of its cables, **When** the snapshot is
   projected, **Then** that VLAN is a domain of one member with no link.
7. **Given** `test/lab` crawled, **When** the snapshot is projected, **Then** the domains match the
   VLANs and trunks its configuration describes.

---

### User Story 2 - Know how well a domain is known (Priority: P1)

An operator, or an agent, reads a domain and needs to know whether every cable in it was confirmed
by the VLAN data of both ends, or whether some part rests on one device's word because the other
could not be read.

**Why this priority**: constitution I. A domain built on an assumption about a device NetMapper never
read must not look the same as one both ends confirmed.

**Independent Test**: a fixture snapshot where one switch of a trunk was unreachable: the domain on
the reachable side carries the lower confidence, and its evidence names the observations it rests
on.

**Acceptance Scenarios**:

1. **Given** a domain where every link had VLAN data at both ends, **When** it is read, **Then** its
   confidence says both ends confirmed it.
2. **Given** a trunk where one switch reported VLAN 10 active and the other switch was unreachable,
   **When** the snapshot is projected, **Then** the reachable switch is in a VLAN 10 domain without
   the unreachable one, the trunk is not a link of that domain, and the domain's confidence says one
   of its cables was not checked at both ends.
3. **Given** a domain of one member built from a VLAN that none of the device's cables carry,
   **When** it is read, **Then** its confidence is the same as a fully confirmed domain: there is no
   unchecked cable in it.
4. **Given** any domain, **When** it is read, **Then** the observations behind each membership and
   each link, and when they were collected, can be named without reading a log.

---

### User Story 3 - See untagged traffic leaking between VLANs (Priority: P2)

Two ends of a cable carry untagged traffic in different VLANs: access VLAN 10 facing access VLAN 20,
or two trunks with different native VLANs. Frames from one VLAN reach the other. The operator needs
to see it.

**Why this priority**: it is a real broadcast domain the per-device data hides, and a frequent
misconfiguration. It comes after stories 1 and 2 because those cover the networks that are
configured as intended.

**Independent Test**: a fixture snapshot with a trunk whose native VLAN is 1 at one end and 99 at the
other.

**Acceptance Scenarios**:

1. **Given** a trunk with native VLAN 1 at one end and 99 at the other, **When** the snapshot is
   projected, **Then** one domain holds both switches, with `local_vlan_id` 1 on the first and 99 on
   the second, keyed by VLAN 1, and a finding names the cable, both ends and both VLANs.
2. **Given** an access port in VLAN 10 facing an access port in VLAN 20, **When** the snapshot is
   projected, **Then** the result is the same as scenario 1: one domain keyed by VLAN 10, and a
   finding.
3. **Given** a trunk whose native VLAN is the same at both ends, **When** the snapshot is projected,
   **Then** that VLAN is joined like any other, with no finding.
4. **Given** the mismatch is fixed and the next snapshot is projected, **When** findings are read,
   **Then** that snapshot reports no mismatch, and the earlier snapshot keeps its own.

---

### Edge Cases

- A cable to something that is not a device entity (a host, an unresolved neighbour): it adds no
  member. Domains are made of device entities.
- A cable known from one side only (one `l1_link` end reported it): it joins a VLAN like any other
  cable when both ports' VLAN data is known. Whether the cable is confirmed is the edge's own
  confidence, already readable on the edge.
- A device whose VLAN data is `unsupported`, `parse_failed` or missing: it has no VLAN data and is a
  member of no domain. Cables toward it join nothing and lower the confidence of the domains on the
  readable side that would otherwise use them (a cable carrying the VLAN at the readable end).
- A device in a domain twice through an untagged mismatch (VLAN 1 on sw1 reaches VLAN 99 on sw2,
  which reaches VLAN 99 back on sw1 through another cable): sw1 is a member once per local VLAN, so
  it appears with both 1 and 99 in the same domain.
- An untagged mismatch where one side's VLAN is not declared on its device: not joined, and no
  finding. A port in an undeclared VLAN forwards nothing.
- A device whose `interface_vlans` is `empty` (only routed ports): it carries no VLAN on any cable.
  It is still a member, alone, of each VLAN it declares.
- A trunk allowing a VLAN that the far end does not declare: the far end does not report it active,
  so it is not joined.
- A trunk with native VLAN tagging on: feature 008 does not record it, so the native VLAN is treated
  as untagged.
- Two cables between the same two switches both carrying VLAN 10 (not bundled): one domain, both
  edges are its links. A loop the topology allows is not a domain split; spanning tree is out of
  scope.
- MLAG: two switches seen as one by a host each carry the VLAN on their own cables; with the
  peer-link carrying it, they are one domain like any other trunk.
- A port-channel with no member cabled: it joins nothing. A member whose channel has no VLAN row
  carries nothing.
- Two members of one domain report different names for the VLAN: the domain has no label.
- A device that declares a VLAN twice cannot happen per feature 008 (one row per VLAN ID); nothing
  to handle.
- Domain order and the choice of `key_id` in a mixed domain MUST NOT depend on the order rows
  were read: the same snapshot gives the same domains every time.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The projection MUST build, for each snapshot it projects, one domain per connected
  component of device entities, per VLAN, over the `l1_link` edges that carry that VLAN at both ends.
- **FR-002**: A port MUST be taken to carry a VLAN tagged when it is a trunk and the VLAN is in its
  active list and is not its native VLAN, and untagged when it is its access VLAN, or its native
  VLAN while in its active list. A member of a port-channel MUST carry what its port-channel
  carries.
- **FR-003**: A cable MUST join a VLAN between its two devices only when both devices have VLAN data,
  both ends carry that VLAN with the same tagging, and both devices declare it. The untagged mismatch
  case is settled by FR-012.
- **FR-004**: Every VLAN declared on a device with VLAN data MUST make that device a member of exactly
  one domain for that local VLAN. A VLAN no cable carries MUST give a domain of one member.
- **FR-005**: A device MUST NOT become a member of a domain through a VLAN it does not declare, nor
  through a cable to a non-entity endpoint.
- **FR-006**: Each domain MUST carry its key type (`vlan` in this feature), its key (the lowest
  local VLAN ID among its members), its members with the VLAN ID used on each member device, and the
  edges it was built from.
- **FR-007**: A cable whose far device has no VLAN data MUST NOT join anything. When the readable end
  carries a VLAN on that cable, the domain holding that VLAN on the readable device MUST carry a
  confidence that says one of its cables was not checked at both ends. A domain with no such cable
  MUST carry a confidence that says it is fully checked, whatever the number of its members.
- **FR-008**: A domain MUST be labelled with the VLAN name when every member that reports a name
  reports the same one, and MUST carry no label otherwise.
- **FR-009**: Each membership and each link MUST be traceable to the observations it was deduced
  from, with their collection time (constitution I).
- **FR-010**: Domains MUST be computed in the same projection as the snapshot's interfaces and edges,
  from the same parse generation and entity set, and replaced with them as one set. No consumer MUST
  read a partial set (feature 004, FR-017 to FR-023 apply to domains unchanged).
- **FR-011**: Projecting the same snapshot again from the same inputs MUST give the same domains,
  members, links, keys and labels, whatever order the inputs were read in. Every tie rule MUST be
  tested with inputs that tie.
- **FR-012**: When the two ends of a cable carry different VLANs untagged, both declared on their
  devices, the cable MUST join those two VLANs into one domain, each member keeping its own local
  VLAN ID, and MUST raise a finding naming the cable, both ends and both VLANs. The finding MUST be
  part of the projection's output and replaced with it, as link disagreements are (feature 004).
  A matching untagged VLAN at both ends MUST raise no finding.
- **FR-013**: Domains MUST be derivable from the collected zone and the entity set alone. Losing every
  domain row MUST cost a recomputation and never a re-crawl.
- **FR-014**: The projection MUST NOT contact a device, resolve a secret, or modify an observation,
  edge, entity, judgement or decision. Building domains MUST NOT change the snapshot verdict.
- **FR-015**: No vendor term MUST appear in the domain rules: they read the platform-neutral `vlans`
  and `interface_vlans` families only (constitution V).
- **FR-016**: The graph engine tests MUST cover, from fixture snapshots runnable with no lab: a trunk
  sharing VLAN 10, a VLAN pruned on the trunk, an access port in a VLAN declared on one side only, a
  VLAN no cable carries, a port-channel, a chain of three devices, a tagged-against-untagged end, an
  untagged mismatch, a far end with no VLAN data, and members that disagree on a VLAN name.
- **FR-017**: The data model document, the finding categories it lists, and any guide describing
  what the projection builds MUST be updated in the same change.
- **FR-018**: Domains MUST NOT be served through the read API in this feature. Its existing answers
  MUST be unchanged, except that the new finding category MUST be readable wherever findings
  already are, with its evidence.

### Key Entities

- **Layer 2 domain**: a broadcast domain in one snapshot, keyed by a VLAN ID, with a confidence and
  an optional label. Computed, never collected.
- **Domain member**: a device entity in a domain, with the VLAN ID that domain uses on that device.
- **Domain link**: a cable (`l1_link` edge) that joins two members of a domain.
- **VLAN mismatch finding**: a report that a cable carries different VLANs untagged at its two ends,
  naming the cable, both ends and both VLANs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With no lab running, the graph engine tests cover every case of FR-016 and pass.
- **SC-002**: On a projected snapshot, every VLAN declared on a device with VLAN data appears in
  exactly one domain membership of that device.
- **SC-003**: Two devices are in the same domain if and only if a path of cables carrying the VLAN at
  both ends joins them; checked on every fixture.
- **SC-004**: Projecting a snapshot twice yields identical domains, members, links, keys and labels.
- **SC-005**: For any domain, a reader can name the observations and collection times behind each
  membership and link, and tell a fully confirmed domain from a partly assumed one, without reading
  a log.
- **SC-006**: Every untagged mismatch in a fixture produces exactly one finding and one joined
  domain; a fixture with none produces no mismatch finding.
- **SC-007**: Deleting every domain row and recomputing restores the same domains with no device
  contacted.

## Assumptions

- Feature 008's `vlans` and `interface_vlans` are the only VLAN input. Arista EOS is the only pack
  producing them; other platforms have no VLAN data and their devices are members of no domain.
- The `l2domain`, `l2domain_member` and `l2domain_link` tables are described in the data model but
  not yet created; creating them is part of this feature.
- VLAN translation is not recorded by feature 008, so `local_vlan_id` differs from the key only after
  an untagged mismatch.
- Serving domains through the read API is #7's work. Until then they are readable in the database,
  through the roles that already read the projected tables.
- VNI domains (#33), the MAC table (#36), `attached` edges, spanning tree state and private VLANs are
  out of scope.
- A stable name for a domain across snapshots (the data model's `pinned_name`) is out of scope: no
  operator decision exists to set it yet.
- `test/lab` as left by feature 008 (VLANs, an access port, a trunk with a pruned list, a
  port-channel between sw1 and sw2) is enough for a live check; no lab change is planned.
