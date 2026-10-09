---
description: "Task list for VLANs and interface VLAN membership on Arista EOS"
---

# Tasks: VLANs and interface VLAN membership on Arista EOS

**Input**: Design documents from `/specs/008-vlan-membership-facts/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/fact-families.md,
quickstart.md

**Tests**: Included. FR-014 asks for a template test per row type runnable with no lab, and the
constitution for template changes checked by replay over recorded output.

**Organization**: Tasks are grouped by user story. US1 (`vlans`) and US2 (`interface_vlans`) are
both P1 and only share Phase 2; they can run side by side. US3 adds the `empty` cases to the US2
recipe, so it follows US2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 007.
- **[VM]** in a description: needs the `NetLab` VM (ssh host `NetLab`), where `test/lab` and
  arista-evpn-vxlan-clab run. Every other task runs on the Mac with no lab.

Commands, templates and fixtures used throughout (research R1, R2). Every command is sent with
` | no-more` appended. A fixture is `packs/arista_eos/testdata/lab/<switch>_<template base>.raw`,
`<switch>_<template base>.yml` beside it (ntc-templates `parsed_sample` format, as in 007), and an
output that gives no row has `_empty` before `.raw`.

| Family | Step | Command | Template (`packs/arista_eos/templates/`) |
| ------ | ---- | ------- | ---------------------------------------- |
| `vlans` | 1 | `show vlan` | `show_vlan.textfsm` |
| `interface_vlans` | 1 | `show interfaces status` | `show_interfaces_status_vlan.textfsm` |
| `interface_vlans` | 2 | `show interfaces trunk` | `show_interfaces_trunk.textfsm` |

`show interfaces status` is also the `interfaces` family's first step, with its own template. The
same recording is saved under both names (`<switch>_show_interfaces_status.raw` and
`<switch>_show_interfaces_status_vlan.raw`), because `TestFamiliesFromLab` finds a step's recording
by template name.

Recording on the VM, as in 007's quickstart section 3:
`docker exec clab-netmapper-<switch> Cli -p 15 -c '<command> | no-more' > <fixture>` for `test/lab`,
`docker exec clab-arista-evpn-fabric-<node> Cli -p 15 -c ...` for the EVPN lab, then strip the
`> <command>` first line `Cli -c` adds to a piped command: `sed -i '1{/^> /d}' <fixtures>`.

---

## Phase 1: Setup

**Purpose**: Give `test/lab` its VLAN configuration (research R7), record every command once on
both labs, and settle the "to confirm" items of research before any template exists.

- [ ] T001 [P] Edit test/lab/sw1.cfg. Keep every existing line (the 007 `empty` cases depend on sw1 having no management configuration). Add a comment block "VLAN configuration for feature 008 (research R7)", then: `vlan 10` / `name USERS`; `vlan 20` / `name SERVERS`; `vlan 30` / `name LAB-30`; `vlan 31` / `name SUSPENDED` / `state suspend`; `vlan 32` / `name LOCAL-SHUT` with the configuration that makes `show vlan` print `act/lshut` (try `shutdown` under the VLAN; if cEOS refuses it, leave VLAN 32 active and note it for T007); `vlan 40-49`; `vlan 99` / `name NATIVE`. `interface Ethernet1` / `switchport mode trunk` / `switchport trunk native vlan 99` / `switchport trunk allowed vlan 10,20,30-32,40-49,99`. `interface Ethernet2` and `interface Ethernet3` / `channel-group 10 mode active`. `interface Port-Channel10` / `switchport mode trunk`. `interface Ethernet4` / `switchport mode access` / `switchport access vlan 10`
- [ ] T002 [P] Edit test/lab/sw2.cfg the same way as T001 (same VLANs, same Ethernet1 trunk, `Port-Channel10` on Ethernet3 and Ethernet4, access VLAN 10 on Ethernet5), plus `vlan 50` / `name SW2-ONLY` (declared on sw2 only, allowed on no trunk) and `interface Ethernet2` / `no switchport` (routed towards sw3, so EOS allocates an internal VLAN). Keep every 007 line
- [ ] T003 [P] Edit test/lab/two-switch.clab.yaml: add links `["sw1:eth2", "sw2:eth3"]`, `["sw1:eth3", "sw2:eth4"]`, `["sw1:eth4", "sw2:eth5"]`. Extend the header comment: sw1 and sw2 carry the VLAN configuration of feature 008 (trunk on eth1 with native 99 and a pruned list, Port-Channel10 on the two new links allowing every VLAN, access VLAN 10 on the third, sw2 eth2 routed); sw4 deployed alone has no Ethernet port and is the `empty` `interface_vlans` case
- [ ] T004 [VM] Deploy: `sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure`. On sw1 and sw2 check `show port-channel` (Port-Channel10, two active members), `show vlan` (VLAN 31 `suspended`, VLAN 32 `act/lshut`), `show interfaces trunk` (Ethernet1 and Port-Channel10 trunking). Check the 001 to 007 behaviour still holds: `show lldp neighbors` on sw1 lists sw2 on Ethernet1 to Ethernet4, and sw3 still refuses the netmapper account
- [ ] T005 [VM] Record on sw1 and sw2 the three step commands of the table above, into `sw1_show_vlan.raw`, `sw2_show_vlan.raw`, `sw1_show_interfaces_status_vlan.raw`, `sw2_show_interfaces_status_vlan.raw`, `sw1_show_interfaces_trunk.raw`, `sw2_show_interfaces_trunk.raw`. Copy each status recording over `sw1_show_interfaces_status.raw` and `sw2_show_interfaces_status.raw` too (refreshed for the new links). Record `show vlan internal usage` on sw2 into `sw2_show_vlan_internal_usage.raw` (test evidence only, no recipe sends it; T018 gives it its own template)
- [ ] T006 [VM] Record the variants research R2, R3 and R5 leave open, each from a temporary change on the running sw1 (configure, record, then `configure replace flash:startup-config` or redeploy sw1): (a) `interface Port-Channel10` / `shutdown`, record `show interfaces trunk` into `sw1_show_interfaces_trunk_po10_down.raw` and `show interfaces status` into `sw1_show_interfaces_status_vlan_po10_down.raw`; (b) `interface Ethernet1` / `switchport trunk allowed vlan none`, record `sw1_show_interfaces_trunk_none.raw`; (c) `interface Ethernet1` / `switchport trunk allowed vlan` with every even VLAN from 2 to 200, record `sw1_show_interfaces_trunk_wrapped.raw`; (d) `interface Ethernet4` / `shutdown`, record `sw1_show_interfaces_status_vlan_access_down.raw`
- [ ] T007 [VM] Deploy sw4 alone (`containerlab deploy -t test/lab/two-switch.clab.yaml --node-filter sw4`), record step 1 and 2 of `interface_vlans` into `sw4_show_interfaces_status_vlan_empty.raw` and `sw4_show_interfaces_trunk_empty.raw`. On the EVPN lab record the three step commands on dc-leaf1 and campus-access1 (`dc-leaf1_show_vlan.raw`, `dc-leaf1_show_interfaces_status_vlan.raw`, `dc-leaf1_show_interfaces_trunk.raw`, same for campus-access1), on dc-spine1 `dc-spine1_show_vlan.raw`, `dc-spine1_show_interfaces_status_vlan_empty.raw`, `dc-spine1_show_interfaces_trunk_empty.raw`, and `show vlan internal usage` on dc-leaf1 into `dc-leaf1_show_vlan_internal_usage.raw`. Strip the `> ` first line on every file of T005 to T007
- [ ] T008 Update specs/008-vlan-membership-facts/research.md from the T004 to T007 recordings: replace each "To confirm when recording" in R1, R2, R3, R5, R6, R8 with what EOS printed. Settle the open rule of R2: if `sw1_show_interfaces_trunk_po10_down.raw` omits Port-Channel10, keep the "VLAN lists are those of an active trunk" line in contracts/fact-families.md and data-model.md and add the same sentence to the spec edge case on shut ports in spec.md; if it lists it, delete that line from both files. If VLAN 32 could not be put in `act/lshut` (T001), write the divergence in R6 and use the edited-copy fallback R6 describes in T018
- [ ] T009 Run `go test -count=1 ./packs/...` on the Mac. `TestNoSecretInRecordedOutput` passes on the new files; `TestPacks` fails only on the new recordings that have no template yet (expected until Phase 3 and 4)

**Checkpoint**: every fixture exists; the lab is not needed again until Phase 6.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The two schemas and the VLAN list form every later task validates against.

- [ ] T010 Add to internal/fact/fact.go: a `VLANList bool` field attribute on `Field`, commented "a set of VLAN IDs as sorted, merged ranges: N or lo-hi, 1 to 4094 (feature 008)"; the family `vlans`: `{Name: "vlan_id", Type: Int, Required: true}`, `{Name: "name", Type: String}`, `{Name: "status", Type: String, Required: true, Enum: []string{"active", "suspended", "shutdown", "other"}}`; the family `interface_vlans`: `{Name: "interface", Type: String, Required: true, Canonical: true}`, `{Name: "mode", Type: String}` (no enum: `access`, `trunk`, or as the device names it), `{Name: "access_vlan", Type: Int}`, `{Name: "native_vlan", Type: Int}`, `{Name: "allowed_vlans", Type: Strings, VLANList: true}`, `{Name: "active_vlans", Type: Strings, VLANList: true}`, `{Name: "channel", Type: String, Canonical: true}`. A comment above the two families: layer 2 families, the engine reads neither in this feature (#37 will). In `check`, a `VLANList` field fails unless every item is `N` or `lo-hi` with 1 <= N, lo < hi <= 4094, items ascending, no two overlapping or touching (`["10-11", "12"]` fails, `["10-12"]` passes)
- [ ] T011 [P] Add tests to internal/fact/fact_test.go for T010: valid `vlans` and `interface_vlans` rows pass; `status: "act/lshut"` fails; a `VLANList` value fails for each of `["0"]`, `["4095"]`, `["20", "10"]`, `["10-12", "12-14"]`, `["10-11", "12"]`, `["12-10"]`, `["x"]`, and passes for `["1-4094"]`, `["10", "20", "30-32"]`
- [ ] T012 Add `NormaliseVLANs(items []string) ([]string, error)` to internal/pack/registry.go next to `NormaliseMAC`: join items with `,`, split on `,`, trim and skip empty tokens, parse each as `N` or `lo-hi`, error on anything else or an ID outside 1 to 4094, sort, merge overlapping and adjacent ranges, print `N` or `lo-hi`. No vendor term (constitution V)
- [ ] T013 [P] Add tests to internal/pack/registry_test.go for T012: `["1,40,4090-4091"]` gives `["1", "40", "4090-4091"]`; `["10-12", "11-20", "21"]` gives `["10-21"]`; `["2,4,", "6"]` (a list cut after a comma) gives `["2", "4", "6"]`; `["1-4094"]` unchanged; `["30-32", "10"]` gives `["10", "30-32"]`; `["0"]`, `["4095"]`, `["a-b"]` error
- [ ] T014 Apply `NormaliseVLANs` in `mapRow` of internal/parse/parse.go: for a field whose schema has `VLANList`, after `values` and `split` have built the list and before it is stored, replace the list with the normalised one; an error from it becomes the row's error (the observation is `parse_failed`). An empty list stays absent (existing rule)
- [ ] T015 [P] Add a test to internal/parse/parse_test.go for T014 on hand-written output with an inline template and recipe: a list value cut over two `List` lines and given out of order comes back sorted and merged; `values` mapping a token to `''` leaves the field absent; a bad token makes `Parse` return `parse_failed`
- [ ] T016 [P] Add `"vlans"` and `"interface_vlans"` to `missingCost` in internal/pack/registry.go, both with a new constant `noVLANs = "no VLAN data for these devices; they will join no L2 domain"`

**Checkpoint**: `go test ./internal/...` passes; the schemas accept the rows of data-model.md.

---

## Phase 3: User Story 1 - See the declared VLANs of a device (Priority: P1), MVP

**Goal**: one `vlans` row per declared VLAN with ID, name and status; no internal VLAN.

**Independent Test**: `go test -count=1 -run 'TestPacks|TestFamiliesFromLab|TestInternalVLAN' ./packs/` passes from the recordings alone; on a live crawl (T034) the `vlans` rows of sw1 and sw2 match their configuration.

- [ ] T017 [US1] Write packs/arista_eos/templates/show_vlan.textfsm (research R1): Values `VLAN_ID (\d+)`, `NAME (\S+)`, `STATUS (\S+)`; skip the `VLAN  Name ... Ports` header, the dashed line, blank lines, continuation lines of the Ports column (leading spaces) and a footnote line about dynamic VLANs; record `^${VLAN_ID}\*?\s+${NAME}\s+${STATUS}(\s+.*)?$$`; any other line `-> Error`. Base it on the T005 and T007 recordings, not on memory
- [ ] T018 [US1] Write packs/arista_eos/templates/show_vlan_internal_usage.textfsm (Values `VLAN_ID (\d+)`, `INTERFACE (\S+)`, one record per `^${VLAN_ID}\s+${INTERFACE}` line, anything else `-> Error`). No recipe uses it: it exists so `TestPacks`, which picks the template with the longest name contained in the recording's name, parses `*_show_vlan_internal_usage.raw` with it and not with `show_vlan.textfsm`, and so T021 can read the IDs. Then write the `.yml` beside each `*_show_vlan_internal_usage.raw` and each `*_show_vlan.raw` of T005 and T007 (sw1, sw2, dc-leaf1, campus-access1, dc-spine1): every VLAN line of the recording, raw values as printed (`act/lshut` stays `act/lshut` here). If T008 chose the R6 fallback, add `sw2_show_vlan_lshut_edited.raw` (a copy of `sw2_show_vlan.raw` with VLAN 32's status changed to `act/lshut`, and a first comment line saying it is edited, if the template allows one; otherwise say so in the `.yml`) and its `.yml`
- [ ] T019 [US1] Write packs/arista_eos/recipes/vlans.yaml: `family: vlans`, one implementation `id: vlans-cli`, `versions: '>=4.36'`, `transport: ssh`, one step `command: show vlan | no-more`, `template: show_vlan.textfsm`; `map: { vlan_id: VLAN_ID, name: NAME, status: STATUS }`; `values: { status: { active: active, suspended: suspended, act/lshut: shutdown, '*': other } }`. A comment: EOS always lists VLAN 1, so this family is never `empty` on EOS (research R8)
- [ ] T020 [US1] Write `sw1_vlans.facts.yml`, `sw2_vlans.facts.yml`, `dc-leaf1_vlans.facts.yml` in packs/arista_eos/testdata/lab/ (`status: collected`, one row per VLAN as `fact` rows: `vlan_id` int, `name`, `status` mapped). sw2: VLAN 31 `suspended`, VLAN 32 `shutdown` (unless T008 recorded otherwise), VLAN 50 present
- [ ] T021 [US1] Add `TestInternalVLANsAreNotDeclared` to packs/packs_test.go (FR-003, research R1): for sw2 and dc-leaf1, parse `<switch>_show_vlan_internal_usage.raw` with show_vlan_internal_usage.textfsm and run the `vlans` recipe over `<switch>_show_vlan.raw`; fail if any internal VLAN ID is a `vlans` row, and fail if the internal usage recording lists no VLAN (the test would prove nothing)

**Checkpoint**: US1 is complete and testable on its own.

---

## Phase 4: User Story 2 - See which VLANs each port carries (Priority: P1)

**Goal**: one `interface_vlans` row per switched port and per member, mode and VLANs per research R2 and R3.

**Independent Test**: `go test -count=1 -run 'TestPacks|TestFamiliesFromLab|TestTrunkVariants' ./packs/` passes; on a live crawl (T034) sw1 and sw2 give the rows of quickstart section 4.

- [ ] T022 [P] [US2] Write packs/arista_eos/templates/show_interfaces_status_vlan.textfsm (research R2): Values `PORT (\S+)`, `VLAN (\S+)`, `CHANNEL (\S+)`. Start from the column layout of show_interfaces_status.textfsm (do not edit that file). Rules in this order: a line whose Vlan column is `in <channel>` records `PORT` and `CHANNEL`; a line whose Vlan column is `routed` is matched and not recorded; any other port line records `PORT` and `VLAN`; the `Port  Name  Status  Vlan ...` header and blank lines are skipped; anything else `-> Error`
- [ ] T023 [P] [US2] Write packs/arista_eos/templates/show_interfaces_trunk.textfsm (research R2, R5): Values `PORT (\S+)`, `NATIVE (\d+)`, `List ALLOWED (\S+)`, `List ACTIVE (\S+)`. States: `Start` skips to the first table on `^Port\s+Mode\s+Status\s+Native vlan`; table 1 records `PORT`, `NATIVE`; on `^Port\s+Vlans allowed\s*$$` go to table 2, which records `PORT`, `ALLOWED`; on `^Port\s+Vlans allowed and active in management domain` go to table 3, which records `PORT`, `ACTIVE`; on `^Port\s+Vlans in spanning tree forwarding state` go to a state that ignores every line (STP is out of scope). A continuation line (leading spaces, no port) appends to the current `List` value, as T006 (c) shows; `^There are no active trunk ports` is matched and not recorded
- [ ] T024 [US2] Write the `.yml` beside every `*_show_interfaces_status_vlan*.raw` and `*_show_interfaces_trunk*.raw` of T005 to T007, raw values as printed (`All`, `in`-less channel names such as `Po10`)
- [ ] T025 [US2] Write packs/arista_eos/recipes/interface_vlans.yaml: `family: interface_vlans`, one implementation `id: interface-vlans-cli`, `versions: '>=4.36'`, `transport: ssh`; step 1 `command: show interfaces status | no-more`, `template: show_interfaces_status_vlan.textfsm`, `empty_lines` the header pattern and a pattern matching a `routed` line (research R2, R8); step 2 `command: show interfaces trunk | no-more`, `template: show_interfaces_trunk.textfsm`, `empty_lines: ['^There are no active trunk ports$']`; `merge_on: [interface]`; `map: { interface: PORT, mode: VLAN, access_vlan: VLAN, channel: CHANNEL, native_vlan: NATIVE, allowed_vlans: ALLOWED, active_vlans: ACTIVE }`; `values: { mode: { trunk: trunk, dot1q-tunnel: dot1q-tunnel, tap: tap, tool: tool, '*': access }, access_vlan: { trunk: '', dot1q-tunnel: '', tap: '', tool: '' }, allowed_vlans: { All: 1-4094, None: '', none: '' }, active_vlans: { All: 1-4094, None: '', none: '' } }`. Comments: why `show interfaces switchport` is not used (members print as access ports, research R2), and that a word in neither table fails as `parse_failed` on `access_vlan` rather than reading as an access port. Check the effective step map keeps `mode` and `access_vlan` off member rows: a member line has no `VLAN`, so both stay absent
- [ ] T026 [US2] Write `sw1_interface_vlans.facts.yml`, `sw2_interface_vlans.facts.yml`, `dc-leaf1_interface_vlans.facts.yml`, `campus-access1_interface_vlans.facts.yml` in packs/arista_eos/testdata/lab/ (`status: collected`). Expected per quickstart section 4: `Ethernet1` `{mode: trunk, native_vlan: 99, allowed_vlans: ["10", "20", "30-32", "40-49", "99"], active_vlans: <as recorded>}`; `Port-Channel10` `{mode: trunk, allowed_vlans: ["1-4094"], ...}`; the two members `{channel: Port-Channel10}` only; the access port `{mode: access, access_vlan: 10}`; no row for `Management0` or sw2 `Ethernet2`. dc-leaf1: `Port-Channel1` `allowed_vlans: ["40"]`, `Port-Channel999` `allowed_vlans: ["1-4094"]`, `active_vlans: ["1", "40", "4090-4091"]`, members `Ethernet1` and `Ethernet10`, no `Vxlan1` row. campus-access1: `Ethernet3` `{mode: access, access_vlan: 60}`, `Port-Channel10` `allowed_vlans: ["60"]`, members `Ethernet1`, `Ethernet2`
- [ ] T027 [US2] Add `TestTrunkVariants` to packs/packs_test.go: run the `interface_vlans` recipe over sw1's status recording with each T006 trunk variant and check: `_none` gives Ethernet1 `mode: trunk` with no `allowed_vlans` and no `active_vlans`; `_wrapped` gives `allowed_vlans` equal to the 100 even VLANs from 2 to 200, one item each; `_po10_down` (with `sw1_show_interfaces_status_vlan_po10_down.raw`) gives what T008 settled for a down trunk; `sw1_show_interfaces_status_vlan_access_down.raw` still gives Ethernet4 `{mode: access, access_vlan: 10}`

**Checkpoint**: US1 and US2 both pass with no lab.

---

## Phase 5: User Story 3 - Tell "no switched port" from "not collected" (Priority: P2)

**Goal**: a device with no switched port is `empty`; drift is `parse_failed`; another platform is `unsupported`.

**Independent Test**: `go test -count=1 -run 'TestFamiliesFromLab|TestDriftIsNotEmpty' ./packs/` and `go test ./internal/collector/` pass.

- [ ] T028 [P] [US3] Write `sw4_interface_vlans.facts.yml` and `dc-spine1_interface_vlans.facts.yml` in packs/arista_eos/testdata/lab/ with `status: empty` and `rows: []` (they read the `_empty` recordings of T007). `TestPacks` already checks those recordings give no template row
- [ ] T029 [P] [US3] Extend `TestDriftIsNotEmpty` in packs/packs_test.go: the `interface_vlans` recipe over sw1's status recording with one extra line whose Vlan column is an unknown word (`foo`) is `parse_failed`, not `collected` and not `empty`; over `dc-spine1_show_interfaces_trunk_empty.raw` with its message reworded is `parse_failed`; over `dc-spine1_show_interfaces_status_vlan_empty.raw` plus one non-routed line is not `empty`
- [ ] T030 [P] [US3] Check that another platform gets `unsupported` (`no_recipe`) for both families: find the existing test that asserts this for the 007 families (internal/collector/newpack_test.go or the fakeos pack tests) and extend it to `vlans` and `interface_vlans`; if it iterates over `fact.Families` already, note in the commit that no change was needed

**Checkpoint**: all stories pass with no lab.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T031 [P] Merge specs/008-vlan-membership-facts/contracts/fact-families.md into specs/001-crawl-loop/contracts/fact-families.md as a section "Layer 2 families (feature 008)" after the management families, and update its closing paragraph ("feature 007 adds ...; feature 008 adds `vlans` and `interface_vlans`") (FR-012)
- [ ] T032 [P] Update README.md and docs/how-to/write-a-pack.md where they list fact families or what a pack collects: add `vlans` and `interface_vlans`; in the pack guide, document the `VLANList` attribute (the parser normalises such a field, a pack only maps the device's tokens, `All` style words via `values`) (FR-018)
- [ ] T033 [P] Add bruno/facts/vlans.bru and bruno/facts/interface-vlans.bru, copied from bruno/facts/snmp.bru with `name` and the family in the URL changed and `seq` set after the last existing request (FR-018)
- [ ] T034 [VM] Run quickstart section 4 against `test/lab` (all four nodes) and check every expectation listed there; then section 5 against the EVPN lab (SC-003: 28 switches, both families, none `parse_failed`). Write what the crawl showed, and any divergence, at the end of specs/008-vlan-membership-facts/quickstart.md under "What the lab confirmed", as 004 did
- [ ] T035 Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` with the 007 quickstart section 1 environment (nothing skipped), and `govulncheck ./...`

---

## Dependencies & Execution Order

- Phase 1 first: T001 to T003 in parallel, then T004 to T007 on the VM in order, then T008, T009.
- Phase 2 needs nothing from Phase 1 and can start at once: T010 then T011; T012 then T013; T014
  needs T010 and T012; T015 needs T014; T016 any time.
- US1 (Phase 3) needs Phase 1 and Phase 2. US2 (Phase 4) needs Phase 1 and Phase 2, not US1.
- US3 (Phase 5) needs US2's recipe (T025).
- Phase 6: T031 to T033 any time after T010; T034 after every story; T035 last.

## Parallel Example: User Story 2

```text
T022 show_interfaces_status_vlan.textfsm   |  T023 show_interfaces_trunk.textfsm
then T024 (.yml files), T025 (recipe), T026 (facts.yml), T027 (variants)
```

Phase 3 (T017 to T021) can run beside Phase 4 by another person, since they touch different
templates, recipes and fixtures; both add one test to packs/packs_test.go, so merge those by hand.

## Implementation Strategy

1. Phase 1 on the VM in one sitting: every fixture, every "to confirm" settled.
2. Phase 2, then US1: `vlans` alone is shippable and is what #33 and #36 refer to.
3. US2, then US3: `interface_vlans` with its `empty` cases.
4. Phase 6, with the live crawl as the last check before the pull request.
