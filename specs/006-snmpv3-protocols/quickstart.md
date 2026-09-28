# Quickstart: validating configurable SNMPv3

Run guide for proving the feature end to end. The document keys are in
[contracts/config.md](contracts/config.md), storage in [data-model.md](data-model.md), the decisions in
[research.md](research.md).

## Prerequisites

Go and Docker with compose. For section 3, containerlab and a cEOS image, as in
[004's quickstart](../004-graph-projector/quickstart.md). For section 4, reach to the
[arista-evpn-vxlan-clab](https://github.com/darnodo/arista-evpn-vxlan-clab) lab management network.

## 1. Automated checks

The development stack gains the `snmpd` test agent (research R7), published on `localhost:1161/udp`.

```sh
docker compose -f deploy/compose.yaml up -d --wait postgres garage snmpd
docker compose -f deploy/compose.yaml run --rm garage-init
export NETMAPPER_TEST_DSN='postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable'
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
export NETMAPPER_TEST_SNMP=127.0.0.1:1161
go test -count=1 ./...
```

Expected: everything passes, nothing skipped. The SNMP transport test covers one user per row:

| User (in `deploy/snmpd/snmpd.conf`) | Set protocols        | Expected                               |
| ----------------------------------- | -------------------- | -------------------------------------- |
| SHA-1 / AES-128                     | none named (default) | sysName read                           |
| SHA-256 / AES-128                   | `sha256` / `aes`     | sysName read                           |
| SHA-512 / AES-256                   | `sha512` / `aes256`  | sysName read                           |
| SHA-1 / AES-256 (Reeder)            | `sha` / `aes256c`    | sysName read                           |
| SHA-256, authNoPriv                 | `sha256` / `none`    | sysName read                           |
| SHA-256 / AES-128                   | `sha` / `aes`        | `ErrAuth` with evidence (research R6)  |
| unknown user                        | any                  | `ErrAuth` with evidence (research R6)  |

## 2. Load-time errors

```sh
netmapper run --config bad.yaml --perimeter lab --seed-set seeds
```

With `auth_protocol: SHA256`, with `priv_protocol` on a `snmp_v2c` set, and with a v3
`secret_ref: vault:kv/data/x#auth`: each exits with code 2 and the message from the contract. No job
is created.

## 3. Repository lab

sw2 carries an SNMPv3 user (SHA-256 / AES-256) and no community; `test/lab/netmapper.yaml` has the
`ro-snmp-v3` set (research R9).

```sh
export LAB_SNMP_COMMUNITY=public LAB_SSH_PASSWORD=admin
export LAB_SNMP_V3='{"auth":"...","priv":"..."}'   # values in test/lab/sw2.cfg
netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds
```

Expected: sw2's identity observation over SNMP is `collected` by `ro-snmp-v3`, and sw2 has a
hostname in `/v1/devices`; sw1, sw3, sw4 and the unused address keep their outcomes. Then remove
`priv` from `LAB_SNMP_V3`, restart the collector and run again: the collector logs
`credential set secret has no priv field` for `ro-snmp-v3` on each target, no v3 request reaches sw2
(only the v2c probe, which times out), and sw2 is identified over SSH instead. sw3, which refuses SSH,
ends `credential_partial: rejected ro-ssh; unresolved ro-snmp-v3 (missing priv)`, and the unused
address `credential_unresolved: ro-snmp-v3 (missing priv)`.

## 4. External lab (SC-001)

In the trial setup used before this feature (SSH set only), add:

```yaml
  - { name: lab-snmp, kind: snmp_v3, username: snmp-ro, auth_protocol: sha256, priv_protocol: aes,
      secret_ref: env:LAB_SNMP_V3, max_attempts_per_device: 1, perimeters: [lab] }
```

with `LAB_SNMP_V3='{"auth":"evpnlab-auth","priv":"evpnlab-priv"}'` in the collector's environment.

Expected: every reachable device is identified over SNMP and every device in `/v1/devices` has a
hostname. With `auth_protocol: sha` instead, the SNMP attempts are `denied` (evidence: `incoming packet is not authentic`, research R6),
and the SSH set still maps the lab.

## Results

Recorded 2026-09-28.

- Section 1: `go test -count=1 ./...` passes with the three `NETMAPPER_TEST_*` variables, no test
  skipped. `gofmt` and `go vet` clean.
- Section 3, containerlab 0.79.0 from the devcontainer image on OrbStack, cEOS 4.36.0F: first run,
  sw2 `collected` over SNMP after the v2c probe timed out, hostname `sw2` in `/v1/devices`; sw1 and
  sw4 over v2c, sw3 `denied`, 172.20.20.9 `unreachable`, as before. Second run without `priv`: as
  expected above. Found on the way: EOS refuses a v3 user in the startup-config while no engineID
  exists (`show startup-config errors`: "Cannot create SNMPv3 user when no engineID is set"), so
  sw2.cfg pins `snmp-server engineID local`.
- Section 4 (SC-001), arista-evpn-vxlan-clab, one seed, sets `lab-ssh` then `lab-snmp`
  (`sha256`/`aes`): 28 devices, 28 with a hostname, 28 identity observations collected over SNMP,
  snapshot `published`.
- Section 4, same crawl with `auth_protocol: sha`: 28 `snmp.get` rows with result `auth_failed` in
  the audit log, SSH mapped the lab, no device has a hostname. No `denied` observation, which is
  correct: SSH worked, and `denied` needs every set refused. One device came out
  `unsupported`/`unknown_platform` through the SSH fingerprint, issue #15, unrelated to this feature.
