# Feature Specification: VLANs and interface VLAN membership on Arista EOS

**Feature Branch**: `008-vlan-membership-facts`

**Created**: 2026-10-09

**Status**: Planned

**Input**: User description: "Implement GitHub issue darnodo/NetMapper#35 (first sub-issue of #34):
collect declared VLANs and which ports carry them on Arista EOS, as two new platform-neutral fact
families (`vlans`, `interface_vlans`), then Arista recipes and templates. Access ports, trunk ports
(native, allowed and active VLANs) and port-channels. Internal VLANs EOS allocates for routed ports
are not declared VLANs. Test data: templates tested against outputs recorded from
arista-evpn-vxlan-clab, and `test/lab` gets a few VLANs, one access port and one trunk with a native
VLAN and a pruned allowed list, so there is a fixture with no remote lab."

## Decisions taken in this spec

- **Two families, as the issue proposes.** A VLAN and a switched port share no field. `vlans` has
  one row per declared VLAN, `interface_vlans` one row per switched port. The MAC table (#36) and the
  `l2domain` projection (#37) are separate work and are not started here.
- **Port membership comes from the per-port views, not from the per-VLAN port list.** The per-VLAN
  list says which ports are in a VLAN but not which one is native or what a trunk allows. The
  device's port status table (mode, access VLAN, port-channel membership) and its trunk view
  (native, allowed and active VLANs) give all of it, so `vlans` carries no port list and there is
  one source of truth for membership. The switchport view was set aside during planning: it prints
  port-channel members as plain access ports (research R2).
- **Internal VLANs are left out.** A VLAN the device allocates on its own for a routed port is not
  declared, and recording it as a VLAN would create broadcast domains that do not exist. They are
  not rows of `vlans`.
- **VLAN settings of a port-channel are recorded on the port-channel.** A member port takes its VLAN
  settings from its channel. The member still gets a row, which names its channel and carries no VLAN
  fields. The `l2domain` projection (#37) needs that link: LLDP links are seen on member ports and
  VLANs are configured on the channel, so without it the two cannot be joined.
- **STP forwarding state is out of scope.** The trunk view also prints which VLANs are forwarding.
  That is spanning tree state, excluded by #34, so only allowed and active VLANs are recorded.
- **Nothing configured is `empty`.** Same rule as feature 007: a device with no switched port gives
  an `empty` `interface_vlans` observation, distinct from `parse_failed`.
- **Allowed and active VLANs are a normalized list of ranges.** Added by clarification: an
  expanded default trunk is 4094 values per port, and a device's own range string depends on the
  vendor. A sorted, merged list of ranges is short and the same for every pack.
- **The read interface needs no change.** The per-family endpoint of feature 007 serves any family
  in the schema, so both new families can be read through it, with evidence, on the day they exist.

## Clarifications

### Session 2026-10-09

- Q: How are a trunk's allowed and active VLANs stored: expanded IDs, a normalized list of ranges,
  or the device's range string? → A: A normalized list of ranges (sorted, merged, single IDs or
  inclusive ranges), for example `["1-9", "11-4094"]`.
- Q: Are a trunk's active VLANs recorded as the device prints them, or computed by the pack from
  the allowed list and `vlans`? → A: As the device prints them, normalized into the range-list
  form. The pack computes nothing.
- Q: What status does a VLAN shut down locally on one switch get? → A: A fourth value, `shutdown`:
  the enum is `active`, `suspended`, `shutdown`, `other`. (Recording showed EOS has no local VLAN
  shutdown; the value stays for other platforms, research R6.)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See the declared VLANs of a device (Priority: P1)

An operator crawls an Arista network. For each device, NetMapper now records the VLANs declared on
it, with their ID, name and status, and nothing for the VLANs the device allocates internally.

**Why this priority**: every later layer 2 step (MAC table per VLAN, `l2domain`, VXLAN VNI to VLAN
mapping in #33) refers to a VLAN by its ID on a device. Without the list of declared VLANs nothing
else in #34 can start.

**Independent Test**: deploy `test/lab`, crawl it, and check that the `vlans` rows of sw1 and sw2
match the VLANs in their configuration, the default VLAN included. Then run the template tests with
the lab stopped: they pass from the recorded fixtures alone.

**Acceptance Scenarios**:

1. **Given** a `test/lab` switch with a few named VLANs, one of them suspended, **When** a crawl
   runs, **Then** the `vlans` observation is `collected` with one row per VLAN giving ID, name and
   status, the default VLAN 1 included, and the suspended VLAN reads as `suspended`. (EOS cannot
   shut a VLAN down locally, so `shutdown` is not produced by this lab; research R6.)
2. **Given** a switch with a routed port, for which the device has allocated an internal VLAN,
   **When** a crawl runs, **Then** that internal VLAN is not a row of `vlans`.
3. **Given** the arista-evpn-vxlan-clab lab running, **When** it is crawled, **Then** the devices
   give the VLANs their configuration declares, with the same templates.

---

### User Story 2 - See which VLANs each port carries (Priority: P1)

For each switched port, the operator sees whether it is access or trunk, its access VLAN or its
native VLAN, which VLANs the trunk allows and which of those are active. A port-channel carries its
own VLAN settings, and each member port says which channel it belongs to.

**Why this priority**: the `l2domain` projection is built from this: two ports joined by a link are
in the same broadcast domain for a VLAN only if both carry it. Same priority as story 1, since one
is not useful without the other.

**Independent Test**: crawl `test/lab` and check that the access port, the trunk and the
port-channel of sw1 and sw2 give the rows their configuration describes. Then run the template tests
with the lab stopped.

**Acceptance Scenarios**:

1. **Given** a `test/lab` switch with an access port in a named VLAN, **When** a crawl runs,
   **Then** `interface_vlans` has a row for that port with its canonical name, mode `access` and the
   access VLAN.
2. **Given** a trunk between sw1 and sw2 with a native VLAN other than 1 and an allowed list pruned
   to a few VLANs and a range, **When** a crawl runs, **Then** each end has a row with mode `trunk`,
   the native VLAN, the allowed VLANs as configured and the active VLANs as the device reports them.
3. **Given** a trunk that allows every VLAN (the default), **When** a crawl runs, **Then** its
   allowed VLANs read as the full range 1 to 4094, not as an empty list.
4. **Given** a port-channel in trunk mode with member ports, **When** a crawl runs, **Then** the
   port-channel has the trunk row, and each member has a row naming the port-channel and no VLAN
   fields.
5. **Given** a routed port or the management interface, **When** a crawl runs, **Then** it has no
   `interface_vlans` row.
6. **Given** the arista-evpn-vxlan-clab lab running, **When** it is crawled, **Then** its MLAG
   peer-links and host-facing port-channels give the rows their configuration describes.

---

### User Story 3 - Tell "no switched port" from "not collected" (Priority: P2)

An operator looks at a device that only routes, such as a spine with only routed ports. They need to
know the device was asked and has no switched port, as opposed to the question never being asked or
the answer not being understood.

**Why this priority**: the `l2domain` projection will read absence as "this device is not part of
any VLAN domain". That is only safe if absence is recorded explicitly.

**Independent Test**: crawl a device with no switched port (an EVPN lab spine, or a fixture of one)
and check the `interface_vlans` observation is `empty`, and that a device of another platform gives
`unsupported`.

**Acceptance Scenarios**:

1. **Given** a device with no switched port, **When** a crawl runs, **Then** `interface_vlans` is
   `empty`.
2. **Given** an output the template does not understand, **When** a crawl runs, **Then** the
   observation is `parse_failed`, never `empty`.
3. **Given** a device of another platform, **When** a crawl runs, **Then** both new families are
   `unsupported` with detail `no_recipe`, and the snapshot verdict is unchanged.

---

### Edge Cases

- A trunk whose allowed list is empty (`none`): allowed VLANs and active VLANs are both absent from
  its row. That is different from the default, which allows every VLAN (`["1-4094"]`).
- A trunk allowing a VLAN that is not declared on the switch: the VLAN is in the allowed list, and
  the active list is whatever the device reports (expected: not in it).
- A trunk allowing every VLAN while the device restricts some of them on that port (for example a
  VLAN bound to a trunk group, as on MLAG peer-links): allowed reads `["1-4094"]` and active holds
  only what the device reports as active. No trunk group is recorded; the active list already
  reflects it.
- A native VLAN that is not in the allowed list: recorded as configured. Pointing out the mismatch
  is compliance work, out of scope.
- A trunk with native VLAN tagging on: the native VLAN is still recorded. Whether it is tagged is
  not recorded in this feature.
- An access port whose access VLAN is not declared: the row records the VLAN as configured; `vlans`
  does not list it.
- An Ethernet port left unconfigured: on EOS it is a switched port in access VLAN 1 by default, so
  it has a row like any access port. Unused ports are not filtered out.
- A port shut down administratively: still has its row. VLAN membership is configuration, not link
  state, and link state is already in `interfaces`. A shut trunk keeps its row with its mode only:
  the device reports VLAN lists for active trunks only (research R2).
- A port in a mode other than access and trunk: on EOS a dot1q-tunnel port is printed like an
  access port in its outer VLAN and is recorded as `access` in that VLAN, which is the VLAN it is a
  member of (research R3). Any mode word the pack has no recording of is `parse_failed`, never read
  as an access port.
- VLANs the device creates dynamically (for example for VXLAN or MLAG) rather than from
  configuration: recorded as rows if the device lists them among its VLANs, with the status it
  prints. Internal VLANs for routed ports are the only ones excluded.
- A VLAN shut down locally on one switch, on a platform that has it: status `shutdown` on that
  switch, whatever its state elsewhere. EOS has no local VLAN shutdown (research R6).
- A VLAN with no name configured: the row carries the name the device prints, which on EOS is a
  default such as `VLAN0010`.
- A port-channel with no member up, or a member port that is down: rows are still recorded as for
  any other port.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The fact schema MUST define two new platform-neutral families, `vlans` and
  `interface_vlans`, with no vendor term in any field name or enum value.
- **FR-002**: `vlans` MUST hold one row per declared VLAN, with its ID (1 to 4094), its name and
  its status. Status MUST take a platform-neutral value: `active`, `suspended`, `shutdown` (the
  VLAN is shut down locally on that device), or `other` for any state the device prints that is none
  of these.
- **FR-003**: `vlans` MUST NOT hold the internal VLANs a device allocates for routed ports.
- **FR-004**: `interface_vlans` MUST hold one row per switched port, port-channels included,
  identified by its interface name canonicalised by the pack's naming rules, so it joins the
  `interfaces` and `neighbours` families.
- **FR-005**: An `interface_vlans` row MUST carry the port's mode (`access`, `trunk`, or the mode as
  the device names it otherwise), the access VLAN for an access port, and for a trunk the device reports as active the native
  VLAN, the allowed VLANs and the active VLANs; a trunk that is not active carries its mode only,
  and `interfaces.oper_state` says why (amended during implementation, research R2). Active VLANs are the device's own report of which
  allowed VLANs are active on the port, recorded as printed (normalized per FR-006); the pack MUST
  NOT compute them from the allowed list or from `vlans`.
- **FR-006**: The allowed and active VLANs MUST be stored as a list of ranges in one
  platform-neutral form that does not depend on how a vendor prints them: each item is a single ID
  (`10`) or an inclusive range (`30-40`), items sorted ascending, adjacent and overlapping ranges
  merged, so one set of VLANs has exactly one form (`["1-9", "11-4094"]`). VLAN IDs are not
  expanded one by one: a default trunk is one item, not 4094.
- **FR-007**: "Every VLAN allowed" and "no VLAN allowed" MUST read differently: the first as the
  full range `["1-4094"]`, the second as an absent allowed list on a trunk row (amended during
  planning, research R4: an empty list is never stored, as for every list field).
- **FR-008**: A member port of a port-channel MUST have a row that names its port-channel
  (canonical name) and carries no VLAN fields. The VLAN settings are on the port-channel's row.
- **FR-009**: Routed ports and management interfaces MUST NOT have an `interface_vlans` row.
- **FR-010**: A family with nothing to report MUST produce an `empty` observation; an output the
  template does not understand MUST produce `parse_failed`.
- **FR-011**: The Arista EOS pack MUST provide a recipe for each new family; no other pack changes.
  Commands MUST be read only (constitution IV).
- **FR-012**: The schemas of the new families MUST be published in the fact families contract in
  the same change as the code.
- **FR-013**: `test/lab` MUST carry VLAN configuration on sw1 and sw2: a few named VLANs including a
  suspended one, an access port, a trunk between them with a native VLAN other than 1 and a pruned
  allowed list including a range, and a port-channel in trunk mode with at least one member. What
  the lab already proves (identification over SSH and SNMP v2c and v3, the denied switch, the serial
  conflict of sw4, the management facts of feature 007 and their `empty` cases on sw1) MUST keep
  working.
- **FR-014**: Every row type of stories 1 and 2 (VLAN in each status the lab can produce, access port, trunk with pruned list, trunk
  allowing every VLAN, port-channel, member port) and the excluded internal VLAN MUST be covered by
  a template test against output recorded from `test/lab` or arista-evpn-vxlan-clab, runnable with
  no lab deployed.
- **FR-015**: The configuration added to `test/lab` and the way to record fixtures from it MUST be
  documented in the same change (topology comments and the feature quickstart).
- **FR-016**: The new families MUST NOT be read by the graph engine in this feature, and MUST NOT
  change the snapshot verdict.
- **FR-017**: Both families MUST be readable through the existing per-device, per-family read
  answer, with their status, rows and evidence, with no change to that answer.
- **FR-018**: The README, the how-to guides and the API request collection MUST be updated in the
  same change where they list fact families or what a pack collects.

### Key Entities

- **Declared VLAN**: a VLAN that exists on one device, identified by its ID on that device, with a
  name and a status. The same ID on two devices is not yet the same broadcast domain; that is the
  `l2domain` projection's job.
- **Switched port**: a port or port-channel that carries layer 2 traffic, with its mode and the VLANs
  it carries (access VLAN, or native, allowed and active VLANs on a trunk).
- **Port-channel membership**: a member port and the port-channel whose VLAN settings it follows.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With no lab running, the test suite covers every row type of the two families and the
  internal VLAN exclusion, and passes.
- **SC-002**: On a crawl of `test/lab`, every VLAN declared on sw1 and sw2 appears as exactly one
  `vlans` row, every switched port and port-channel as exactly one `interface_vlans` row, and no
  internal VLAN appears.
- **SC-003**: On a crawl of the 28 arista-evpn-vxlan-clab switches, when that lab is running, every
  device has an observation for both new families and none is `parse_failed`.
- **SC-004**: A device of another platform gets two `unsupported` (`no_recipe`) observations and
  the same snapshot verdict as before the feature.
- **SC-005**: For every trunk of `test/lab`, a reader can tell from the stored facts alone whether
  a given VLAN ID is allowed and whether it is active, without knowing how the device prints ranges.

## Assumptions

- `test/lab` has one link between sw1 and sw2 today. Building a port-channel there needs at least a
  second link between them; adding it is part of this feature. It adds LLDP neighbours to a live
  crawl of the lab; recorded fixtures of earlier features are not affected.
- Which ports of `test/lab` carry the access VLAN, the trunk and the port-channel is a planning
  choice. sw3 (denied) cannot serve, since nothing is collected from it.
- The arista-evpn-vxlan-clab lab (cEOS 4.36.0F) is not always running. It is a second fixture source
  and a check on 28 switches when available. Its spines are expected to have only routed ports and
  to give the `empty` case of story 3.
- The collector's existing SSH account (privilege 15, read-only role) is enough to read VLAN and
  switchport state; no new credential or privilege is needed.
- No VLAN or switchport data is a secret, so feature 007's secret check is not extended to these
  recipes.
- The existing list-of-strings field type can hold the allowed and active VLANs. Checking that each
  item is a valid, normalized range is part of this feature.
- The MAC table (#36), the `l2domain` projection (#37), VXLAN (#33), STP state, private VLANs and
  VLAN translation are out of scope. VLAN translation and private VLAN settings are not recorded even
  if the device prints them.
- Other platforms are separate work, once these schemas are settled on Arista.
