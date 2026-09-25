# Research: Graph projector (interfaces and edges)

Phase 0 of [plan.md](plan.md). Each entry is a decision, why it was taken, and what was rejected.
Nothing here was left as NEEDS CLARIFICATION: the two questions the feature could have inherited are
already settled in [spec.md](spec.md), and the rest are consequences of the code that exists.

## R1. Where projection runs

**Decision**: a third step in `jobrunner.Tick`, after `judgeStep` and `resolveStep`, sweeping closed
snapshots that carry a `resolution` row and no current `projection` row, oldest first by closing time.
`netmapper project <snapshot-id>` is the operator's way to redo one. The work lives in a new package,
`internal/graph`, behind a single `Project` function.

**Rationale**: this is the third sweep of the same shape, and both earlier ones exist for the reason
FR-021 states: a runner that dies between two steps repairs itself on the next tick, with no recovery
path of its own. Projection depends on resolution, so it runs after it in the same tick, and a snapshot
resolved this tick is projected in the next one at the latest.

**Alternatives considered**: doing it inside the resolution transaction, which would make a projection
failure roll back a perfectly good entity set, and is the coupling 003 already refused for judging;
a fourth role or a long-running projector process, which the one-binary constraint rules out.

## R2. From an observation to the entity it describes

**Decision**: follow the task lineage. `entity_claim` gives the `identifier_claim` ids, each claim
carries its `observation_id`, and that observation is the `identity` one a `find` task wrote. A
`neighbours` observation carries the same `task_id` as the `identity` observation, because `find`
collects both. An `interfaces` observation is written by the `scrape` task whose `parent_task_id` is
that `find` task. So one query maps every entity to the find task ids behind it, and a second maps
those to both families.

**Rationale**: it uses only what is already stored and adds no column. It also handles the device
reached on two addresses for free: resolution collapsed those claim groups into one entity, so both
find tasks map to it, and the interfaces they collected land on the same entity rather than being
duplicated per address.

**Alternatives considered**: matching on `observation.target`, which breaks precisely on the
two-addresses case the spec calls out and on a device whose management address changed mid-run;
adding an `entity_id` column to `observation`, which would put a computed value in the collected zone
and break the rebuildability boundary Principle II draws.

## R3. Canonicalising the far end, and the engine reading packs

**Decision**: the projector loads the platform packs and calls `pack.Registry.Normalise` on the port
spelling a neighbour reports for the far end, using the platform of the entity that far end resolved
to. `netmapper engine` and `netmapper project` gain the `--packs` flag `netmapper collector` already
has.

**Rationale**: FR-003 says the canonical name comes from the naming rules the platform pack declares
and that the system must not decide per vendor what a port is called, which is Principle V restated.
The parser already canonicalises `interfaces.name` and `neighbours.local_interface`, because at parse
time it knows the platform of the device it is parsing. It cannot canonicalise `remote_interface`,
because the far end's platform is unknown until identity resolution has run. The projector is the
first component that knows both, so it is where the rule has to be applied.

This is a change to the engine's shape: 003 recorded that resolution reads no pack, and
`cmd/netmapper/engine.go` says so in a comment that this feature makes false. It stays inside
Principle III, which is about credentials and reach, not about data: a pack is a directory of
read-only commands and regexes, it holds no secret, and loading it opens nothing.

**Alternatives considered**: matching the reported spelling against the remote device's known
canonical names with a general rule (case-insensitive prefix, digits preserved), which is a
vendor-agnostic guess about vendor naming and is the exact leak FR-003 forbids; storing the naming
rules in the database at crawl time, which duplicates the pack into a second place that can drift.

## R4. What an interface is, and where it comes from

**Decision**: one `interface` row per `(entity_id, canonical_name)`. A row is built from the
`interfaces` family when the device described the port itself, and from a `neighbours` row when the
port is only known because a report named it, on either end. A `source` column holds `device` or
`neighbour`, and `device` wins when both apply.

**Rationale**: FR-002 makes the canonical name unique within the entity and nowhere else, which is
what lets two devices each have a `port1`. FR-006 asks for the distinction between a port the device
described and one a neighbour revealed, and that distinction is one column. Creating the port from
the report rather than dropping the link is what the spec's first edge case asks for, and it is also
what makes the far end of a one-sided link attachable when the remote device did resolve but never
listed that port.

**Alternatives considered**: refusing to create a port a device did not describe, which drops real
topology whenever the interfaces recipe failed or the platform has none, and contradicts FR-006's
acceptance scenario 4; an `origin` array holding every source, which is a set of two values with a
precedence rule, written as a set.

## R5. Aliases: every spelling, including the canonical one

**Decision**: `interface_alias` holds one row per `(interface_id, spelling)`, with the source kind
(`device` or `neighbour`) and the observation the spelling came from. Every spelling seen is recorded,
including one that already equals the canonical name. When the same spelling arrives from two
observations, the lowest observation id is kept.

**Rationale**: FR-004 says every spelling, with the source each came from, and a reader asking "what
else calls this port something else" should not have to reconstruct the canonical spelling from the
absence of a row. The lowest observation id is a tie-break, so it is tested with two observations
carrying the same spelling for one port rather than assumed.

**Alternatives considered**: recording only the spellings that differ from the canonical name, which
makes the table a diff against a value stored elsewhere and loses which observation used it; storing
the source as a free-text label, which puts the same two values in the table under four spellings.

## R6. An edge is named by its endpoints

**Decision**: an endpoint is a text reference in one of four forms:

| Form                            | Means                                              |
| ------------------------------- | -------------------------------------------------- |
| `if:<device_key>/<canonical>`   | a port of a resolved device                        |
| `dev:<device_key>`              | a resolved device as a whole                       |
| `addr:<address>`                | an address a device answered on                    |
| `unknown:<kind>=<value>[/<port>]` | a far end no entity accounts for, named by the best identifier the report gave |

`edge.name` is `type:from_ref|to_ref`, a stored generated column, and `(snapshot_id, name)` is unique.
An `l1_link` is oriented by putting the lower reference first, so one cable has one name whichever end
is read; a `has_address` is always device then address, because the type names the direction.

**Rationale**: FR-015 wants a name derived from the endpoints, stable for as long as they are, needing
no registry and no mint. A device key is already stable across runs (003) and a canonical name is
already stable across spellings (R3, R4), so their concatenation inherits both. Generating the column
in the database rather than in Go means the name cannot drift from the columns it is built from, and
FR-009's "one link, not two" then falls out of the unique key rather than out of a code path that has
to remember to deduplicate.

The `unknown:` form takes the far end's port spelling when the report gave one, so two cables to the
same unmanaged box stay two edges (FR-011). The identifier is chosen in a fixed order:
`remote_chassis_id`, then `remote_mgmt_address`, then `remote_system_name`. A report that says nothing
at all about the far end yields the local interface and no edge: there is no endpoint to connect to,
and inventing one would be the opposite of FR-010.

**Alternatives considered**: a minted edge key in a registry, which is what spec.md already rejected
and which would make this feature as large as 003 for a problem it does not have; a hash of the two
endpoints, which is stable and unreadable, and which no operator can type into a query.

## R7. The ordering comparison is byte order, in both places

**Decision**: the `l1_link` orientation is `from_ref < to_ref` compared as bytes. The Go side compares
with `<` on strings, and the `CHECK` constraint writes `from_ref COLLATE "C" < to_ref COLLATE "C"`.

**Rationale**: Go compares strings by byte; PostgreSQL compares `text` under the database collation,
which for ICU or a `*.UTF-8` locale is not byte order and treats punctuation specially. A reference
contains `:` and `/`, so the two comparisons can disagree, and the disagreement shows up as a failing
insert on one specific pair of port names months later. Pinning the constraint to the `C` collation
makes the database agree with the projector by construction.

**Alternatives considered**: leaving the constraint out and trusting the code, which is the version of
this bug that is not caught at all.

## R8. Pairing two reports into one agreed link

**Decision**: each `neighbours` row from a resolved device A gives a report `(A, A.local, far end)`.
The far end resolves to an entity B when the report's `remote_chassis_id` matches a strong identifier
value of B, or its `remote_mgmt_address` matches one of B's addresses when the type is `ipv4` or
`ipv6`, or a strong identifier value when the type is `mac`. Chassis identifier first, address second;
the first that matches wins. When a value matches more than one entity, nothing matches: the far end
stays unresolved and the link stays `one_end`, carrying the identifier it was named by.
Two reports become one `both_ends` edge when each names the other's local port after canonicalisation:
A says B's port is `B.local`, and B says A's port is `A.local`. Otherwise each stays a `one_end` edge.
Two rows from one device describing the same cable under different protocols produce one edge, because
the protocol is not part of an edge's identity (FR-011); `attributes.protocols` holds the set of
protocols that reported it, and each row's observation is cited.

**Rationale**: FR-012 names the chassis identifier and the management address as what a link attaches
by, and deliberately does not name the system name: a hostname is weak in every pack here, so matching
on it would let two boxes that share a name become one cable.

Refusing an identifier that matches two entities is the same trade 003 made for a contradicting
component: a wrong attachment is invisible and corrupts every consumer of the edge, while a link left
known from one side is visible and says so. The case is reachable, because resolution turns a component
contradicting itself into several entities that can still carry one chassis MAC between them, and that
snapshot already carries an `identity_conflict` finding naming them. Clarified on 2026-09-25, against an
earlier draft of this entry that took the lowest `device_key`.

**Alternatives considered**: matching on `remote_system_name` as a third fallback, rejected above;
pairing by device pair rather than by port pair, which merges two cables between the same two switches
into one edge and contradicts FR-011.

## R9. A disagreement is reported, never resolved

**Decision**: two reports about the same pair of devices that name one port in common and a different
port opposite it are a disagreement, because one port cannot face two different far ends. Two reports
about one pair that share no port are two separate cables, each `one_end`, and not a disagreement. Both
contradicting reports stay as their own `one_end` edges, no `both_ends` edge is written, and one
`finding` with the new category `link_disagreement` names both sides and what each said, citing both
observations.

**Rationale**: FR-014 asks for exactly this shape, and it is the shape 003 already chose for an
identity collision: refuse the merge, keep the pieces, report. The findings surface exists, the engine
and the operator already hold `INSERT` and `DELETE` on it, and adding a second reporting surface for
the same kind of problem would be the thing 003's R11 rejected.

Requiring a port in common is what keeps a shared medium out of this. Three ports on one segment produce
three pairs that each agree with themselves, so the rule stays silent where R10 says it should.

Like an `identity_conflict`, a `link_disagreement` is part of a projection's output and is replaced
with it, restricted in code to that category and that snapshot.

**Alternatives considered**: picking the report from the device with the lower key, which invents a
link one side never described; writing a third confidence value for a contradicted link, which hides
the contradiction in a column nobody filters on.

## R10. Three or more devices reporting one link

**Decision**: nothing special. Reports are paired by port pair, so a port that three devices claim to
be cabled to produces one edge per pair, each with its own confidence, and none is discarded.

**Rationale**: the spec's edge case asks that the result not silently pick two and discard the rest,
and pairwise identity gives that without a rule. A shared medium genuinely is several links between
several ports, and a reader who wants to notice one can count the `l1_link` edges on an interface.

**Alternatives considered**: raising a finding when an interface carries more than one `l1_link`,
which FR-025 puts outside this feature's output and which would fire on every hub and every lab bridge.

## R11. `has_address` edges

**Decision**: one `has_address` edge per address in the entity's `attributes.targets`, from
`dev:<device_key>` to `addr:<address>`, confidence `direct`, cited by the identity observation
collected on that address.

**Rationale**: FR-013 asks for the address a device answered on as its own kind of edge, so that a
consumer can ask what answers at an address without reading identity claims. `attributes.targets` is
already the answer resolution computed, in a defined order, so this edge is a projection of a value
rather than a second computation of it.

**Alternatives considered**: reading `observation.target` again, which would recompute what resolution
already decided and could disagree with it; hanging the address off an interface, which would be a
claim about which port the address sits on, and nothing collected says that.

## R12. One current projection per snapshot

**Decision**: a `projection` row per snapshot, primary keyed on `snapshot_id`, holding
`projector_version` and the `resolution.computed_at` the projection was built from. The whole set is
written in one transaction that first deletes the snapshot's interfaces and edges, under
`pg_advisory_xact_lock` on the snapshot id.

**Rationale**: FR-018 asks for a complete new set, never a patch, and for no consumer ever reading a
partial one, which is the same requirement 003 met the same way. The lock is on the snapshot and not
on the perimeter, because this feature adds no cross-snapshot state: there is no registry to serialise
and nothing two snapshots share.

Recording `resolution.computed_at` is what makes a re-resolution repair itself. `interface` cascades
from `entity`, so re-resolving a snapshot destroys its interfaces and, through them, its edges; the
`projection` row would otherwise survive and claim a set that no longer exists. The sweep therefore
takes a snapshot whose `projection.resolution_at` no longer matches its `resolution.computed_at`.
That is not FR-022's "deciding on its own to redo one": the input changed and the output is already
gone, which is the self-repair FR-021 asks for. Redoing a projection whose inputs are unchanged still
needs `netmapper project`.

**Alternatives considered**: having `entity.Resolve` delete the `projection` row, which makes identity
resolution write the projector's tables and couples two packages that otherwise share nothing;
versioning projections like `snapshot_judgement` does, which buys a history nobody has asked to read.

## R13. Reading only the active parse generation

**Decision**: every read joins `parse_generation` on `active`, the way `readGroups` does.

**Rationale**: FR-019 states it, and FR-017's reproducibility rests on it. It costs one join and is
the single place a re-parse becomes visible to this feature.

## R14. Entity kinds stay as they are

**Decision**: no widening of `entity.kind`. An unresolved far end is recorded in the edge's reference
and attributes, and produces no entity.

**Rationale**: FR-012 says a link naming a device nothing accounts for is recorded against what is
known, not that the unknown device becomes an entity. Resolution left the kind check deliberately
narrow and said the day the projector adds a kind is a migration; this feature does not need one, and
saying so is worth more than adding one speculatively.

**Alternatives considered**: minting a `device` entity for each unresolved far end, which would put
devices in the entity set that the crawl never reached, corrupt the coverage gate's device counts, and
give them keys the registry would then carry forever.

## R15. Grants

**Decision**: `netmapper_engine` and `netmapper_operator` both get `SELECT, INSERT, UPDATE, DELETE` on
`projection`, `interface`, `interface_alias`, `interface_evidence`, `edge` and `edge_evidence`, plus
`USAGE` on `interface_id_seq` and `edge_id_seq`. `finding.category` gains `link_disagreement`; no new
right on `finding` is needed, since 003 already gave both roles `SELECT, INSERT, DELETE`. The collector
gains nothing.

**Rationale**: `netmapper project` runs the projector in the operator's own process, exactly as
`netmapper resolve` runs the resolver, so the operator needs the same write rights. The reads it needs
on `observation` and `entity` it already holds from 001 and 003. The constitution's workflow rule about
testing a path under the role the contracts assign it comes from the defect 003 shipped here, so the
`project` path is tested under `netmapper_operator` and the sweep under `netmapper_engine`, not both
under whichever connection is convenient.

**Alternatives considered**: giving the operator nothing and making `project` ask the engine to do it,
which needs a queue and a wait for a command that has to print a result.

## R16. The operator surface

**Decision**: one new subcommand, `netmapper project <snapshot-id>`, printing
`<interfaces> interfaces, <edges> edges, <disagreements> disagreements`. Exit 2 for a snapshot that
does not exist, is not closed, or has no entity set.

**Rationale**: it mirrors `netmapper resolve` exactly, including which failures are input errors
rather than runtime ones. Retrieval (FR-024) is SQL until an interface exists, which is the same
limit 003 recorded and the same place Principle I's response contract will be enforced.

## R17. Scale and cost

**Decision**: nothing measured, no index beyond the keys and the foreign keys.

**Rationale**: the same reasoning 003 used. A homelab perimeter is a few hundred devices with a few
dozen ports each, read once per closed snapshot, written once in one transaction. An index added now
would be guessing at a query pattern that the API feature has not written yet.
