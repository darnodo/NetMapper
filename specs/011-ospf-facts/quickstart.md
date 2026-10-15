# Quickstart: validating OSPF interface and adjacency facts

Run guide for proving the feature end to end. Field tables are in [data-model.md](data-model.md)
and [contracts/fact-families.md](contracts/fact-families.md), the decisions in
[research.md](research.md).

Sections 1 and 2 need no lab. Sections 3 to 5 need the `NetLab` VM, where both containerlab labs
run; it is remote and not always up.

## 1. Automated checks (no lab)

Same environment as [007's quickstart](../007-device-management-facts/quickstart.md) section 1, then:

```sh
go test -count=1 ./...
```

Expected: everything passes, nothing skipped, and in particular:

| Test | Proves |
| ---- | ------ |
| `packs` `TestPacks` | every recorded output of the three new templates yields rows; `_empty` outputs (blank, refusals) yield none |
| `packs` `TestFamiliesFromLab` | sw1, sw2, core1 and core2 recordings give the rows of `<switch>_ospf_interfaces.facts.yml` and `<switch>_ospf_neighbours.facts.yml`; sw4 and dc-leaf1 give `empty` for both; `sw4noadj` gives `collected` interfaces and `empty` neighbours |
| `packs` `TestNoSecretInRecordedOutput` | every `.raw` under `*/testdata/*/` (the lab recordings and the `testdata/ntc` samples; the test fails if `ntc` has no sample) is free of the `test/lab` OSPF keys and of any `md5 7` / `sha<n> 7` / `authentication-key 7` hash |
| `packs` `TestOSPFErrorsAreNotEmpty` | `% Authorization denied ...`, `% Invalid input` or `% OSPF instance does not exist ...` on any step give `parse_failed`, never `empty`; blank output on every step gives `empty` |
| `internal/fact` | `Missing`: a row without `auth_type` is refused by `Validate`; `unknown` is accepted. `DottedID`: `area: 1` and `0.0.0.256` are refused by `Validate`, `0.0.0.1` is accepted |
| `internal/parse` | a mapped row with no `auth_type` gives `unknown`, never `none` and never absent (FR-013a); a `join_on` step fills matching rows, creates none, does not overwrite, and alone does not make the observation `collected`; an interface whose area has no join row is `parse_failed` |
| `internal/pack` | a pack without `pack_schema` loads as version 1, `covers` loads in both versions; `join_on`, `unmatched` or `guard` in a version 1 pack, a guard that is not first, has a `map` or `empty_lines`, or has no `nothing_pattern`, `nothing_pattern` on a step that is not a guard, `pack_schema: 3`, `join_on` with no row-creating step before it, `join_on` without `unmatched`, or a `defaults` entry on a `Missing` field is refused at load |
| `internal/parse` (`unmatched`) | a join row that matches no row gives `parse_failed` naming the step and key with `fail`, and the same rows plus the returned key with `ignore` |
| `internal/parse` (`guard`) | the guard's "none" document gives `empty`, pretty-printed or on one line; a blank guard output gives `parse_failed`; the "none" document with another step holding output gives `parse_failed`; a guard error line gives `parse_failed`; a populated guard with blank row steps gives `empty` |
| `internal/graph` | a snapshot with OSPF rows projects the same graph and verdict as one without |
| `internal/parse` `TestGuard`, `TestJoin` | the guard and join rules listed above, by name |
| `internal/parse` `TestGuardExpectRows` | guard counts of 0 with a blank step give `empty`; a count above 0 with a blank step gives `parse_failed`; a count with a row gives `collected` |
| `internal/pack` `TestPackSchema`, `TestNoDefaultOnMissing` | the load refusals listed above by name, including `expect_rows` outside a guard; a default on `auth_type` is refused |
| `internal/fact` `TestOSPFFamilies`, `TestMissingIsRequired` | the OSPF rows and enums; every field with `Missing` is also `Required` |
| `internal/graph` `TestOSPFNotProjected` | OSPF rows towards the other device change neither the graph nor the verdict |
| `packs` `TestOSPFErrorsAreNotEmpty` (sw1 `expect_rows` case) | sw1's guard counts 5 interfaces, so a blank interface view is `parse_failed`; a blank neighbour view stays `empty` |
| `internal/parse` `TestOSPFAuthUnknown` | FR-013a: a row with no authentication value gives `unknown`; `No authentication` gives `none`; an unknown line gives `other` |
| `internal/parse` `TestDottedID` | `1` gives `0.0.0.1`, `256` gives `0.0.1.0`, a dotted area is kept, `x` or `4294967296` gives `parse_failed` |
| `internal/fact` (`OnlyWith` list, if applied) | a DR or BDR router ID on a point-to-point row is refused |
| `packs` `TestOSPFInterfacesJoinInterfaces` | every OSPF row's interface is in canonical form |
| `packs` `TestOSPFSteadyState` | the same recordings give the same rows; the VLAN 44 adjacency is not `full` |
| `internal/collector` `TestBlankOutputWithErrorIsNeverEmpty` | stall, deadline, dropped session and no prompt after login never give `empty` |
| `internal/api` `TestOSPFUnknownAndAbsent` | a stored row with `auth_type: unknown` and no `dr_router_id` is served exactly as stored |

## 2. Load warnings

Start `./netmapper collector` as in the README and read its startup log.

Expected: no warning for `arista_eos`. Any other platform pack with a fingerprint logs one line per
new family, with the effect "no OSPF data for these devices".

## 3. Record fixtures from the repository lab (NetLab VM)

Copy the changed `test/lab` files to `~/netmapper-test-lab` on the VM and redeploy (research R10):

```sh
sudo containerlab deploy -t two-switch.clab.yaml --reconfigure
```

Check the lab before recording, on sw1 and sw2 with `show ip ospf neighbor vrf all`:

- VLAN 41 (area 0, md5) and VLAN 42 (area 1 stub, point-to-point) are `FULL` on both ends;
- VLAN 43 in VRF `blue` (instance 2, sha) is `FULL` on both ends;
- VLAN 44 (MTU mismatch) is held in `EXSTART` or `EXCHANGE`, and still is a few minutes later.

Then settle the "to confirm" items of research R2, R4, R5 and R6 from the outputs:

- the stub area line of `show ip ospf vrf all`;
- the point-to-point network type and state words, and whether DR lines are printed there;
- the md5 and sha authentication lines of `show ip ospf interface vrf all`;
- over SNMP, `ospfIfAuthType` for the md5 and sha interfaces (walk from a lab EOS container's
  bash, as on core2 during planning).

If an interface with authentication prints no authentication line, or SNMP answers nothing usable
for SHA, record that output as a real `unknown` fixture (spec FR-013a). Never edit a recording.

Record the four step commands (the guard `show ip ospf vrf all | json | no-more`, the interface, neighbour
and instance views) through an interactive SSH session, which is how the collector runs
them, named `<switch>_<template name>.raw`. Strip the `> <command>` first line if `Cli -c` was used
(feature 007). Also:

| Fixture | Change on the lab | Command |
| ------- | ----------------- | ------- |
| `sw4_show_ip_ospf_*_empty.raw` | none (sw4 has no OSPF) | the four step commands, interactive SSH; check the guard answers `{"vrfs": {}}` as on dc-leaf1, and whether the guard JSON on sw4noadj carries an interface count per area or instance (research R14) |
| `sw4noadj_show_ip_ospf_*.raw` | sw4: `ip routing`, `interface Loopback0` / `ip address 10.255.0.4/32`, `router ospf 1` / `router-id 10.255.0.4` / `network 10.255.0.4/32 area 0.0.0.0`, recorded a few seconds after configuration (Loopback0 in `Waiting`), all reverted after | the four step commands |
| `sw2_show_ip_ospf_interface_vrf_all_denied_empty.raw` | sw2: the temporary role of feature 010 denying `show ip ospf.*`, removed after | step 1 as that user |
| `sw1_show_ip_ospf_neighbor_detail_vrf_all_invalid_empty.raw` | none | `show ip ospf neighbor detail vrf nosuch`, interactive SSH |

Then, with no lab:

```sh
go test -count=1 ./packs/...
```

## 4. Crawl the repository lab

Same setup as 010's quickstart section 4 (collector on the NetLab VM, engine, `run` and API on the
Mac). Take sw4 off the network first (`docker exec clab-netmapper-sw4 ip link set eth0 down`).

```sh
netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw1/facts/ospf_interfaces | jq
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw1/facts/ospf_neighbours | jq
```

Expected (SC-002, SC-005):

- sw1 and sw2 `ospf_interfaces` `collected`: the four VLAN SVIs and Loopback0, each with its VRF,
  instance, router ID, area and area type; Loopback0 `passive: yes`; VLAN 41 and VLAN 43
  `auth_type: message_digest` (EOS prints md5 and sha alike), VLAN 42 `none`; DR and BDR router IDs on the broadcast segments;
- `ospf_neighbours` `collected`: `full` on VLANs 20, 21 and 22, the VLAN 43 row in VRF `blue`; the
  VLAN 44 row in the state seen in section 3; no row on Loopback0;
- every `interface` value is also in the same switch's `interfaces` rows;
- no stored output and no `observation.parsed` holds `nm-lab-secret-`;
- the graph and verdict are those of the same lab without OSPF: no edge from an adjacency, no
  `find` task towards a neighbour.

Volatility check (SC-004): crawl again a few minutes later. Expected: identical rows for both
families, although the stored outputs differ (dead timers, ages).

## 5. External lab (SC-003)

Crawl arista-evpn-vxlan-clab with the setup of
[006's quickstart](../006-snmpv3-protocols/quickstart.md) section 4. Expected:

- every switch has both observations, none `parse_failed`;
- core1 and core2: two interface rows (`Loopback0` passive, `Ethernet5.100`), VRF `default`,
  instance `1`, area `0.0.0.0` `normal`, `auth_type: none`, DR router ID 10.0.200.1 and BDR
  10.0.200.2 on `Ethernet5.100`; one `full` adjacency to each other;
- every other switch `empty` for both.

Record core1's, core2's and dc-leaf1's four outputs as fixtures, as in section 3. Then crawl again
with the lab's traffic running and check that every device's rows are identical to the first crawl.

## What the lab confirmed

Run on 2026-10-15: collector on the NetLab VM, PostgreSQL, object store, engine, `run` and API on
the Mac over the Tailnet, cEOS 4.36.0F.

- Section 1: `go test -count=1 ./...` with the three `NETMAPPER_TEST_*` variables passes, no test
  skipped; `gofmt`, `go vet` clean; `govulncheck` reports nothing reachable from our code.
- Section 3: every recording made from `test/lab` (sw1, sw2, sw4, sw4noadj) and the EVPN lab (core1,
  core2, dc-leaf1). Findings that changed the design or the lab: OSPF keys are 16 characters at
  most (the first keys were refused at boot, so VLANs 41 and 43 came up with no key); EOS prints
  md5 and sha256 keys alike in both the text and JSON views, hence `message_digest` (research R6,
  decided with the user); the guard JSON carries `numIntf` per area, hence `expect_rows` (task
  T031); EOS prints no DR on point-to-point, so T027 did not apply; a v2c SNMP walk sees the default
  VRF only. `Cli -c` with `?` stores `?` as a key: do not use help through `Cli -c`.
- Section 4, `test/lab` with sw4 off the network (snapshots 1 and 2): sw1 and sw2 `ospf_interfaces`
  (5 rows) and `ospf_neighbours` (4 rows) `collected`, equal to their `.facts.yml` through the API:
  VLAN 44 in `exchange` on sw1 and `exstart` on sw2, VRF `blue` instance 2, the stub area 1 on a
  point-to-point interface with no DR. sw3 `denied`, sw4 and 172.20.20.9 `unreachable`, BGP of 010
  `collected` on both. Every OSPF `interface` value is in the same switch's `interfaces` rows
  (SC-005). None of the 70 stored outputs nor any `observation.parsed` holds `nm-lab-secret-`, a
  `message-digest-key` or an `authentication-key`. The projection has only `has_address` and
  `l1_link` edges, and `find` tasks target only the management addresses. The second crawl gives
  the same OSPF rows on both switches while the raw neighbour outputs differ (SC-004).
- Section 5, EVPN lab, one seed, SSH as `admin` and SNMPv3 `snmp-ro` (snapshots 3 and 4): 28
  devices identified, both families `collected` on core1 and core2 and `empty` on the 26 others,
  none `parse_failed`; core1 and core2 equal to their `.facts.yml`. The second crawl, with the lab's
  traffic running, gives the same rows for all 56 observations while core1's raw outputs differ.
- Not seen live: a `join step rows matched no row` log line (no device of either lab has an area
  without a neighbour during a crawl; `sw4noadj` covers it from recordings and `TestJoin`).
