# Implementation Plan: Project layer 2 domains from VLAN data

**Branch**: `009-l2domain-projection` | **Date**: 2026-10-12 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/009-l2domain-projection/spec.md`

## Summary

The graph projector of feature 004 gains one more pass, in the same transaction: after the `l1_link`
edges are built, it reads the `vlans` and `interface_vlans` families of 008, works out what each port
carries tagged and untagged, and joins `(device, local VLAN)` nodes over the cables whose two ends
carry the same VLAN the same way (research R3, R4). Each connected component is an `l2domain` row,
with its members and the cables behind it. A cable where one end's VLAN data is missing joins nothing
and is recorded against the domain as `unchecked`, which makes the domain `partial` (R5). Two ends
carrying different VLANs untagged join both VLANs into one domain and raise one `vlan_mismatch`
finding per pair of facing ports (R7).

One migration creates the three tables the data model already describes, with two evidence columns
on the member row and a uniqueness key that enforces "one declared VLAN, one domain" (R8). No pack
change, no new fact family, no API endpoint. Test fixtures run through fake devices with VLAN recipes
added to a temporary copy of the fakeos pack, so the shared test pack keeps proving `no_recipe` (R9).

Spec amendment from planning, made in this change: US2 scenario 2 now says the unchecked trunk is
listed against the domain as unchecked, rather than "not a link" (R5).

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `pgx/v5`, `goose` migrations. No new dependency; union-find
is two short functions (R4).

**Storage**: PostgreSQL. Migration `0011_l2domain.sql`: three tables, one column on `projection`, one
value on `finding_category_check`, grants.

**Testing**: `go test ./...`. New tests in `internal/graph` (crawls of fake devices, as in 004),
fixture pack data in `internal/graph/testdata/fakeos-l2/`. One map entry tested in `internal/api`.

**Target Platform**: Linux container, `engine` role and the `netmapper project` operator command.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. Work is linear in (declared VLANs × devices) plus (cables × VLANs
carried); a few thousand nodes on the 28-switch EVPN lab, in memory, inside the existing transaction.

**Constraints**: tests pass with no lab up; the projection stays one transaction (004 FR-018);
deterministic output whatever the read order (FR-011).

**Scale/Scope**: hundreds of domains per snapshot at most on the labs in use. No pagination or
batching beyond the per-domain batch the edge writer already uses.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | Each member carries its `vlans` and `interface_vlans` observations; each link points at an edge with its own evidence; a partial domain lists the unchecked cables that make it partial; the finding cites both ends' observations. Domains are not served yet, so no answer goes out without them. |
| II. Observations immutable, rest rebuildable | yes | Domains are computed from the collected zone and the entity set only, replaced with the projection, and recomputed by `netmapper project`. `projectorVersion` bumped to 2 so older sets can be found. |
| III. Credentials stay in the collector | no | The projector contacts no device and resolves no secret. `netmapper_api` gets no grant. |
| IV. Read only, outward | no | Nothing is written outside NetMapper's own tables. |
| V. Vendor specifics are data | yes | The domain rules read only the platform-neutral families; no vendor name in `internal/graph`. The fake VLAN recipes are test data of the fakeos pack. |

Workflow rules: the data model document, its finding categories and its roles paragraph are updated
in the same change (FR-017); the deploy guide's upgrade section says older snapshots need
`netmapper project` to get domains (R11). The tie rules (lowest key, lowest observation id per row,
label on disagreement) are each tested with inputs that tie (FR-011). The new tables are tested under
the engine role, which is the role that writes them, and under the operator role through
`netmapper project`.

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/009-l2domain-projection/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── l2domain.md
└── tasks.md               # /speckit-tasks
```

### Source Code (repository root)

```text
migrations/0011_l2domain.sql          # three tables, projection.l2domains, vlan_mismatch, grants
internal/graph/l2domain.go            # carry rules, union-find, domains, unchecked links, mismatches
internal/graph/graph.go               # family status read, call after buildLinks, Result counts,
                                      # projectorVersion 2
internal/graph/write.go               # clear and write the three tables and vlan_mismatch findings
internal/graph/l2domain_test.go       # FR-016 cases, determinism, rebuild
internal/graph/testdata/fakeos-l2/    # recipes/vlans.yaml, recipes/interface_vlans.yaml, templates
internal/api/findings.go              # "vlan_mismatch": "derived"
cmd/netmapper/project.go              # print domains and mismatches
docs/c4-model/04-data-model.md        # notes on the three tables, finding categories, roles
docs/how-to/deploy.md                 # upgrade note: re-project older snapshots
```

**Structure Decision**: the existing layout. One new Go file in `internal/graph` beside `links.go`
and `interfaces.go`, which is how 004 split the projector by what it builds.

## Implementation order

1. Migration `0011_l2domain.sql`, up and down, with the grants.
2. Fixture pack data and a test helper that crawls fake devices with it (R9). First test: two
   switches, one trunk, VLAN 10, expect one domain.
3. `l2domain.go`: status read and carry rules (R2, R3), then union-find and domains (R4), then
   unchecked links and confidence (R5), then label (R6), then mismatches (R7). Each with its FR-016
   test before moving on.
4. `write.go`: clear and write, `projection.l2domains`, findings. Reproducibility and wipe-and-
   recompute tests extended to domains (SC-004, SC-007).
5. API category map, `netmapper project` output, data model document, deploy guide.
6. Quickstart section 4 on the `NetLab` VM when it is up; record differences in research R10.

Steps 1 to 5 need no lab.

## Complexity Tracking

No constitution violation to justify.
