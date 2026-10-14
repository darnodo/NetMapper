---
description: "Task list for BGP sessions on Arista EOS"
---

# Tasks: BGP sessions on Arista EOS

**Input**: Design documents from `/specs/010-bgp-peer-facts/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/fact-families.md,
quickstart.md

**Tests**: Included. FR-013 asks for a test per row type runnable with no lab, the constitution for
template changes checked by replay over recorded output and for tie-breaking rules tested with
inputs that tie (absent against `[]`, two step orders).

**Organization**: Tasks are grouped by user story. US1 (sessions of a device, EVPN lab fixtures)
and US2 (sessions not up or not complete, `test/lab` fixtures) are both P1. US1 writes the recipe
and templates; US2 extends them with the shutdown, dynamic and description captures, so it follows
US1. US3 (`empty` and errors) adds `empty_lines` to the same recipe and follows US1.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 009.
- **[VM]** in a description: needs the `NetLab` VM (ssh host `NetLab`), where `test/lab` and
  arista-evpn-vxlan-clab run. Every other task runs on the Mac with no lab.

Commands, templates and fixtures used throughout (research R1, R2). Every command is sent with
` | no-more` appended. A fixture is `packs/arista_eos/testdata/lab/<switch>_<template base>.raw`,
`<switch>_<template base>.yml` beside it (ntc-templates `parsed_sample` format, as in 007), and an
output that gives no row has `_empty` before `.raw`.

| Step | Command | Template (`packs/arista_eos/templates/`) | Gives |
| ---- | ------- | ---------------------------------------- | ----- |
| 1 | `show bgp neighbors vrf all` | `show_bgp_neighbors_vrf_all.textfsm` | one record per peer |
| 2 | `show ip bgp summary vrf all` | `show_ip_bgp_summary_vrf_all.textfsm` | `ipv4 unicast` configured |
| 3 | `show ipv6 bgp summary vrf all` | `show_ipv6_bgp_summary_vrf_all.textfsm` | `ipv6 unicast` configured |
| 4 | `show bgp evpn summary` | `show_bgp_evpn_summary.textfsm` | `l2vpn evpn` configured |

Templates 2 to 4 have the same content: `TestFamiliesFromLab` finds a step's recording by template
name (research R2).

Recording on the VM, as in 007's quickstart section 3:
`docker exec clab-netmapper-<switch> Cli -p 15 -c '<command> | no-more' > <fixture>` for `test/lab`,
`docker exec clab-arista-evpn-fabric-<node> Cli -p 15 -c ...` for the EVPN lab, then strip the
`> <command>` first line `Cli -c` adds to a piped command: `sed -i '1{/^> /d}' <fixtures>`.

---

## Phase 1: Setup

**Purpose**: Give `test/lab` its BGP configuration (research R10), record every command once on
both labs, and settle the "to confirm" items of research before any template exists.

- [ ] T001 [P] Edit test/lab/sw1.cfg. Keep every existing line (the 007 `empty` cases and the 008 VLAN configuration depend on them). Add a comment block "BGP configuration for feature 010 (research R10)", then: `ip routing`; `interface Vlan10` / `ip address 10.0.10.1/24`; `interface Loopback0` / `ip address 10.255.0.1/32`; `interface Loopback1` / `ip address 10.255.1.1/32`; `ip route 10.255.0.2/32 10.0.10.2`; `ip route 10.255.0.12/32 10.0.10.2`; `router bgp 65101` / `router-id 10.255.0.1` / `neighbor 10.0.10.2 remote-as 65102` / `neighbor 10.0.10.2 password nm-lab-secret-bgp` / `neighbor 10.0.10.2 description TO-SW2` (session 1) / `neighbor 10.255.0.2 remote-as 65199` / `neighbor 10.255.0.2 update-source Loopback0` / `neighbor 10.255.0.2 ebgp-multihop 2` (session 2, wrong AS: sw2 is 65102 and has no neighbour for 10.255.0.1) / `neighbor 10.0.10.99 remote-as 65102` / `neighbor 10.0.10.99 shutdown` (session 3) / `neighbor 10.255.0.12 remote-as 65102` / `neighbor 10.255.0.12 update-source Loopback1` / `neighbor 10.255.0.12 ebgp-multihop 2` (session 4, into sw2's listen range) / `address-family ipv4` with `neighbor 10.0.10.2 activate`, `neighbor 10.255.0.2 activate`, `neighbor 10.0.10.99 activate`, `neighbor 10.255.0.12 activate` / `address-family ipv6` with `neighbor 10.0.10.2 activate` (the capability mismatch: sw2 does not activate ipv6)
- [ ] T002 [P] Edit test/lab/sw2.cfg the same way, keeping every existing line: `ip routing`; `interface Vlan10` / `ip address 10.0.10.2/24`; `interface Loopback0` / `ip address 10.255.0.2/32`; `interface Loopback1` / `ip address 10.255.0.12/32`; `ip route 10.255.0.1/32 10.0.10.1`; `ip route 10.255.1.0/24 10.0.10.1`; `router bgp 65102` / `router-id 10.255.0.2` / `neighbor 10.0.10.1 remote-as 65101` / `neighbor 10.0.10.1 password nm-lab-secret-bgp` / `neighbor 10.0.10.1 description TO-SW1` / `neighbor DYN peer group` / `neighbor DYN remote-as 65101` / `neighbor DYN ebgp-multihop 2` / `bgp listen range 10.255.1.0/24 peer-group DYN remote-as 65101` / `address-family ipv4` with `neighbor 10.0.10.1 activate` and `neighbor DYN activate`. No `address-family ipv6`. Adjust the listen range syntax to what cEOS 4.36 accepts and write any change in research R10 (T008)
- [ ] T003 [P] Extend the header comment of test/lab/two-switch.clab.yaml: sw1 (AS 65101) and sw2 (AS 65102) carry the BGP configuration of feature 010 over an SVI on VLAN 10: a session with a password and a description where sw1 alone activates ipv6 unicast, a wrong-AS peer and a shut peer on sw1, and a dynamic peer on sw2 from a listen range sw1 connects into from its Loopback1; sw4 has no BGP and is the `empty` case of `bgp_peers`. No new link
- [ ] T004 [VM] Deploy: `sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure`. Check on sw1 and sw2 with `show bgp neighbors vrf all | include BGP neighbor|state is` that session 1 is `Established` on both ends, session 2 is not `Established`, session 3 is `Idle` and shut, and sw2 lists the dynamic peer 10.255.1.1 `Established`. Check that 001 to 009 behaviour still holds: `show lldp neighbors` on sw1 lists sw2 on Ethernet1 to Ethernet4, `show interfaces trunk` still lists Ethernet1 and Port-Channel10, sw3 still refuses the netmapper account
- [ ] T005 [VM] Record the four step commands of the table above on sw1 and sw2 (`sw1_show_bgp_neighbors_vrf_all.raw`, `sw1_show_ip_bgp_summary_vrf_all.raw`, `sw1_show_ipv6_bgp_summary_vrf_all.raw`, `sw1_show_bgp_evpn_summary.raw`, same for sw2; an output with only header lines or nothing gets `_empty` before `.raw`). On sw4 record the four commands with `_empty` in every name. Record one temporary variant on sw1: `router bgp 65101` / `neighbor 10.0.10.2 shutdown`, record step 1 into `sw1_show_bgp_neighbors_vrf_all_down.raw` (a session that was established and went down: shows whether EOS keeps printing the last capabilities and router ID, research R4), then `no neighbor 10.0.10.2 shutdown`
- [ ] T006 [VM] Record the error outputs research R8 needs, as test input only: on sw1, a temporary role that denies BGP commands (`role nm-no-bgp` / `10 deny command show bgp.*` / `20 deny command show ip bgp.*` / `30 permit command .*`, a temporary user `nm-tmp` with that role), log in as that user and record step 1 into `sw1_show_bgp_neighbors_vrf_all_denied_empty.raw`; then remove the user and the role. On the EVPN lab, record `show bgp evpn summary vrf all` on core1 into `core1_show_bgp_evpn_summary_invalid_empty.raw`. Record step 1 through an SSH session as the netmapper user on sw4 (not `Cli -c`) into `sw4_show_bgp_neighbors_vrf_all_ssh_empty.raw`, to see the exact `% BGP inactive` line the collector receives. The names carry a template name and `_empty` because `TestPacks` parses every `.raw` with the template its name contains and requires `_empty` ones to yield no row, which an error text does; the recipe-level outcome (`parse_failed` or `empty`) is checked by T031. No `.yml`
- [ ] T007 [VM] On the EVPN lab, record the four step commands on dc-leaf1 and core1 (`dc-leaf1_show_bgp_neighbors_vrf_all.raw` and so on; core1's ipv6 and evpn outputs are header-only and get `_empty`), and on dc-access1 the four commands with `_empty`. Strip the `> ` first line on every file of T005 to T007. Grep every new file for `nm-lab-secret-` and for `password`: expected nothing; if a recording holds the password, stop and choose another command (FR-008)
- [ ] T008 Update specs/010-bgp-peer-facts/research.md from the T004 to T007 recordings: replace each "To confirm when recording" in R1 (Local AS line on a peer that never connected), R2 (a down peer is listed in its families' summaries), R3 (exact shutdown and dynamic markers; if `show bgp neighbors` has no dynamic marker, write the `show bgp peer-group` fallback step into R3 and add it to the step table of this file), R4 (whether capabilities and router ID survive a session going down, from `sw1_show_bgp_neighbors_vrf_all_down.raw`), R7 (description line), R8 (exact `% BGP inactive` over SSH, exact refusal text), R9 (what step 1 prints for the password session) with what EOS printed. If the Local AS line is missing for a never-connected peer, take `local_as` and `router_id` from the summary headers instead and amend data-model.md and the contract to say so
- [ ] T009 Run `go test -count=1 ./packs/...` on the Mac. `TestNoSecretInRecordedOutput` passes on the new files; `TestPacks` fails only on the new recordings that have no template yet (expected until Phase 3)

**Checkpoint**: every fixture exists; the lab is not needed again until Phase 6.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The schema, the three field attributes, the list merge and `covers` every later task
validates against. None of it needs the lab.

- [ ] T010 Add to `Field` in internal/fact/fact.go three attributes, each with a one-line comment: `EmptyList bool` ("a strings field stored as [] when it has no item, instead of left out (feature 010); the exception to the empty-set rule of feature 008"), `Set bool` ("a strings field whose order means nothing: the parser sorts it and drops duplicates (feature 010)"), `OnlyWith [2]string` ("the field is present exactly when the field OnlyWith[0] holds OnlyWith[1] (feature 010)"). Add the family `bgp_peers`, with a comment above it "The routing families (feature 010): the engine reads none of them, and no routing peer queues a find": `{Name: "vrf", Type: String, Required: true}`, `{Name: "peer_address", Type: String, Required: true}`, `{Name: "peer_as", Type: Int, Required: true}`, `{Name: "type", Type: String, Required: true, Enum: []string{"ebgp", "ibgp"}}`, `{Name: "state", Type: String, Required: true, Enum: []string{"idle", "connect", "active", "opensent", "openconfirm", "established", "other"}}`, `{Name: "shutdown", Type: String, Required: true, Enum: []string{"yes", "no"}}`, `{Name: "dynamic", Type: String, Required: true, Enum: []string{"yes", "no"}}`, `{Name: "local_as", Type: Int, Required: true}`, `{Name: "router_id", Type: String, Required: true}`, `{Name: "configured_address_families", Type: Strings, Required: true, EmptyList: true, Set: true}`, `{Name: "negotiated_address_families", Type: Strings, EmptyList: true, Set: true, OnlyWith: [2]string{"state", "established"}}`, `{Name: "peer_router_id", Type: String, OnlyWith: [2]string{"state", "established"}}`, `{Name: "peer_group", Type: String}`, `{Name: "description", Type: String}`
- [ ] T011 In internal/fact/fact.go `Validate` and `check`: a `Set` list fails unless sorted ascending with no duplicate; a field with `OnlyWith` fails when present on a row where `row[OnlyWith[0]] != OnlyWith[1]`, and an `OnlyWith` field that is also `EmptyList` fails when missing from a row where the condition holds. The error names the family, row and field, like the existing errors
- [ ] T012 [P] Add tests to internal/fact/fact_test.go for T010 and T011: a full established `bgp_peers` row passes; the same row with `negotiated_address_families: []` passes; with the field removed fails; a row with `state: active` and `negotiated_address_families: []` fails; with `peer_router_id` set fails; with neither passes; `configured_address_families: []` passes, missing fails; `["l2vpn evpn", "ipv4 unicast"]` (unsorted) and `["ipv4 unicast", "ipv4 unicast"]` fail; `state: "Established"`, `type: "external"`, `shutdown: "true"` fail
- [ ] T013 Change `merge` in internal/parse/parse.go: when both rows carry a `[]string` value for a field, append the later row's items that the earlier one lacks, instead of keeping the earlier value only. Scalars keep the existing first-wins rule
- [ ] T014 Add a pass in `Parse` of internal/parse/parse.go, after the merge loop and before `fact.Validate`: for each row and each field of the family's schema, in this order: (1) an `OnlyWith` field on a row where the condition does not hold is deleted (stale output, research R4 and R12); (2) an `EmptyList` field missing from a row is set to `[]string{}` when it has no `OnlyWith`, or when its `OnlyWith` condition holds; (3) a `Set` field is sorted and deduplicated. Nothing else is removed; a row still breaking a rule fails `fact.Validate` and the observation is `parse_failed` (existing path). Make sure `[]string{}` is stored, not nil, so the row marshals to JSON `[]`
- [ ] T015 [P] Add tests to internal/parse/parse_test.go for T013 and T014, on hand-written output with inline templates and a recipe, in the style of `TestStepMapMergeAndDefaults`: two steps mapping `'=ipv4 unicast'` and `'=l2vpn evpn'` into one list on the same key give `["ipv4 unicast", "l2vpn evpn"]`, and the same steps in the other order give the same row; a key present in no list step gives `configured_address_families: []`; an established row with no negotiated line gives `negotiated_address_families: []`, a non-established row whose output prints negotiated lines and a router ID gives neither field; a row that still breaks a rule after the pass (for example a `Set` value a template cannot produce, injected through `defaults`) makes `Parse` return `parse_failed` with the field name in the error, and no row
- [ ] T016 Add `Covers map[string][]string \`yaml:"covers"\`` to `Impl` in internal/pack/registry.go, commented "the values the steps can put in a list field, declared so a reader knows what an empty list covers (feature 010)". In the pack load checks, refuse an implementation when, for a field in `Covers`, the field is not a `Strings` field of the family, or the set of `=`-literal values that the steps' maps give that field differs from the declared list (a missing or an extra value both refuse). Add `"bgp_peers": "no BGP sessions for these devices"` to `missingCost`
- [ ] T017 [P] Add cases to `TestRefusals` in internal/pack/registry_test.go for T016: `covers` naming a value no step maps, a step mapping a value `covers` lacks, `covers` on a non-list field, each refused with a message naming the recipe and field; a matching `covers` loads

**Checkpoint**: `go test ./internal/...` passes; no template exists yet.

---

## Phase 3: User Story 1 - See the BGP sessions of a device (Priority: P1), MVP

**Goal**: one row per peer per VRF with VRF, local AS and router ID, peer address and AS, type,
state, configured and negotiated families, peer group; EVPN overlay sessions included.

**Independent Test**: `go test ./packs/...` with the lab stopped: dc-leaf1's and core1's recordings
give the rows of their `.facts.yml` (quickstart section 1); then quickstart section 5 on the VM.

- [ ] T018 [P] [US1] Write packs/arista_eos/templates/show_bgp_neighbors_vrf_all.textfsm: values `PEER_ADDRESS`, `PEER_AS`, `LINK` (`external` or `internal`), `PEER_ROUTER_ID`, `VRF`, `PEER_GROUP`, `STATE`, `NEGOTIATED` (List: the family name of each `Multiprotocol <family>: ... negotiated` line under `Neighbor Capabilities`; a line not ending in `negotiated` is skipped), `LOCAL_AS`, `ROUTER_ID`. A record starts at `^BGP neighbor is` (Record the previous one first) and ends at end of output, because the `Local AS is <n>, local router ID <id>` line comes after the statistics blocks (research R1). Skip every other line: timers, message and prefix statistics, TCP socket lines are never captured (FR-007). Accept an IPv6 peer address in the first line. Leave `DESCRIPTION`, `SHUTDOWN` and `DYNAMIC` to T027
- [ ] T019 [P] [US1] Write packs/arista_eos/templates/show_ip_bgp_summary_vrf_all.textfsm: `VRF` (Filldown, from `^BGP summary information for VRF (\S+)`), `NEIGHBOR` (the first column of each peer line, IPv4 or IPv6); header lines (`Router identifier`, `Neighbor Status Codes`, the column header) are skipped and no other column is captured. Copy it unchanged to show_ipv6_bgp_summary_vrf_all.textfsm and show_bgp_evpn_summary.textfsm in the same directory
- [ ] T020 [US1] Write packs/arista_eos/recipes/bgp_peers.yaml: `family: bgp_peers`, one implementation `id: bgp-peers-cli`, `versions: '>=4.36'` (comment: only 4.36 recorded and scanned for secrets, research R13), `transport: ssh`, the four steps of the table above in order. Step 1 map: `vrf: VRF`, `peer_address: PEER_ADDRESS`, `peer_as: PEER_AS`, `type: LINK`, `state: STATE`, `peer_router_id: PEER_ROUTER_ID`, `peer_group: PEER_GROUP`, `negotiated_address_families: NEGOTIATED`, `local_as: LOCAL_AS`, `router_id: ROUTER_ID`. Steps 2 to 4 map `vrf: VRF`, `peer_address: NEIGHBOR` and `configured_address_families` to `'=ipv4 unicast'`, `'=ipv6 unicast'`, `'=l2vpn evpn'`. `merge_on: [vrf, peer_address]`. `covers: { configured_address_families: [ipv4 unicast, ipv6 unicast, l2vpn evpn] }`. `values`: `type: { external: ebgp, internal: ibgp }`; `state: { Idle: idle, Connect: connect, Active: active, OpenSent: opensent, OpenConfirm: openconfirm, Established: established, '*': other }`; `negotiated_address_families: { IPv4 Unicast: ipv4 unicast, IPv6 Unicast: ipv6 unicast, L2VPN EVPN: l2vpn evpn }` (any other family kept as printed); `peer_router_id: { 0.0.0.0: '' }`. `defaults: { shutdown: 'no', dynamic: 'no' }`. Comments as in interface_vlans.yaml: why the summaries give the configured list (research R2), why EVPN has no `vrf all` (R2), why no counter is mapped (FR-007)
- [ ] T021 [US1] Write the `.yml` beside each dc-leaf1 and core1 recording of T007 (ntc `parsed_sample`: the template rows exactly), so `TestPacks` passes on them. Header-only summaries have `_empty` names and no `.yml`; `TestPacks` requires them to yield no row, so if the template records a row holding only the filled-down `VRF`, change the template until it does not
- [ ] T022 [US1] Write packs/arista_eos/testdata/lab/dc-leaf1_bgp_peers.facts.yml: `status: collected`, five rows in VRF `default`, local AS 65001, router ID 10.0.250.11: 10.0.1.0 and 10.0.2.0 (`peer_as` 65000, `ebgp`, `peer_group: underlay`), 10.0.3.1 (65001, `ibgp`, `underlay_ibgp`), each with configured and negotiated `["ipv4 unicast"]`; 10.0.250.1 and 10.0.250.2 (65000, `ebgp`, `evpn`) with configured and negotiated `["l2vpn evpn"]`; all `established`, `shutdown: 'no'`, `dynamic: 'no'`, `peer_router_id` as recorded. Write core1_bgp_peers.facts.yml the same way: five rows in `default` and five in `gold`, local AS 65500, router ID 10.0.200.1, every row with configured and negotiated `["ipv4 unicast"]` (spec story 1 scenario 3). Values as recorded in T007, not as listed here, if they differ
- [ ] T023 [US1] Add a test to packs/packs_test.go: parse dc-leaf1's four recordings twice, once with the summary outputs in recipe order and once with steps 2 to 4 reversed (build a copy of the implementation with its steps reversed after step 1), and require identical rows (FR-005d)

**Checkpoint**: US1 is shippable: the EVPN lab's sessions are readable through the facts endpoint.

---

## Phase 4: User Story 2 - See a session that is not up or not complete (Priority: P1)

**Goal**: a session that is down, shut, dynamic, or up with a configured family not negotiated is
recorded with what tells it apart.

**Independent Test**: `go test ./packs/...` with the lab stopped: sw1's and sw2's recordings give
the rows of their `.facts.yml`; then quickstart section 4 on the VM.

- [ ] T024 [US2] Extend packs/arista_eos/templates/show_bgp_neighbors_vrf_all.textfsm with `DESCRIPTION` (the `Description:` line), `SHUTDOWN` (the shut marker on or after the state line) and `DYNAMIC` (the dynamic peer marker), using the exact forms T008 wrote into research R3 and R7. If T008 chose the `show bgp peer-group` fallback for dynamic peers, write packs/arista_eos/templates/show_bgp_peer_group.textfsm instead (`VRF`, `NEIGHBOR`, `DYNAMIC` from the `Dynamic peer-group members:` heading) and add it as step 5 in T025
- [ ] T025 [US2] Extend packs/arista_eos/recipes/bgp_peers.yaml: step 1 map `description: DESCRIPTION`, `shutdown: SHUTDOWN`, `dynamic: DYNAMIC`; `values` `shutdown` and `dynamic` mapping the recorded markers to `'yes'` (defaults stay `'no'`)
- [ ] T026 [US2] Write the `.yml` beside each sw1 and sw2 recording of T005 (including `sw1_show_bgp_neighbors_vrf_all_down.raw`), and refresh the dc-leaf1 and core1 `.yml` of T021 for the three new values (all empty there)
- [ ] T027 [US2] Write packs/arista_eos/testdata/lab/sw1_bgp_peers.facts.yml: `status: collected`, VRF `default`, local AS 65101, router ID 10.255.0.1, four rows: 10.0.10.2 (`peer_as` 65102, `ebgp`, `established`, `shutdown: 'no'`, `dynamic: 'no'`, `description: TO-SW2`, configured `["ipv4 unicast", "ipv6 unicast"]`, negotiated `["ipv4 unicast"]`, `peer_router_id: 10.255.0.2`); 10.255.0.2 (65199, the recorded state, which is not `established`, `shutdown: 'no'`, configured `["ipv4 unicast"]`, no negotiated list, no `peer_router_id`); 10.0.10.99 (65102, `idle`, `shutdown: 'yes'`, configured `["ipv4 unicast"]`, no negotiated list); 10.255.0.12 (65102, as recorded, configured `["ipv4 unicast"]`). Write sw2_bgp_peers.facts.yml: 10.0.10.1 (65101, `established`, `description: TO-SW1`, configured and negotiated `["ipv4 unicast"]`, `peer_group` absent) and 10.255.1.1 (65101, `established`, `dynamic: 'yes'`, `peer_group: DYN`, configured and negotiated `["ipv4 unicast"]`). Values as recorded, if they differ
- [ ] T028 [US2] Add a test to packs/packs_test.go: parse `sw1_show_bgp_neighbors_vrf_all_down.raw` with sw1's three summary recordings through the bgp_peers implementation, and require the row of 10.0.10.2 to have `shutdown: 'yes'`, a state other than `established`, no `negotiated_address_families` and no `peer_router_id` even if the recording still prints them (research R4), and `configured_address_families` `["ipv4 unicast", "ipv6 unicast"]`. Add `regexp.MustCompile(`password 7 \S+`)` to the patterns of `TestNoSecretInRecordedOutput` (the `nm-lab-secret-` literal already covers the plain password, research R9)

**Checkpoint**: US1 and US2 together cover every row type of the spec.

---

## Phase 5: User Story 3 - Tell "no BGP" from "not collected" (Priority: P2)

**Goal**: a device without BGP is `empty`; any error is `parse_failed`, never `empty`.

**Independent Test**: `go test ./packs/...`: sw4 and dc-access1 give `empty`; the error recordings
give `parse_failed`.

- [ ] T029 [US3] Add `empty_lines` to each step of packs/arista_eos/recipes/bgp_peers.yaml: on all four steps `'^% BGP inactive$'` (the form the collector's SSH session is expected to receive) and `'^% BGP inactive at line 1$'` (the form `Cli -c` records in the fixtures); if T008 found that SSH also prints ` at line 1`, keep only the second. Anchored, no wildcard after `inactive`; on steps 2 to 4 also the header lines of a summary with no peer: `'^BGP summary information for VRF \S+$'`, `'^Router identifier \S+, local AS number \d+$'`, `'^Neighbor Status Codes: .*$'`, `'^\s*Neighbor\s+V\s+AS\s+MsgRcvd\s+MsgSent\s+InQ\s+OutQ\s+Up/Down\s+State\s+PfxRcd\s+PfxAcc\s+PfxAdv$'`. A comment: only these exact lines are "nothing here"; a refusal or any other `%` line must reach the template and fail (research R8, FR-009)
- [ ] T030 [US3] Write packs/arista_eos/testdata/lab/sw4_bgp_peers.facts.yml and dc-access1_bgp_peers.facts.yml with `status: empty` and no row. Check core1_bgp_peers.facts.yml (T022) still gives `collected` now that its header-only ipv6 and evpn summaries are skipped by `empty_lines`
- [ ] T031 [US3] Add `TestBGPErrorsAreNotEmpty` to packs/packs_test.go: for each of `sw1_show_bgp_neighbors_vrf_all_denied_empty.raw`, `core1_show_bgp_evpn_summary_invalid_empty.raw`, and an output made of `% BGP inactive` plus one other line, fed as the output of each step in turn with the other steps given sw1's recordings, `parse.Parse` returns `parse_failed` and no row; `sw4_show_bgp_neighbors_vrf_all_ssh_empty.raw` fed to all four steps returns `empty` and no row, never `collected` (FR-009)

**Checkpoint**: every story is covered with no lab running.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [ ] T032 [P] Add a test to internal/graph/graph_test.go, in the style of `TestReproducible`: project a snapshot, add `bgp_peers` observations for its devices (rows from sw1_bgp_peers.facts.yml), project again, and require the same nodes, edges and verdict, and no new frontier task (FR-015)
- [ ] T033 [P] Merge specs/010-bgp-peer-facts/contracts/fact-families.md into specs/001-crawl-loop/contracts/fact-families.md as a section "Routing families (feature 010)" after the layer 2 families, and update its closing paragraph ("... feature 008 adds `vlans` and `interface_vlans`; feature 010 adds `bgp_peers`") (FR-011)
- [ ] T034 [P] Update README.md and docs/how-to/write-a-pack.md where they list fact families or what a pack collects: add `bgp_peers`. In the pack guide document: list fields now merge across steps (items added up, not first-wins); the `covers` key and its load check; that `EmptyList`, `Set` and `OnlyWith` are schema attributes the parser applies, so a pack only maps what the device prints (FR-017)
- [ ] T035 [P] Add bruno/facts/bgp-peers.bru, copied from bruno/facts/vlans.bru with `name` and the family in the URL changed and `seq` set after the last existing request (FR-017)
- [ ] T036 [VM] Run quickstart section 4 against `test/lab` and check every expectation listed there, including the volatility check (two crawls a few minutes apart, identical `bgp_peers` rows, SC-004) and a search of the object store bucket for `nm-lab-secret-` (SC-002). Check what earlier features prove still holds: sw1 over SNMP v2c and SSH, sw2 over SNMPv3 only, sw3 `denied`, 172.20.20.9 `unreachable`, sw1 and sw4 not merged; the 007 to 009 families unchanged on sw1 and sw2. Then section 5 against the EVPN lab (SC-003: every switch has `bgp_peers`, none `parse_failed`; two crawls with traffic running give identical rows). Write what the crawl showed, and any divergence, at the end of specs/010-bgp-peer-facts/quickstart.md under "What the lab confirmed", as 008 did
- [ ] T037 Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` with the 007 quickstart section 1 environment (nothing skipped), and `govulncheck ./...`

---

## Dependencies & Execution Order

- Phase 1 first: T001 to T003 in parallel, then T004 to T007 on the VM in order, then T008, T009.
- Phase 2 needs nothing from Phase 1 and can start at once: T010 then T011 then T012; T013 and T014
  need T010, T015 needs both; T016 then T017.
- US1 (Phase 3) needs Phase 1 and Phase 2: T018 and T019 in parallel, then T020 to T023 in order.
- US2 (Phase 4) needs US1 (same template and recipe): T024 to T028 in order.
- US3 (Phase 5) needs US1's recipe (T020); it can run beside US2, but T029 and T025 edit the same
  recipe, so merge by hand.
- Phase 6: T032 to T035 any time after T010; T036 after every story; T037 last.

## Parallel Example: User Story 1

```text
T018 show_bgp_neighbors_vrf_all.textfsm   |  T019 the three summary templates
then T020 (recipe), T021 (.yml files), T022 (facts.yml), T023 (step order test)
```

Phase 2: T012 (fact tests), T015 (parse tests) and T017 (registry tests) touch different files and
can be written side by side once their code task is done.

## Implementation Strategy

1. Phase 1 on the VM in one sitting: every fixture, every "to confirm" settled, the secret grep.
2. Phase 2, then US1: the EVPN lab's sessions, shippable on their own.
3. US2: down, shut, dynamic and mismatched sessions from `test/lab`.
4. US3: `empty` and errors.
5. Phase 6, with the live crawl as the last check before the pull request.
