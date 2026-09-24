# Implementation Plan: Identity resolution (claims into device entities)

**Branch**: `003-identity-resolution` | **Date**: 2026-09-24 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/003-identity-resolution/spec.md`

## Summary

Once a snapshot is closed, the engine reads its `identity` observations and their identifier claims,
groups them with union-find over shared strong identifiers, and writes one `entity` row per device with
the claims it was built from. A component that contradicts itself on a strong kind is not merged at all:
each claim group becomes its own entity and one `identity_conflict` finding names the collision. Each
group is then looked up in a small per-perimeter registry (`device`, `device_identifier`) that hands back
a stable device key, or mints one from the group's anchor identifier, so the same box carries the same
key in every run. Operator decisions (`merge`, `split`, `never_merge`) are append-only rows replayed on
every resolution, never applied in place. A second step in `jobrunner.Tick` sweeps closed snapshots with
no `resolution` row; `netmapper resolve <id>` is the only way to resolve one twice, and
`netmapper decide` records a decision. Details and trade-offs are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27.1, same toolchain and module as 001 and 002

**Primary Dependencies**: none added. pgx/v5 and goose/v3 are already in `go.mod`; this feature is SQL,
one in-memory graph algorithm and control flow. No transport, no parser, no pack, no object store access

**Storage**: PostgreSQL only. Five new tables (`device`, `device_identifier`, `resolution`, `entity`,
`entity_claim`), one append-only table (`entity_decision`), one widened `CHECK` on `finding.category`,
and the grants, which include a `DELETE` on `finding` for the replace rule. Garage is untouched: an entity cites observations, it stores no bytes

**Testing**: `go test`; integration tests against PostgreSQL from `deploy/compose.yaml`, building
snapshots with the fake transport from 001; the containerlab two-switch topology for the end-to-end
scenarios in [quickstart.md](quickstart.md)

**Target Platform**: Linux containers (amd64, arm64), unchanged

**Project Type**: single Go binary with role subcommands; this feature adds two operator subcommands
(`resolve`, `decide`) and one step to the engine's existing loop

**Performance Goals**: none measured (R14). One read of a snapshot's identity observations and claims, an
in-memory union-find over a few hundred devices, one write transaction, once per closed snapshot

**Constraints**: no device contacted and no secret resolved (FR-014); nothing in the collected zone
modified (FR-014, FR-020); no decision rewritten in place, enforced by withheld grants rather than by
convention (FR-009); the whole entity set replaced in one transaction under an advisory lock, so no
consumer ever reads a partial set (FR-015); that lock is per snapshot, so it does not serialize the
registry between two snapshots resolving at once, and a concurrent mint conflicts on
`device_identifier`'s primary key, fails that transaction and is retried on the next tick rather than
upserted over; resolution stays out of the snapshot-closing transaction, so a failing grouping can never
keep a job from finishing

**Scale/Scope**: six new tables, one migration, two new subcommands, one new package
(`internal/entity`), plus the resolving step in `internal/jobrunner`. Three decision kinds, two conflict
shapes

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle / constraint                               | Status | How this plan holds it                                                                                                                                                                                                                                                                                             |
| ---------------------------------------------------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| I. Evidence travels with the answer                  | Pass   | `entity_claim` is the first link of the chain entity → claim → observation → `observation_raw` → bytes, and every entity carries `first_seen`/`last_seen` from its own evidence (FR-005, FR-006). Conflict findings cite the observations behind each side. No interface serves an entity yet, so the response-level part of the principle is not in scope |
| II. Observations immutable, rest rebuildable         | Pass   | The collected zone is read only here. Entities and the registry are computed and replaceable; `resolver_version` records which grouping produced a set so a corrected one is a replay (R9). Decisions are append only and replayed rather than applied in place (FR-009), which is the principle's "no manual repair step" |
| III. Credentials and reach stay in the collector     | Pass   | Resolution runs in `engine` and in the operator CLI. Neither loads packs, resolves a `secret_ref` nor dials. No perimeter check is needed because no packet is sent                                                                                                                                                 |
| IV. Read only, outward                               | Pass   | No device contacted at all                                                                                                                                                                                                                                                                                         |
| V. Vendor specifics are data                         | Pass   | The resolver reads `kind`, `value` and `strength` off the claim and never interprets any of them. No vendor name and no kind priority list appears in the feature, which is why R4 rejects merging by a declared kind ordering                                                                                      |
| One binary, three roles                              | Pass   | No new long-running role: a step inside the existing `engine` loop, plus `resolve` and `decide` alongside `run`, `cancel`, `judge`, `migrate`                                                                                                                                                                       |
| Frontier in PostgreSQL, no crash state in memory     | Pass   | The sweep is a query over `snapshot` left-joined to `resolution`, so a killed engine leaves nothing behind and the next tick finds the same work (FR-016). The union-find lives only inside one call                                                                                                                |
| Raw output in object store by hash                   | Pass   | Not touched                                                                                                                                                                                                                                                                                                        |
| Status enum closed and non-null                      | Pass   | The six observation statuses are unchanged and read only. The new closed enums (`entity.kind`, `entity_decision.kind`) are `NOT NULL` with a `CHECK`, and `finding.category` gains exactly one value                                                                                                               |
| Snapshots immutable once closed                      | Pass   | The `snapshot_closed_is_final` trigger from 001 stays; the entity set lives in its own tables precisely so the snapshot row is never touched (R8)                                                                                                                                                                  |
| Config YAML posted whole, versioned, recorded on run | Pass   | No configuration change. A device key is scoped to the perimeter **name**, the identity 002 established across config versions (002, R1)                                                                                                                                                                           |
| Workflow: open questions go to `docs/`               | Action | The deltas below must be carried into `docs/c4-model/04-data-model.md` in this branch, including closing the "per snapshot or validity interval" question it records as open                                                                                                                                        |

Post-design re-check, second pass after the 2026-09-24 clarifications: still passes. Phase 1 added the
registry, the `resolution` marker and the decision table. The registry is the one addition worth re-reading against Principle II, because it is
cross-snapshot state: it stays rebuildable because replaying the snapshots in closing order mints the
same keys from the same claims (R5, SC-008), and the quickstart makes that a test rather than a claim.

The clarifications added one thing worth re-reading against the constitution: the engine now holds
`DELETE` on `finding` and `finding_evidence`, so that a re-resolution replaces its own collision findings
(FR-015). That is the first delete any role but the owner may perform outside the computed zone. It stays
inside the rules because the reported zone is not the collected zone: 001's grant matrix withholds
`UPDATE` and `DELETE` on observations, claims, raw output and `audit_log`, and nothing here touches those.
The delete is bounded in code to `category = 'identity_conflict'` rows of the snapshot being resolved, and
the quickstart asserts that the collector's own findings survive a re-resolution. A narrower guard, a
`SECURITY DEFINER` function taking only a snapshot id, was considered and left out: it would be the fourth
such function for a delete that already cannot reach anything an operator would miss.

Third pass, after the 2026-09-24 analysis: still passes, with one placement made explicit. The collision
findings are deleted and raised inside the same transaction that rewrites the entity set, so a report and
the set it describes are never separately visible and a failed resolution leaves neither. The grouping and
the registry return their conflicts rather than writing them, which keeps every write in one file and one
transaction. Two limits are now recorded rather than implied: resolution cannot separate two devices whose
strong identifiers are identical in every kind, because that evidence is the same as one device answering
on two addresses (R4, settled at crawl time by 001's live deduplication); and FR-018's retrieval is SQL
until an interface exists, which is where Principle I's response contract will be enforced.

Principles touched by this feature: I, II.

## Documentation deltas to carry into `docs/c4-model/04-data-model.md`

- New computed-zone tables `device`, `device_identifier`, `resolution`, `entity`, `entity_claim`, and the
  append-only `entity_decision`. The doc already lists `entity`, `entity_claim` and `entity_decision` as
  planned; their columns here are what actually ships, and `interface`/`interface_alias` stay unbuilt.
- Close the open question "whether `entity` and `edge` carry a row per snapshot or a validity interval
  across snapshots" for entities: a row per snapshot, with a stable key held in a registry. Edges are
  still open and inherit nothing from this decision.
- `entity_decision.subjects` holds device keys, not row ids, and `split` carries the identifier it
  detaches. That is what makes a decision survive a recomputation.
- `finding.category` gains `identity_conflict`.
- The engine's grant set gains the computed-zone tables and, for the first time, `INSERT` and `DELETE` on
  `finding` and `finding_evidence`: until now only the collector raised findings, and none were ever
  removed. The delete exists because a collision finding is part of a resolution's output and is replaced
  with it; it is restricted in code to `identity_conflict` rows of the snapshot being resolved.
- Note for the gate: 002's `compare` matches devices across snapshots on a shared strong claim, pairwise
  and without transitive closure. Once entities carry a device key, that query has a simpler and more
  correct source. Changing it is a separate change with its own `gate_version` bump, not part of this
  feature.

## Project Structure

### Documentation (this feature)

```text
specs/003-identity-resolution/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── cli.md
├── checklists/
│   └── requirements.md
└── tasks.md              # /speckit-tasks
```

### Source Code (repository root)

```text
cmd/netmapper/
├── main.go                   # + resolve and decide subcommand dispatch
├── resolve.go                # netmapper resolve <snapshot-id>
└── decide.go                 # netmapper decide merge|split|never-merge

internal/
├── entity/                   # the feature: grouping, registry, decisions
│   ├── entity.go             # Resolve(ctx, db, snapshotID): the whole entity set, one transaction
│   ├── group.go              # claim groups, union-find, contradiction detection (R2, R3, R4)
│   ├── registry.go           # device keys: lookup, mint, the two-device case (R5, R6)
│   ├── decisions.go          # read, order, apply merge/split/never_merge (R7, FR-011)
│   └── write.go              # the replace-in-one-transaction path and the advisory lock (R8)
├── jobrunner/                # + resolveStep in Tick, sweep query (R1)
├── store/                    # unchanged; RaiseFinding already does what R11 needs
└── gate/                     # unchanged

migrations/0006_entity.sql    # six tables, the finding CHECK, grants
```

**Structure Decision**: one new package, `internal/entity`, holding everything that decides what a device
is; `internal/jobrunner` only calls `Resolve`, exactly as it only calls `gate.Judge`. That boundary is
what lets the sweep, the two subcommands and the tests all go through one function, which is what makes
FR-013 (same inputs, same grouping) testable rather than aspirational. Splitting the package into five
files is not architecture, it is four topics that each fit on a screen: the grouping, the registry, the
decisions and the write.

A note on vocabulary: what spec.md calls a claim set is what this plan, the research and the code call a
claim group, `ClaimGroup` in `internal/entity`. The same thing read at two altitudes.

## Known open points

| Point                                                              | Why it is open                                                                                                                                             | Closed when                                                                                       |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| Entities are readable only through SQL                             | No interface serves them yet; the spec puts presentation out of scope and `api` does not exist as a role                                                   | The API feature ships and reads entities with their evidence                                      |
| The registry accumulates identifiers forever                       | Nothing evicts `device` or `device_identifier`, and nothing needs to at homelab scale: a few hundred rows that outlive the snapshots they were minted from | Eviction becomes a feature, at which point a device with no surviving snapshot is a candidate     |
| A weakly identified device keys on the address it answered on      | Clarified on 2026-09-24: a hostname is not unique, so the address is the only handle left. A renumbered weak device reads as a replacement                    | A pack yields a strong identifier for such a platform, or the case is settled once with a merge decision |
| A device that loses every strong identifier between runs           | It mints a new key and reads as a replacement, which is FR-023 working as decided. In a lab where SNMP sometimes returns nothing, this may prove noisy     | The lab runs long enough to say whether it happens; the answer is either a pack fix or a decision |

## Complexity Tracking

| Addition                                                    | Why needed                                                                                                                                                | Simpler alternative rejected because                                                                                                                                                       |
| ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A cross-snapshot registry (`device`, `device_identifier`)   | A key that means the same thing in two runs is the whole of the clarification answered on 2026-09-24, and it is what keeps the diff from regrouping claims | Hashing the identifier set per snapshot changes the key the first time a run reads a chassis MAC but not a serial, which is the common SNMP case, and it would silently break the diff (R5) |
| Union-find in Go rather than one SQL statement              | The grouping has to consult decisions and detect contradictions while it links, and it is transitive                                                      | A recursive CTE computes components but cannot apply a decision mid-flight, and the result is a query nobody can debug six months later (R3)                                               |
| Three decision kinds instead of one                         | `merge`, `split` and `never_merge` are three different things an operator can know, and each replays differently                                          | One `override` kind with a free-form payload gives one table, three meanings and no constraint the database can check (R7)                                                                 |
| Refusing to merge a whole contradicting component           | A wrong merge is invisible and corrupts every edge built on it; an over-split is visible and one decision fixes it                                        | Cutting the minimum set of links to make a component consistent has no unique answer, so reproducibility would rest on a tie-break nobody can predict (R4)                                  |
