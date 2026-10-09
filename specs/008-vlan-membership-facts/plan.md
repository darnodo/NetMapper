# Implementation Plan: VLANs and interface VLAN membership on Arista EOS

**Branch**: `008-vlan-membership-facts` | **Date**: 2026-10-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/008-vlan-membership-facts/spec.md`

## Summary

Two new platform-neutral fact families, `vlans` and `interface_vlans`, in `internal/fact`, filled on
Arista EOS by one recipe each. `vlans` reads `show vlan`, which leaves out internal VLANs on its own
(research R1). `interface_vlans` merges three steps on the interface: `show interfaces switchport`
for mode, access, native and allowed VLANs; `show interfaces trunk` for the active VLANs the device
reports; `show interfaces status` for port-channel membership (R2). One vendor-neutral schema
addition: a `VLANList` field attribute, which the parser normalises into sorted, merged ranges and
`fact.Validate` enforces (R4). `test/lab` gets three more sw1-sw2 links, VLANs, a trunk, a
port-channel, an access port and a routed port (R7). No API change: the per-family endpoint of 007
already serves any family.

Spec amendment from research, made in this change: "no VLAN allowed" on a trunk is an absent
`allowed_vlans` field, not a stored empty list (R4, FR-007).

The `NetLab` VM was unreachable when this plan was written. Research items that depend on what EOS
prints are marked "to confirm when recording" and are settled in implementation step 1, before any
template is written.

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `gotextfsm` (templates), `go.yaml.in/yaml/v3` (packs). No
new dependency.

**Storage**: none new. Rows go to `observation.parsed`, outputs to the object store. No migration.

**Testing**: `go test ./...`; template fixtures in `packs/arista_eos/testdata/lab/` (`TestPacks`),
`TestFamiliesFromLab` over sw1, sw2 and sw4 recordings, an internal VLAN test, unit tests for the
normaliser, the `VLANList` check and the step 3 `empty_lines`.

**Target Platform**: Linux container (one binary, three roles); collector against Arista EOS 4.36+,
fixtures from cEOS 4.36.0F.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. Four more short `show` commands per EOS device.

**Constraints**: tests pass with no lab up; `NetLab` VM needed once to record fixtures and once for
the live crawl.

**Scale/Scope**: up to a few hundred rows per device (one per port and per VLAN); the VLAN lists stay
a few items per trunk thanks to ranges. 28-switch EVPN lab as the widest check.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | Both families are served by the existing facts endpoint, with observation, snapshot and collection time. No new answer. |
| II. Observations immutable, rest rebuildable | yes | Rows live in `observation.parsed`; VLAN list normalisation is in the parser, so a rule fix is a replay over stored outputs. |
| III. Credentials stay in the collector | no | No new credential, grant or secret path. VLAN data holds no secret. |
| IV. Read only, outward | yes | Only `show` commands. |
| V. Vendor specifics are data | yes | Commands, templates, `ALL`/`NONE`, `act/lshut`, `static access`, `in Po` are in `packs/arista_eos`. `VLANList` and its normaliser carry no vendor term. |

Workflow rules: the README, `docs/how-to/write-a-pack.md` and Bruno requests are updated for the new
families and the `VLANList` attribute (FR-018); the fact families contract of 001 is updated in the
same change (FR-012); the `test/lab` topology comment and this quickstart document the lab change
(FR-015). Every template change is checked by replay over the recorded outputs (`TestPacks`).

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/008-vlan-membership-facts/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── fact-families.md   # merged into specs/001-crawl-loop/contracts/fact-families.md
└── tasks.md               # /speckit-tasks
```

### Source Code (repository root)

```text
internal/fact/fact.go              # two families, Field.VLANList, its check in Validate
internal/pack/registry.go          # NormaliseVLANs (beside NormaliseMAC), missingCost entries
internal/parse/parse.go            # apply NormaliseVLANs to VLANList fields in mapRow
packs/arista_eos/recipes/          # vlans.yaml, interface_vlans.yaml
packs/arista_eos/templates/        # show_vlan, show_interfaces_switchport, show_interfaces_trunk,
                                   # show_interfaces_status_channel (.textfsm)
packs/arista_eos/testdata/lab/     # sw1_*, sw2_*, sw4_*_empty recordings, .yml,
                                   # <switch>_vlans.facts.yml, <switch>_interface_vlans.facts.yml,
                                   # sw2_show_vlan_internal_usage.raw (evidence only)
packs/packs_test.go                # internal VLAN test
test/lab/sw1.cfg, sw2.cfg          # VLANs, trunk, port-channel, access port, routed port
test/lab/two-switch.clab.yaml      # three links, topology comment
specs/001-crawl-loop/contracts/fact-families.md
bruno/facts/vlans.bru, bruno/facts/interface-vlans.bru
README.md, docs/how-to/write-a-pack.md
```

**Structure Decision**: the existing layout; no new Go file, everything extends files in place.

## Implementation order

1. `test/lab` configuration and topology (R7). On the `NetLab` VM: deploy, record every step
   command of both recipes on sw1, sw2 and sw4, and `show vlan internal usage` on sw2. Settle the
   "to confirm" items of R1 to R8 from the recordings and update `research.md`.
2. `internal/fact`: the two schemas and `VLANList`, with tests.
3. `internal/pack` and `internal/parse`: `NormaliseVLANs` and its use in `mapRow`, with tests.
4. Templates and recipes: `vlans`, then `interface_vlans` step by step, each with its `.yml`, then
   the `facts.yml` files and the internal VLAN test.
5. `missingCost`, contract, README, write-a-pack guide, Bruno requests.
6. Quickstart sections 4 and 5 on the VM.

Steps 2 to 5 need no lab. Step 1 must come first: three of the four templates depend on output not
yet seen.

## Complexity Tracking

No constitution violation to justify.
