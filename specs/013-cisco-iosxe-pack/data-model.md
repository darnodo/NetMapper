# Data model: Cisco IOS-XE platform pack

No table, schema or fact family changes. A Catalyst's facts are rows in `observation.parsed` in
the families of [fact-families.md](../001-crawl-loop/contracts/fact-families.md), and its outputs
go to the object store, like an Arista's. What this feature adds is pack data and lab files.

## IOS-XE pack (`packs/cisco_iosxe`)

| Part | Holds | Rules |
| ---- | ----- | ----- |
| `pack.yaml` | name, version, `read_only`, fingerprint, identifiers, interface name rules | name equals the directory; version bumped on any template or recipe change |
| `scrapli.yaml` | the SSH session definition | one privilege level, no on-open or on-close command, pty height 32767 |
| `recipes/*.yaml` | one recipe per family: `neighbours`, `interfaces`, `vlans`, `interface_vlans` | every command starts with `show `; `versions: '>=17.15'` |
| `templates/*.textfsm` | five templates | compile in Go TextFSM (no lookbehind or lookahead) |
| `testdata/lab/` | recorded outputs and expected rows | see "Recorded fixture" |
| `README.md` | recorded release, tested platforms, known gaps | FR-014 list |
| `NOTICE` | what came from ntc-templates and what changed | as `packs/arista_eos/NOTICE` |

Identity of a Catalyst, as claims: `serial` and `chassis_mac` strong, `hostname` weak. The
`chassis_mac` value is the base MAC, which is also the LLDP chassis ID the Catalyst advertises, so
an Arista's neighbour row resolves to the Catalyst entity by that claim. A stack is one device,
identified by what `show version` prints; after a switchover both strong values may change and
the stack is then a new entity (known gap).

Canonical interface names: the long form IOS-XE prints in `show interfaces`
(`GigabitEthernet1/0/1`, `TwentyFiveGigE1/1/1`, `Port-channel1`). Every abbreviation maps to it.

## Mixed lab topology (`test/lab-iosxe`)

| Node | Kind and image | Management address | Role |
| ---- | -------------- | ------------------ | ---- |
| `eos-a` | `arista_ceos`, `ceos:4.36.0F` | 172.20.22.2 | seed |
| `cat1` | `cisco_cat9kv`, `vrnetlab/cisco_cat9kv:17.15.01`, management passthrough | 172.20.22.3 | the Catalyst |
| `eos-b` | `arista_ceos`, `ceos:4.36.0F` | 172.20.22.4 | reached only through `cat1` |

| Link | Purpose |
| ---- | ------- |
| `eos-a` eth1, eth2 to `cat1` Gi1/0/1, Gi1/0/2 | port-channel trunk, native VLAN and pruned allowed list |
| `cat1` Gi1/0/3 to `eos-b` eth1 | access port, VLAN 10 |

Readiness: vrnetlab's `Startup complete` in the `cat1` container log, then `cat1` in `eos-a`'s
`show lldp neighbors`.

## Recorded fixture

| File | Holds |
| ---- | ----- |
| `cat1_<template>.raw` | one command's output, as the collector stores it |
| `cat1_<template>.yml` | optional, the template's expected rows (ntc `parsed_sample` format) |
| `cat1<variant>_<template>.raw` | a variant (LLDP off, member port, wrapped list, down trunk) |
| `*_empty.raw` | an output that must give no row (refusal, nothing to report) |
| `cat1_<family>.facts.yml` | `status` and `rows` the whole recipe must give from `cat1`'s recordings |
