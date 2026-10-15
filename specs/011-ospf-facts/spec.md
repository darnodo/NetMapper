# Feature Specification: OSPF interfaces and adjacencies on Arista EOS

**Feature Branch**: `011-ospf-facts`

**Created**: 2026-10-15

**Status**: Draft

**Input**: User description: "darnodo/NetMapper#30, second sub-issue now: darnodo/NetMapper#32.
NetLab is running and the Arista EVPN lab is up with traffic flowing." Issue #32: collect OSPF on
Arista EOS (instances, areas, OSPF interfaces, adjacencies) as platform-neutral fact families, with
the rules of the parent issue (VRF on every row, no secret in recorded output, decide what to do
with counters and timers), following feature 010 (BGP).

## Decisions taken in this spec

- **Two families, as drafted in the issue: `ospf_interfaces` and `ospf_neighbours`.** One row per
  interface in OSPF, one row per adjacency. Instance and area data (instance, router ID, area type)
  are repeated on each row rather than given a family of their own, as the issue proposed and as
  `bgp_peers` does for the local AS and router ID.
- **An interface's DR/BDR role is collected, as a status.** The interface's OSPF state (`dr`,
  `bdr`, `drother`, `point_to_point`, `loopback`, `waiting`, `down`, `other`) changes when an
  election runs or a link fails, not with traffic, like the BGP session state. The issue named this
  field `role`; it is named `state` here because `down`, `waiting` and `loopback` are states, not
  roles.
- **An adjacency's state is collected, its timers are not.** The neighbour state (`down`,
  `attempt`, `init`, `2way`, `exstart`, `exchange`, `loading`, `full`, `other`) is a status.
  The dead timer, adjacency uptime, retransmission and state change counts move between two
  identical runs and are left out, as for BGP. That data belongs to telemetry.
- **The neighbour's own DR/BDR role is not recorded on the adjacency row.** It is the neighbour's
  interface state, recorded on the neighbour's own `ospf_interfaces` row when that device is
  crawled. Instead, each interface row on a segment with an election records the router IDs of the
  segment's DR and BDR, so who holds those roles is readable from one device even when the
  neighbour is not crawled. They are router IDs on every transport, never interface addresses.
- **Cost, network type, passive and area are configuration, and are collected.** They are what an
  operator checks first when a path is not the expected one.
- **Area types are `normal`, `stub`, `nssa`.** A stub or NSSA area with summaries suppressed
  (`no-summary`) records its base type; the variant is not recorded in this feature.
- **No secret reaches recorded output; the authentication type is recorded.** The families are
  read from commands that do not print a key, and the secret check covers the lab's OSPF key. Each
  interface row records the authentication type in effect on it (`none`, `simple`, `md5`, `sha`,
  `other`, `unknown`), never the key: a type mismatch between the two ends of a link is a common
  reason an adjacency never forms, and it is readable by comparing both ends. This differs from
  `bgp_peers`, which records nothing about its password.
- **Nothing configured is `empty`, per family.** A device with no OSPF gives `empty` for both. A
  device whose OSPF interfaces are all passive, or have no neighbour, gives `collected` for
  `ospf_interfaces` and `empty` for `ospf_neighbours`.
- **Interface names are canonical**, the same names the `interfaces` family uses, so a row can be
  joined to its interface (core1's OSPF runs on a subinterface, `Ethernet5.100`).
- **No graph edge and no discovery from an adjacency.** Same as feature 010 and the default of #30:
  the families are not read by the graph engine and a neighbour does not queue a `find`.
- **The read interface needs no change.** The per-family endpoint of feature 007 serves any family
  in the schema.

## Clarifications

### Session 2026-10-15

- Q: Should each OSPF interface row record its authentication type, without the key? → A: Yes,
  per interface: `none`, `simple`, `md5`, `sha`, `other`, `unknown`. The key is never recorded.
  (Amended 2026-10-15: `unknown` added. It means the platform or transport did not expose the
  type, and stays distinct from `none`, which means the device reported no authentication.)
- Q: Should each adjacency row record the neighbour's DR/BDR role? → A: No. Each interface row
  records the DR and BDR of its segment instead. (Amended during planning, research R5, approved:
  they are recorded as router IDs, `dr_router_id` and `bdr_router_id`, because the interface view
  prints router IDs, not segment addresses. The field means router ID on every transport.)
- Q: Should `test/lab` get an OSPF instance in a non-default VRF, beyond what #32 lists? → A: Yes,
  one instance in a non-default VRF between sw1 and sw2, with an adjacency, so the VRF rule of #30
  is tested on recorded output.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See where OSPF runs on a device and with whom it is adjacent (Priority: P1)

An operator crawls an Arista network. For each device, NetMapper now records which interfaces run
OSPF, in which instance, VRF and area, with their cost, network type, whether they are passive and
their DR/BDR state, and which adjacencies the device has: neighbour router ID and address, the
interface and area, and the adjacency state.

**Why this priority**: "which links carry OSPF, at what cost, and which adjacencies are up" is the
first question when tracing a path in an OSPF network. It is the reason for the feature.

**Independent Test**: crawl the arista-evpn-vxlan-clab lab and check that core1 and core2 match
their configuration: OSPF on the loopback (passive) and on `Ethernet5.100`, one full adjacency to
each other. Then run the template tests with the lab stopped: they pass from the recorded fixtures
alone.

**Acceptance Scenarios**:

1. **Given** core1 of the EVPN lab, with OSPF instance 1 in VRF `default`, area 0, a passive
   loopback and `Ethernet5.100` towards core2, **When** a crawl runs, **Then** `ospf_interfaces`
   has one row per interface, each with VRF `default`, instance, router ID, area `0.0.0.0`, area
   type `normal`, network type, cost, passive `yes` for the loopback and `no` for `Ethernet5.100`,
   and the interface state the device reports. The `Ethernet5.100` row also carries the DR
   router ID `10.0.200.1` and the BDR router ID `10.0.200.2`.
2. **Given** the same device, **When** a crawl runs, **Then** `ospf_neighbours` has one row for
   core2: router ID `10.0.200.2`, address `10.0.200.129`, interface `Ethernet5.100`, area
   `0.0.0.0`, state `full`.
3. **Given** a device with an OSPF instance in a VRF other than `default`, **When** a crawl runs,
   **Then** each of its rows names that VRF and that instance's router ID.
4. **Given** a crawl run twice a few minutes apart with traffic flowing and no change to the
   network, **When** the two observations are compared, **Then** their rows are identical.

---

### User Story 2 - See an adjacency that is not up and an interface with no adjacency (Priority: P1)

An adjacency that should be full and is not is what an operator looks for. NetMapper records it
with the state it is stuck in. An OSPF interface that should have a neighbour and has none is the
other half of the same fault, visible by comparing the two families.

**Why this priority**: a list of working adjacencies hides the broken one. Same priority as story 1.

**Independent Test**: crawl `test/lab`, where sw1 and sw2 have one adjacency in area 0, one in a
non-backbone area, one with authentication, one passive interface and one adjacency held out of
`full`, and check each row. Then run the template tests with the lab stopped.

**Acceptance Scenarios**:

1. **Given** a `test/lab` adjacency between sw1 and sw2 with authentication on both ends, **When**
   a crawl runs, **Then** both ends have an `ospf_neighbours` row with state `full`, both
   `ospf_interfaces` rows carry the same authentication type, and the key appears in no recorded
   output.
2. **Given** an adjacency held in a state other than `full` by its configuration, **When** a crawl
   runs, **Then** its row records that state, and two crawls a few minutes apart record the same
   state.
3. **Given** an interface in a non-backbone area of type stub or NSSA, **When** a crawl runs,
   **Then** its row carries that area ID and that area type.
4. **Given** a passive interface, **When** a crawl runs, **Then** it has an `ospf_interfaces` row
   with passive `yes` and no `ospf_neighbours` row points to it.
5. **Given** a platform or transport that does not expose the authentication type of an OSPF
   interface, **When** a crawl runs, **Then** the interface has an `ospf_interfaces` row with
   authentication type `unknown`, never `none` and never absent. The "missing means `unknown`"
   rule does not depend on a vendor, so it is tested on the vendor-independent side, with a parsed
   row that lacks the field, not with an edited device output.

---

### User Story 3 - Tell "no OSPF" from "not collected" (Priority: P2)

An operator looks at a device that runs no OSPF, such as a leaf of the EVPN lab. They need to know
it was asked and has no OSPF, as opposed to the question never being asked or the answer not
understood.

**Why this priority**: anything reading these families later will read absence as "this device has
no OSPF". That is only safe if absence is recorded.

**Independent Test**: crawl a device with no OSPF (an EVPN lab leaf, or sw4 of `test/lab`) and
check both observations are `empty`, and that a device of another platform gives `unsupported`.

**Acceptance Scenarios**:

1. **Given** a device with no OSPF configured, **When** a crawl runs, **Then** `ospf_interfaces`
   and `ospf_neighbours` are both `empty`.
2. **Given** a device running OSPF with no adjacency, **When** a crawl runs, **Then**
   `ospf_interfaces` is `collected` and `ospf_neighbours` is `empty`.
3. **Given** an output the template does not understand, a command the device refuses, any
   error output, or a blank output on the step that decides "no OSPF here", **When** a crawl runs,
   **Then** the observation is `parse_failed` with a finding, never `empty`.
4. **Given** a device of another platform, **When** a crawl runs, **Then** both families are
   `unsupported` with detail `no_recipe`, and the snapshot verdict is unchanged.

---

### Edge Cases

- An OSPF instance configured with no interface in it: no row, so `ospf_interfaces` is `empty`.
  The instance's router ID is not recorded in this feature.
- An interface in OSPF whose link is down: a row with state `down`. It has no adjacency row.
- Several neighbours on one broadcast segment: one row per neighbour. A neighbour that stays in
  `2way` because neither end is DR or BDR is normal on such a segment; the row records `2way` as is.
- Two OSPF instances on one device, in the same VRF or in different VRFs: rows told apart by
  instance and VRF.
- The same interface name cannot appear in two instances; the same neighbour router ID can appear
  on two interfaces (two parallel links): two rows, told apart by interface.
- An unnumbered OSPF interface: not in either lab. It MUST NOT be dropped silently; an output the
  template cannot read is `parse_failed`.
- An adjacency that flaps between two crawls: each observation records the state seen at collection
  time.
- A DR/BDR state that changes after a lab redeploy (election order): a change between snapshots,
  not between two crawls of an unchanged network. The same holds for the DR and BDR router IDs.
- A broadcast segment with only one OSPF router: the row has a DR router ID (its own) and no BDR
  router ID.
- A neighbour on a broadcast segment that is not crawled: its role is read from the local row's DR
  and BDR router IDs, compared with the adjacency row's neighbour router ID.
- A transport that reports the DR or BDR by interface address rather than router ID (SNMP: the
  OSPF-MIB DR and BDR columns hold addresses): the address is resolved to a router ID through the
  device's neighbour table, or to the device's own router ID when the DR or BDR is the device
  itself. When it cannot be resolved, the field reads `unknown`, never the raw address.
- A command that returns an empty output after a timeout, a dropped session or a failure to detect
  the prompt: the observation keeps its failure status (`unreachable` or `parse_failed`, or no
  observation, the task failing), never `empty`, so a later diff does not read it as OSPF that
  disappeared.
- A blank output on the step that decides "no OSPF here": `parse_failed`, never `empty`, even with
  no transport error.
- An area ID the device prints as a number rather than dotted form: converted to dotted form (1
  becomes `0.0.0.1`, 256 becomes `0.0.1.0`), so the same area reads the same on every device. A
  value that is neither a dotted ID nor an integer from 0 to 4294967295 is `parse_failed`.
- A neighbour that a device stops listing (dead timer expired): it has no row in the next
  observation. If the interface still runs OSPF, the gap is visible from `ospf_interfaces`.
- A state word the pack has no recording of: `other`, never read as `full` or `down`.
- A device that prints an authentication key in a command the recipe could use: that command is not
  used.
- Authentication set on the area rather than on the interface: the interface row records the type
  in effect, as inherited.
- A platform or transport that does not expose the authentication type: the row reads `unknown`,
  never `none`. Comparing `unknown` with any other type says nothing about a mismatch.
- Two ends of a link with different authentication types: no adjacency row on either end, and the
  two interface rows show different types.
- OSPFv3 (IPv6) configured on a device: not collected, and does not make OSPFv2 `parse_failed`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The fact schema MUST define two new platform-neutral families, `ospf_interfaces` and
  `ospf_neighbours`, with no vendor term in any field name or enum value.
- **FR-002**: `ospf_interfaces` MUST hold one row per interface in OSPF, identified by VRF and
  interface. `ospf_neighbours` MUST hold one row per adjacency, identified by VRF, interface and
  neighbour router ID.
- **FR-003**: Every row of both families MUST carry its VRF (`default` for the global table), the
  instance identifier as the device names it, the instance's router ID, and the area in dotted form.
  An area the device prints as a plain number is converted to dotted form (1 becomes `0.0.0.1`),
  not rejected.
- **FR-004**: Every `ospf_interfaces` row MUST carry the interface (canonical name, as the
  `interfaces` family names it), the area type (`normal`, `stub`, `nssa`), the network type
  (`broadcast`, `point_to_point`, `nbma`, `point_to_multipoint`, `loopback`, or `other`), the
  cost, whether the interface is passive, the interface state (`dr`, `bdr`, `drother`,
  `point_to_point`, `loopback`, `waiting`, `down`, or `other`), and the authentication type in
  effect on the interface, whether set on the interface or inherited from its area (`none`,
  `simple`, `md5`, `sha` for any HMAC-SHA variant, `other`, or `unknown`). `none` MUST be
  recorded only when the device reports that no authentication is configured. `unknown` means the
  platform or the transport used did not expose the type. A row MUST NOT read `none` when the
  information is missing. The field is always present: this is an exception to FR-006, and
  `unknown` takes the place of an absent field.
- **FR-004a**: An `ospf_interfaces` row on a network type with a DR election (`broadcast`, `nbma`)
  MUST carry the router ID of the segment's DR (`dr_router_id`) and of its BDR (`bdr_router_id`),
  each when the device reports one. Both are absent on other network types, and each is absent
  when no router holds that role (no BDR elected, or the election not finished). They are statuses
  like the interface state: they change with an election, not with traffic. Both fields mean
  router ID on every transport. An implementation whose source gives the DR or BDR as an interface
  address (SNMP `ospfIfDesignatedRouter` and `ospfIfBackupDesignatedRouter`) MUST resolve it
  through the device's neighbour table (`ospfNbrIpAddr` to `ospfNbrRtrId`), or to the device's own
  router ID (`ospfRouterId`) when the address is the device's own. When it cannot be resolved, the
  field MUST read `unknown`, never the raw address. Absent means no router holds the role;
  `unknown` means one does and its router ID could not be established.
- **FR-005**: Every `ospf_neighbours` row MUST carry the interface (canonical name), the neighbour
  router ID, the neighbour address, and the adjacency state (`down`, `attempt`, `init`, `2way`,
  `exstart`, `exchange`, `loading`, `full`, or `other`).
- **FR-006**: A field the device does not report is absent, never empty or zero, except the
  authentication type, which reads `unknown` (FR-004). A row missing a required field MUST NOT be
  dropped silently: the observation is `parse_failed` and a finding records why.
- **FR-007**: No row MUST carry the dead timer, adjacency uptime, hello or retransmission timers,
  LSA or SPF counts, or state change counts. Two crawls of an unchanged network MUST give the same
  rows.
- **FR-008**: No configured OSPF authentication key MUST appear in any recorded output. The recipe
  MUST use commands that do not print it, and the existing secret check MUST cover the OSPF key of
  `test/lab`. Only the authentication type is recorded (FR-004), never a key or key ID.
- **FR-009**: A device with no OSPF interface MUST produce an `empty` `ospf_interfaces`
  observation, and a device with no adjacency an `empty` `ospf_neighbours` observation, not a
  `collected` one with no row. `empty` MUST come only from outputs the pack declares as "nothing
  here". On Arista EOS, the text commands print nothing at all when OSPF is not running, and have
  no "OSPF not running" answer (amended during planning, research R7, approved). So "no OSPF here"
  MUST be decided by a step whose "nothing here" answer is not blank: the structured form of the
  instance view, which answers a non-empty document listing no VRF (research R14). A blank output
  on that step is never valid and MUST give `parse_failed`. A blank output on another step (such
  as no adjacency) is accepted only once the guard step has shown OSPF running. Any blank output means
  "nothing here" only when the command completed normally. A blank output after a timeout, a
  dropped session or a failure to detect the prompt MUST keep its failure status (`unreachable` or
  `parse_failed`, or no observation, the task failing), never `empty`, so a diff never reads it as
  a disappearance. A refused command, an invalid command or any other error output
  MUST produce `parse_failed`, so a later diff never reads an error as adjacencies that
  disappeared.
- **FR-010**: The Arista EOS pack MUST provide a recipe for each new family; no other pack changes.
  Commands MUST be read only (constitution IV).
- **FR-011**: The schema of both families MUST be published in the fact families contract in the
  same change as the code.
- **FR-012**: `test/lab` MUST carry OSPF configuration on sw1 and sw2: one adjacency in area 0, one
  interface in a non-backbone stub or NSSA area, one passive interface, one adjacency with
  authentication, one adjacency held in a state other than `full` that stays the same between two
  crawls, and one instance in a VRF other than `default` with a `full` adjacency between the two
  switches. What the lab already proves (features
  001 to 010, including the BGP sessions of feature 010 and every `empty` case) MUST keep working.
- **FR-013**: Every row type of stories 1 and 2 (a broadcast interface with DR/BDR state, a
  loopback, a passive interface, a non-backbone area, a non-default VRF, a full adjacency with and
  without authentication, an adjacency not full), the two `empty` cases of story 3 and an error
  output giving `parse_failed` MUST be covered by a test against output recorded from `test/lab` or
  arista-evpn-vxlan-clab, runnable with no lab deployed. Pack fixtures MUST be unmodified device
  output: no fixture is edited to create a case.
- **FR-013a**: The rule that a missing authentication type reads `unknown` (FR-004, story 2
  scenario 5) MUST be covered by a vendor-independent unit test of the mapping or schema
  validation, given a parsed row with no authentication type. If the lab shows a real case where
  EOS does not expose the type (see Assumptions), that recorded output MUST also become a pack
  fixture with its test.
- **FR-014**: The configuration added to `test/lab` and the way to record fixtures from it MUST be
  documented in the same change (topology comments and the feature quickstart).
- **FR-015**: The new families MUST NOT be read by the graph engine in this feature, MUST NOT queue
  a discovery task, and MUST NOT change the snapshot verdict.
- **FR-016**: Both families MUST be readable through the existing per-device, per-family read
  answer, with their status, rows and evidence, with no change to that answer.
- **FR-017**: The README, the how-to guides and the API request collection MUST be updated in the
  same change where they list fact families or what a pack collects.

### Key Entities

- **OSPF instance**: the OSPF process of one device in one VRF, with its identifier and router ID.
  Carried on each of its rows rather than as a row of its own.
- **OSPF area**: an area of an instance, with its ID and type. Carried on each interface row, and
  its ID on each adjacency row.
- **OSPF interface**: an interface of one device taking part in one instance, with its area, cost,
  network type, passive flag, state, authentication type and, on a segment with an election, the
  DR and BDR router IDs.
- **OSPF adjacency**: a relation from one device to one neighbour router ID over one interface in
  one VRF, with the neighbour's address and the adjacency state. The same adjacency seen from both
  ends is two rows on two devices; joining them is not this feature's job.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With no lab running, the test suite covers every row type of both families and the
  `empty` cases, and passes.
- **SC-002**: On a crawl of `test/lab`, every OSPF interface and adjacency configured on sw1 and sw2
  appears as exactly one row, with the state the lab is built to produce, and the lab's OSPF key
  appears in no recorded output.
- **SC-003**: On a crawl of the arista-evpn-vxlan-clab switches, when that lab is running, every
  device has an observation for both families and none is `parse_failed`; core1 and core2 each have
  two interface rows and one `full` adjacency, and every other device is `empty`.
- **SC-004**: Two crawls of the EVPN lab a few minutes apart, with traffic flowing and no change to
  the network, give identical rows for both families on every device.
- **SC-005**: Every `ospf_interfaces` and `ospf_neighbours` row names an interface that the same
  device's `interfaces` observation also names.
- **SC-006**: A device of another platform gets one `unsupported` (`no_recipe`) observation per
  family and the same snapshot verdict as before the feature.

## Assumptions

- The arista-evpn-vxlan-clab lab (on the NetLab VM) is a fixture source. Checked on 2026-10-15:
  core1 and core2 run OSPF instance 1 in VRF `default`, area 0 only, with `passive-interface
  default`, their loopback and one adjacency on `Ethernet5.100` (dot1q subinterface, broadcast,
  core1 DR and core2 BDR), no authentication (type `none`). No other device of that lab runs OSPF,
  so they give the `empty` case. No device has OSPF in a non-default VRF, a
  non-backbone area, a passive interface other than the loopback, authentication or a non-full
  adjacency: those cases come from
  `test/lab` only.
- To check on the lab during planning: whether EOS shows the authentication type on an interface
  with no authentication configured, and whether EOS answers the authentication type of an OSPF
  interface over SNMP (`ospfIfAuthType`). Either path that yields a real `unknown` case gives a
  pack fixture (FR-013a).
- `test/lab` has no OSPF today. Which interfaces and addresses carry it, and how the non-full
  adjacency is held in place, are planning choices. sw3 (denied) cannot serve.
- The collector's existing SSH account is enough to read OSPF state; no new credential or privilege
  is needed.
- The interface state and the adjacency state are treated as statuses, not volatile data: they
  change on failure, repair or election, not with traffic.
- OSPFv3, the LSDB, the routing table, IS-IS, static routes, BFD and other platforms are out of
  scope.
- Whether an OSPF adjacency becomes a graph edge, and how a neighbour router ID or address is
  matched to an entity, stay open in #30 and are separate work.
