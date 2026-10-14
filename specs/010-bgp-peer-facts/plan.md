# Implementation Plan: BGP sessions on Arista EOS

**Branch**: `010-bgp-peer-facts` | **Date**: 2026-10-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/010-bgp-peer-facts/spec.md`

## Summary

One new platform-neutral fact family, `bgp_peers`, in `internal/fact`, filled on Arista EOS by one
recipe with four steps. `show bgp neighbors vrf all` gives one record per peer in every VRF: peer
address and AS, type, state, shutdown and dynamic markers, peer router ID, peer group, description,
local AS and router ID, and the negotiated families (research R1, R3, R4). The configured families
come from the three per-family summaries (`show ip bgp summary vrf all`, `show ipv6 bgp summary vrf
all`, `show bgp evpn summary`): a peer is listed in a summary exactly when it is activated for that
family, whatever its state (R2). No counter, timer or uptime is mapped (FR-007).

Three vendor-neutral schema attributes and one merge change in the parser (R5): `EmptyList` stores
`[]` instead of dropping an empty list, `OnlyWith` keeps a field only on rows where another field
holds a given value (the negotiated list and the peer router ID, only when `established`), `Set`
sorts and deduplicates a list, and a merge now adds up the items of a list field instead of keeping
the first. One pack format addition (R2): a recipe's `covers` key declares the families its
configured list covers, and the registry refuses a recipe whose steps do not produce exactly that
set. `empty` comes only from the exact `% BGP inactive` answer or an empty peer list; any other
error output is `parse_failed` (R8). A row that breaks a rule makes the observation `parse_failed`
with a finding, never a silent drop (R12). `test/lab` gets BGP on sw1
and sw2: a session with a password, a description and a capability mismatch, a wrong-AS peer, a
shut peer and a dynamic peer (R10). No API, engine or discovery change (R11).

Plan review (2026-10-14) added the declared coverage, the sorted lists, the exact empty match, the
check that the summaries span every VRF, and the no-silent-drop rule; the spec carries them as
FR-005c, FR-005d, FR-009 and FR-005a. Spec amendment from research, agreed at review: no
`update_source` field. The device does not
print the configured update source in its peer view, and the local TCP address it prints is
connection state (R6, FR-006).

R1, R2 and R8 were checked on the EVPN lab (cEOS 4.36.0F) on 2026-10-14. What only `test/lab` can
show is marked "to confirm when recording" and is settled in implementation step 1.

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `gotextfsm` (templates), `go.yaml.in/yaml/v3` (packs). No
new dependency.

**Storage**: none new. Rows go to `observation.parsed`, outputs to the object store. No migration.
An empty list is stored as JSON `[]`, distinct from an absent key.

**Testing**: `go test ./...`; template fixtures in `packs/arista_eos/testdata/lab/` (`TestPacks`),
`TestFamiliesFromLab` over sw1, sw2, sw4, dc-leaf1 and core1 recordings, unit tests for
`EmptyList`, `OnlyWith`, `Set` and the list merge in `internal/fact` and `internal/parse`, error
outputs (`% Authorization denied`, `% Invalid input`) giving `parse_failed`, a registry test for
`covers`, a graph test that `bgp_peers` rows change neither the graph nor the verdict.

**Target Platform**: Linux container (one binary, three roles); collector against Arista EOS 4.36+,
fixtures from cEOS 4.36.0F.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. Four more `show` commands per EOS device. `show bgp neighbors vrf
all` is about 120 lines per peer, so a few thousand lines on a large router; outputs are stored by
content hash and change on every run because of their counters (the rows do not).

**Constraints**: tests pass with no lab up; `NetLab` VM needed once to record fixtures and once for
the live crawl.

**Scale/Scope**: one row per peer per VRF, tens per device in the labs. The EVPN lab (28 switches,
core1 with a second VRF) is the widest check.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | Served by the existing facts endpoint with observation, snapshot and collection time. No new answer. |
| II. Observations immutable, rest rebuildable | yes | Rows live in `observation.parsed`. `EmptyList`, `OnlyWith`, `Set` and the list merge run in the parser, so a rule fix is a replay over stored outputs. |
| III. Credentials stay in the collector | yes | No new credential or grant. Recipes use commands that print no BGP password (R9); the secret check covers the lab password. |
| IV. Read only, outward | yes | Only `show` commands. |
| V. Vendor specifics are data | yes | Commands, templates, state words, `% BGP inactive`, family names as EOS prints them and the covered set are in `packs/arista_eos`. `EmptyList`, `OnlyWith`, `Set`, `covers` and the merge carry no vendor term. |

Workflow rules: the README, `docs/how-to/write-a-pack.md` (list merge, `covers`) and the
Bruno requests are updated (FR-017); the fact families contract of 001 is updated in the same change
(FR-011); the `test/lab` topology comment and this quickstart document the lab change (FR-014).
Every template change is checked by replay over the recorded outputs (`TestPacks`). The rule that
settles absent against `[]` is tested with inputs that hit each side: an established session with
no negotiated family, one that is not established, a peer with no family activated. The sort is
tested with steps in two orders that must give the same row, and the empty rule with outputs on
each side of it (`% BGP inactive` alone, the same line next to another one, a refusal).

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/010-bgp-peer-facts/
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
internal/fact/fact.go              # bgp_peers, Field.EmptyList, OnlyWith and Set, their checks
internal/parse/parse.go            # list merge, then the EmptyList, OnlyWith and Set pass
internal/pack/registry.go          # Impl.Covers and its load check, missingCost entry
internal/graph/                    # test: bgp_peers rows change neither graph nor verdict
packs/arista_eos/recipes/          # bgp_peers.yaml
packs/arista_eos/templates/        # show_bgp_neighbors_vrf_all, show_ip_bgp_summary_vrf_all,
                                   # show_ipv6_bgp_summary_vrf_all, show_bgp_evpn_summary (.textfsm)
packs/arista_eos/testdata/lab/     # sw1_*, sw2_*, sw4_*_empty, dc-leaf1_*, core1_* recordings, .yml,
                                   # <switch>_bgp_peers.facts.yml
packs/packs_test.go                # password 7 pattern in TestNoSecretInRecordedOutput
test/lab/sw1.cfg, sw2.cfg          # ip routing, SVI, loopbacks, router bgp
test/lab/two-switch.clab.yaml      # topology comment
specs/001-crawl-loop/contracts/fact-families.md
bruno/facts/bgp-peers.bru
README.md, docs/how-to/write-a-pack.md
```

**Structure Decision**: the existing layout; no new Go file, everything extends files in place.

## Implementation order

1. `test/lab` configuration (R10). On the `NetLab` VM: deploy, check the four sessions reach the
   intended states, record the four step commands on sw1, sw2 and sw4, and on dc-leaf1 and core1 of
   the EVPN lab. Settle the "to confirm" items of R1, R2, R3, R4, R7, R8 and R9 from the
   recordings and update `research.md`.
2. `internal/fact`: the `bgp_peers` schema, `EmptyList`, `OnlyWith`, `Set`, with tests.
3. `internal/parse`: the list merge and the pass after the merge, with tests that tie on each side
   of absent against `[]`, steps in two orders, and rule breaches giving `parse_failed`.
   `internal/pack`: `covers` and its load check.
4. Templates and recipe: the neighbours step, then the three summary steps, each with its `.yml`,
   then the `facts.yml` files, then the error outputs giving `parse_failed`.
5. Secret pattern, graph test, `missingCost`, contract, README, write-a-pack guide, Bruno request.
6. Quickstart sections 4 and 5 on the VM.

Steps 2 to 5 need no lab. Step 1 must come first: the templates are tested against its recordings.

## Complexity Tracking

No constitution violation to justify.
