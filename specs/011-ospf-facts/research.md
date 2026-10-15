# Research: OSPF interfaces and adjacencies on Arista EOS

Sources: read-only `show` commands and an SNMP walk run on the EVPN lab on 2026-10-15 (cEOS
4.36.0F, NetLab VM): core1, core2 and dc-leaf1. The parser, pack and test code. What only
`test/lab` can show (a non-backbone area, a point-to-point interface, authentication, a non-full
adjacency, a non-default VRF) is marked "to confirm when recording" and is settled in
implementation step 1.

## R1. Interface rows come from `show ip ospf interface vrf all`

- **Decision**: the first step of `ospf_interfaces`, `show ip ospf interface vrf all | no-more`,
  gives one record per interface in OSPF, in every VRF:
  - `<Interface> is up`: interface, already in canonical form (`Ethernet5.100`, `Loopback0`);
  - `Interface Address <ip>/<len>, instance <id>, VRF <vrf>, Area <area>`: instance, VRF, area;
  - `Network Type <type>, Cost: <n>`: network type and cost;
  - `Transmit Delay is 1 sec, State <state>, Priority <n>`: interface state;
  - `Designated Router is <id>` and `Backup Designated Router is <id>`, or `No Backup Designated
    Router on this network`: R5;
  - `Neighbor Count is <n> (Passive Interface)`: passive when the parenthesis is there;
  - `No authentication`, or another authentication line: R6.
- **Skipped** (FR-007): transmit delay, priority, interface speed, the timer line, the neighbour
  count, traffic engineering and TI-LFA lines.
- **Seen** on core1 and core2: `Loopback0` (passive, network type `Broadcast`, state `DR`, no BDR)
  and `Ethernet5.100` (`Broadcast`, core1 `DR`, core2 `Backup DR`). EOS reports a loopback as a
  broadcast interface in state DR, not as `loopback`; the row records what the device prints.
- **Alternatives considered**: `show ip ospf interface brief vrf all` (no network type, passive or
  authentication, short interface names); `show running-config section router ospf` (gives what is
  configured, not what is in effect, and needs inheritance resolved, same objection as feature 010
  R2).

## R2. Router ID and area type come from `show ip ospf vrf all`, joined onto rows

- **Finding**: neither the interface view nor the neighbour view prints the local router ID or the
  area type. `show ip ospf vrf all` prints, per instance, `OSPF instance <id> with ID <router-id>
  VRF <vrf>`, and per area `Area <area>` followed by `It is a normal area`. Stub and NSSA wording:
  to confirm when recording (`test/lab` area 1).
- **Problem**: the parser's merge appends every row whose key matches no row so far. An area row
  keyed on `(vrf, instance, area)` would become a row of its own, with no interface, and fail
  validation.
- **Decision**: one vendor-neutral pack format addition, a per-step `join_on` list. A step with
  `join_on` only adds fields to rows already there that hold the same values on those fields; it
  never creates a row, and its rows do not make the observation non-empty. An area row that matches
  no interface (an area with no interface in it) is ignored. An interface whose area row is missing
  keeps no `area_type`, which is required, so the observation is `parse_failed` (R12). The registry
  refuses `join_on` on a first step (there is nothing to join onto).
- **The same step serves both families**: neighbour rows carry VRF, instance and area too (R3), so
  `ospf_neighbours` joins the same output on the same key for the router ID. Both recipes name the
  same template, so `TestFamiliesFromLab` reads the same recording for both.
- **Alternatives considered**: a third family for instances and areas (the spec chose repeated
  fields); a template that reads two outputs (TextFSM reads one); reading `area <n> stub` from the
  running-config (same join problem, plus configuration logic).

## R3. Adjacency rows come from `show ip ospf neighbor detail vrf all`

- **Decision**: one step, `show ip ospf neighbor detail vrf all | no-more`:
  - `Neighbor <router-id>, instance <id>, VRF <vrf>, interface address <ip>`: neighbour router
    ID, instance, VRF, neighbour address;
  - `In area <area> interface <Interface>`: area, interface (canonical already);
  - `Neighbor priority is <n>, State is <STATE>, <n> state changes`: state only.
  Then the `show ip ospf vrf all` join step of R2 for the router ID.
- **Skipped** (FR-007): priority, state change count, adjacency and state ages, `DR IP Address ...
  BDR IP Address ...` (the neighbour's view of the election, which the spec leaves out), options,
  dead timer, inactivity and retransmission counts, graceful restart lines.
- **Seen** on core1: one neighbour, 10.0.200.2, `FULL`, on `Ethernet5.100`, area 0.0.0.0.
- **Alternatives considered**: `show ip ospf neighbor vrf all` (no area column, a dead time column,
  `FULL/BDR` mixes the state with the neighbour's role, short interface names).

## R4. State and type words

- **Interface state**: `DR`, `Backup DR`, `DR Other` (to confirm), `P2P` (to confirm), `Waiting`,
  `Down`, `Loopback` map to `dr`, `bdr`, `drother`, `point_to_point`, `waiting`, `down`,
  `loopback`; `'*': other`. Seen: `DR`, `Backup DR`.
- **Network type**: `Broadcast`, `Point-To-Point` (to confirm), `NBMA`, `Point-To-Multipoint` map
  to `broadcast`, `point_to_point`, `nbma`, `point_to_multipoint`; `'*': other`.
- **Adjacency state**: `DOWN`, `ATTEMPT`, `INIT`, `2WAYS` or `2-WAY` (EOS spelling to confirm),
  `EXSTART`, `EXCHANGE`, `LOADING`, `FULL` map to the spec's words; `'*': other`. Seen: `FULL`.
- **Area type**: `normal`, `stub`, `nssa` from the `It is a ... area` line; a `no-summary` variant
  maps to its base type (spec decision). `'*'` maps to nothing, so an unknown wording leaves the
  required field empty and the observation is `parse_failed` rather than a guess.
- **Passive**: `yes` when `(Passive Interface)` is on the neighbour count line, default `no`, like
  `bgp_peers.shutdown`.

## R5. The interface prints the DR and BDR router IDs, not their addresses (spec amendment)

- **Finding**: on core2, `Designated Router is 10.0.200.1` and `Backup Designated Router is
  10.0.200.2` are core1's and core2's router IDs (their loopbacks), while the segment addresses are
  10.0.200.128 and 10.0.200.129. Only the neighbour view (`DR IP Address 10.0.200.128 BDR IP Address
  10.0.200.129`, per neighbour) and the SNMP `ospfIfDesignatedRouter` column give the addresses.
- **Decision**: the interface row records the DR and BDR **router IDs**, as fields
  `dr_router_id` and `bdr_router_id`. FR-004a, the clarification entry, story 1 scenario 1 and the
  edge case that reads a neighbour's role are amended in this change to say router ID, and the
  comparison is with the adjacency row's `neighbour_router_id`.
- **Rationale**: the router ID is how OSPF names a router, it is what the interface view prints,
  and it is available on a segment where this device has no neighbour. Taking the addresses from
  the neighbour view would leave them missing exactly on interfaces with no adjacency, and a third
  join for two fields is not worth it. SNMP would mean a second transport for one family.
- **Absent cases**: `No Backup Designated Router on this network` gives no BDR field. On a
  point-to-point interface, whether EOS prints DR lines: to confirm when recording. If it does, the
  pack maps them on that network type anyway and the contract says the fields are absent there, so
  `OnlyWith` is extended to a list of values (`broadcast`, `nbma`) in the same change.
- **To agree at review**: this is a change to what the user clarified ("DR/BDR IPs"). Same
  treatment as feature 010 R6.

## R6. Authentication type, and `unknown`

- **CLI** (seen on core1): an interface with no authentication prints `No authentication`. That
  line maps to `none`. The md5 and sha forms: to confirm when recording on `test/lab`; each maps to
  `md5` or `sha`, and any other authentication line to `other`.
- **SNMP** (seen on core1, walked from core2 with the lab's read-only community): EOS answers
  OSPF-MIB `ospfIfAuthType`, `none(0)` on both interfaces. The MIB defines only `none`,
  `simplePassword` and `md5`, so what EOS answers for a SHA interface is to confirm on `test/lab`.
  The walk also shows `ospfIfAuthKey`, empty as the MIB requires. The recipes use SSH only; SNMP
  was checked because the spec asks whether it gives a real `unknown` case.
- **`unknown`** (spec FR-004, FR-013a): a vendor-neutral schema attribute, `Field.Missing`. When a
  row has no value for the field after the merge, the join and the defaults, the parser sets it
  to that value (`unknown`). `Required` stays, so a row written without the parser and without the
  field is still rejected. The registry refuses a recipe `defaults` entry on a `Missing` field, so
  no pack can turn a missing value into `none`.
- **Test** (FR-013a): a unit test in `internal/parse` feeds a mapped row with no
  `auth_type` and checks `unknown`, and one in `internal/fact` checks that `Validate` rejects the
  same row when it skips the parser. No recorded output is edited. If a recording shows a real case
  (an authentication EOS does not print, or a SHA interface over SNMP), it becomes a pack fixture.
- **Not recorded**: the key, the key ID, the algorithm beyond `md5` or `sha`.

## R7. A device with no OSPF is `empty`

- **Seen**: dc-leaf1 prints nothing at all to the three commands (blank output through `Cli -c`).
  The parser already treats a blank output as nothing. No "OSPF not running" line exists on EOS 4.36
  for the `vrf all` forms. FR-009's wording is amended to say the exact "nothing here" answer is a
  blank output on this platform.
- **Recorded in step 1**: the same three commands on sw4 through an interactive SSH session, which
  is how the collector runs them (feature 010 R8: `Cli -c` and SSH exec add ` at line 1` to error
  lines, the collector's session does not).
- **No adjacency**: a device with OSPF and no neighbour (sw4 with OSPF on its loopback only, the
  `sw4noadj_*` recordings) prints interface and instance output and a blank neighbour output:
  `ospf_interfaces` is `collected`, `ospf_neighbours` is `empty`. Since a `join_on` step does not
  count towards non-empty (R2), the neighbour family is `empty` even though the instance step has
  output.
- **Errors**: `% Invalid input`, `% Authorization denied ...` and a VRF-specific `% OSPF instance
  does not exist in vrf <vrf>` (seen on core1 for `vrf gold`) match no empty line, reach the
  template, yield no row, and the observation is `parse_failed`. A refusal is recorded on sw2 with
  the temporary role used in feature 010.

## R8. Counters and timers

Nothing in the three outputs that moves with time or traffic is mapped: hello, dead, retransmit
and transmit delay values, dead timer due, adjacency and state ages, state change counts, LSA and
SPF counts and checksums, `Time since last SPF`. Cost, network type, area, passive flag and
authentication type are configuration; the interface state, DR and BDR router IDs and adjacency
state are statuses (spec decisions). Two recordings of core1 a few minutes apart give the same rows
(checked in quickstart section 5).

## R9. Secrets

- **Seen**: no command of R1 to R3 prints a configuration line. The authentication line names a
  type, not a key (to confirm on the `test/lab` md5 and sha interfaces).
- **Secret check**: the `test/lab` keys start with `nm-lab-secret-`, which
  `TestNoSecretInRecordedOutput` already looks for. EOS stores OSPF keys as `ip ospf
  authentication-key 7 <hash>` (already caught by `key 7 [0-9A-Fa-f]{6,}`) and `ip ospf
  message-digest-key <id> md5 7 <hash>` or `... sha<n> 7 <hash>`: one pattern,
  `(md5|sha\d*) 7 \S+`, is added.

## R10. The `test/lab` OSPF configuration

- **Decision**: no new link. New VLANs on the Port-Channel10 trunk (it allows every VLAN), one SVI
  per case on sw1 and sw2, `/30` addresses (exact values chosen in step 1):
  1. VLAN 20, OSPF instance 1, area 0, broadcast, md5 authentication on both ends: a full adjacency
     with DR and BDR, auth type `md5`.
  2. VLAN 21, instance 1, area 1 as stub, `ip ospf network point-to-point`, no authentication: a
     non-backbone area, a point-to-point interface, auth type `none`.
  3. VLAN 22 in VRF `blue`, instance 2, area 0, sha256 authentication on both ends: the
     non-default VRF case (clarification), auth type `sha`.
  4. VLAN 23, instance 1, area 0, a different MTU on each end: the adjacency stays in `EXSTART` or
     `EXCHANGE` while hellos keep it alive, so two crawls see the same state. To confirm when
     recording; if EOS does not hold it, `ip ospf mtu-ignore` absence or a mismatched network type
     on one end is tried next.
  5. `Loopback0` of each switch (10.255.0.1, 10.255.0.2, already there for BGP) in area 0,
     `passive-interface`: the passive case.
- **sw4**: no OSPF, the `empty` case (`_empty` recordings). Then, temporarily, `router ospf 1` with
  its loopback only, for the `sw4noadj_*` recordings (R7), reverted after.
- **Side effects**: new VLANs and SVIs appear in a live crawl's `vlans`, `interface_vlans` and
  `interfaces` rows. The recordings of features 007 to 010 are not re-recorded and their tests are
  unaffected, as in feature 010 R10. The BGP sessions of feature 010 use VLAN 10 and are untouched.
- **sw3**: denied, cannot serve.

## R11. The engine and discovery need no change

Same finding as feature 010 R11: the graph projector reads `neighbours`, `interfaces`, `vlans` and
`interface_vlans` by name, and `find` tasks come from `neighbours` only. Both families go into
`testutil.NoRecipeFamilies` (fakeos has no recipe for them) and `missingCost`. A graph test asserts
that a snapshot with OSPF rows has the same graph and verdict.

## R12. A row that breaks a rule is never dropped

Same path as feature 010 R12: `parse.Parse` validates every row after the merge and the join, and
any error makes the observation `parse_failed` with a finding. A missing required field (an
interface whose area has no instance row, an area type wording the pack does not know) goes
through it.

## R13. Versions

`versions: '>=4.36'`, the only version recorded and scanned for secrets, like feature 010.
