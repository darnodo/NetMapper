# Quickstart: validating BGP peer facts

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
| `packs` `TestPacks` | every recorded output of the four new templates parses to its `.yml`; `_empty` outputs give no row |
| `packs` `TestFamiliesFromLab` | sw1, sw2, dc-leaf1 and core1 recordings give the rows of `<switch>_bgp_peers.facts.yml`; sw4 gives `empty` |
| `packs` `TestNoSecretInRecordedOutput` | no recording holds the `test/lab` BGP password or a `password 7` hash |
| `packs` error outputs | `% Authorization denied ...` or `% Invalid input` on any step, and `% BGP inactive` next to another line, give `parse_failed`, never `empty`; `% BGP inactive` alone on every step gives `empty` with no row |
| `internal/fact` | `EmptyList`, `OnlyWith`, `Set`: a negotiated list on a row not established is refused, a missing one on an established row is refused, `[]` is accepted, an unsorted or duplicated list is refused |
| `internal/parse` | an established session with no negotiated line gives `[]`, one not established gives no field even if the output prints capabilities, a peer in no summary gives configured `[]`, lists from several steps add up sorted and without duplicates, the steps in another order give the same row, a row breaking a rule makes the observation `parse_failed` with the field in the error |
| `internal/pack` | a recipe whose `covers` differs from the literals its steps map is refused at load |
| `internal/graph` | a snapshot with `bgp_peers` rows projects the same graph and verdict as one without |

## 2. Load warnings

Start `./netmapper collector` as in the README and read its startup log.

Expected: no warning for `arista_eos`. Any other platform pack with a fingerprint logs one line for
`bgp_peers`, with the effect "no BGP sessions for these devices".

## 3. Record fixtures from the repository lab (NetLab VM)

Deploy the changed lab (research R10):

```sh
sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure
```

Check the lab before recording, on sw1 and sw2 with `show bgp neighbors vrf all | include state`:

- the password session is `Established` on both ends;
- sw1's wrong-AS peer is not `Established`;
- sw1's shut peer is `Idle` and shut;
- sw2 lists one dynamic peer from its listen range, `Established`.

Record the four step commands, sent exactly as the recipe writes them, on sw1 and sw2, named
`<switch>_<template name>.raw`:

```sh
docker exec clab-netmapper-sw1 Cli -p 15 -c 'show bgp neighbors vrf all | no-more' \
  > packs/arista_eos/testdata/lab/sw1_show_bgp_neighbors_vrf_all.raw
```

On sw4, record the four commands with `_empty` in the names. Also record, as test input for the
error cases, what EOS prints for a command the account may not run (for example through a role
without `show bgp`), so the refusal used in the tests is the device's own. As in 007, strip the `> <command>`
first line that `Cli -c` adds to a piped command (`sed -i '1{/^> /d}' *.raw`).

Before writing a template, answer the "to confirm" items of research R1 to R4 and R7 to R9 from
these files and update `research.md` with what EOS printed. Then search the recordings for the
password by hand once, before the test does:

```sh
grep -l 'nm-lab-secret-' packs/arista_eos/testdata/lab/*bgp*.raw   # expected: nothing
```

Then, with no lab:

```sh
go test -count=1 ./packs/...
```

## 4. Crawl the repository lab

Same setup as 007's quickstart section 4 (collector on the NetLab VM, engine, `run` and API on the
Mac). Take sw4 off the network first, as in 008 (`docker exec clab-netmapper-sw4 ip link set eth0
down`), so sw1 is scraped.

```sh
netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw1/facts/bgp_peers | jq
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw2/facts/bgp_peers | jq
```

Expected (SC-002, SC-005):

- sw1, `collected`, three rows in VRF `default`:
  - the password session: `established`, `shutdown: no`, `dynamic: no`, the description,
    `configured_address_families` `["ipv4 unicast", "ipv6 unicast"]`,
    `negotiated_address_families` `["ipv4 unicast"]` (the mismatch), `peer_router_id` set;
  - the wrong-AS peer: the configured `peer_as`, a state other than `established`, `shutdown: no`,
    no `negotiated_address_families`, no `peer_router_id`, `configured_address_families` present;
  - the shut peer: `idle`, `shutdown: yes`, no negotiated list.
- sw2, `collected`: the password session (`ipv4 unicast` configured and negotiated), and the dynamic
  peer with `dynamic: yes`.
- The password appears in no stored output (search the object store bucket for `nm-lab-secret-`).
- sw4 (back up and crawled alone): `bgp_peers` `empty`.
- The graph and verdict are those of the same lab without BGP: no edge from a BGP session, no new
  device from a peer address.

Volatility check (SC-004): run the crawl a second time a few minutes later and compare the
`bgp_peers` rows of the two snapshots. Expected: identical, although the stored outputs differ (their
counters and timers changed).

## 5. External lab (SC-003, when it runs)

Crawl arista-evpn-vxlan-clab with the setup of [006's quickstart](../006-snmpv3-protocols/quickstart.md)
section 4. Expected:

- every switch has a `bgp_peers` observation, none `parse_failed`;
- the access switches and anything else without BGP are `empty` (dc-access1 prints
  `% BGP inactive`);
- dc-leaf1: five rows in `default`, two eBGP underlay and one iBGP with `ipv4 unicast` configured
  and negotiated, two eBGP EVPN with `l2vpn evpn` configured and negotiated, peer groups `underlay`,
  `underlay_ibgp` and `evpn`;
- core1: five rows in `default` and five in `gold`, local AS 65500 and router ID 10.0.200.1 on all,
  the `gold` rows with `configured_address_families` `["ipv4 unicast"]` like the `default` ones.

Record dc-leaf1's and core1's four outputs as fixtures, as in section 3. Then crawl again with the
lab's traffic still running and check that every device's rows are identical to the first crawl.
