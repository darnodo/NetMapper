# Implementation Plan: Graph projector (interfaces and edges)

**Branch**: `004-graph-projector` | **Date**: 2026-09-25 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/004-graph-projector/spec.md`

## Summary

Once a closed snapshot carries an entity set, the engine reads its `interfaces` and `neighbours`
observations, attaches them to entities by task lineage, and writes one `interface` row per port with
every spelling ever seen recorded against it. It then pairs the neighbour reports: two devices that
each name the other's port produce one `l1_link` marked `both_ends`, one that reports alone produces
one marked `one_end` carrying whatever it said about the far end, and two that contradict each other
produce no link and one `link_disagreement` finding. Each address an entity answered on becomes a
`has_address` edge. Nothing is minted: an edge is named by its endpoints, `type:from_ref|to_ref`, a
generated column whose uniqueness is what makes one cable one row. A third step in `jobrunner.Tick`
sweeps closed snapshots that carry a `resolution` row and no current `projection`;
`netmapper project <id>` is the only way to redo one whose inputs are unchanged. The engine gains a
`--packs` flag, because canonicalising the port a neighbour reports for the far end needs the far
end's naming rules and nothing before this point knows both. Details and trade-offs are in
[research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27.1, same toolchain and module as 001, 002 and 003

**Primary Dependencies**: none added. pgx/v5 and goose/v3 are already in `go.mod`. This feature is SQL,
string handling and control flow, plus one existing in-process call, `pack.Registry.Normalise`. No
transport, no secret backend, no object store access

**Storage**: PostgreSQL only. Six new tables (`projection`, `interface`, `interface_alias`,
`interface_evidence`, `edge`, `edge_evidence`), one widened `CHECK` on `finding.category`, and the
grants. No cross-snapshot table, unlike 003. Garage is untouched: an edge cites observations, it
stores no bytes

**Testing**: `go test`; integration tests against PostgreSQL from `deploy/compose.yaml`, building
snapshots with the fake transport from 001; the containerlab two-switch topology for the end-to-end
scenarios in [quickstart.md](quickstart.md). The `fakeos` test pack gains a `remote_interface` field
and a naming rule the far-end spelling exercises, which is pack data, not code

**Target Platform**: Linux containers (amd64, arm64), unchanged

**Project Type**: single Go binary with role subcommands; this feature adds one operator subcommand
(`project`), one step to the engine's existing loop, and a `--packs` flag to the engine

**Performance Goals**: none measured (R17). One read of a snapshot's interfaces and neighbours, an
in-memory pairing over a few hundred devices, one write transaction, once per projected snapshot

**Constraints**: no device contacted and no secret resolved (FR-020); nothing in the collected zone
modified, and no entity, registry row or operator decision written (FR-020); only the active parse
generation read (FR-019); the whole set replaced in one transaction under an advisory lock on the
snapshot, so no consumer ever reads a partial one (FR-018); projection stays out of both the
snapshot-closing transaction and the resolution transaction, so a failing projection can never keep a
job from finishing or roll back a good entity set

**Scale/Scope**: six new tables, one migration, one new subcommand, one new package
(`internal/graph`), plus the projecting step in `internal/jobrunner`. Two edge types, three confidence
values, one new finding category

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle / constraint                               | Status | How this plan holds it |
| ---------------------------------------------------- | ------ | ---------------------- |
| I. Evidence travels with the answer                  | Pass   | `interface_evidence` and `edge_evidence` are the first link of the chain to `observation`, `observation_raw` and the bytes, and every row carries `first_seen`/`last_seen` from its own evidence (FR-007, FR-008). `edge_evidence.side` is what makes "both ends agreed" readable from the evidence and not only from a column. No interface serves any of it yet, so the response-level part of the principle is not in scope |
| II. Observations immutable, rest rebuildable         | Pass   | The collected zone is read only here, and so is the entity set. Everything written is replaceable in one transaction; `projector_version` records which projector produced a set so a corrected one is a replay. Nothing about an edge is stored that cannot be recomputed from the observations and the entity set (FR-023, SC-008) |
| III. Credentials and reach stay in the collector     | Pass   | Projection runs in `engine` and in the operator CLI. Neither resolves a `secret_ref` nor dials. Reading the packs is new here and stays inside the principle: a pack is data, holds no secret, and loading it opens nothing. No perimeter check is needed because no packet is sent |
| IV. Read only, outward                               | Pass   | No device contacted at all |
| V. Vendor specifics are data                         | Pass   | This is the principle the feature leans on hardest. FR-003 forbids deciding per vendor what a port is called, and the answer is `pack.Registry.Normalise` with the far end's platform, the same call the parser already makes for the near end. No vendor name and no naming heuristic appears anywhere in `internal/graph`, which is why R3 rejects matching spellings with a general rule |
| One binary, three roles                              | Pass   | No new long-running role: a step inside the existing `engine` loop, plus `project` alongside `run`, `cancel`, `judge`, `resolve`, `decide`, `migrate` |
| Frontier in PostgreSQL, no crash state in memory     | Pass   | The sweep is a query over `snapshot` joined to `resolution` and left-joined to `projection`, so a killed engine leaves nothing behind and the next tick finds the same work (FR-021). The pairing lives only inside one call |
| Raw output in object store by hash                   | Pass   | Not touched. Edges cite observations, which cite hashes |
| Status enum closed and non-null                      | Pass   | The six observation statuses are unchanged and read only. The new closed enums (`edge.type`, `edge.confidence`, `interface.source`, `interface_alias.source`, `edge_evidence.side`) are `NOT NULL` with a `CHECK`, and `finding.category` gains exactly one value |
| Snapshots immutable once closed                      | Pass   | The `snapshot_closed_is_final` trigger from 001 stays; the projection lives in its own tables precisely so the snapshot row is never touched |
| Config YAML posted whole, versioned, recorded on run | Pass   | No configuration change. The `--packs` flag is a process argument, like the collector's, not a configuration key |
| Workflow: a path is tested under its own role        | Action | `netmapper project` is tested on a connection as `netmapper_operator` and the sweep as `netmapper_engine`. This rule exists because of the defect 003 shipped, so it is a named test in [quickstart.md](quickstart.md), not an intention |
| Workflow: a tie-break is tested with inputs that tie | Action | Two of them: two endpoint references that are equal, and one spelling arriving from two observations. Each is a named test. A remote identifier matching two entities is deliberately not a tie-break: the 2026-09-25 clarification settles it by refusing to attach |
| Workflow: reference documents corrected in the same change | Action | The deltas below must be carried into `docs/c4-model/04-data-model.md` in this branch, including closing the "per snapshot or validity interval" question it still records as open for edges, and correcting the `engine` comment in `cmd/netmapper/engine.go` that says it never reads packs |

Post-design re-check: still passes. Phase 1 added the endpoint reference grammar, the generated
`edge.name` and the `projection` marker. Two things are worth re-reading against the constitution.

The first is the engine reading packs. Principle III is about credentials and reach, and a pack is
neither: it is a directory of read-only commands, regexes and naming rules, already loaded in the same
binary by the collector role. The alternative would be a vendor-agnostic guess at vendor naming inside
`internal/graph`, which is precisely the leak Principle V exists to prevent, so the two principles do
not pull in opposite directions here. What does change is a stated property of the engine, and the
comment asserting it is corrected in this branch rather than left to drift.

The second is the sweep re-projecting a snapshot whose entity set was replaced. FR-022 says the system
must not decide on its own to redo a projection it has already done, and this looks like it does. It
does not: `interface` cascades from `entity`, so a re-resolution has already destroyed the projection,
and the `projection` row that survives is a claim about a set that no longer exists. Re-projecting is
the self-repair FR-021 asks for, on inputs that changed. Redoing a projection whose inputs are
unchanged still needs `netmapper project`, and `projection.resolution_at` is what draws that line in
the schema rather than in a comment.

One limit is recorded rather than implied: FR-024's retrieval is SQL until an interface exists, which
is the same limit 003 recorded for entities and the same place Principle I's response contract will be
enforced.

Principles touched by this feature: I, II, V.

## Documentation deltas to carry into `docs/c4-model/04-data-model.md`

- New computed-zone tables `projection`, `interface_alias`, `interface_evidence` and `edge_evidence`,
  and the columns that actually ship for `interface`, `edge` and `edge_evidence`, which the doc lists
  as planned. `interface.if_index`, `interface.kind` and `interface.parent_interface_id` are not built:
  no recipe collects an interface index and nothing yet describes a subinterface, so they would be
  three always-null columns. `edge.from_ref`/`to_ref` ship as documented, with a grammar, and gain the
  nullable entity and interface columns that make them joinable.
- Close the open question "whether `edge` carries a row per snapshot or a validity interval across
  snapshots": a row per snapshot, named by its endpoints, with no registry. That is the last of the
  two halves of that question; entities were settled by 003.
- Of the edge types the doc lists, only `l1_link` and `has_address` are built. `attached` needs
  forwarding or address tables and `protocol_adjacency` needs routing protocol state; neither fact
  family is collected, so neither type has a data source to build from.
- `finding.category` gains `link_disagreement`, raised and replaced with the projection that describes
  it, restricted in code to that category and that snapshot.
- The engine's grant set gains the six projected tables. The engine now also loads platform packs,
  which the container list and the component description both state it does not.
- `entity.kind` keeps its `CHECK (kind = 'device')`. The doc says the projector's kinds are a
  migration rather than a silent widening; this feature needs none, and an unresolved far end is a
  reference and some attributes, not an entity.

## Project Structure

### Documentation (this feature)

```text
specs/004-graph-projector/
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
├── main.go                   # + project subcommand dispatch
├── engine.go                 # + --packs, loaded at startup and passed to the runner
└── project.go                # netmapper project <snapshot-id>

internal/
├── graph/                    # the feature: interfaces, links, addresses
│   ├── graph.go              # Project(ctx, db, reg, snapshotID): the whole set, one transaction;
│   │                         #   reads the entity set and its task lineage; has_address edges (R2, R11)
│   ├── interfaces.go         # interfaces and aliases from both families, canonical names (R3, R4, R5)
│   ├── links.go              # endpoint references, pairing, orientation, disagreements (R6, R8, R9)
│   └── write.go              # the replace-in-one-transaction path and the advisory lock (R12)
├── jobrunner/                # + projectStep in Tick, sweep query (R1, R12)
├── pack/                     # unchanged; Normalise already does what R3 needs
└── entity/                   # unchanged; it does not know the projector exists

migrations/0007_graph.sql     # six tables, the finding CHECK, grants

internal/pack/testdata/fakeos/  # + remote_interface in the neighbours recipe and template,
                                #   + a naming rule the far-end spelling exercises
```

**Structure Decision**: one new package, `internal/graph`, holding everything that decides what a port
and a link are; `internal/jobrunner` only calls `Project`, exactly as it calls `gate.Judge` and
`entity.Resolve`. That boundary is what lets the sweep, the subcommand and the tests all go through one
function, which is what makes FR-017 testable rather than aspirational. `internal/entity` is untouched
and does not learn that a projector exists: the dependency runs one way, through the `resolution` row
the sweep reads.

Four files rather than one for the same reason 003 used five: they are four topics that each fit on a
screen. The one judgement call is putting `has_address` in `graph.go` instead of a fifth file; it is a
dozen lines projecting a value resolution already computed, and it does not deserve a file of its own.

A note on vocabulary: what spec.md calls a link is an `edge` of type `l1_link` in the schema and in the
code. The spec says link because an engineer says cable; the schema says edge because the table holds
more than one kind.

## Known open points

| Point                                                              | Why it is open                                                                                                                                                                | Closed when                                                                                          |
| ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| Interfaces and edges are readable only through SQL                 | No interface serves them yet; the spec puts presentation out of scope and `api` does not exist as a role                                                                       | The API feature ships and reads them with their evidence                                             |
| A new active parse generation does not re-trigger projection alone | `projection.resolution_at` tracks the entity set, not the generation. Nothing flips `active` today, and when something does it will have to re-resolve first anyway           | A re-parse path exists; either it re-resolves, or `projection` tracks the generation too             |
| An interface carries no index, kind or parent                      | No recipe collects an interface index, and nothing describes a subinterface, so the columns would always be null                                                               | A pack yields them, which is a pack change plus three columns                                        |
| Two ports of one device cabled to each other                       | Produces one edge with two `if:` references of the same device, which is correct and untested against real hardware                                                            | A lab topology has a loopback cable, or the case is dismissed                                        |
| An `l1_link` on a shared medium                                    | Produces one edge per pair of reporting ports, which is what the evidence says and may read as noise on a hub or a lab bridge                                                  | The lab runs long enough to say whether it happens, at which point it is a query, not a rule          |
| The far-end spelling of an unresolved device                       | Cannot be canonicalised, because nothing knows its platform, so the reference carries the raw spelling and two spellings of one unmanaged port would read as two ports         | That device is reached and resolved, which is the only thing that can settle it                      |

## Complexity Tracking

| Addition                                              | Why needed                                                                                                                                          | Simpler alternative rejected because                                                                                                                       |
| ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| The engine loads platform packs                       | FR-003: the far end's canonical name comes from the far end's pack, and the projector is the first component that knows both the spelling and the platform | Any vendor-neutral spelling rule is a vendor-specific decision written outside a pack, which Principle V exists to reject (R3)                              |
| Four endpoint reference forms instead of two ids      | An edge connects a port to a port, a device to an address, or a port to something no entity accounts for. Two nullable ids cannot say the third        | Minting an entity for every unresolved far end puts devices the crawl never reached into the entity set and into the coverage gate's counts (R14)            |
| A generated `name` column on top of the two references | FR-015 wants a name a consumer can follow across runs, and generating it means it cannot drift from the columns it is built from                      | Computing it in Go gives a column that is right until one code path forgets, and FR-009's "one link, not two" would then rest on that path rather than on a key |
| `projection.resolution_at` rather than a bare marker  | A re-resolution cascades the interfaces away; without it the `projection` row claims a set that is gone                                              | Having `entity.Resolve` delete the row makes identity resolution write the projector's tables, coupling two packages that otherwise share nothing (R12)      |
| A `COLLATE "C"` on the ordering constraint            | Go compares strings by byte and PostgreSQL does not; a reference contains `:` and `/`, so the two disagree on some pairs of port names                | Leaving the constraint out is the version of this bug nobody catches, and it surfaces as a failing insert months later (R7)                                  |
