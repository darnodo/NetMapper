# Feature Specification: BGP sessions on Arista EOS

**Feature Branch**: `010-bgp-peer-facts`

**Created**: 2026-10-14

**Status**: Draft

**Input**: User description: "darnodo/NetMapper#30, focus on darnodo/NetMapper#31: collect BGP
sessions on Arista EOS as a new platform-neutral fact family, with the rules of the parent issue
(VRF on every row, no secret in recorded output, decide what to do with counters and timers). The
EVPN lab is deployed and generating traffic. Rule for collection: volatile data is not collected
unless the tool needs it (the MAC table is too volatile). The session state (Established, etc.)
changes, but it is a status, not volatile data, so it is collected."

## Decisions taken in this spec

- **One family, `bgp_peers`, one row per peer per VRF.** The issue left open whether a row is a peer
  or a peer and address family. Per-family prefix counts were the only reason to split, and they are
  not kept (next point), so a row is a peer with a list of address families.
- **Two lists of address families: configured and negotiated.** Added by clarification. The
  configured list says what a session should carry, even while it is down. The negotiated list says
  what it carries now: absent (null) when the session is not established, an empty list when it is
  established with no family negotiated. The configured list is never absent, and is empty for a
  peer with no family activated. Both lists are an explicit exception to the feature 008 rule
  that an empty set is an absent field, and the fact families contract says so for this family. A
  family configured but not negotiated on an established session is a capability mismatch, visible
  on the row itself, with no stored mismatch field.
- **The configured list covers a declared set of families.** Added at plan review. A device may
  not print per peer which families are configured, and a pack may read only some of them. Each
  recipe declares the families its configured list covers; an empty list means "none of those",
  not "carries nothing". Both lists are sorted and hold no duplicate, so the order commands run in
  never shows up as a difference between two runs.
- **Dynamic peers are rows, marked as dynamic.** Added by clarification. A peer accepted from a
  listen range is a live adjacency, so it is recorded like a configured peer, with a marker saying
  it is dynamic. The marker tells a later diff that its disappearance is a state change, not a
  configuration change. The listen range itself is not recorded.
- **Session state is collected, counters and timers are not.** The state of a session
  (`established`, `idle`, `active`, and so on) is a status: it changes when something breaks or is
  fixed, not with traffic, and it answers "is this session up" in a snapshot. Uptime, message
  counts, queue depths, prefix counts and last-reset details change between two identical runs and
  are left out, as the MAC table was (#36). That data belongs to telemetry.
- **The state is a platform-neutral value from the BGP state machine.** `idle`, `connect`, `active`,
  `opensent`, `openconfirm`, `established`, or `other`. A peer shut down by configuration is told
  apart from one that is down on its own by a separate field, since both read `idle` on the wire.
- **EVPN overlay sessions are rows like any other.** A session carrying `l2vpn evpn` is a BGP peer
  with that address family. What the overlay carries (routes, learned VTEPs) is not recorded.
- **No secret reaches recorded output.** The family is read from commands that do not print a
  configured password, and the secret check covers the lab's BGP password. Whether a password is
  set is not recorded either: knowing it is configured adds nothing to a topology answer.
- **Nothing configured is `empty`.** Same rule as features 007 and 008: a device with no BGP gives
  an `empty` observation, distinct from `parse_failed`.
- **No graph edge and no discovery from a peer.** A BGP session is a relation between two devices,
  but matching a peer address to a known entity is identity resolution's job and is open in #30. The
  family is not read by the graph engine, and a peer does not queue a `find`: an eBGP peer is often
  outside the perimeter, and `neighbours` already drives discovery.
- **The read interface needs no change.** The per-family endpoint of feature 007 serves any family
  in the schema.

## Clarifications

### Session 2026-10-14

- Q: Are a peer's address families those configured, those negotiated, or both? → A: Both, as two
  lists per row. The negotiated list is absent (null) when the session is not established, never an
  empty list (refined by the third question below). A family configured but not negotiated on an established session is a capability
  mismatch and must be visible as such.
- Q: Is the mismatch stored as its own field? → A: No. It is read by comparing the two lists.
- Q: How does an established session with no family negotiated read? → A: An empty negotiated list.
  Null means not established, negotiated families unknown; empty means established, none
  negotiated.
- Q: When a peer is configured but no address family is activated for it, is its configured list
  empty or absent? → A: Empty. The configured list is never absent. Both lists are an explicit
  exception to the feature 008 empty-set rule. Configured families that could not be collected are
  carried by the observation status, not by the field.
- Q: Are peers accepted from a listen range (dynamic peers) recorded? → A: Yes, as rows marked as
  dynamic. A dynamic peer is a live adjacency; the marker tells the diff that its disappearance is a
  state change, not a configuration change.

### Plan review 2026-10-14

- The configured list covers only the families the recipe reads, and the recipe declares that set.
- The summaries the configured list comes from run across every VRF; core1's sessions in VRF `gold`
  carry their configured families.
- `empty` comes only from the device's exact "BGP not running" output. A denied command or any other
  error is `parse_failed`.
- Both lists are deduplicated and sorted.
- A row that breaks the rules of FR-005a never disappears silently: the observation is
  `parse_failed` and a finding records why.
- Update source is removed from FR-006 and story 1 (agreed).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See the BGP sessions of a device (Priority: P1)

An operator crawls an Arista network. For each device, NetMapper now records its BGP sessions: in
which VRF, the local AS and router ID, the peer's address and AS, whether the session is eBGP or
iBGP, its state, which address families it carries, and the configured peer group and
description. The EVPN overlay sessions are among them. (Update source was dropped during planning,
research R6.)

**Why this priority**: "what does this device peer with, and is it up" is the first question when
tracing a path or checking a migration. It is the reason for the feature.

**Independent Test**: crawl the arista-evpn-vxlan-clab lab and check that a leaf's rows match its
configuration: underlay eBGP to the spines, iBGP to its MLAG peer, EVPN sessions to the spines.
Then run the template tests with the lab stopped: they pass from the recorded fixtures alone.

**Acceptance Scenarios**:

1. **Given** an EVPN lab leaf with underlay sessions to two spines and an iBGP session to its MLAG
   peer, **When** a crawl runs, **Then** `bgp_peers` has one row per session in VRF `default`, with
   local AS, router ID, peer address, peer AS, type `ebgp` or `ibgp`, state `established`, `ipv4 unicast`
   as both configured and negotiated address family, and the peer group from the configuration.
2. **Given** the same leaf with EVPN sessions to the spines' loopbacks, **When** a crawl runs,
   **Then** each has a row with `l2vpn evpn` configured and negotiated.
3. **Given** a device with BGP sessions in a VRF other than `default` (core1, VRF `gold`), **When**
   a crawl runs, **Then** each row names its VRF, the local AS and router ID are those of that VRF,
   and its configured families are recorded as for a session in `default`.
4. **Given** a crawl run twice a few minutes apart with traffic flowing and no change to the
   network, **When** the two observations are compared, **Then** their rows are identical.

---

### User Story 2 - See a session that is not up or not complete (Priority: P1)

A session that should be up and is not is what an operator looks for. NetMapper records it with its
state, and says whether it is down because it was shut down by configuration. A session that is up
but does not carry every family configured for it is the same kind of fault, one level down.

**Why this priority**: a list showing only working sessions hides the one that matters. Same
priority as story 1.

**Independent Test**: crawl `test/lab`, where sw1 and sw2 have one session up, one with a wrong peer
AS, one shut down by configuration, and one family configured on one end only, and check each row's
state, shutdown field and address family lists. Then run the
template tests with the lab stopped.

**Acceptance Scenarios**:

1. **Given** a `test/lab` eBGP session between sw1 and sw2 with a password on both ends, **When** a
   crawl runs, **Then** both ends have a row with state `established`, and the password appears in
   no recorded output.
2. **Given** a session configured with the wrong peer AS, **When** a crawl runs, **Then** the row
   records the configured peer AS, a state other than `established`, and is not marked shut down.
3. **Given** a session shut down by configuration, **When** a crawl runs, **Then** the row reads
   state `idle` and is marked shut down.
4. **Given** a session that is not up, **When** a crawl runs, **Then** the fields learned from the
   peer (its router ID, its negotiated address families) are absent rather than empty or zero, and
   its configured address families are still recorded.
5. **Given** an established session where sw1 configures an address family that sw2 does not,
   **When** a crawl runs, **Then** sw1's row lists that family as configured and not as negotiated,
   and a reader can see the mismatch from that row alone.

---

### User Story 3 - Tell "no BGP" from "not collected" (Priority: P2)

An operator looks at a device that runs no BGP, such as an access switch. They need to know it was
asked and has no BGP, as opposed to the question never being asked or the answer not understood.

**Why this priority**: anything reading this family later (a graph projection, a migration check)
will read absence as "this device has no BGP". That is only safe if absence is recorded.

**Independent Test**: crawl a device with no BGP (an EVPN lab access switch, or sw4 of `test/lab`)
and check the observation is `empty`, and that a device of another platform gives `unsupported`.

**Acceptance Scenarios**:

1. **Given** a device with no BGP configured, **When** a crawl runs, **Then** `bgp_peers` is
   `empty`.
2. **Given** an output the template does not understand, **When** a crawl runs, **Then** the
   observation is `parse_failed`, never `empty`.
3. **Given** a command the device refuses (authorization denied, invalid input) or any error line
   other than the exact "BGP not running" one, **When** a crawl runs, **Then** the observation is
   `parse_failed` with a finding, never `empty`.
4. **Given** a device of another platform, **When** a crawl runs, **Then** `bgp_peers` is
   `unsupported` with detail `no_recipe`, and the snapshot verdict is unchanged.

---

### Edge Cases

- BGP configured with no neighbour: no row, so `empty`. The local AS and router ID of an instance
  with no peer are not recorded in this feature.
- An IPv6 session (peer address IPv6): a row like any other, with the IPv6 address and its address
  families.
- A session carrying several address families (for example `ipv4 unicast` and `l2vpn evpn` on one
  peer): one row, with both in each list.
- A peer activated only for a family the recipe does not cover (for example VPN-IPv4, labeled
  unicast, flowspec): its configured list is empty, which reads "none of the covered families",
  and the family can still appear in its negotiated list. Covering it is a new recipe step later.
- A family negotiated but not configured locally: not expected, since a speaker only negotiates
  what it is configured for. If a device prints it, it is recorded as printed in the negotiated
  list.
- An established session where no configured family is negotiated: not in either lab. The
  negotiated list is empty, not absent, and every configured family is a mismatch.
- A peer configured with every address family deactivated: a row with an empty configured list
  and, if the session is established, an empty negotiated list.
- A peer with no family configured explicitly, relying on the device's default (on most platforms
  `ipv4 unicast`): the configured list holds the family in effect for that peer, not an empty list.
- The same peer address in two VRFs: two rows, told apart by VRF.
- A peer that inherits its AS or description from a peer group: the row records the
  value in effect for that peer, and names the peer group.
- A session that flaps between two crawls: each observation records the state seen at collection
  time. How often a session flaps is telemetry, not recorded.
- A peer configured with no description or peer group: those fields are absent.
- A peer accepted from a listen range (dynamic peer) rather than configured one by one: a row like
  any other, marked as dynamic. When it disconnects it is no longer listed and has no row in the
  next observation; the marker on its last row says that was a state change.
- A dynamic peer whose fields come from the listen range's peer group: recorded as for any peer
  inheriting from a peer group.
- A peer defined by an interface rather than an address (unnumbered BGP): not in either lab. It
  MUST NOT be dropped silently; an output the template cannot read is `parse_failed`.
- A device that prints the configured password in a command the recipe could use: that command is
  not used.
- A state word the pack has no recording of: `other`, never read as `established` or `idle`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The fact schema MUST define one new platform-neutral family, `bgp_peers`, with no
  vendor term in any field name or enum value.
- **FR-002**: `bgp_peers` MUST hold one row per BGP peer per VRF, identified by VRF and peer address.
- **FR-003**: Every row MUST carry its VRF (`default` for the global table), the local AS and router
  ID of the BGP instance in that VRF, the peer address, the peer AS, and the session type (`ebgp` or
  `ibgp`).
- **FR-004**: Every row MUST carry the session state as one of `idle`, `connect`, `active`,
  `opensent`, `openconfirm`, `established`, or `other` for anything else the device prints, and
  whether the peer is shut down by configuration.
- **FR-005**: Every row MUST carry two lists of address families, as platform-neutral names (at
  least `ipv4 unicast`, `ipv6 unicast`, `l2vpn evpn`): the families configured for the peer, and the
  families negotiated with it. The configured list MUST be recorded whatever the session state, is
  never absent, and is an empty list for a peer with no family activated. The negotiated list MUST
  be absent when the session is not `established` (see FR-005a). Both lists are an explicit
  exception to the feature 008 rule that an empty set is an absent field: an empty list is stored
  and means "none". If the configured families cannot be collected, that is carried by the
  observation status (`parse_failed`), never by an absent or empty field.
- **FR-005a**: The negotiated list MUST take one of three forms, each with its own meaning:
  absent (null) when the session is not `established` and its negotiated families are unknown; an
  empty list when the session is `established` and no family was negotiated; a non-empty list
  otherwise. Absent and empty MUST NOT be conflated, in either direction. A family in the configured
  list and not in the negotiated list of an established session is a capability mismatch. It MUST
  be readable from the row alone, by comparing its two lists, with no other family or device
  needed, and is not stored as a field of its own. The pack MUST NOT infer one list from the other.
  A row that breaks these rules MUST NOT be dropped silently: the observation is `parse_failed`
  and a finding records the reason.
- **FR-005c**: The configured list MUST cover only the families the recipe reads, and each recipe
  MUST declare that set, so an empty or partial list can be read against it. A recipe whose steps
  produce a family outside its declared set, or declare a family no step produces, MUST be refused
  when the pack loads.
- **FR-005d**: Both lists MUST be sorted and hold no duplicate, whatever the order of commands or of
  the device's output.
- **FR-005b**: Every row MUST say whether the peer is dynamic (accepted from a listen range) or
  configured one by one. Dynamic peers MUST be recorded, not filtered out.
- **FR-006**: A row MUST carry, when known, the peer's router ID, the peer group and the
  description. A field the device does not report is absent, never empty or zero. The peer's router
  ID is learned from the peer and follows the negotiated list's rule: absent when the session is
  not `established`. (Amended during planning, research R6: the configured update source is not
  printed by the device's peer view, only in its configuration, usually through a peer group; it is
  left for a later change.)
- **FR-007**: A row MUST NOT carry uptime, message, queue or prefix counts, timers, or last-reset
  details. Two crawls of an unchanged network MUST give the same rows.
- **FR-008**: No configured BGP password MUST appear in any recorded output. The recipe MUST use
  commands that do not print it, and the existing secret check MUST cover the BGP password of
  `test/lab`.
- **FR-009**: A device with no BGP peer MUST produce an `empty` observation, not a `collected` one
  with no row. `empty` MUST come only from outputs the pack declares as "nothing here": the
  device's exact "BGP not running" answer, or a peer list with no peer. A refused command, an
  invalid command or any other error output MUST produce `parse_failed`, so a later diff never
  reads an error as sessions that disappeared. An output the template does not understand MUST
  produce `parse_failed`.
- **FR-010**: The Arista EOS pack MUST provide a recipe for the new family; no other pack changes.
  Commands MUST be read only (constitution IV).
- **FR-011**: The schema of the new family MUST be published in the fact families contract in the
  same change as the code.
- **FR-012**: `test/lab` MUST carry BGP configuration on sw1 and sw2: one eBGP session between them
  with a password, one session with a wrong peer AS, one session shut down by configuration, one established
  session with an address family configured on one end only, and one dynamic peer (a listen range
  on one switch that the other connects to).
  What the lab already proves (identification over SSH and SNMP, the denied switch, the serial
  conflict of sw4, the management and layer 2 facts of features 007 and 008 and their `empty`
  cases) MUST keep working.
- **FR-013**: Every row type of stories 1 and 2 (underlay eBGP, iBGP, EVPN overlay, a non-default
  VRF, an IPv6 session if a lab has one, an established session with a password, a session not up,
  a session shut down, an established session with a capability mismatch, a dynamic peer), the
  `empty` case and an error output giving `parse_failed` MUST be covered by a test against output
  recorded from `test/lab` or arista-evpn-vxlan-clab, runnable with no lab deployed.
- **FR-014**: The configuration added to `test/lab` and the way to record fixtures from it MUST be
  documented in the same change (topology comments and the feature quickstart).
- **FR-015**: The new family MUST NOT be read by the graph engine in this feature, MUST NOT queue a
  discovery task, and MUST NOT change the snapshot verdict.
- **FR-016**: The family MUST be readable through the existing per-device, per-family read answer,
  with its status, rows and evidence, with no change to that answer.
- **FR-017**: The README, the how-to guides and the API request collection MUST be updated in the
  same change where they list fact families or what a pack collects.

### Key Entities

- **BGP instance**: the BGP process of one device in one VRF, with its local AS and router ID.
  Carried on each of its peer rows rather than as a row of its own.
- **BGP peer**: a session from one device to one peer address in one VRF, with the peer's AS, the
  session type, its state, whether it is dynamic, its configured and negotiated address families
  and its configured attributes. The same session
  seen from both ends is two rows on two devices; joining them is not this feature's job.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With no lab running, the test suite covers every row type of the family and the
  `empty` case, and passes.
- **SC-002**: On a crawl of `test/lab`, every BGP session configured on sw1 and sw2 appears as
  exactly one row, with the state the lab is built to produce, and the lab's BGP password appears
  in no recorded output.
- **SC-003**: On a crawl of the arista-evpn-vxlan-clab switches, when that lab is running, every
  device has a `bgp_peers` observation and none is `parse_failed`; the number of rows per leaf
  matches the sessions in its configuration.
- **SC-004**: Two crawls of the EVPN lab a few minutes apart, with traffic flowing and no change to
  the network, give identical `bgp_peers` rows on every device.
- **SC-005**: For every session of `test/lab`, a reader can tell from the stored row alone whether
  it is up, whether it was shut down by configuration, and which configured families it fails to
  carry.
- **SC-006**: A device of another platform gets one `unsupported` (`no_recipe`) observation and the
  same snapshot verdict as before the feature.

## Assumptions

- The arista-evpn-vxlan-clab lab (on the NetLab VM) is the fixture source for underlay, iBGP and
  EVPN overlay sessions. Checked on 2026-10-14: the leaves and border leaves peer in VRF `default`
  only; core1 has a session in VRF `gold`, which gives the non-default VRF case; dc-access1 runs no
  BGP and gives the `empty` case. No device of that lab has an IPv6 session or a listen range, so the dynamic peer case comes
  from `test/lab` only.
- `test/lab` has no BGP today and runs either on NetLab or locally in OrbStack. Which interfaces and
  addresses carry its sessions is a planning choice. sw3 (denied) cannot serve.
- The collector's existing SSH account is enough to read BGP state; no new credential or privilege
  is needed.
- A session's state is treated as a status, not volatile data: it changes on failure or repair, not
  with traffic, so it does not break snapshot diffs the way the MAC table would.
- OSPF (#32), the routing table, EVPN routes and learned VTEPs, IS-IS, static routes, BFD, and
  other platforms are out of scope.
- A dynamic peer is a live adjacency and is expected to become part of the graph, like configured
  peers, once #30 settles how sessions become edges. This feature only records the marker.
- Whether a BGP session becomes a graph edge, and how a peer address is matched to an entity, stay
  open in #30 and are separate work.
