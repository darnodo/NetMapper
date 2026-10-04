# Quickstart: validating device management facts

Run guide for proving the feature end to end. Field tables are in [data-model.md](data-model.md),
the endpoint in [contracts/rest-facts.md](contracts/rest-facts.md), the decisions in
[research.md](research.md).

Sections 1 and 2 need no lab. Sections 3 to 5 need the `NetLab` VM, where both containerlab labs
run; it is remote and not always up.

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d --wait postgres garage snmpd
docker compose -f deploy/compose.yaml run --rm garage-init
export NETMAPPER_TEST_DSN='postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable'
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
export NETMAPPER_TEST_SNMP=127.0.0.1:1161
go test -count=1 ./...
```

Expected: everything passes, nothing skipped, and in particular:

| Test | Proves |
| ---- | ------ |
| `packs` `TestPacks` | every recorded output of the new templates parses to its `.yml`; `_empty` outputs give no row |
| `packs` family test (research R11) | sw2's recorded steps give the rows of `sw2_<family>.facts.yml` for each of the six families |
| `packs` secret test (research R9) | no recorded lab output holds a listed secret, a hash or a type 7 key |
| `internal/pack` | `map` on a step, `defaults` and `split` load, and a bad field, enum or type is refused |
| `internal/parse` | per-step literal, default, split and list handling on hand-written outputs |
| `internal/api` | the facts endpoint: 200, `no_such_family`, `not_collected`, `empty` as 200, under the `netmapper_api` role |

## 2. Load warnings

Start `./netmapper collector` as in the README and read its startup log (packs load first).

Expected: no warning for `arista_eos`. Any other platform pack with a fingerprint logs one line per
new family, with the effect "no management inventory for these devices".

## 3. Record fixtures from the repository lab (NetLab VM)

Deploy the changed lab, as in [001's quickstart](../001-crawl-loop/quickstart.md):

```sh
sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure
```

Record each step of each new recipe, on sw2 (configured) and sw1 (bare). `docker exec ... Cli`
skips AAA, so it does not depend on the dead TACACS+ and RADIUS addresses:

```sh
docker exec clab-netmapper-sw2 Cli -p 15 -c 'show tacacs | no-more' \
  > packs/arista_eos/testdata/lab/sw2_show_tacacs.raw
```

One file per command of research R1, named `<switch>_<template name>.raw`; sw1 outputs that give no
row get `_empty` in their name. Then, with no lab:

```sh
go test -count=1 ./packs/...
```

Expected: the secret test passes on the new files before any template is written. If it fails, the
command prints a secret on this EOS version: drop it from the recipe, do not mask the fixture.

## 4. Crawl the repository lab

```sh
export LAB_SNMP_COMMUNITY=public LAB_SSH_PASSWORD=admin
export LAB_SNMP_V3='{"auth":"...","priv":"..."}'   # values in test/lab/sw2.cfg
time netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds
netmapper project
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw2/facts/aaa_methods | jq
```

Expected:

- sw2: the six families `collected`, rows matching `test/lab/sw2.cfg` (SC-002), including the VRF
  MGMT rows and the syslog host on port 1514.
- sw1: `aaa_servers`, `aaa_methods` and `management_servers` are `empty`; `management_apis` lists SSH.
- sw3, sw4 and the unused address keep the outcomes of 001 and 006; sw2 is still identified over
  SNMPv3.
- Crawl duration within a few seconds of the same crawl on `main`: no login waits on the dead AAA
  servers (research R10).
- `curl .../v1/devices/sw2/facts/nope` gives 404 `no_such_family`; the same request with
  `?snapshot=` on a snapshot taken before this feature gives 404 `not_collected`.
- `grep -r nm-lab-secret-` over a dump of `observation.parsed` and of the stored raw objects finds
  nothing.

## 5. External lab (SC-003, when it runs)

Crawl arista-evpn-vxlan-clab with the setup of [006's quickstart](../006-snmpv3-protocols/quickstart.md)
section 4. Expected: the 28 switches have the six families, none `parse_failed`; dc-leaf1 and
campus-access1 match the tables of the lab's `docs/observability/management-plane.md`. Record
dc-leaf1's and campus-access1's outputs as extra fixtures, as in section 3, and rerun the secret
test.
