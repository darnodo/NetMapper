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
| `internal/pack` | `NormaliseVLANs`: sorting, merging, `ALL`, wrapped lists, bad tokens |
| `internal/fact` | a `VLANList` field out of normal form, or with an ID outside 1 to 4094, is refused |
| `internal/parse` | the step 3 `empty_lines` rule skips a status table with no member, and still fails on drift |

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
active members, `show vlan` lists VLAN 31 as `suspended` and VLAN 32 as `act/lshut` (research R6).

Record each step command of both recipes, sent exactly as the recipe writes it, on sw1 and sw2,
named `<switch>_<template name>.raw`:

```sh
docker exec clab-netmapper-sw2 Cli -p 15 -c 'show interfaces trunk | no-more' \
  > packs/arista_eos/testdata/lab/sw2_show_interfaces_trunk.raw
```

Also record, as test evidence only (no recipe sends it), `show vlan internal usage | no-more` on
sw2. Deploy sw4 alone (`--node-filter sw4`, see the topology comment) and record the three
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
  `suspended`, VLAN 32 `shutdown`, no ID from `show vlan internal usage`.
- sw1 and sw2: `interface_vlans` `collected`:
  - `Ethernet1`: `trunk`, `native_vlan` 99, `allowed_vlans` `["10", "20", "30-32", "40-49", "99"]`,
    `active_vlans` without 31 and 32;
  - `Port-Channel10`: `trunk`, `allowed_vlans` `["1-4094"]`;
  - `Ethernet2` and `Ethernet3` (sw1) or `Ethernet3` and `Ethernet4` (sw2): `channel`
    `Port-Channel10`, nothing else;
  - the access port: `access`, `access_vlan` 10;
  - sw2 `Ethernet2` (routed, towards sw3): no row.
- sw3, sw4 and the unused address keep the outcomes of 001 and 006; the 007 families keep theirs.
- `netmapper project` now reports four links between sw1 and sw2 (three new), all `both_ends`.

## 5. External lab (SC-003, when it runs)

Crawl arista-evpn-vxlan-clab with the setup of [006's quickstart](../006-snmpv3-protocols/quickstart.md)
section 4. Expected: the 28 switches have both families, none `parse_failed`; spines give `empty`
`interface_vlans`; dc-leaf1 gives `Port-Channel999` (MLAG peer-link, trunk groups) and
`Port-Channel1` (`allowed_vlans` `["40"]`) with their members; campus-access1 gives its access port
in VLAN 60. Record dc-leaf1's and dc-spine1's outputs as extra fixtures, as in section 3. On
`Port-Channel999`, `allowed_vlans` is `["1-4094"]` and `active_vlans` is what the device reports,
which shows whether the trunk group restricts it (spec edge case).
