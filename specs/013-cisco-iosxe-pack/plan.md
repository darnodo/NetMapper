# Implementation Plan: Cisco IOS-XE platform pack

**Branch**: `013-cisco-iosxe-pack` | **Date**: 2026-10-21 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/013-cisco-iosxe-pack/spec.md`

## Summary

A new platform pack, `packs/cisco_iosxe`, with no Go change. It fingerprints Catalyst 9000
switches over SNMP by sysObjectID (the 9300 and 9500 models of CISCO-PRODUCTS-MIB and the Cat9kv,
`1.3.6.1.4.1.9.1.3160`) and over SSH by `show version` (research R1). It claims the serial and
the base MAC as strong identifiers and sysName as a weak one. The LLDP chassis ID the Catalyst
advertises is that base MAC, so an Arista's LLDP row resolves to the Catalyst by identifier (R2).
Four recipes:

- `neighbours`: `show lldp neighbors detail` (R5);
- `interfaces`: `show interfaces`, with the speed read from `1000Mb/s` (R7);
- `vlans`: `show vlan brief`, minus VLANs 1002 to 1005 (R9);
- `interface_vlans`: `show interfaces switchport` merged with `show interfaces trunk` (R10).

The interface name rules map every IOS-XE abbreviation to the long form (R8). The other nine
families have no recipe and are `unsupported` with `no_recipe` (spec FR-007).

IOS-XE has no per-command pager switch. Paging is turned off by the SSH pty height, set in the
pack's `scrapli.yaml` (`transport-pty-height: 32767`): IOS-XE takes its terminal length from the
pty request (recorded, R3). Nothing is sent on open, and there is no `enable`.

A new lab, `test/lab-iosxe/`: `eos-a` to `cat1` over a port-channel trunk, and `cat1` to `eos-b`
over an access port, on its own management subnet. The Catalyst runs in management passthrough
mode so it advertises its real address in LLDP (R6, R11).

Planning checks on a throwaway probe on NetLab (Cat9kv 17.15.01 and cEOS 4.36.0F, 2026-10-21,
deployed twice, then destroyed): R1 to R11. What only the full lab can show is marked "to confirm when recording" in research and
is settled in implementation step 1.

**Spec amendments from research, to approve before `/speckit-tasks`**:

- Edge case "Privilege level" and clarification 1: a command refused at privilege 1 makes its
  family `parse_failed`, not `denied`. IOS-XE answers a refused command with `% Invalid input
  detected`, the same as an unknown command, and the collector gives `denied` only when a session
  fails to open (R4). Changing that would be a collector change for one wording.
- FR-013, the "Catalyst still booting" edge case and SC-005: the boot time measured on NetLab is
  12 to 16 minutes (about 12 min 30 s to `Startup complete` on both deploys, up to 3 more before
  the data ports come up), not 5 to 10. SC-005 becomes 25 minutes (R11).

## Technical Context

**Language/Version**: Go 1.27 (no Go change in this feature).

**Primary Dependencies**: existing only: `gotextfsm`, scrapligo (platform file options),
`go.yaml.in/yaml/v3`. Templates adapted from ntc-templates (Apache 2.0), recorded in a `NOTICE`
as for `arista_eos`.

**Storage**: none new. No migration.

**Testing**: `go test ./...`. `TestPacks` (every `.raw` against its template),
`TestFamiliesFromLab` (`<switch>_<family>.facts.yml` for `cat1`), `TestNoSecretInRecordedOutput`,
all picking up the new pack by glob. New tests in `packs/packs_test.go`: interface name rules
(R8), legacy VLANs left out (R9), a refused command giving `parse_failed` (R4), each `show
version` matching only its own pack (R1), LLDP off giving `empty` (R5).

**Target Platform**: collector against Catalyst 9300 and 9500 on IOS-XE 17.15 or later; fixtures
from the Cat9kv 17.15.01 on NetLab.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. Five commands per Catalyst for the four families. `show
interfaces` is about 30 lines per port; one session reads up to 32767 lines without paging (R3).

**Constraints**: tests pass with no lab up. NetLab is needed to record fixtures and for the live
crawl. Each Cat9kv deploy costs 12 to 16 minutes of boot and takes 18 GB of RAM.

**Scale/Scope**: one pack, four recipes, five templates, one lab of three nodes.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | no | Rows go through the existing observation and facts endpoints, with evidence and age. |
| II. Observations immutable, rest rebuildable | yes | New recipes write observations like any pack; every template is checked by replay over recorded outputs (`TestPacks`, `TestFamiliesFromLab`). |
| III. Credentials stay in the collector | yes | No new credential kind or grant. The lab reuses the `test/lab` credential sets, as references in `netmapper.yaml`. No recipe prints a secret (R13). |
| IV. Read only, outward | yes | `read_only: ["show "]`; no on-open command, no `enable` (R3). The device account is privilege 1, unable to enter configuration mode (FR-016). |
| V. Vendor specifics are data | yes | Fingerprint, identifiers, naming rules, templates, value words and the pty height are all in `packs/cisco_iosxe`. No vendor name added outside `packs/` (R12). |

Workflow rules: the README (supported platforms), `docs/how-to/write-a-pack.md` (paging by pty
height for a platform without a per-command pager, the copy-from list), and
`docs/how-to/deploy.md` (the IOS-XE device account at privilege 1) are updated in the same change
(FR-015). The fact families contract is unchanged. The new lab is documented in its topology
comment and in [quickstart.md](quickstart.md). Every template change is checked by replay over
recorded outputs.

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/013-cisco-iosxe-pack/
├── plan.md              # this file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   └── cisco-iosxe-pack.md   # commands, families and field mapping of the pack
└── tasks.md             # /speckit-tasks
```

### Source Code (repository root)

```text
packs/cisco_iosxe/
├── pack.yaml                 # fingerprint, identifiers, interface_names
├── scrapli.yaml              # exec only, no on-open, transport-pty-height
├── README.md                 # recorded release, tested platforms, known gaps (FR-014)
├── NOTICE                    # ntc-templates origin and changes
├── recipes/
│   ├── neighbours.yaml
│   ├── interfaces.yaml
│   ├── vlans.yaml
│   └── interface_vlans.yaml
├── templates/
│   ├── show_lldp_neighbors_detail.textfsm
│   ├── show_interfaces.textfsm
│   ├── show_vlan_brief.textfsm
│   ├── show_interfaces_switchport.textfsm
│   └── show_interfaces_trunk.textfsm
└── testdata/
    ├── lab/                  # cat1_*.raw, cat1_<family>.facts.yml, variants
    └── ntc/                  # ntc-templates samples and licence
packs/packs_test.go           # the IOS-XE specific tests listed above

test/lab-iosxe/
├── iosxe.clab.yaml           # eos-a, cat1, eos-b
├── eos-a.cfg
├── cat1.cfg
├── eos-b.cfg
└── netmapper.yaml            # perimeter 172.20.22.0/24, seed eos-a

README.md                     # supported platforms
docs/how-to/write-a-pack.md   # paging by pty height
docs/how-to/deploy.md         # IOS-XE device account
```

**Structure Decision**: the pack follows `packs/arista_eos`. The lab gets its own directory
rather than more nodes in `test/lab`, so the existing lab's addresses, names and quickstarts are
unchanged and the Catalyst's boot time is not added to every `test/lab` deploy.

## Implementation order

1. Deploy `test/lab-iosxe` on NetLab and wait for readiness (R11). Record every command of the
   contract from `cat1` at privilege 1, plus the variants research marks "to confirm": LLDP off,
   a port-channel member, a wrapped allowed list, a down trunk, a refused command. Record the
   Arista side's LLDP view of `cat1`.
2. Pack skeleton: `pack.yaml`, `scrapli.yaml`, naming rules and their test, fingerprint test.
3. One recipe at a time, with its template, recordings and `facts.yml`: `neighbours`,
   `interfaces`, `vlans`, `interface_vlans`.
4. Live crawl from `eos-a` with the branch build (quickstart section 4), compare with the facts.
5. Documentation (FR-013 to FR-015), pack README with the known gaps.

## Complexity Tracking

No violation to justify.
