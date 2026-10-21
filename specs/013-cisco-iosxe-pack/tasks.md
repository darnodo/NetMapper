---
description: "Task list for the Cisco IOS-XE platform pack"
---

# Tasks: Cisco IOS-XE platform pack

**Input**: Design documents from `/specs/013-cisco-iosxe-pack/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/cisco-iosxe-pack.md,
quickstart.md

**Tests**: Included. FR-010 asks for every recipe to be tested from recorded output with no lab,
and the constitution for template changes checked by replay over recorded output.

**Organization**: one deploy of the Cat9kv records everything (T008), because each boot costs 12
to 16 minutes. The lab files and the pack skeleton come before it; every recipe task after it
works offline from the recordings. US4 (the lab) is built in Phase 2 because every other story
needs its recordings; its documentation and the live crawl close the feature. US1 (identity,
neighbours, interfaces) is the MVP. US2 (VLANs) and US3 (`no_recipe` families) follow and touch
different files.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US4 from spec.md
- Paths are relative to the repository root. Tests live in `packs/packs_test.go`, as in 007 to 011.
- **[VM]** in a description: needs the `NetLab` VM (ssh host `NetLab`). Every other task runs on
  the Mac with no lab.

Fixtures (research R5 to R10): `packs/cisco_iosxe/testdata/lab/<device>_<template base>.raw`, one
command each, as the collector stores it: no command echo on the first line, no prompt on the
last, no `\r`. An output that must give no row has `_empty` before `.raw`; a refusal also has
`_invalid_` in its name. Expected rows per device and family: `<device>_<family>.facts.yml`
(`status:` and `rows:`). Outputs with no template (`show version`, SNMP values, the refusal of
`show running-config`) are `.txt`, so `TestPacks` does not parse them.

| Step | Command | Template (`packs/cisco_iosxe/templates/`) | Family |
| ---- | ------- | ----------------------------------------- | ------ |
| L | `show lldp neighbors detail` | `show_lldp_neighbors_detail.textfsm` | `neighbours` |
| I | `show interfaces` | `show_interfaces.textfsm` | `interfaces` |
| V | `show vlan brief` | `show_vlan_brief.textfsm` | `vlans` |
| S | `show interfaces switchport` | `show_interfaces_switchport.textfsm` | `interface_vlans`, step 1 |
| T | `show interfaces trunk` | `show_interfaces_trunk.textfsm` | `interface_vlans`, step 2 |

---

## Phase 1: Setup

**Purpose**: the pack directory and its licence files.

- [X] T001 Create `packs/cisco_iosxe/` with empty `recipes/`, `templates/`, `testdata/lab/` and `testdata/ntc/` directories (add a `.gitkeep` only where a directory would stay empty at commit)
- [X] T002 [P] Copy from ntc-templates (pin one commit, written in the NOTICE) `cisco_ios_show_lldp_neighbors_detail.textfsm`, `cisco_ios_show_interfaces.textfsm`, `cisco_ios_show_vlan.textfsm`, `cisco_ios_show_interfaces_switchport.textfsm` and `cisco_ios_show_interfaces_trunk.textfsm` with one sample `.raw` and `.yml` each into `packs/cisco_iosxe/testdata/ntc/` (prefix `cisco_ios_` removed), and the licence as `packs/cisco_iosxe/testdata/ntc/LICENSE-ntc-templates`; start `packs/cisco_iosxe/NOTICE` in the format of `packs/arista_eos/NOTICE`

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the lab (US4), the session definition, and one recording run that gives every
fixture the stories need.

- [X] T003 [P] Write `test/lab-iosxe/iosxe.clab.yaml`: topology `netmapper-iosxe`, management network `netmapper-iosxe-mgmt` on `172.20.22.0/24`; nodes `eos-a` (`arista_ceos`, `ceos:4.36.0F`, 172.20.22.2), `cat1` (`cisco_cat9kv`, `vrnetlab/cisco_cat9kv:17.15.01`, 172.20.22.3, `env: {CLAB_MGMT_PASSTHROUGH: "true"}`), `eos-b` (`arista_ceos`, `ceos:4.36.0F`, 172.20.22.4); links `eos-a:eth1`-`cat1:Gi1/0/1`, `eos-a:eth2`-`cat1:Gi1/0/2` (port-channel), `cat1:Gi1/0/3`-`eos-b:eth1` (access VLAN 10). Header comment in the style of `test/lab/two-switch.clab.yaml`: what each node and link is for, the boot time (12 to 16 minutes measured), the readiness check (`docker logs clab-netmapper-iosxe-cat1 2>&1 | grep "Startup complete"`, then `cat1` in `show lldp neighbors` on `eos-a`), the 18 GB and 4 vCPU the Cat9kv takes, and that the vrnetlab dummy NICs Gi1/0/4 to Gi1/0/8 report `connected` with nothing behind them (research R7, R11)
- [X] T004 [P] Write `test/lab-iosxe/cat1.cfg`: `hostname cat1`, `username netmapper privilege 1 secret admin`, `lldp run`, `snmp-server community public RO`, VLANs 10, 20 and 99 with names, `interface Port-channel1` trunk with native VLAN 99 and allowed list `10,20,99`, `channel-group 1 mode active` on Gi1/0/1 and Gi1/0/2, Gi1/0/3 `switchport mode access` and `switchport access vlan 10`. Header comment (research R6): appended to vrnetlab's bootstrap and applied once at first boot; must not configure `GigabitEthernet0/0`, `Mgmt-vrf`, the vty lines or the `admin` account; a change needs a redeploy
- [X] T005 [P] Write `test/lab-iosxe/eos-a.cfg` and `test/lab-iosxe/eos-b.cfg` from `test/lab/sw1.cfg`'s account, AAA, SNMP, LLDP management-address and `management ssh` lines (`username netmapper privilege 15 role network-operator secret admin`, `aaa authorization exec default local`, `snmp-server community public ro`, `lldp management-address Management0`). `eos-a`: VLANs 10, 20, 99, `Port-Channel1` (LACP active on Ethernet1 and Ethernet2) trunk, native VLAN 99, allowed `10,20,99`. `eos-b`: VLAN 10, Ethernet1 access VLAN 10
- [X] T006 [P] Write `test/lab-iosxe/netmapper.yaml` from `test/lab/netmapper.yaml`: perimeter `lab-iosxe` including `172.20.22.0/24` and excluding `172.20.22.1/32`; the credential sets `ro-snmp` (`env:LAB_SNMP_COMMUNITY`) and `ro-ssh` (`env:LAB_SSH_PASSWORD`) scoped to it; seed set `iosxe-seeds` with `172.20.22.2` only; the same `discovery` block
- [X] T007 Write `packs/cisco_iosxe/scrapli.yaml` per contracts/cisco-iosxe-pack.md "scrapli.yaml": `platform-type: 'cisco_iosxe'`, one `exec` level with pattern `'(?im)^[\w.\-@()/: ]{1,63}[>#]\s?$'`, no escalation, `default-desired-privilege-level: 'exec'`, `failed-when-contains` `% Ambiguous command`, `% Incomplete command`, `% Invalid input detected`, `% Unknown command`, `network-on-open: []`, `network-on-close: []`, and the option `transport-pty-height` with value `32767`. Header comment explaining why the pty height replaces `terminal length 0` (research R3). Check against scrapligo v1.4.1 `platform/definition.go` where `options` sits in the file
- [X] T008 [VM] One recording run on NetLab, one deploy, then destroy. Copy `test/lab-iosxe/` to `~/netmapper-iosxe-lab` and run `sudo containerlab deploy -t iosxe.clab.yaml`, noting the time. Wait for readiness (T003 header) and note the time of `Startup complete` and of `cat1` appearing in `eos-a`'s LLDP view. Then, in this order, without redeploying:
  1. As `netmapper` on `cat1`, check `show privilege` gives level 1; if the login is refused, log in as `admin`, read `show running-config`, fix `cat1.cfg` and redeploy (research R6, quickstart section 3).
  2. Record the base state from `cat1` as `netmapper` through an interactive session (`script -qc "stty rows 32767 cols 200; sshpass -p admin ssh -tt netmapper@172.20.22.3"`, commands fed on stdin, research R3), cleaned as the Fixtures paragraph says: `cat1_show_lldp_neighbors_detail.raw`, `cat1_show_interfaces.raw`, `cat1_show_vlan_brief.raw`, `cat1_show_interfaces_switchport.raw`, `cat1_show_interfaces_trunk.raw`, `cat1_show_version.txt`.
  3. Record the refusal: `show running-config` as `netmapper`, saved as `cat1_show_running_config_refused.txt` (research R4).
  4. SNMP from `eos-a`'s bash: `snmpget -v2c -c public -On 172.20.22.3` on `1.3.6.1.2.1.1.2.0`, `1.3.6.1.2.1.1.1.0`, `1.3.6.1.2.1.1.5.0`, `1.3.6.1.2.1.47.1.1.1.1.11.1`, saved as `cat1_snmp_system.txt` (research R1, R2).
  5. Record the Arista side: `show lldp neighbors detail | no-more` on `eos-a` and `eos-b` with `docker exec ... Cli -p 15 -c`, first line stripped, saved as `packs/arista_eos/testdata/lab/eos-a_show_lldp_neighbors_detail.raw` and `packs/arista_eos/testdata/lab/eos-b_show_lldp_neighbors_detail.raw`. Check that `cat1` is advertised with `172.20.22.3`, not `10.0.0.15`.
  6. Open point "LLDP off" (research R5): as `admin` on `cat1`, `configure terminal`, `no lldp run`, `end`; record `show lldp neighbors detail` as `netmapper` into `cat1lldpoff_show_lldp_neighbors_detail_empty.raw`; restore `lldp run`.
  7. Open point "port-channel member" (research R10): the base recordings of step 2 hold Gi1/0/1 and Gi1/0/2 as members of Po1. Check that `show interfaces switchport` prints `(member of bundle Po1)` for them; if it does not, record `show etherchannel summary` into `cat1_show_etherchannel_summary.txt` for the template decision.
  8. Open point "wrapped allowed list" (research R10): on `cat1` Po1 and `eos-a` Port-Channel1, set the allowed list to every even VLAN from 2 to 200 plus 99 (long enough to wrap); record `show interfaces trunk` into `cat1wrapped_show_interfaces_trunk.raw`; restore `10,20,99` on both.
  9. Open point "down trunk" (research R10): `shutdown` on `cat1` Port-channel1; record `show interfaces switchport` into `cat1trunkdown_show_interfaces_switchport.raw` and `show interfaces trunk` into `cat1trunkdown_show_interfaces_trunk.raw` (or `..._empty.raw` if it prints no trunk); `no shutdown`.
  10. Also from step 2's outputs, settle `show vlan brief` at privilege 1 and the `lshut` wordings (research R9): if no `act/lshut` line can be produced (`shutdown` under `vlan 20` as `admin`), record `cat1lshut_show_vlan_brief.raw` that way and restore.
  11. Pty check (research R3): with T007's `scrapli.yaml`, a throwaway Go program (in the scratchpad, never committed) that opens a session with `internal/transport/ssh` and `Platforms: {"cisco_iosxe": <scrapli.yaml>}` and runs `show interfaces` on 172.20.22.3, built for linux/amd64 and run on NetLab; its output must equal `cat1_show_interfaces.raw` with no `--More--`.
  12. `sudo containerlab destroy -t iosxe.clab.yaml --cleanup`, remove `~/netmapper-iosxe-lab`, and check `docker ps` lists no `clab-netmapper-iosxe-*` container.
  All files go to `packs/cisco_iosxe/testdata/lab/` unless named otherwise. Write the measured times and every open point's outcome under "Recorded on 2026-10-.." at the end of `specs/013-cisco-iosxe-pack/research.md`. Never edit a recording after this task
- [X] T009 Change `TestFamiliesFromLab` in `packs/packs_test.go` to ask each pack for the version its recordings were made on instead of `4.36.0F` for all: a map `{"arista_eos": "4.36.0F", "cisco_iosxe": "17.15.1"}` next to the test, failing on a pack missing from it. Run `go test ./packs/` and check the Arista cases still pass

**Checkpoint**: every recording is committed, the lab is destroyed, and no task after this needs
NetLab until T033.

---

## Phase 3: User Story 1 - A Catalyst in the path no longer stops the crawl (Priority: P1) MVP

**Goal**: a Catalyst is identified, its identifiers claimed, its LLDP neighbours and interfaces
collected, so the crawl goes past it.

**Independent Test**: `go test ./packs/` with `cat1_neighbours.facts.yml` and
`cat1_interfaces.facts.yml`; then the crawl of T033 shows `eos-b` discovered through `cat1`.

### Tests for User Story 1

- [X] T010 [P] [US1] `TestIOSXEInterfaceNames` in `packs/packs_test.go`: for each abbreviation of research R8 (`Twe`, `Tw`, `Te`, `Fi`, `Fo`, `Hu`, `Gi`, `Fa`, `Ap`, `Po`, `Vl`, `Lo`, `Tu`) and its long form, `reg.Normalise("cisco_iosxe", ...)` gives the same canonical name; `Tw1/0/1` gives `TwoGigabitEthernet1/0/1` and `Twe1/0/1` gives `TwentyFiveGigE1/0/1`; a long name is returned unchanged
- [X] T011 [P] [US1] `TestIOSXEFingerprint` in `packs/packs_test.go`: `reg.Fingerprint` with `CLI["show version"]` set to `cat1_show_version.txt` gives `cisco_iosxe` and `17.15.1`; with sysObjectID `1.3.6.1.4.1.9.1.3160` and the sysDescr from `cat1_snmp_system.txt` gives `cisco_iosxe` and `17.15.1`; with sysObjectID `1.3.6.1.4.1.9.1.1` (not listed) gives no match. And no pack but `cisco_iosxe` matches `cat1_show_version.txt`, no pack but `arista_eos` matches an EOS `show version` taken from `packs/arista_eos/testdata` (or the fingerprint string used in its existing tests)
- [X] T012 [P] [US1] `TestIOSXEIdentifiers` in `packs/packs_test.go`: the `serial` and `chassis_mac` SSH regexes of `packs/cisco_iosxe/pack.yaml` applied to `cat1_show_version.txt` give the recorded serial and the base MAC; `pack.NormaliseMAC` of that MAC equals `pack.NormaliseMAC` of the chassis ID `eos-a_show_lldp_neighbors_detail.raw` gives for `cat1` (research R2)
- [X] T013 [P] [US1] `TestIOSXERefusedIsNotEmpty` in `packs/packs_test.go`: for each of the four recipes, the content of `cat1_show_running_config_refused.txt` given as the output of any one step (the others from `cat1`'s recordings) gives `parse_failed`, never `empty` or `collected` (research R4)
- [X] T014 [P] [US1] `TestIOSXELLDPOffIsEmpty` in `packs/packs_test.go`: `cat1lldpoff_show_lldp_neighbors_detail_empty.raw` gives `empty` for `neighbours`; `cat1_show_lldp_neighbors_detail.raw` gives `collected`

### Implementation for User Story 1

- [X] T015 [US1] Write `packs/cisco_iosxe/pack.yaml` per contracts/cisco-iosxe-pack.md "pack.yaml": `name: cisco_iosxe`, `version: 1`, `read_only: ["show "]`; `fingerprint.snmp` one rule per sysObjectID (`1.3.6.1.4.1.9.1.3160` and the Catalyst 9300 and 9500 entries of CISCO-PRODUCTS-MIB, each with a comment naming the model) with `version_regex: 'Version (?P<version>\d+\.\d+\.\d+\w*),'`; `fingerprint.ssh` `show version`, `match: 'Cisco IOS XE Software'`, same `version_regex`; `identifiers` `serial` strong (`System Serial Number\s+:\s+(?P<value>\S+)`, SNMP `1.3.6.1.2.1.47.1.1.1.1.11.1`), `chassis_mac` strong `normalise: mac` (`Base Ethernet MAC Address\s+:\s+(?P<value>\S+)`), `hostname` weak (SNMP `1.3.6.1.2.1.1.5.0`); `interface_names` the rules of research R8, longest abbreviation first, each `^<abbr>(\d.*)$`. Leave `pack_schema` out unless a recipe needs a format 2 or 3 key. Makes T010 to T012 pass
- [X] T016 [US1] Write `packs/cisco_iosxe/templates/show_lldp_neighbors_detail.textfsm` from the ntc template of T002: values `LOCAL_INTERFACE`, `CHASSIS_ID`, `NEIGHBOR_NAME`, `NEIGHBOR_INTERFACE`, `MGMT_ADDRESS`, `MGMT_ADDRESS_SUBTYPE` (`IP:` gives `IP`, `IPV6:` gives `IPV6`), keeping the first IPv4 address of a block, else the first IPv6 (research R5); no lookbehind or lookahead. Record every change in `packs/cisco_iosxe/NOTICE`
- [X] T017 [US1] Write `packs/cisco_iosxe/recipes/neighbours.yaml`: `family: neighbours`, implementation `lldp-cli`, `versions: '>=17.15'`, `transport: ssh`, one step `show lldp neighbors detail` with `empty_lines` `'^Total entries displayed: 0$'` plus the LLDP-off wording recorded in T008 step 6; `map` per contracts "neighbours", `protocol: '=lldp'`; `values: { remote_mgmt_address_type: { IP: ipv4, IPV6: ipv6, '*': other } }`. Then write `packs/cisco_iosxe/testdata/lab/cat1_neighbours.facts.yml` by hand from `cat1_show_lldp_neighbors_detail.raw` (three rows: two towards `eos-a`, one towards `eos-b`, `local_interface` in long form) and make `TestFamiliesFromLab` and T014 pass
- [X] T018 [US1] Write `packs/cisco_iosxe/templates/show_interfaces.textfsm` from the ntc template of T002: values `PORT` (long name), `DESCRIPTION`, `LINK_STATUS`, `PROTOCOL_STATUS`, `MTU`, `MAC_ADDRESS`, and `LINK_SPEED` `(\d+(?:\.\d+)?[MG])` captured from a rule ending in the literal `b/s` on the duplex line, empty when the port prints `Auto-speed` (research R7); skip counters and timers. Add a `cat1_show_interfaces.yml` only if a row-level check beyond the facts file is useful. Record the changes in `packs/cisco_iosxe/NOTICE`
- [X] T019 [US1] Write `packs/cisco_iosxe/recipes/interfaces.yaml`: `family: interfaces`, implementation `interfaces-cli`, `versions: '>=17.15'`, `transport: ssh`, one step `show interfaces`; `map` per contracts "interfaces"; `values: { admin_state: { administratively down: down, '*': up }, oper_state: { up: up, down: down, '*': other } }`. Write `packs/cisco_iosxe/testdata/lab/cat1_interfaces.facts.yml` by hand from `cat1_show_interfaces.raw` (every interface once, `speed_bps` only on up ports, `mac` in lowercase colon form) and make `TestFamiliesFromLab` pass
- [X] T020 [P] [US1] Write `packs/arista_eos/testdata/lab/eos-a_neighbours.facts.yml` from `eos-a_show_lldp_neighbors_detail.raw`: the rows towards `cat1` carry `remote_interface` as `cat1` prints it (`Gi1/0/1`, `Gi1/0/2`), `remote_chassis_id` the base MAC in dotted form, `remote_mgmt_address` `172.20.22.3` typed `ipv4`. Run `go test ./packs/`

**Checkpoint**: US1 is done offline. `go test ./packs/` passes.

---

## Phase 4: User Story 2 - Layer 2 facts on a Catalyst (Priority: P2)

**Goal**: `vlans` and `interface_vlans` on a Catalyst, following the fact-family contract.

**Independent Test**: `go test ./packs/` with `cat1_vlans.facts.yml`,
`cat1_interface_vlans.facts.yml` and the variant tests; the L2 domain check of T033.

### Tests for User Story 2

- [X] T021 [P] [US2] `TestIOSXELegacyVLANsAreNotDeclared` in `packs/packs_test.go`: `cat1_show_vlan_brief.raw` holds lines for 1002 to 1005 (asserted on the raw text) and the `vlans` rows hold none of them (spec FR-006)
- [X] T022 [P] [US2] `TestIOSXETrunkVariants` in `packs/packs_test.go`, in the table style of `TestTrunkVariants`: the base recordings give Po1 `mode: trunk`, `native_vlan: 99`, `allowed_vlans: ["10", "20", "99"]` and Gi1/0/1 and Gi1/0/2 as member rows with `channel: Port-channel1` and no other field; `cat1wrapped_*` gives the whole even list with 99 merged in VLAN list form; `cat1trunkdown_*` gives Po1 with `mode` only; Gi1/0/3 gives `mode: access`, `access_vlan: 10`

### Implementation for User Story 2

- [X] T023 [US2] Write `packs/cisco_iosxe/templates/show_vlan_brief.textfsm` (from the ntc `show_vlan` template, first table only): values `VLAN_ID`, `NAME`, `STATUS`; a rule `^100[2-5]\s` that records nothing; the Ports column and its continuation lines ignored. Record the origin in `packs/cisco_iosxe/NOTICE`
- [X] T024 [US2] Write `packs/cisco_iosxe/recipes/vlans.yaml`: `family: vlans`, implementation `vlans-cli`, `versions: '>=17.15'`, `transport: ssh`, one step `show vlan brief`; `map: { vlan_id: VLAN_ID, name: NAME, status: STATUS }`; `values: { status: { active: active, suspended: suspended, act/lshut: shutdown, sus/lshut: shutdown, '*': other } }` (field rule from the contract: "status: yes: `active`, `suspended`, `shutdown`, `other`"). Comment that VLAN 1 always exists so the family is never `empty`. Write `packs/cisco_iosxe/testdata/lab/cat1_vlans.facts.yml` (and `cat1lshut_vlans` coverage in T021's file if T008 step 10 recorded it) and make `TestFamiliesFromLab` and T021 pass
- [X] T025 [US2] Write `packs/cisco_iosxe/templates/show_interfaces_switchport.textfsm`: one record per `Name:` block; a block with `Switchport: Disabled` records nothing; `Operational Mode` sets `MODE`; on `(member of bundle <Po>)` capture `CHANNEL` and leave `MODE` and `ACCESS_VLAN` empty (TextFSM states, since `Access Mode VLAN` comes after); `ACCESS_VLAN` from `Access Mode VLAN: <n>` only when the operational mode is `static access`. Follow what T008 step 7 found if the member wording differs. Record the origin in `packs/cisco_iosxe/NOTICE`
- [X] T026 [US2] Write `packs/cisco_iosxe/templates/show_interfaces_trunk.textfsm`: `PORT` and `NATIVE` from the first table; `ALLOWED` from "Vlans allowed on trunk" and `ACTIVE` from "Vlans allowed and active in management domain", each a TextFSM `List` so a list wrapped over several lines (`cat1wrapped_show_interfaces_trunk.raw`) comes back whole; the fourth table ignored. Record the origin in `packs/cisco_iosxe/NOTICE`
- [X] T027 [US2] Write `packs/cisco_iosxe/recipes/interface_vlans.yaml`: `family: interface_vlans`, implementation `interface-vlans-cli`, `versions: '>=17.15'`, `transport: ssh`, steps S then T (table above), `merge_on: [interface]`; `map` per contracts "interface_vlans"; `values: { mode: { trunk: trunk, static access: access }, allowed_vlans: { none: '' }, active_vlans: { none: '' } }`, any other mode word kept as printed ("mode: no: `access`, `trunk`, or as the device names it"); `empty_lines` on step T for the no-active-trunk output recorded in T008 step 9, and on step S only if T008 recorded a device with no switched port. Port names in step T are short (`Po1`) and go through the naming rules. Write `packs/cisco_iosxe/testdata/lab/cat1_interface_vlans.facts.yml` from the base recordings ("A member row has `interface` and `channel` only"; "absent `allowed_vlans` means the trunk allows no VLAN") and make `TestFamiliesFromLab` and T022 pass

**Checkpoint**: US1 and US2 pass offline.

---

## Phase 5: User Story 3 - Families the pack does not cover yet (Priority: P3)

**Goal**: the nine other families are `unsupported` with `no_recipe` and change no verdict.

**Independent Test**: T028, and the observations of T033.

- [X] T028 [P] [US3] `TestIOSXEFamilies` in `packs/packs_test.go`: `reg.Families("cisco_iosxe")` is exactly `neighbours`, `interfaces`, `vlans`, `interface_vlans`; for `snmp`, `aaa_servers`, `local_users`, `management_apis`, `aaa_methods`, `management_servers`, `bgp_peers`, `ospf_interfaces` and `ospf_neighbours`, `reg.Implementations("cisco_iosxe", family, "17.15.1")` is empty (the collector then writes `no_recipe`, `internal/collector/scrape.go`). No new collector test: the `no_recipe` path and its effect on the verdict are already covered for `fakeos` (`internal/testutil` `NoRecipeFamilies`)

---

## Phase 6: User Story 4 - A lab to test against (Priority: P1)

**Goal**: the lab of Phase 2 documented and checked end to end. Its files were written in T003 to
T006.

**Independent Test**: T033 deploys it from a stopped NetLab and crawls it.

- [X] T029 [P] [US4] Write `test/lab-iosxe/README.md`: what the lab proves (spec US4), the deploy and destroy commands, copying the directory to NetLab (no repository clone there), readiness (T003 header), the `cat1.cfg` rules of T004, the fallback when the `netmapper` login is refused (quickstart section 3), how to record a fixture as in T008 step 2, the resources and the advice to stop the EVPN fabric if `cat1` does not start (FR-013)

---

## Phase 7: Polish and cross-cutting

- [X] T030 [P] Write `packs/cisco_iosxe/README.md` in the style of `packs/arista_eos/README.md`: recorded on Cat9kv 17.15.01 (`vrnetlab/cisco_cat9kv`), standing in for Catalyst 9300 and 9500; which sysObjectIDs were seen on a device (only `1.3.6.1.4.1.9.1.3160`); paging by pty height, up to 32767 lines (research R3); VLANs 1002 to 1005 are not rows (FR-006); and the known gaps of FR-014: C9200 (no virtual image, no family claimed until fixtures from real hardware exist); IOS-XE has CDP on and LLDP off by default, so a Catalyst without `lldp run` shows no neighbours, and CDP is not collected; the management and routing families have no recipe yet; a command refused at privilege 1 is `parse_failed`, not `denied`, because IOS-XE answers it with `% Invalid input detected`, like an unknown command; stacks are a single device with no stack-specific logic, and after a switchover the serial and base MAC may change, so the stack may be seen as a new device
- [X] T031 [P] Update `README.md` (supported platforms: Arista EOS and Cisco IOS-XE, Catalyst 9300 and 9500), `docs/how-to/write-a-pack.md` (section 5: a platform with no per-command pager sets `transport-pty-height` in `scrapli.yaml`, with `packs/cisco_iosxe` as the example; section 1: `packs/cisco_iosxe` as a second real pack to copy from) and `docs/how-to/deploy.md` ("The device account": on IOS-XE a privilege 1 account, `username netmapper privilege 1 secret <password>`, is enough for every family the pack collects; a refused command is `parse_failed`) (FR-015, FR-016)
- [X] T032 Run `gofmt -l .`, `go vet ./...` and `go test -count=1 ./...` with the `NETMAPPER_TEST_*` variables as in 007's quickstart section 1; check `go test ./packs/` takes under a minute (SC-004) and the collector's startup log lists nine `no_recipe` warnings for `cisco_iosxe` and none for `arista_eos` (quickstart section 2)
- [X] T033 [VM] End-to-end crawl, second and last deploy (the first one cannot serve: SC-005 starts from a stopped NetLab, and the crawl needs the finished pack). Note the start time, deploy `test/lab-iosxe` as in T008, wait for readiness, then crawl with the branch build as in quickstart section 4 (collector on NetLab with `packs/`, the rest on the Mac over the Tailnet) and `netmapper run --config test/lab-iosxe/netmapper.yaml --perimeter lab-iosxe --seed-set iosxe-seeds`. Check every "Expected" line of quickstart section 4: three devices, `cat1` as `cisco_iosxe` 17.15.1, `eos-b` found through `cat1`, three `l1_link` edges confirmed by both ends with `GigabitEthernet1/0/<n>` on `cat1`'s side, the four families equal to their `.facts.yml`, the nine others `no_recipe`, the L2 domains, no secret in stored outputs. Crawl again for SC-003, replay for SC-006, and check the total time against 25 minutes (SC-005). Destroy the lab and clean `~/netmapper-iosxe-lab` and `~/netmapper-run`
- [X] T034 Add "What the lab confirmed" at the end of `specs/013-cisco-iosxe-pack/quickstart.md` with T008's and T033's results (times, snapshot numbers, anything that differed from the plan), as in 011's quickstart

---

## Dependencies and execution order

- Phase 1 (T001, T002) → Phase 2. T003 to T007 are parallel; T008 needs all of them; T009 needs
  nothing but can wait for T008 so its run includes the new recordings.
- US1 (T010 to T020) needs T008 and T009. US2 (T021 to T027) needs T008, T009 and T015 (the naming
  rules are in `pack.yaml`). US3 (T028) needs T017, T019, T024 and T027 to be final. US4's T029
  needs T008's findings.
- Polish: T030 and T031 after the stories; T032 after everything offline; T033 after T032; T034
  last.
- Within a story: tests first, written against the recordings, then the template, then the recipe
  with its `.facts.yml`.

## Parallel examples

- Phase 2: T003, T004, T005, T006 and T007 at once (five files).
- US1: T010 to T014 at once (separate test functions in `packs/packs_test.go`, written one after
  another if edited by a single agent), and T020 beside T016 to T019.
- US2: T021 and T022 together; T023 and T025/T026 touch different templates.
- Polish: T029, T030 and T031 at once.

## Implementation strategy

1. MVP: Phases 1 and 2, then US1. At that point a Catalyst is identified and crawled through, the
   issue's problem, testable offline.
2. US2 adds the L2 families; US3 is one test; US4's README closes the lab.
3. T033 is the only other boot of the Cat9kv: run it once everything else passes, so a failure
   there is a live-only difference worth a new recording, not a template bug found late.

## Phase 8: Convergence

- [X] T035 [VM] On the next deploy of `test/lab-iosxe` (`sudo containerlab deploy -t iosxe.clab.yaml`, no `containerlab exec` by hand), check that the `exec` of `eos-a` and `eos-b` in `test/lab-iosxe/iosxe.clab.yaml` ran (no `Failed to execute command` in the deploy log, `no lldp transmit` and `no lldp receive` under Management0 in `show running-config interfaces Management0`) and that `show lldp neighbors` on `eos-a` lists `cat1` on Et1 and Et2 and nothing on Ma0; fix the `exec` form if not, then record the outcome in `specs/013-cisco-iosxe-pack/research.md` ("Recorded on") per FR-012, US4/AC1 (partial)
- [X] T036 Reword the introduction of `docs/how-to/write-a-pack.md` (line 5, "needs no change to Go code") so it agrees with section 8: adding a pack needs no change outside `packs/`, and its test version goes in `recordedOn` in `packs/packs_test.go` per FR-015, plan: workflow rule on documents (contradicts)
- [X] T037 [P] Write a `.yml` (ntc `parsed_sample` format, keys in lower case as `TestPacks` compares them) next to each of the seven samples in `packs/cisco_iosxe/testdata/ntc/`, with the values this pack's templates extract, checked by hand against each `.raw`; update `packs/cisco_iosxe/NOTICE` (the `.yml` files are written for this pack, not copied) per T002 (partial)
