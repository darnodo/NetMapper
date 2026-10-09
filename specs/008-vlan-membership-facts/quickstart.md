# Quickstart: validating VLAN and interface VLAN facts

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
| `packs` `TestPacks` | every recorded output of the new templates parses to its `.yml`; `_empty` outputs give no row |
| `packs` `TestFamiliesFromLab` | sw1's and sw2's recorded steps give the rows of `<switch>_vlans.facts.yml` and `<switch>_interface_vlans.facts.yml`; sw4 gives `empty` `interface_vlans` |
| `packs` internal VLAN test (research R1) | no ID listed by sw2's recorded `show vlan internal usage` is a `vlans` row |
| `internal/pack` | `NormaliseVLANs`: sorting, merging, wrapped lists, bad tokens, IDs out of range |
| `internal/fact` | a `VLANList` field out of normal form, or with an ID outside 1 to 4094, is refused |
| `internal/parse` `TestVLANList` | a VLAN list wrapped over lines comes back sorted and merged, `none` leaves the field absent, a bad token is `parse_failed` |
| `packs` `TestVLANDriftIsNotEmpty` | the status step's `empty_lines` skips a table of only `routed` lines, and still fails on drift |
| `packs` `TestTrunkVariants` | a shut trunk has its mode only, `None` leaves both lists absent, a wrapped list comes back whole, a shut access port keeps its VLAN, a dot1q-tunnel port reads as access |
| `packs` `TestDescriptionIsNotTheVlanColumn` | a description holding a status word or a VLAN-like token is read as the Name |

## 2. Load warnings

Start `./netmapper collector` as in the README and read its startup log.

Expected: no warning for `arista_eos`. Any other platform pack with a fingerprint logs one line per
new family, with the effect "no VLAN data for these devices; they will join no L2 domain".

## 3. Record fixtures from the repository lab (NetLab VM)

Deploy the changed lab (three new links, research R7):

```sh
sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure
```

Check the lab before recording: on sw1 and sw2, `show port-channel` lists `Port-Channel10` with two
active members, `show vlan` lists VLAN 31 as `suspended` (EOS has no local VLAN shutdown, research R6).

Record each step command of both recipes (`show vlan`, `show interfaces status`, `show interfaces trunk`), sent exactly as the recipe writes it, on sw1 and sw2,
named `<switch>_<template name>.raw`:

```sh
docker exec clab-netmapper-sw2 Cli -p 15 -c 'show interfaces trunk | no-more' \
  > packs/arista_eos/testdata/lab/sw2_show_interfaces_trunk.raw
```

Also record, as test evidence only (no recipe sends it), `show vlan internal usage | no-more` on
sw2. On sw4 (a node of the topology, already up after the deploy) record the two
`interface_vlans` commands there, with `_empty` in the names. As in 007, strip the `> <command>`
first line that `Cli -c` adds to a piped command (`sed -i '1{/^> /d}' *.raw`).

Before writing a template, answer the "to confirm" items of research R1 to R8 from these files and
update `research.md` with what EOS printed.

Then, with no lab:

```sh
go test -count=1 ./packs/...
```

## 4. Crawl the repository lab

Same setup as 007's quickstart section 4 (collector on the NetLab VM, engine, `run` and API on the
Mac).

```sh
time netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds
netmapper project
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw2/facts/interface_vlans | jq
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw2/facts/vlans | jq
```

Expected (SC-002):

- sw1 and sw2: `vlans` `collected`, one row per VLAN of their configuration and VLAN 1, VLAN 31
  `suspended`, no ID from `show vlan internal usage`.
- sw1 and sw2: `interface_vlans` `collected`:
  - `Ethernet1`: `trunk`, `native_vlan` 99, `allowed_vlans` `["10", "20", "30-32", "40-49", "99"]`,
    `active_vlans` `["10", "20", "30", "32", "40-49", "99"]` (31 is suspended);
  - `Port-Channel10`: `trunk`, `allowed_vlans` `["1-4094"]`;
  - `Ethernet2` and `Ethernet3` (sw1) or `Ethernet3` and `Ethernet4` (sw2): `channel`
    `Port-Channel10`, nothing else;
  - the access port: `access`, `access_vlan` 10;
  - sw2 `Ethernet2` (routed, towards sw3): no row.
- sw3, sw4 and the unused address keep the outcomes of 001 and 006; the 007 families keep theirs.
- The projected graph has four Ethernet links between sw1 and sw2 (three new), all `both_ends`.

With sw4 up, the find step may take sw1 and sw4, which share a pinned serial, for one device and
scrape only one of them (004 quickstart, divergence 11; issue #26 is the same area). To see sw1's
rows, take sw4 off the network first: `docker exec clab-netmapper-sw4 ip link set eth0 down`, crawl,
then `ip link set eth0 up`.

## 5. External lab (SC-003, when it runs)

Crawl arista-evpn-vxlan-clab with the setup of [006's quickstart](../006-snmpv3-protocols/quickstart.md)
section 4. Expected: the 28 switches have both families, none `parse_failed`; spines give `empty`
`interface_vlans`; dc-leaf1 gives `Port-Channel999` (MLAG peer-link, trunk groups) and
`Port-Channel1` (`allowed_vlans` `["40"]`) with their members; campus-access1 gives its access port
in VLAN 60. Record dc-leaf1's and dc-spine1's outputs as extra fixtures, as in section 3. On
`Port-Channel999`, `allowed_vlans` is `["1-4094"]` and `active_vlans` is what the device reports,
which shows whether the trunk group restricts it (spec edge case).

## What the lab confirmed

Run on 2026-10-09: collector on the NetLab VM, PostgreSQL, object store, engine, `run` and API on
the Mac over the Tailnet, cEOS 4.36.0F.

- Section 1: `go test -count=1 ./...` with the three `NETMAPPER_TEST_*` variables passes, no test
  skipped; `gofmt`, `go vet` clean; `govulncheck` reports nothing reachable from our code.
- Section 3: every recording made from `test/lab` (sw1, sw2, sw4) and the EVPN lab (dc-leaf1,
  campus-access1, dc-spine1). What EOS printed settled research R1 to R8; three findings changed
  the design or the spec: `show interfaces switchport` prints port-channel members as access
  ports (R2), a trunk that is down is absent from the trunk view (R2, FR-005 amended), EOS has no
  local VLAN shutdown (R6), and a dot1q-tunnel port reads as access in its outer VLAN (R3).
- Section 4, job 1 (all four nodes up): sw1 marked `duplicate_of_task` of sw4 and not scraped,
  three entities and an `identity_conflict` on the chassis MAC (003 expectation holds). Job 2 (sw4
  off the network): sw1 and sw2 identified over SNMP and SSH, both new families `collected`, rows
  equal to `sw1_*.facts.yml` and `sw2_*.facts.yml` through the API, with evidence; sw1's
  `aaa_servers` and `management_servers` still `empty`; sw3 `denied` with a `credential_denied`
  finding; sw4 and 172.20.20.9 `unreachable`; 19 s per crawl; four sw1-sw2 Ethernet links
  `both_ends`.
- Section 5, job 3 (one seed, SSH `admin` then SNMPv3 `snmp-ro`): 28 devices identified, `vlans`
  `collected` on 28, `interface_vlans` `collected` on 22 and `empty` on the six spines and cores,
  none `parse_failed`, snapshot `published`, 31 s. dc-leaf1, campus-access1 and dc-spine1 equal to
  their `.facts.yml` through the API. dc-leaf1's MLAG peer-link `Port-Channel999` allows every VLAN
  and reports `1`, `40`, `4090-4091` active.
- Found on the way, not part of the feature: `flash:startup-config` on the redeployed sw1 was a
  stale file without the VLAN configuration, so `configure replace flash:startup-config` is not a
  safe reset in this lab; T006 reset sw1 from a copy of `test/lab/sw1.cfg` instead.
