# Quickstart: validating the Cisco IOS-XE pack

Run guide for proving the feature end to end. What the pack sends and fills is in
[contracts/cisco-iosxe-pack.md](contracts/cisco-iosxe-pack.md), the decisions in
[research.md](research.md), the lab layout in [data-model.md](data-model.md).

Sections 1 and 2 need no lab. Sections 3 and 4 need the `NetLab` VM, which is remote and not
always up.

## 1. Automated checks (no lab)

Same environment as [007's quickstart](../007-device-management-facts/quickstart.md) section 1,
then:

```sh
go test -count=1 ./...
```

Expected: everything passes, nothing skipped, in under a minute for `./packs/` (SC-004), and in
particular:

| Test | Proves |
| ---- | ------ |
| `packs` `TestPacks` | the `cisco_iosxe` pack loads with every load check (read-only prefixes, templates compile, map targets exist), and every `cat1` recording yields rows; `_empty` ones yield none |
| `packs` `TestFamiliesFromLab` | `cat1`'s recordings give the rows of `cat1_neighbours.facts.yml`, `cat1_interfaces.facts.yml`, `cat1_vlans.facts.yml` and `cat1_interface_vlans.facts.yml` |
| `packs` `TestNoSecretInRecordedOutput` | no `cat1` recording holds a lab secret or a `secret 9` / type 7 hash |
| `packs` IOS-XE naming test | every abbreviation of research R8 and its long form give one canonical name; `Tw1/0/1` and `Twe1/0/1` give different ones |
| `packs` legacy VLAN test | `cat1`'s `show vlan brief` gives no row for 1002 to 1005 |
| `packs` refusal test | `% Invalid input detected at '^' marker.` on any step gives `parse_failed`, never `empty` |
| `packs` fingerprint test | `cat1`'s `show version` matches `cisco_iosxe` only; an Arista `show version` matches `arista_eos` only; SNMP sysObjectID `1.3.6.1.4.1.9.1.3160` with `cat1`'s sysDescr gives `cisco_iosxe` and `17.15.1`, and so does SSH |
| `packs` LLDP off test | the recorded LLDP-off output gives `empty` |

## 2. Load warnings

Start `./netmapper collector --packs ./packs` and read its startup log.

Expected: one warning per family `cisco_iosxe` has no recipe for, nine in all (the six management
families, `bgp_peers`, `ospf_interfaces`, `ospf_neighbours`). None for `arista_eos`.

## 3. Deploy the mixed lab and record fixtures (NetLab VM)

Copy `test/lab-iosxe/` to `~/netmapper-iosxe-lab` on the VM (NetLab has no repository clone) and
deploy:

```sh
sudo containerlab deploy -t iosxe.clab.yaml
```

The Catalyst takes 12 to 16 minutes (research R11). It is ready when both hold:

```sh
docker logs clab-netmapper-iosxe-cat1 2>&1 | grep "Startup complete"
docker exec clab-netmapper-iosxe-eos-a Cli -c "show lldp neighbors"   # lists cat1
```

Until then `cat1` is `unreachable` or its ports are `notconnect`: record nothing and crawl nothing.
If the EVPN fabric is running too and `cat1` does not start, stop one of the two labs (research
R11).

Check that the startup-config was loaded (research R6): if the `netmapper` login is refused,
the bootstrap applied without `cat1.cfg`; log in as `admin` (the vrnetlab default account), read
`show running-config`, fix `cat1.cfg` and redeploy with `--reconfigure`.

Check that `eos-a` sees nothing on Management0 (`show lldp neighbors`: `cat1` on Et1 and Et2
only); if it does, run the `exec` command of `iosxe.clab.yaml` on `eos-a` and `eos-b` by hand.

Check before recording, on `cat1` as `netmapper`: `show privilege` says level 1; `show lldp
neighbors` lists `eos-a` twice and `eos-b` once, each with its 172.20.22.x address under
`Management Addresses`. On `eos-a`, `show lldp neighbors detail` gives `cat1` a 172.20.22.3
management address, not 10.0.0.15 (research R6).

Record through an interactive SSH session as `netmapper`, which is how the collector runs
commands, one file per command, named `cat1_<template name>.raw`: `show version`, `show lldp
neighbors detail`, `show interfaces`, `show vlan brief`, `show interfaces switchport`, `show
interfaces trunk`. Then the variants, each reverted after:

| Fixture | Change on the lab | Command |
| ------- | ----------------- | ------- |
| `cat1lldpoff_show_lldp_neighbors_detail_empty.raw` | `cat1`: `no lldp run` | `show lldp neighbors detail` |
| `cat1trunkdown_*` | `cat1`: `shutdown` on Port-channel1 | `show interfaces switchport`, `show interfaces trunk` |
| `cat1wrapped_show_interfaces_trunk.raw` | `cat1` and `eos-a`: an allowed list long enough to wrap | `show interfaces trunk` |
| `cat1_show_running_config_refused_empty.raw` | none | `show running-config` (refused at privilege 1) |

Settle the "to confirm" items of research R3, R5, R9 and R10 from these outputs. Never edit a
recording. Then, with no lab:

```sh
go test -count=1 ./packs/...
```

## 4. Crawl across platforms

Same setup as [011's quickstart](../011-ospf-facts/quickstart.md) section 4: a linux/amd64 build
and `packs/` copied to `~/netmapper-run` on NetLab, the collector there, PostgreSQL, the object
store, the engine, `run` and the API on the Mac over the Tailnet.

```sh
netmapper run --config test/lab-iosxe/netmapper.yaml --perimeter lab-iosxe --seed-set iosxe-seeds
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices | jq
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/cat1/facts/interface_vlans | jq
```

Expected:

- three devices identified, `cat1` with platform `cisco_iosxe` and version `17.15.1`, none
  `unknown_platform` (SC-001);
- `eos-b` reached by a `find` queued from `cat1`'s `neighbours` observation, since `eos-a` does
  not see it (spec US1, FR-012);
- three `l1_link` edges, two between `eos-a` and `cat1` and one between `cat1` and `eos-b`, each
  confirmed by both ends, with `cat1`'s side named `GigabitEthernet1/0/<n>` from both reports
  (SC-002);
- `cat1`'s `neighbours`, `interfaces`, `vlans` and `interface_vlans` `collected` and equal to
  their `.facts.yml`; the nine other families `unsupported` with `no_recipe`; the snapshot
  verdict as with `cat1` an Arista;
- the L2 domain of each VLAN allowed on the port-channel spans `eos-a` and `cat1`, and VLAN 10
  spans all three;
- no stored output holds a lab secret.

Volatility check (SC-003): crawl again a few minutes later. Expected: identical rows for every
`cat1` family. Replay check (SC-006): replaying the snapshot's stored outputs through the pack
gives the same rows.

Timing check (SC-005): from a stopped lab, deploy, readiness and the crawl fit in the time the
spec states.

Destroy the lab after use:

```sh
sudo containerlab destroy -t iosxe.clab.yaml
```

## What the lab confirmed

Run on 2026-10-21: lab on the NetLab VM (Cat9kv 17.15.01, cEOS 4.36.0F), collector on NetLab with
the branch build, PostgreSQL, object store, engine, `run` and API on the Mac over the Tailnet.

- Section 1: `go test -count=1 ./...` with the three `NETMAPPER_TEST_*` variables passes, no test
  skipped; `go test ./packs/` takes 4 seconds; `gofmt` and `go vet` clean.
- Section 2: the collector's startup log on NetLab has nine `no recipe` warnings, all for
  `cisco_iosxe`, none for `arista_eos`.
- Section 3 (tasks T008, one deploy): every recording made; the results and the open points are in
  research.md, "Recorded on 2026-10-21". The collector's own SSH transport read `show interfaces`
  whole with the pack's pty height.
- Section 4, three deploys in all for this section: the first crawl (snapshot 1) was cancelled
  when `eos-a` turned out to still see `eos-b` on Management0 (the startup-config lines had been
  rewritten by containerlab, research "Recorded on 2026-10-21"); a node-filtered redeploy of the
  Arista nodes failed on containerlab 0.79, so the lab was destroyed and redeployed, and the fixed
  `exec` was applied with `containerlab exec`. On that deploy (14:28:17 UTC):
  - ready at 14:44:08 (`Startup complete` after 12 min 37 s, data ports up after 15 min 51 s);
    crawl started 14:44:36, snapshot 2 closed with its graph at 14:44:53: 16 min 36 s from the
    deploy (SC-005, 25 minutes);
  - three devices, `cat1.example.com` as `cisco_iosxe` 17.15.1 over SNMP, SNMP and SSH answering;
    no `unknown_platform` (SC-001);
  - the `find` of `eos-b` (172.20.22.4) was queued by `cat1`'s, itself queued by the seed's
    (FR-012);
  - three `l1_link` edges, each `both_ends` with two evidence rows, `cat1`'s side named
    `GigabitEthernet1/0/1` to `1/0/3` (SC-002);
  - `cat1`'s four families `collected` and equal to their `.facts.yml` once MACs and chassis IDs
    (new on every deploy) are left out, except Gi1/0/6, a vrnetlab dummy port that was
    `notconnect` when recorded and up on this deploy; the nine other families `unsupported` with
    `no_recipe`;
  - L2 domains: VLAN 10 spans the three devices, VLANs 20 and 99 span `eos-a` and `cat1`, each
    VLAN 1 is local;
  - none of the 80 stored outputs holds a hash or a secret;
  - snapshot 2 was `quarantined` with coverage 0: its baseline was snapshot 1, made on the
    previous deploy, whose devices had other serials and MACs. Not a pack issue.
- Second crawl (snapshot 3, same deploy): `published`, coverage 1 against snapshot 2; the rows of
  the five families of the three devices are identical to snapshot 2 while 45 stored outputs
  differ (SC-003).
- Replay (SC-006): `cat1`'s stored outputs of snapshot 2, fetched through the API and parsed with
  the pack, give the stored rows exactly, for the four families.
