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
| `packs` `TestNoSecretInRecordedOutput` | no recording holds the `test/lab` OSPF keys or an `md5 7` / `sha<n> 7` hash |
| `packs` `TestOSPFErrorsAreNotEmpty` | `% Authorization denied ...`, `% Invalid input` or `% OSPF instance does not exist ...` on any step give `parse_failed`, never `empty`; blank output on every step gives `empty` |
| `internal/fact` | `Missing`: a row without `auth_type` is refused by `Validate`; `unknown` is accepted |
| `internal/parse` | a mapped row with no `auth_type` gives `unknown`, never `none` and never absent (FR-013a); a `join_on` step fills matching rows, creates none, does not overwrite, and alone does not make the observation `collected`; an interface whose area has no join row is `parse_failed` |
| `internal/pack` | a pack without `pack_schema` loads as version 1, `covers` loads in both versions; `join_on`, `unmatched` or `guard` in a version 1 pack, a guard that is not first, has a `map` or `empty_lines`, or has no `none`, `none` on a step that is not a guard, `pack_schema: 3`, `join_on` with no row-creating step before it, `join_on` without `unmatched`, or a `defaults` entry on a `Missing` field is refused at load |
| `internal/parse` (`unmatched`) | a join row that matches no row gives `parse_failed` naming the step and key with `fail`, and the same rows plus the returned key with `ignore` |
| `internal/parse` (`guard`) | the guard's "none" document gives `empty`, pretty-printed or on one line; a blank guard output gives `parse_failed`; the "none" document with another step holding output gives `parse_failed`; a guard error line gives `parse_failed`; a populated guard with blank row steps gives `empty` |
| `internal/collector` | on the fake transport, a blank output returned with an idle timeout gives `unreachable`, with a deadline or a dropped session no observation for the family; never `empty` |
| `internal/graph` | a snapshot with OSPF rows projects the same graph and verdict as one without |

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

- VLAN 20 (area 0, md5) and VLAN 21 (area 1 stub, point-to-point) are `FULL` on both ends;
- VLAN 22 in VRF `blue` (instance 2, sha) is `FULL` on both ends;
- VLAN 23 (MTU mismatch) is held in `EXSTART` or `EXCHANGE`, and still is a few minutes later.

Then settle the "to confirm" items of research R2, R4, R5 and R6 from the outputs:

- the stub area line of `show ip ospf vrf all`;
- the point-to-point network type and state words, and whether DR lines are printed there;
- the md5 and sha authentication lines of `show ip ospf interface vrf all`;
- over SNMP, `ospfIfAuthType` for the md5 and sha interfaces (walk from a lab EOS container's
  bash, as on core2 during planning).

If an interface with authentication prints no authentication line, or SNMP answers nothing usable
for SHA, record that output as a real `unknown` fixture (spec FR-013a). Never edit a recording.

Record the four step commands (the guard `show ip ospf vrf all | json`, the interface, neighbour
and instance views) through an interactive SSH session, which is how the collector runs
them, named `<switch>_<template name>.raw`. Strip the `> <command>` first line if `Cli -c` was used
(feature 007). Also:

| Fixture | Change on the lab | Command |
| ------- | ----------------- | ------- |
| `sw4_show_ip_ospf_*_empty.raw` | none (sw4 has no OSPF) | the four step commands, interactive SSH; check the guard answers `{"vrfs": {}}` as on dc-leaf1, and whether the guard JSON on sw4noadj carries an interface count per area or instance (research R14) |
| `sw4noadj_show_ip_ospf_*.raw` | sw4: `router ospf 1`, Loopback0 in area 0, reverted after | the four step commands |
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
  instance, router ID, area and area type; Loopback0 `passive: yes`; VLAN 20 `auth_type: md5`,
  VLAN 22 `sha`, VLAN 21 `none`; DR and BDR router IDs on the broadcast segments;
- `ospf_neighbours` `collected`: `full` on VLANs 20, 21 and 22, the VLAN 22 row in VRF `blue`; the
  VLAN 23 row in the state seen in section 3; no row on Loopback0;
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
