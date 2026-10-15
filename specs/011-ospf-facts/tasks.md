---
description: "Task list for OSPF interfaces and adjacencies on Arista EOS"
---

# Tasks: OSPF interfaces and adjacencies on Arista EOS

**Input**: Design documents from `/specs/011-ospf-facts/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/fact-families.md,
quickstart.md

**Tests**: Included. FR-013 asks for a test per row type runnable with no lab, FR-013a for a
vendor-independent unit test of the `unknown` rule, and the constitution for template changes
checked by replay over recorded output and for tie-breaking rules tested with inputs on each side
(`none` against `unknown`, the guard's "none" document against a blank output).

**Organization**: Tasks are grouped by user story. US1 (where OSPF runs and with whom, EVPN lab
fixtures) and US2 (adjacencies not up, areas, passive, authentication, `test/lab` fixtures) are both
P1. US1 writes the templates and recipes; US2 extends their value tables and fixtures, so it follows
US1. US3 (`empty` and errors) relies on the guard of Phase 2 and the recipes of US1.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 010.
- **[VM]** in a description: needs the `NetLab` VM (ssh host `NetLab`), where `test/lab` and
  arista-evpn-vxlan-clab run. Every other task runs on the Mac with no lab.

Commands, templates and fixtures used throughout (research R1 to R3, R14). A fixture is
`packs/arista_eos/testdata/lab/<switch>_<template base>.raw`; an output whose template yields no row
has `_empty` before `.raw`. Expected rows per device and family are in
`<switch>_<family>.facts.yml` (no `.yml` per recording, as in 010).

| Step | Command | Template (`packs/arista_eos/templates/`) | Role |
| ---- | ------- | ---------------------------------------- | ---- |
| G | `show ip ospf vrf all \| json` | `show_ip_ospf_vrf_all_json.textfsm` | guard, first step of both recipes (R14) |
| I | `show ip ospf interface vrf all \| no-more` | `show_ip_ospf_interface_vrf_all.textfsm` | `ospf_interfaces` rows (R1) |
| N | `show ip ospf neighbor detail vrf all \| no-more` | `show_ip_ospf_neighbor_detail_vrf_all.textfsm` | `ospf_neighbours` rows (R3) |
| J | `show ip ospf vrf all \| no-more` | `show_ip_ospf_vrf_all.textfsm` | joined on `[vrf, instance, area]`, last step of both recipes (R2) |

`ospf_interfaces` = G, I, J. `ospf_neighbours` = G, N, J. G and J are shared: `TestFamiliesFromLab`
reads the same recording for both families.

Recording on the VM: copy `test/lab` to `~/netmapper-test-lab` (NetLab has no repo clone). Record
through an interactive SSH session as the netmapper user, which is how the collector runs commands
(`printf '<command>\nexit\n' | sshpass -p admin ssh -tt netmapper@<mgmt-ip>`), then keep only the
command's output. When `docker exec ... Cli -p 15 -c` is used for a non-error output, strip the
`> <command>` first line (`sed -i '1{/^> /d}' <files>`). Never edit a recording otherwise (FR-013).

Expected values in `.facts.yml` files: "values as recorded" applies only to values the spec does
not fix (router IDs, costs, which switch wins the DR election, the instance spelling). Any recorded
value that departs from a functional requirement or an acceptance scenario (a DR or BDR on a
point-to-point interface, the VLAN 44 adjacency reaching `full`, an area not in dotted form, a
missing field) stops the task: it goes back to research.md or the spec, never into a
`.facts.yml`.

---

## Phase 1: Setup

**Purpose**: give `test/lab` its OSPF configuration (research R10), record every command once on
both labs, and settle the "to confirm" items of research before any template exists.

- [ ] T001 [P] Edit test/lab/sw1.cfg. Keep every existing line (the 007 to 010 cases depend on them). Add a comment block "OSPF configuration for feature 011 (research R10)", then: `vrf instance blue`; `ip routing vrf blue`; `interface Vlan41` / `ip address 10.0.41.1/30` / `ip ospf authentication message-digest` / `ip ospf message-digest-key 1 md5 nm-lab-secret-ospf-md5`; `interface Vlan42` / `ip address 10.0.42.1/30` / `ip ospf network point-to-point`; `interface Vlan43` / `vrf blue` / `ip address 10.0.43.1/30` / `ip ospf authentication message-digest` / `ip ospf message-digest-key 1 sha256 nm-lab-secret-ospf-sha`; `interface Vlan44` / `ip address 10.0.44.1/30`; `router ospf 1` / `router-id 10.255.0.1` / `passive-interface Loopback0` / `network 10.255.0.1/32 area 0.0.0.0` / `network 10.0.41.0/30 area 0.0.0.0` / `network 10.0.44.0/30 area 0.0.0.0` / `network 10.0.42.0/30 area 1` / `area 1 stub` (area 1 written as an integer on purpose: T008 checks how EOS prints it, spec FR-003); `router ospf 2 vrf blue` / `router-id 10.255.2.1` / `network 10.0.43.0/30 area 0.0.0.0`. Use VLANs 41 to 44 only: they are already declared (`vlan 40-49`) and trunked, so `vlans` and `interface_vlans` do not change. Adjust any syntax cEOS 4.36 refuses and write the change in research R10 (T008)
- [ ] T002 [P] Edit test/lab/sw2.cfg the same way, keeping every existing line: same VRF, SVIs with `.2/30` addresses, the same keys, `router ospf 1` with `router-id 10.255.0.2`, `passive-interface Loopback0`, `network 10.255.0.2/32 area 0.0.0.0` and the same networks and stub area, `router ospf 2 vrf blue` with `router-id 10.255.2.2`. On `interface Vlan44` add `mtu 1400` (sw1 keeps the default): the MTU mismatch that holds the adjacency below `full` (research R10 case 4)
- [ ] T003 [P] Extend the header comment of test/lab/two-switch.clab.yaml: sw1 and sw2 carry the OSPF configuration of feature 011 on SVIs of VLANs 41 to 44 (already trunked, no new link or VLAN): area 0 with md5 on VLAN 41, a point-to-point stub area 1 on VLAN 42, instance 2 in VRF `blue` with sha on VLAN 43, an MTU mismatch on VLAN 44 that holds the adjacency below `full`, and passive Loopback0 in area 0; sw4 has no OSPF and is the `empty` case of `ospf_interfaces` and `ospf_neighbours`
- [ ] T004 [VM] Deploy: copy test/lab to `~/netmapper-test-lab`, `sudo containerlab deploy -t two-switch.clab.yaml --reconfigure`. Check on sw1 and sw2 with `show ip ospf neighbor vrf all` that VLANs 41, 42 and 43 are `FULL` on both ends and VLAN 44 is in `EXSTART` or `EXCHANGE`; check again five minutes later that VLAN 44 has not moved (if it has, apply the fallbacks of research R10 case 4 and write which one held). Check that 001 to 010 behaviour still holds: `show lldp neighbors` on sw1 lists sw2 on Ethernet1 to Ethernet4, `show interfaces trunk` still lists Ethernet1 and Port-Channel10, the BGP sessions of 010 reach their states, sw3 still refuses the netmapper account
- [ ] T005 [VM] Record steps G, I, N and J on sw1 and sw2 (`sw1_show_ip_ospf_vrf_all_json.raw`, `sw1_show_ip_ospf_interface_vrf_all.raw`, `sw1_show_ip_ospf_neighbor_detail_vrf_all.raw`, `sw1_show_ip_ospf_vrf_all.raw`, same for sw2). On sw4 record the four commands with `_empty` in every name, and check that G answers a `vrfs` key holding an empty object, as dc-leaf1 did (research R14). Then on sw4, temporarily, `ip routing`, `interface Loopback0` / `ip address 10.255.0.4/32`, `router ospf 1` / `router-id 10.255.0.4` / `network 10.255.0.4/32 area 0.0.0.0`; record the four commands into `sw4noadj_*` (N blank: `sw4noadj_show_ip_ospf_neighbor_detail_vrf_all_empty.raw`), then remove what was added
- [ ] T006 [VM] Record the error outputs research R7 needs, as test input only, through an interactive SSH session: on sw2, which enforces command authorization, a temporary role `nm-no-ospf` (`10 deny command show ip ospf.*`, `20 permit command .*`) and a temporary user with it; record step I as that user into `sw2_show_ip_ospf_interface_vrf_all_denied_empty.raw`, then remove the user and the role. On sw1, `show ip ospf neighbor detail vrf nosuch` into `sw1_show_ip_ospf_neighbor_detail_vrf_all_invalid_empty.raw` (the `% OSPF instance does not exist` form). The names carry a template name and `_empty` because `TestPacks` parses each `.raw` with the template in its name and requires `_empty` ones to yield no row; the recipe-level outcome is checked by T034
- [ ] T007 [VM] On the EVPN lab, record the four commands on core1 and core2 (`core1_show_ip_ospf_vrf_all_json.raw` and so on) and on dc-leaf1 with `_empty` in every name. Grep every new file of T005 to T007 for `nm-lab-secret-`, `message-digest-key`, `authentication-key` and ` 7 `: expected nothing; if a recording holds a key, stop and choose another command (FR-008)
- [ ] T008 [VM] Settle the "to confirm" items and update specs/011-ospf-facts/research.md with what EOS printed: R2 (the `It is a ... area` line of a stub area), R4 (the state words for point-to-point, DR Other and 2-way, the `Point-To-Point` network type word), R5 (whether EOS prints DR or BDR lines on the point-to-point VLAN 42; if it does, amend data-model.md: `OnlyWith` takes a list of values), R6 (the md5 and sha authentication lines of step I; and, walking `ospfIfAuthType` on sw1 from a lab EOS container's bash with `snmpwalk -v2c -c public 172.20.20.2 1.3.6.1.2.1.14.7.1.6`, what EOS answers for the md5 and sha interfaces), R2 and FR-003 (how `show ip ospf interface vrf all`, `show ip ospf neighbor detail vrf all` and `show ip ospf vrf all` print area 1, configured as an integer in T001: `0.0.0.1` or `1`; either is handled by `DottedID`, write which), R14 (sw4's guard answer, and whether the guard JSON on sw1 and sw4noadj carries an interface count per area or per instance, with its key name). If an authenticated interface prints no authentication line, or SNMP answers nothing usable for sha, write it as a real `unknown` case (FR-013a) and add its recording to T034
- [ ] T009 Run `go test -count=1 ./packs/...` on the Mac. `TestNoSecretInRecordedOutput` passes on the new files; `TestPacks` fails only on the new recordings that have no template yet (expected until Phase 3)

**Checkpoint**: every fixture exists; the lab is not needed again until Phase 6.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the schemas, the `Missing` attribute, `pack_schema`, and the `guard`, `join_on` and
`unmatched` step keys every later task depends on. None of it needs the lab.

- [ ] T010 Add `Missing string` to `Field` in internal/fact/fact.go, commented "the value the parser sets when no step gives the field, such as \"unknown\"; the field must also be Required (feature 011)", and `DottedID bool`, commented "a 32-bit ID in dotted form; the parser converts a plain integer from 0 to 4294967295 (1 becomes 0.0.0.1) (feature 011)". Add the two families after `bgp_peers`, under the existing routing comment. `ospf_interfaces`: `{Name: "vrf", Type: String, Required: true}`, `{Name: "instance", Type: String, Required: true}`, `{Name: "router_id", Type: String, Required: true}`, `{Name: "interface", Type: String, Required: true, Canonical: true}`, `{Name: "area", Type: String, Required: true, DottedID: true}`, `{Name: "area_type", Type: String, Required: true, Enum: []string{"normal", "stub", "nssa"}}`, `{Name: "network_type", Type: String, Required: true, Enum: []string{"broadcast", "point_to_point", "nbma", "point_to_multipoint", "loopback", "other"}}`, `{Name: "cost", Type: Int, Required: true}`, `{Name: "passive", Type: String, Required: true, Enum: []string{"yes", "no"}}`, `{Name: "state", Type: String, Required: true, Enum: []string{"dr", "bdr", "drother", "point_to_point", "loopback", "waiting", "down", "other"}}`, `{Name: "auth_type", Type: String, Required: true, Missing: "unknown", Enum: []string{"none", "simple", "md5", "sha", "other", "unknown"}}`, `{Name: "dr_router_id", Type: String}` and `{Name: "bdr_router_id", Type: String}`, each commented "a router ID on every transport, or \"unknown\" when a router holds the role and its router ID could not be resolved; never an interface address (FR-004a)". `ospf_neighbours`: `vrf`, `instance`, `router_id` (all required strings, as above), `{Name: "interface", Type: String, Required: true, Canonical: true}`, `{Name: "area", Type: String, Required: true, DottedID: true}`, `{Name: "neighbour_router_id", Type: String, Required: true}`, `{Name: "neighbour_address", Type: String, Required: true}`, `{Name: "state", Type: String, Required: true, Enum: []string{"down", "attempt", "init", "2way", "exstart", "exchange", "loading", "full", "other"}}`
- [ ] T011 [P] Add tests to internal/fact/fact_test.go: a full `ospf_interfaces` row passes; the same row without `auth_type` fails with `auth_type` in the error (a row that skipped the parser is still refused, research R6); `auth_type: "unknown"` passes; `auth_type: "md5-hmac"`, `state: "DR"`, `passive: "true"`, `network_type: "Broadcast"` fail; a row with `dr_router_id: "unknown"` passes; a full `ospf_neighbours` row passes and fails without `neighbour_router_id`. `area: "1"` and `area: "0.0.0.256"` fail and `area: "0.0.0.1"` passes (`DottedID`). Add a check that every schema field with `Missing` set is also `Required` (a table test over `fact.Families`)
- [ ] T012 In internal/pack/registry.go: add `PackSchema int \`yaml:"pack_schema"\`` to `Pack` ("the pack format version; absent means 1 (feature 011)"); add to `Step` `Guard bool \`yaml:"guard"\``, `NothingPattern string \`yaml:"nothing_pattern"\`` ("a guard's \"nothing here\" pattern, matched against the whole output with all whitespace removed"), `JoinOn []string \`yaml:"join_on"\`` and `Unmatched string \`yaml:"unmatched"\``, each with a one-line comment pointing to data-model.md. In the load checks, refuse (one message naming pack, recipe and step): `pack_schema` other than absent, 1 or 2; `guard`, `nothing_pattern`, `join_on` or `unmatched` in a pack below 2; a guard that is not the first step, has a `map`, has `empty_lines`, or has no `nothing_pattern`; `nothing_pattern` on a step that is not a guard; a `nothing_pattern` that does not compile; `join_on` with no row-creating step (not a guard, not a join) before it; `join_on` without `unmatched`, `unmatched` without `join_on`, `unmatched` other than `fail` or `ignore`; a `join_on` field that is not in the family's schema; a `defaults` entry on a field whose schema has `Missing`. `covers` stays valid in both versions. Add `missingCost` entries `"ospf_interfaces": noOSPF` and `"ospf_neighbours": noOSPF` with `noOSPF = "no OSPF data for these devices"`
- [ ] T013 [P] Add cases to `TestRefusals` in internal/pack/registry_test.go for every refusal of T012, each with the expected message fragment, and a case that a pack with no `pack_schema` and a `covers` key loads (version 1), and that the same pack with `pack_schema: 2` loads too
- [ ] T014 In internal/parse/parse.go: rename the body of `Parse` to `ParseDetail(reg, platform, family, im, outputs) (status string, rows []map[string]any, unmatched []string, err error)` and keep `Parse` as a wrapper that drops `unmatched`, so existing callers do not change. In the step loop: (1) a guard step: a blank output (only whitespace) returns `ParseFailed` with "guard step <command> gave a blank output"; an output whose whitespace-free form matches `st.NothingPattern` sets a flag "guard says none" and continues; otherwise the template runs as for any step, must yield a row (existing "template yielded no row" error), and its rows are discarded, not mapped. (2) a step with `JoinOn`: map its rows as usual, then for each mapped row, add its fields to every row so far holding the same values on all `JoinOn` fields, without overwriting a set value; a mapped row matching no row is, with `unmatched: fail`, `ParseFailed` with "step <command>: no row for <field>=<value>, ..." and, with `ignore`, its key string appended to `unmatched`. A join step never sets `empty = false`. (3) after the loop, if the guard said none and any non-guard step was not nothing, return `ParseFailed` with "guard says nothing here but step <command> has output"; if the guard said none, return `Empty`. Keep the existing order: merge, defaults, `settle`, `fact.Validate`
- [ ] T015 In `mapRow` of internal/parse/parse.go, next to the `Canonical` and `MAC` normalisation, convert a `DottedID` value that is a plain integer from 0 to 4294967295 to dotted form (1 becomes `0.0.0.1`, 256 becomes `0.0.1.0`); a dotted value is kept, anything else is kept as is so `fact.Validate` refuses it. In `settle`, after the existing rules: a field whose schema has `Missing` and that is absent from the row is set to that value. Update the comments of `mapRow` and `settle` to list both
- [ ] T016 [P] Add tests to internal/parse/parse_test.go on hand-written outputs with inline templates and a recipe, in the style of `TestBGPPeerRules`: `TestGuard`: the "none" document pretty-printed on three lines and on one line (`{"vrfs": {}}`) both give `empty`; a blank guard output gives `parse_failed`; the "none" document with a row step holding output gives `parse_failed`; `% Invalid input` as guard output gives `parse_failed`; a populated guard with blank row steps gives `empty`; a populated guard with a row step gives `collected` and no row from the guard. `TestJoin`: a join step fills `router_id` and `area_type` on matching rows and creates none; it does not overwrite a value a row step set; a join row matching nothing gives `parse_failed` naming the step with `fail` and the same rows plus one `unmatched` key with `ignore`; a device whose only non-blank row-step output is the join step gives `empty`; an interface row whose area has no join row gives `parse_failed` with `area_type` in the error. `TestDottedID`: an area mapped as `1` gives `0.0.0.1`, `256` gives `0.0.1.0`, `0.0.0.1` is kept, `x` and `4294967296` give `parse_failed`; a join on `area` matches when the row step prints `1` and the join step prints `0.0.0.1` (conversion happens before the join)

**Checkpoint**: `go test ./internal/...` passes; no template exists yet.

---

## Phase 3: User Story 1 - See where OSPF runs and with whom (Priority: P1), MVP

**Goal**: one row per OSPF interface and one per adjacency, with VRF, instance, router ID, area and
area type, network type, cost, passive flag, state, DR and BDR router IDs, authentication type.

**Independent Test**: `go test ./packs/...` with the lab stopped: core1's and core2's recordings
give the rows of their `.facts.yml` (quickstart section 1); then quickstart section 5 on the VM.

- [ ] T017 [P] [US1] Write packs/arista_eos/templates/show_ip_ospf_vrf_all_json.textfsm: one value `VRF`, recorded for each key at the second level under `"vrfs"` in the pretty-printed JSON (a line `^\s+"(\S+)": \{$` directly inside the `vrfs` object; track nesting with states, not with a fixed indent count). The "none" document yields no row. A comment: this template only proves OSPF runs (research R14); the pack has no JSON parser and none is added
- [ ] T018 [P] [US1] Write packs/arista_eos/templates/show_ip_ospf_interface_vrf_all.textfsm: values `INTERFACE` (from `^(\S+) is (up|down)`, a new record), `INSTANCE`, `VRF`, `AREA` (from `Interface Address <ip>/<len>, instance <id>, VRF <vrf>, Area <area>`), `NETWORK_TYPE` and `COST` (from `Network Type <type>, Cost: <n>`), `STATE` (from `State <state>, Priority` on the transmit delay line, the state may hold a space: `Backup DR`), `DR` (from `Designated Router is <id>`), `BDR` (from `Backup Designated Router is <id>`; `No Backup Designated Router on this network` gives nothing), `PASSIVE` (`Passive Interface` when the neighbour count line ends with `(Passive Interface)`), `AUTH` (`No authentication` or the authentication line forms T008 recorded). Every other line is skipped: transmit delay, priority, interface speed, timers, neighbour count, traffic engineering, TI-LFA (FR-007)
- [ ] T019 [P] [US1] Write packs/arista_eos/templates/show_ip_ospf_neighbor_detail_vrf_all.textfsm: values `NEIGHBOR_ID`, `INSTANCE`, `VRF`, `ADDRESS` (from `Neighbor <id>, instance <n>, VRF <vrf>, interface address <ip>`, a new record), `AREA` and `INTERFACE` (from `In area <area> interface <name>`), `STATE` (from `State is <STATE>,`). Skipped: priority, state change count, ages, `DR IP Address ... BDR IP Address ...`, options, dead timer, inactivity and retransmission counts, graceful restart lines (FR-007, research R3)
- [ ] T020 [P] [US1] Write packs/arista_eos/templates/show_ip_ospf_vrf_all.textfsm: `INSTANCE`, `ROUTER_ID`, `VRF` (Filldown, from `^OSPF instance (\S+) with ID (\S+) VRF (\S+)`), `AREA` (from `^\s+Area (\S+)$`, a new record per area), `AREA_TYPE` (from `It is a (\S+) area`, and the stub and NSSA forms T008 recorded). Every LSA, SPF, threshold and timer line is skipped
- [ ] T021 [US1] Write packs/arista_eos/recipes/ospf_interfaces.yaml: `family: ospf_interfaces`, one implementation `id: ospf-interfaces-cli`, `versions: '>=4.36'` (comment: only 4.36 recorded and scanned for secrets, research R13), `transport: ssh`, steps G, I, J of the table above. G: `guard: true`, `nothing_pattern: '^\{"vrfs":\{\}\}$'`, no map. I: map `vrf: VRF`, `instance: INSTANCE`, `interface: INTERFACE`, `area: AREA`, `network_type: NETWORK_TYPE`, `cost: COST`, `state: STATE`, `dr_router_id: DR`, `bdr_router_id: BDR`, `passive: PASSIVE`, `auth_type: AUTH`. J: `join_on: [vrf, instance, area]`, `unmatched: ignore`, map `vrf: VRF`, `instance: INSTANCE`, `area: AREA`, `router_id: ROUTER_ID`, `area_type: AREA_TYPE`. `merge_on: [vrf, interface]`. `values`: `network_type: { Broadcast: broadcast, Point-To-Point: point_to_point, NBMA: nbma, Point-To-Multipoint: point_to_multipoint, '*': other }`; `state: { DR: dr, Backup DR: bdr, DR Other: drother, P2P: point_to_point, Loopback: loopback, Waiting: waiting, Down: down, '*': other }`; `passive: { Passive Interface: 'yes' }`; `auth_type: { No authentication: none, '*': other }` plus the md5 and sha forms of T008; `area_type: { normal: normal, stub: stub, NSSA: nssa }` with the forms of T008 and no `'*'` (an unknown wording leaves the required field missing: `parse_failed`, research R4); `dr_router_id` and `bdr_router_id: { 0.0.0.0: '' }`. `defaults: { passive: 'no' }`, and no default on `auth_type` (refused at load, T012). Comments: why a guard (R14), why the instance view is joined and why `ignore` (R2), why no timer is mapped (FR-007). Use the exact words T008 recorded where they differ
- [ ] T022 [US1] Write packs/arista_eos/recipes/ospf_neighbours.yaml: `family: ospf_neighbours`, `id: ospf-neighbours-cli`, same `versions` and `transport`, steps G, N, J. G as in T021. N: map `vrf: VRF`, `instance: INSTANCE`, `interface: INTERFACE`, `area: AREA`, `neighbour_router_id: NEIGHBOR_ID`, `neighbour_address: ADDRESS`, `state: STATE`. J: `join_on: [vrf, instance, area]`, `unmatched: ignore` (an area whose interfaces have no neighbour is normal, R2), map `vrf`, `instance`, `area`, `router_id`. `merge_on: [vrf, interface, neighbour_router_id]`. `values.state: { DOWN: down, ATTEMPT: attempt, INIT: init, 2WAYS: 2way, 2-WAY: 2way, EXSTART: exstart, EXCHANGE: exchange, LOADING: loading, FULL: full, '*': other }`, with the 2-way spelling T008 recorded
- [ ] T023 [US1] Set `pack_schema: 2` in packs/arista_eos/pack.yaml and bump `version` from 4 to 5
- [ ] T024 [US1] Write packs/arista_eos/testdata/lab/core1_ospf_interfaces.facts.yml: `status: collected`, two rows, VRF `default`, `instance: '1'`, `router_id: 10.0.200.1`, `area: 0.0.0.0`, `area_type: normal`, `auth_type: none`: `Loopback0` (`network_type: broadcast`, `cost: 10`, `passive: 'yes'`, `state: dr`, `dr_router_id: 10.0.200.1`, no BDR) and `Ethernet5.100` (`broadcast`, 10, `passive: 'no'`, `state: dr`, `dr_router_id: 10.0.200.1`, `bdr_router_id: 10.0.200.2`). core2_ospf_interfaces.facts.yml the same with router ID 10.0.200.2 and `Ethernet5.100` in `state: bdr`. core1_ospf_neighbours.facts.yml: one row, `default`, `'1'`, `router_id: 10.0.200.1`, `interface: Ethernet5.100`, `area: 0.0.0.0`, `neighbour_router_id: 10.0.200.2`, `neighbour_address: 10.0.200.129`, `state: full`; core2 the mirror (10.0.200.1, 10.0.200.128). Values the spec does not fix as recorded in T007 (see the rule above the phases)
- [ ] T025 [US1] Add `TestOSPFInterfacesJoinInterfaces` to packs/packs_test.go: for core1, every `interface` value of the `ospf_interfaces` and `ospf_neighbours` rows parsed from the fixtures is a canonical name the pack's interface naming rules leave unchanged (SC-005; the `interfaces` recordings of 007 are older than the subinterface, so the check is on canonical form, and the live check is quickstart section 4)

**Checkpoint**: US1 is shippable: the EVPN lab's OSPF interfaces and adjacencies are readable
through the facts endpoint.

---

## Phase 4: User Story 2 - See an adjacency that is not up and an interface with no adjacency (Priority: P1)

**Goal**: a stuck adjacency, a non-backbone area, a passive interface, authentication types and a
non-default VRF are recorded with what tells them apart.

**Independent Test**: `go test ./packs/...` with the lab stopped: sw1's and sw2's recordings give the
rows of their `.facts.yml`; then quickstart section 4 on the VM.

- [ ] T026 [US2] Write packs/arista_eos/testdata/lab/sw1_ospf_interfaces.facts.yml: `status: collected`, rows for `Loopback0` (`default`, `'1'`, area `0.0.0.0` `normal`, `passive: 'yes'`, `auth_type: none`), `Vlan41` (`default`, `'1'`, `0.0.0.0`, `broadcast`, `auth_type: md5`, DR and BDR router IDs 10.255.0.1 and 10.255.0.2 in the elected order), `Vlan42` (`default`, `'1'`, `0.0.0.1`, `area_type: stub`, `network_type: point_to_point`, `state: point_to_point`, `auth_type: none`, no DR or BDR), `Vlan43` (`vrf: blue`, `instance: '2'`, `router_id: 10.255.2.1`, `0.0.0.0`, `auth_type: sha`), `Vlan44` (`default`, `'1'`, `0.0.0.0`, `auth_type: none`). Write sw2_ospf_interfaces.facts.yml the same with sw2's router IDs. Values the spec does not fix as recorded (T005, T008); a departure from an FR stops the task (rule above the phases)
- [ ] T027 [US2] Conditional on T008 (research R5, spec FR-004a): if EOS prints DR or BDR lines on point-to-point VLAN 42, change `OnlyWith` in internal/fact/fact.go from one value to a list of values (`OnlyWith: fact.When{"network_type", "broadcast", "nbma"}` or an equivalent small type; keep `bgp_peers`' single-value uses working), set it on `dr_router_id` and `bdr_router_id`, and add tests: in internal/fact/fact_test.go a point-to-point row with `dr_router_id` is refused, a broadcast row with it passes; in internal/parse/parse_test.go a point-to-point row whose output prints DR lines gives no `dr_router_id` (dropped by `settle`, as stale BGP fields are). Update data-model.md and contracts/fact-families.md in the same change. If EOS prints no DR line on point-to-point, mark this task done as "not applicable": sw1's `Vlan42` row in T026's `.facts.yml` already asserts both fields absent
- [ ] T028 [US2] Write packs/arista_eos/testdata/lab/sw1_ospf_neighbours.facts.yml: `status: collected`, rows on `Vlan41`, `Vlan42` (`state: full`, `router_id: 10.255.0.1`, `neighbour_router_id: 10.255.0.2`), `Vlan43` (`vrf: blue`, `instance: '2'`, `router_id: 10.255.2.1`, `neighbour_router_id: 10.255.2.2`, `full`) and `Vlan44` (the recorded state, `exstart` or `exchange`); no row on `Loopback0`. sw2_ospf_neighbours.facts.yml the mirror. Values the spec does not fix as recorded; a departure from an FR stops the task (rule above the phases)
- [ ] T029 [US2] Add `TestOSPFAuthUnknown` to internal/parse/parse_test.go (FR-013a, spec story 2 scenario 5): an inline `ospf_interfaces` recipe whose row step maps no `auth_type` (the template has no `AUTH` value) gives rows with `auth_type: "unknown"`, never `"none"` and never absent; the same recipe whose template captures `No authentication` gives `"none"`; one capturing an authentication line not in `values` gives `"other"` through `'*'`. No recorded output is used or edited. If T008 found a real `unknown` case, also add its recording and `.facts.yml` to packs/arista_eos/testdata/lab/
- [ ] T030 [US2] Add `regexp.MustCompile(`(md5|sha\d*) 7 \S+`)` to the patterns of `TestNoSecretInRecordedOutput` in packs/packs_test.go, commented "an OSPF message-digest key as EOS stores it (feature 011)". The `nm-lab-secret-` literal already covers the plain keys (research R9)
- [ ] T031 [US2] Conditional on T008 (research R14): if the guard JSON carries an interface count per area or per instance, extend show_ip_ospf_vrf_all_json.textfsm to capture it with the VRF and instance, and add a rule to `ParseDetail` (vendor-neutral: a guard value named by a new step key, e.g. `expect_rows: COUNT`, checked against the rows of the step that creates rows) so a blank interface step while that count is above zero gives `parse_failed`; add a `pack_schema: 2` load check for the key, a `TestGuard` case for each side (count 0 with a blank step, count 2 with a blank step), add the key to data-model.md (the guard section and the `pack_schema` version 2 list) and to T039's write-a-pack list, and update research R7 to say the #15 residual is closed for `ospf_interfaces`. Not applied to `ospf_neighbours`: a blank neighbour step stays valid. If the JSON has no such count, mark this task done as "not applicable" and leave R7 unchanged
- [ ] T032 [US2] Add `TestOSPFSteadyState` to packs/packs_test.go: parse sw1's recordings for both families twice with the step outputs unchanged and require identical rows, then check that the `Vlan44` row's state is not `full` (the stuck adjacency has a fixture, spec story 2 scenario 2)

**Checkpoint**: US1 and US2 together cover every row type of the spec.

---

## Phase 5: User Story 3 - Tell "no OSPF" from "not collected" (Priority: P2)

**Goal**: a device without OSPF is `empty`, decided by the guard; a device with OSPF and no
adjacency gives `collected` interfaces and `empty` neighbours; any error or a blank guard is
`parse_failed`; a blank output with a transport error is never `empty`.

**Independent Test**: `go test ./...`: sw4 and dc-leaf1 give `empty`, sw4noadj gives `collected`
then `empty`, the error recordings and a blank guard give `parse_failed`, the collector tests pass.

- [ ] T033 [US3] Write packs/arista_eos/testdata/lab/sw4_ospf_interfaces.facts.yml, sw4_ospf_neighbours.facts.yml, dc-leaf1_ospf_interfaces.facts.yml and dc-leaf1_ospf_neighbours.facts.yml with `status: empty` and no row. Write sw4noadj_ospf_interfaces.facts.yml (`collected`, one row: `Loopback0`, `default`, `'1'`, `router_id: 10.255.0.4`, `0.0.0.0`, `normal`, as recorded) and sw4noadj_ospf_neighbours.facts.yml (`status: empty`)
- [ ] T034 [US3] Add `TestOSPFErrorsAreNotEmpty` to packs/packs_test.go: for each of `sw2_show_ip_ospf_interface_vrf_all_denied_empty.raw` and `sw1_show_ip_ospf_neighbor_detail_vrf_all_invalid_empty.raw`, fed as the output of each step in turn of each recipe with the other steps given sw1's recordings, `parse.Parse` returns `parse_failed` and no row; a blank guard output with sw1's other recordings returns `parse_failed`; sw4's `_empty` recordings on every step return `empty` and no row, never `collected`; sw4's guard recording with sw1's interface recording returns `parse_failed` (the guard says none, a step has output) (FR-009)
- [ ] T035 [US3] Add a `Drop map[string]bool` to `Device` in internal/transport/fake/fake.go ("command fails mid-output: the session dropped"), returning a plain error from `Run` for that command, and a `NoPrompt bool` ("the CLI never prints a prompt after login"), making `Open` return `ErrIdleTimeout` as the SSH transport does (`TestOpenSlowCLI` in internal/transport/ssh/ssh_test.go covers that transport-level behaviour; nothing covers it at collector level). Add `TestBlankOutputWithErrorIsNeverEmpty` to internal/collector, next to the existing scrape tests, on a fakeos family: a step that stalls (`Stall`) gives that family `unreachable` with detail `timeout`; a step that overruns (`Overrun`) or drops (`Drop`) fails the task and writes no observation for the family; a device with `NoPrompt` gets the status the collector gives a session that cannot open (assert it as found, `unreachable` or a failed task); in no case is the family `empty` (FR-009, research R7)
- [ ] T036 [US3] In internal/collector/scrape.go, call `parse.ParseDetail` instead of `parse.Parse`, and when it returns `unmatched` keys, log one `slog.Info` line per observation: "join step rows matched no row", with `target`, `family`, `recipe` and `keys` (research R2: never dropped silently). Add a test in internal/collector only if the existing scrape tests can assert a log line without a lab; otherwise the `TestJoin` case of T016 covers the returned keys

**Checkpoint**: every story is covered with no lab running.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T037 [P] Add `ospf_interfaces` and `ospf_neighbours` to `NoRecipeFamilies` in internal/testutil/lab.go and update its comment ("... and bgp_peers of feature 010, the OSPF families of feature 011"). Add a test to internal/graph/graph_test.go, in the style of `TestReproducible`: project a snapshot, add `ospf_interfaces` and `ospf_neighbours` observations for its devices (rows from sw1's `.facts.yml`), project again, and require the same nodes, edges and verdict (FR-015). State in research R11 that discovery is unchanged by code inspection, as 010 did
- [ ] T038 [P] Merge specs/011-ospf-facts/contracts/fact-families.md into specs/001-crawl-loop/contracts/fact-families.md: rename the heading to "Routing families (features 010 and 011)", add `ospf_interfaces`, `ospf_neighbours` and their common rules after `bgp_peers`, and update the closing paragraph ("... feature 010 adds `bgp_peers`; feature 011 adds `ospf_interfaces` and `ospf_neighbours`") (FR-011)
- [ ] T039 [P] Update docs/how-to/write-a-pack.md: `pack_schema` (absent means 1; 2 adds `guard`, `nothing_pattern`, `join_on`, `unmatched`; `covers` valid in both); the `guard` step (first step, no `map`, no `empty_lines`, a `nothing_pattern` matched against the whitespace-free output, a blank output is a failure); `join_on` and `unmatched: fail | ignore`; that a field with `Missing` cannot take a recipe default; the two families in the missing-recipe table with "No OSPF data for these devices: no OSPF interfaces or adjacencies. The graph is unaffected." Check README.md for any list of families or pack keys and update it if there is one (FR-017)
- [ ] T040 [P] Add bruno/facts/ospf-interfaces.bru and bruno/facts/ospf-neighbours.bru, copied from bruno/facts/bgp-peers.bru with `name` and the family in the URL changed and `seq` 13 and 14 (FR-017)
- [ ] T041 [P] Add `TestOSPFUnknownAndAbsent` to internal/api/facts_test.go, in the style of `TestBGPEmptyAndAbsentLists`: store an `ospf_interfaces` observation with one row holding `auth_type: "unknown"` and no `dr_router_id` or `bdr_router_id`, read it through the facts endpoint, and require `auth_type` to be `"unknown"` and both DR fields to be absent keys, not null or empty (FR-016, FR-004, FR-004a)
- [ ] T042 [VM] Run quickstart section 4 against `test/lab` and check every expectation listed there, including the volatility check (two crawls a few minutes apart, identical rows for both families, SC-004) and a search of the object store bucket and `observation.parsed` for `nm-lab-secret-` (SC-002). Check that every `interface` of the OSPF rows is in the same switch's live `interfaces` rows (SC-005). Check what earlier features prove still holds: sw1 over SNMP v2c and SSH, sw2 over SNMPv3 only, sw3 `denied`, 172.20.20.9 `unreachable`, sw1 and sw4 not merged, the 007 to 010 families unchanged on sw1 and sw2. Then section 5 against the EVPN lab (SC-003: every switch has both observations, none `parse_failed`; two crawls with traffic running give identical rows). Write what the crawl showed, and any divergence, at the end of specs/011-ospf-facts/quickstart.md under "What the lab confirmed", as 010 did. Destroy `test/lab` on the VM and remove the copied folders afterwards
- [ ] T043 Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` with the 007 quickstart section 1 environment (nothing skipped), and `govulncheck ./...`

---

## Dependencies & Execution Order

- Phase 1 first: T001 to T003 in parallel, then T004 to T008 on the VM in order, then T009.
- Phase 2 needs nothing from Phase 1 and can start at once: T010 then T011; T012 then T013; T014
  and T015 need T010 and T012, T016 needs both.
- US1 (Phase 3) needs Phase 1 (recordings, T008's words) and Phase 2: T017 to T020 in parallel,
  then T021 to T025 in order.
- US2 (Phase 4) needs US1 (same templates and recipes): T026 to T032 in order; T029 and T030 only
  need Phase 2 and can run earlier.
- US3 (Phase 5) needs US1's recipes (T021, T022); T035 needs nothing from this feature and can run
  any time; T036 needs T014.
- Phase 6: T037 to T041 any time after T010; T042 after every story; T043 last.

## Parallel Example: User Story 1

```text
T017 guard template  |  T018 interface template  |  T019 neighbour template  |  T020 instance template
then T021 (interfaces recipe), T022 (neighbours recipe), T023 (pack.yaml), T024 (facts.yml), T025
```

Phase 2: T011 (fact tests), T013 (registry tests) and T016 (parse tests) touch different files and
can be written side by side once their code task is done.

## Implementation Strategy

1. Phase 1 on the VM in one sitting: every fixture, every "to confirm" settled (including the
   guard's interface count and the SNMP auth type), the secret grep.
2. Phase 2, then US1: the EVPN lab's OSPF, shippable on its own.
3. US2: areas, passive, authentication, the stuck adjacency and VRF `blue` from `test/lab`.
4. US3: `empty`, errors, the guard's failure cases and the transport failure paths.
5. Phase 6, with the live crawl as the last check before the pull request.
