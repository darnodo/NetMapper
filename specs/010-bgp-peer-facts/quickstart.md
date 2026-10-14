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
| `packs` `TestPacks` | every recorded output of the five new templates yields rows; `_empty` outputs (no BGP, header-only summaries, no peer group, refusals) yield none |
| `packs` `TestFamiliesFromLab` | sw1, sw2, dc-leaf1 and core1 recordings give the rows of `<switch>_bgp_peers.facts.yml`; sw4 gives `empty` |
| `packs` `TestNoSecretInRecordedOutput` | no recording holds the `test/lab` BGP password or a `password 7` hash |
| `packs` `TestBGPErrorsAreNotEmpty` | `% Authorization denied ...` or `% Invalid input` on any step, and `% BGP inactive` next to another line, give `parse_failed`, never `empty`; `% BGP inactive` alone on every step gives `empty` with no row |
| `internal/fact` | `EmptyList`, `OnlyWith`, `Set`: a negotiated list on a row not established is refused, a missing one on an established row is refused, `[]` is accepted, an unsorted or duplicated list is refused |
| `internal/parse` | an established session with no negotiated line gives `[]`, one not established gives no field even if the output prints capabilities, a peer in no summary gives configured `[]`, lists from several steps add up sorted and without duplicates, the steps in another order give the same row, a row breaking a rule makes the observation `parse_failed` with the field in the error |
| `internal/pack` `TestCovers` | a recipe whose `covers` differs from the literals its steps map, or names a field that is not a list, is refused at load |
| `packs` `TestBGPStepOrder` | dc-leaf1's summary steps in reverse order give the same rows |
| `packs` `TestBGPSessionThatWentDown` | a session shut after it was up keeps no negotiated list and no peer router ID, though EOS still prints the old router ID |
| `packs` `TestBGPDefaultActivation` | a peer relying on EOS's default ipv4 activation has `ipv4 unicast` in its configured list |
| `internal/graph` | a snapshot with `bgp_peers` rows projects the same graph and verdict as one without |
| `internal/api` | a stored row with `negotiated_address_families: []` and one without the field are served as stored |

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

Record the five step commands, sent exactly as the recipe writes them, on sw1 and sw2, named
`<switch>_<template name>.raw`:

```sh
docker exec clab-netmapper-sw1 Cli -p 15 -c 'show bgp neighbors vrf all | no-more' \
  > packs/arista_eos/testdata/lab/sw1_show_bgp_neighbors_vrf_all.raw
```

On sw4, record the five commands with `_empty` in the names, in the form an interactive SSH session prints (`% BGP inactive`, research R8). Also record, as test input for the
error cases, what EOS prints for a command the account may not run (for example through a role
without `show bgp`), so the refusal used in the tests is the device's own. As in 007, strip the `> <command>`
first line that `Cli -c` adds to a piped command (`sed -i '1{/^> /d}' *.raw`).

Record the variants the tests need, each from a temporary change on the running lab, undone (or the
lab redeployed) after recording. An error line is recorded through an interactive SSH session,
which is how the collector runs commands, not through `Cli -c`, which adds ` at line 1` (research
R8):

```sh
printf 'show bgp neighbors vrf all\nexit\n' | sshpass -p admin ssh -tt netmapper@172.20.20.5
```

| Fixture | Change on the lab | Command |
| ------- | ----------------- | ------- |
| `sw1_show_bgp_neighbors_vrf_all_down.raw` | sw1: `router bgp 65101` / `neighbor 10.0.10.2 shutdown`, after the session was up | step 1 |
| `sw2_show_bgp_peer_group_nomembers_empty.raw` | sw1: `neighbor 10.255.0.12 shutdown`, so sw2's dynamic peer leaves | step 5 on sw2 |
| `sw2_show_bgp_neighbors_vrf_all_denied_empty.raw` | sw2 (it enforces command authorization): a temporary role denying `show bgp.*` and `show ip bgp.*`, a temporary user with it, both removed after | step 1 as that user, interactive SSH |
| `sw1_show_bgp_evpn_summary_invalid_empty.raw` | none | `show bgp evpn summary vrf all`, interactive SSH |
| `sw4nopeer_*` (five files) | sw4: `ip routing`, `router bgp 65104` with no neighbour | the five steps |
| `sw2_show_ip_bgp_summary_vrf_all_defaultactive.raw` | sw2: `address-family ipv4` / `default neighbor 10.0.10.1 activate` (removes the explicit activate) | step 2 |

`linklocal_*` is not recorded: no lab has an IPv6 or link-local session, so it is written by hand in
the format of sw1's recordings, as its `.facts.yml` header says.

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

- sw1, `collected`, four rows in VRF `default`:
  - the password session: `established`, `shutdown: no`, `dynamic: no`, the description,
    `configured_address_families` `["ipv4 unicast", "ipv6 unicast"]`,
    `negotiated_address_families` `["ipv4 unicast"]` (the mismatch), `peer_router_id` set;
  - the wrong-AS peer: the configured `peer_as`, a state other than `established`, `shutdown: no`,
    no `negotiated_address_families`, no `peer_router_id`, `configured_address_families` present;
  - the shut peer: `idle`, `shutdown: yes`, no negotiated list;
  - the outbound peer into sw2's listen range (10.255.0.12): `dynamic: no` on sw1's side, since sw1
    configures it; the `dynamic: yes` row is on sw2.
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

Record dc-leaf1's and core1's five outputs as fixtures, as in section 3. Then crawl again with the
lab's traffic still running and check that every device's rows are identical to the first crawl.

## What the lab confirmed

Run on 2026-10-14: collector on the NetLab VM, PostgreSQL, object store, engine, `run` and API on
the Mac over the Tailnet, cEOS 4.36.0F.

- Section 1: `go test -count=1 ./...` with the three `NETMAPPER_TEST_*` variables passes, no test
  skipped; `gofmt`, `go vet` clean; `govulncheck` reports nothing reachable from our code.
- Section 3: every recording made from `test/lab` (sw1, sw2, sw4) and the EVPN lab (dc-leaf1,
  core1, dc-access1). Four findings changed the design: `show bgp neighbors` has no dynamic marker,
  so `show bgp peer-group` is a fifth step (research R3); a summary gains a `Description` column
  when any peer has a description (R2); the collector's interactive session prints `% BGP
  inactive` with no ` at line 1` (R8); and a default used to shadow a later step's value in a
  merge, so non-key defaults now fill the merged row (R5).
- Section 4, `test/lab` with sw4 off the network (snapshot 4): sw1 and sw2 `bgp_peers`
  `collected`, rows equal to `sw1_bgp_peers.facts.yml` and `sw2_bgp_peers.facts.yml` through the
  API: sw1's mismatch (`ipv6 unicast` configured, not negotiated), the wrong-AS peer in `connect`,
  the shut peer `idle` with `shutdown: yes`; sw2's dynamic peer with `dynamic: yes`. The other
  families keep their outcomes: sw1 `aaa_servers` and `management_servers` `empty`, sw3 `denied`,
  sw4 and 172.20.20.9 `unreachable`. None of the 60 stored outputs of the snapshot, nor any
  `observation.parsed`, holds `nm-lab-secret-bgp`. The projection has only `has_address` and
  `l1_link` edges, none to a BGP peer address, and no `find` task targets one. A second crawl
  (snapshot 5) gives the same `bgp_peers` rows on both switches.
- Section 5, EVPN lab, one seed (snapshots 6 and 7): 28 devices identified, `bgp_peers`
  `collected` on 22 and `empty` on the six access switches, none `parse_failed`, snapshot
  `published`. dc-leaf1 and core1 equal to their `.facts.yml`. The second crawl, with the lab's
  traffic running, gives the same rows on all 28 devices, while the stored neighbour outputs differ
  (timers, last read and write).
- Not run live: sw4 crawled alone for its `empty` observation; its recordings cover it
  (`sw4_bgp_peers.facts.yml`).
