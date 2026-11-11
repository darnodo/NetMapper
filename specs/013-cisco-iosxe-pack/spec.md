# Feature Specification: Cisco IOS-XE platform pack

**Feature Branch**: `013-cisco-iosxe-pack`

**Created**: 2026-10-21

**Status**: Draft

**Input**: User description: "darnodo/NetMapper#60. NetLab is up, but a topology with one or two
Catalysts and an Arista is needed to validate the links and the crawl between two devices. The
cisco_cat9k docker image is available on NetLab. Account for a boot time of 5 to 10 minutes."
Issue #60: NetMapper only knows Arista EOS, so a Cisco Catalyst switch in the path stops the crawl.
A pack for Cisco IOS-XE should let a Catalyst 9300 or 9500 be discovered like the Arista ones:
system info, LLDP neighbours, interfaces, and so on. A Cat9kv runs in containerlab to test against.
There is no virtual C9200, so that one needs fixtures from real hardware later.

## Clarifications

### Session 2026-10-21

- Q: What privilege level should the netmapper account on the Catalyst have? → A: Privilege 1 (the default user level); a command it refuses makes its family `parse_failed` (amended 2026-10-21 after research R4: IOS-XE answers a refused command like an unknown one, and the collector gives `denied` only when a session fails to open), and the gap is documented.
- Q: Should the VLANs IOS-XE always lists (1002 to 1005) be reported in the `vlans` family? → A: No; they are left out, like Arista's internal VLANs, and the pack documents it.
- Q: How should this feature handle a Catalyst 9300 stack? → A: Out of scope: a stack is a single device like any other, with no stack-specific logic; the switchover caveat (serial and base MAC may change) is a known gap.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A Catalyst in the path no longer stops the crawl (Priority: P1)

An operator seeds a crawl on an Arista switch whose LLDP neighbour is a Catalyst 9000 running
IOS-XE, and behind that Catalyst sits another device. Today the Catalyst is recorded as
`unknown_platform` and the crawl ends there. With the pack, the Catalyst is identified as IOS-XE,
its identifiers are claimed, its LLDP neighbours and interfaces are collected, and the crawl goes on
to the devices it reports.

**Why this priority**: this is the issue. A campus with Catalysts in it cannot be mapped at all
today; every other family is secondary to getting through the device.

**Independent Test**: crawl the mixed lab from the first Arista and check that the closed snapshot
holds the Catalyst as an identified device, an edge between each Arista and the Catalyst that both
ends agree on, and the second Arista as discovered through the Catalyst.

**Acceptance Scenarios**:

1. **Given** a Catalyst reachable over SSH with the lab credentials, **When** a `find` runs on it,
   **Then** its `identity` observation is `collected` with platform set to the IOS-XE pack and the
   IOS-XE version filled, and its serial number and chassis MAC are written as strong identifier
   claims.
2. **Given** a Catalyst that answers SNMP and SSH, **When** it is fingerprinted, **Then** both
   transports lead to the same pack and the same version.
3. **Given** an Arista and a Catalyst connected by one link with LLDP running on both, **When** the
   crawl closes, **Then** the graph holds one edge between the two interfaces, backed by the LLDP
   observation of each side, and the interface names on the Catalyst side are in canonical form on
   both the Catalyst's own rows and the Arista's neighbour row.
4. **Given** a Catalyst whose LLDP neighbour is a device the crawl has not seen, **When** the
   Catalyst's `neighbours` observation is written, **Then** a `find` is queued for that neighbour's
   management address, subject to the perimeter as for any other pack.
5. **Given** a Catalyst, **When** its `interfaces` family is collected, **Then** every physical,
   port-channel, VLAN and loopback interface appears once with its description, admin state, oper
   state and speed when the device reports one.

---

### User Story 2 - Layer 2 facts on a Catalyst (Priority: P2)

An operator looks at the L2 domains of a campus and expects the Catalyst's VLANs and its trunk and
access ports to be part of them, the same way the Arista ones are.

**Why this priority**: Catalysts are mostly access and distribution switches, so their VLAN facts
are what makes L2 domains across a mixed campus correct. It depends on story 1 to reach the device.

**Independent Test**: on the mixed lab, configure a trunk with a native VLAN and a pruned allowed
list between the Arista and the Catalyst, crawl, and check that the L2 domain built for a VLAN
spans both devices.

**Acceptance Scenarios**:

1. **Given** a Catalyst with VLANs configured, **When** `vlans` is collected, **Then** every
   configured VLAN appears with its id, name and state. The legacy default VLANs 1002 to 1005,
   which IOS-XE always lists as `act/unsup`, are not rows.
2. **Given** a trunk between the Arista and the Catalyst with the same native VLAN and allowed list
   on both sides, **When** the crawl closes, **Then** `interface_vlans` on each side gives the same
   mode, native VLAN and allowed list, and the L2 domain for each allowed VLAN covers both devices.
3. **Given** a Catalyst port in access mode, **When** `interface_vlans` is collected, **Then** the
   row gives the access VLAN.

---

### User Story 3 - Families the pack does not cover yet (Priority: P3)

An operator crawling a Catalyst sees the management families (`snmp`, `aaa_servers`,
`local_users`, `management_apis`, `aaa_methods`, `management_servers`) and the routing families
(`bgp_peers`, `ospf_interfaces`, `ospf_neighbours`) reported as not covered, rather than missing or
wrong. Filling them is a later change.

**Why this priority**: the map is correct without these families; what matters here is that their
absence is stated and does not affect the snapshot.

**Independent Test**: crawl the mixed lab and check that every one of these families has an
observation for each Catalyst, `unsupported` with `no_recipe`, and that the snapshot verdict is the
same as with them left out.

**Acceptance Scenarios**:

1. **Given** a Catalyst, **When** it is collected, **Then** each management and routing family is
   `unsupported` with detail `no_recipe`.
2. **Given** a crawl where every Catalyst is identified, **When** the snapshot closes, **Then** the
   `no_recipe` families do not change its verdict.

---

### User Story 4 - A lab to test against (Priority: P1)

The maintainer needs a containerlab topology on NetLab with a Catalyst 9000 virtual switch between
two Arista nodes, to record fixtures and to run a live crawl across platforms.

**Why this priority**: without it, nothing in stories 1 to 3 can be recorded or checked live. It is
P1 with story 1.

**Independent Test**: deploy the topology on NetLab, wait for every node to be ready, and check
from each Arista node that the Catalyst is an LLDP neighbour and from the Catalyst that both Arista
nodes are.

**Acceptance Scenarios**:

1. **Given** NetLab with the cat9k image, **When** the topology is deployed, **Then** every node
   comes up with the configuration it needs for the crawl (management address, the netmapper
   account, SNMP, LLDP) without a manual step on the device.
2. **Given** a deployment just started, **When** a Catalyst node has not finished booting, **Then**
   the documented procedure says how to tell it is ready and nothing is recorded or crawled before
   that.
3. **Given** the existing Arista test lab, **When** the new topology is deployed alongside it,
   **Then** neither changes the other's addresses or names.

### Edge Cases

- **Catalyst still booting**: a Catalyst that is up as a container but not yet answering SSH or
  SNMP during its boot (12 to 16 minutes measured on NetLab) is `unreachable` like any silent device. The lab procedure
  waits for readiness rather than relying on the crawl retrying.
- **LLDP off by default**: IOS-XE does not run LLDP unless it is enabled globally. A Catalyst with
  LLDP off has an `empty` neighbours family over LLDP, and the Arista sees no neighbour on that
  link either.
- **CDP-only neighbours**: a Catalyst with CDP on and LLDP off, or a Cisco phone or access point
  that only speaks CDP, shows no neighbour. CDP is not collected in this feature; neighbours come
  from LLDP only, as on Arista.
- **Interface name spellings**: IOS-XE names ports `GigabitEthernet1/0/1`,
  `TenGigabitEthernet1/1/1`, `TwentyFiveGigE1/1/1` and others, abbreviated as `Gi`, `Te`, `Twe`,
  `Hu` and so on in LLDP and in some show commands. Every spelling maps to one canonical name so
  an edge seen from both sides matches.
- **Stacks**: a Catalyst 9300 stack is treated as a single device like any other, with no
  stack-specific logic: it is identified by the serial and base MAC its `show version` gives. After
  a switchover, the serial and base MAC may change, and the stack may then be seen as a new device.
  This is a known gap, not a tested behaviour.
- **Privilege level**: the netmapper account runs at privilege 1. A show command refused at that
  level prints `% Invalid input detected`, like an unknown command, so its family is
  `parse_failed`, with the device's answer kept in the raw output. The refusal is listed as a
  known gap.
- **Version differences**: output formats that change between IOS-XE releases are handled by
  version ranges on the recipe; a device on a release the pack does not cover gets
  `no_matching_version`, not wrong rows.
- **Arista and Catalyst disagree on a trunk**: a native VLAN or allowed list mismatch between the
  two ends is reported as each side states it; the pack does not reconcile them.
- **C9200**: no virtual image exists. The pack may fingerprint it, but no family is claimed as
  supported on it until fixtures from real hardware exist.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST ship a platform pack for Cisco IOS-XE that identifies Catalyst 9300
  and 9500 switches over SSH and over SNMP, and records the IOS-XE version.
- **FR-002**: The pack MUST claim the serial number and the chassis MAC (the base MAC on IOS-XE)
  as strong identifiers and the hostname as a weak one, as the Arista pack does.
- **FR-003**: The pack MUST fill the `neighbours` family from LLDP only, with the remote
  management address and its type, so the crawl continues past a Catalyst. CDP is out of scope.
- **FR-004**: The pack MUST fill the `interfaces` family.
- **FR-005**: The pack MUST map every IOS-XE interface name spelling, long and abbreviated, to one
  canonical name, so that an Arista's LLDP row about a Catalyst port and the Catalyst's own rows
  name the same port the same way.
- **FR-006**: The pack MUST fill `vlans` and `interface_vlans` following the fact-family contract.
  The legacy default VLANs 1002 to 1005 are not `vlans` rows, as for VLANs a device creates on
  its own; the pack README states it.
- **FR-007**: The management families (`snmp`, `aaa_servers`, `local_users`, `management_apis`,
  `aaa_methods`, `management_servers`) and the routing families (`bgp_peers`, `ospf_interfaces`,
  `ospf_neighbours`) are out of scope: the pack defines no recipe for them, and they are
  `unsupported` with `no_recipe` on a Catalyst.
- **FR-008**: Every command in the pack MUST be read-only, and the pack MUST declare the read-only
  prefixes that enforce it.
- **FR-009**: Adding the pack MUST NOT require a change to transports, worker pool, resolver,
  projector or API.
- **FR-010**: Every recipe MUST be tested from recorded output of a real or virtual Catalyst
  committed with the pack, with no lab needed to run the tests.
- **FR-011**: A family the pack does not implement MUST be `unsupported` with `no_recipe`; a family
  whose recipe has no version range matching the device MUST be `unsupported` with
  `no_matching_version`.
- **FR-012**: The repository MUST provide a containerlab topology for NetLab with two Arista nodes
  and one Catalyst 9000 virtual switch between them (Arista, Catalyst, Arista), linked with LLDP
  enabled on every side and configured at deploy time with what the crawl needs. The second Arista
  is reachable from the seed only through the Catalyst's LLDP neighbours, so the crawl proves it
  goes through the Catalyst.
- **FR-013**: The lab documentation MUST state the Catalyst boot time (12 to 16 minutes measured
  on NetLab), how to check that a node is ready, and how to record outputs from it.
- **FR-014**: The pack documentation MUST list the IOS-XE release it was recorded on, the platforms
  it was tested on, and its known gaps, including:
  - C9200: no virtual image, no family claimed as supported until fixtures from real hardware
    exist;
  - IOS-XE has CDP on and LLDP off by default, so a Catalyst without `lldp run` shows no
    neighbours; CDP is not collected;
  - the management and routing families have no recipe yet;
  - a command refused at privilege 1 is `parse_failed`, not `denied`: IOS-XE answers it with
    `% Invalid input detected`, the same as an unknown command;
  - stacks: a stack is treated as a single device with no stack-specific logic; after a
    switchover, its serial and base MAC may change, so the stack may be seen as a new device.
- **FR-015**: The README and the how-to guides MUST be updated in the same change to list IOS-XE as
  a supported platform.
- **FR-016**: The netmapper account on a Catalyst MUST be privilege 1, with no access to
  configuration mode, in the lab configuration and in the deploy guide. The pack MUST NOT need a
  higher privilege for the families of this feature.

### Key Entities

- **IOS-XE pack**: the platform pack for Cisco Catalyst 9000 switches: fingerprint rules,
  identifier rules, interface name rules, recipes and templates, with recorded outputs for tests.
- **Mixed lab topology**: the NetLab containerlab topology joining Arista and Catalyst nodes, its
  startup configurations and its readiness procedure.
- **Recorded fixture**: a raw command output captured from a lab Catalyst, with the rows it is
  expected to parse into.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A crawl seeded on the first Arista node of the mixed lab identifies all three nodes,
  the Catalyst as IOS-XE, with no `unknown_platform` among them.
- **SC-002**: Every physical link between an Arista and a Catalyst in the lab appears as exactly
  one edge, confirmed by both ends.
- **SC-003**: Two crawls of the unchanged lab produce the same rows for every Catalyst family.
- **SC-004**: The pack's tests run in under a minute from committed fixtures with NetLab off.
- **SC-005**: Starting from a stopped NetLab, the maintainer can deploy the lab, wait for
  readiness and run the cross-platform crawl in under 25 minutes by following the documented
  procedure.
- **SC-006**: Replaying the stored raw output of a Catalyst collection through the pack gives the
  same rows as the live collection.

## Assumptions

- The Catalyst image on NetLab is the Cat9kv (Catalyst 9000 virtual), which stands in for both the
  9300 and the 9500; output differences between those two hardware families are not visible in the
  lab and are listed as a gap if found later.
- The Cat9kv data plane is enough to bring up LLDP, trunks, port-channels and routing adjacencies
  with the Arista; features it does not emulate are listed as known gaps.
- NetLab has the memory and CPU for the existing Arista lab or the EVPN lab plus one Cat9kv
  node; if not, the mixed lab is deployed on its own.
- Collection is over SSH CLI and SNMP, as for Arista; no NETCONF or RESTCONF transport is added.
- The show commands this feature needs (version, inventory, LLDP, interfaces, VLANs, trunks) run
  at privilege 1 on IOS-XE; this is checked on the Cat9kv when fixtures are recorded.
- Out of scope, each a later change: C9200 support (fixtures from real hardware), CDP, stack
  handling (fixtures from a real stack), and the management and routing families.
- Principles touched: IV (read-only commands only) and V (vendor specifics stay in the pack). No
  change to the engine, API or collector code is expected.
