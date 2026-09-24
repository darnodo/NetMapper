# Research: Identity resolution (claims into device entities)

The stack is fixed by 001 and 002: same Go module, same binary, same database, same three roles. This
file settles what the spec leaves to the plan, and records what reading the 001 and 002 code turned
up. Each entry gives the decision, why, and what else was considered.

## R1. Where resolution runs

- Decision: a new step in `jobrunner.Tick`, after `judgeStep`, sweeping closed snapshots that carry no
  resolution row, plus a `netmapper resolve <snapshot-id>` subcommand for the explicit re-resolve of
  FR-017.
- Rationale: FR-016 asks for exactly the sweep 002 already built, and `Tick` is the engine's only
  component. Hanging resolution off the closing transaction would leave a permanent gap whenever the
  engine dies between the two, and would let a failing computation keep a job from finishing.
- Alternatives: resolving inside `closeIn` (a computation failure blocks the run); a fourth
  long-running role (forbidden without a constitutional amendment); resolving lazily when someone
  reads a snapshot (there is no reader yet, and it would make the first read arbitrarily slow).
- Ordering note: resolution and judging are independent (FR-025), so the two steps do not wait on each
  other. Judging first is only an artefact of `judgeStep` already being there.

## R2. The unit that gets grouped

- Decision: the unit is one `identity` observation with its `identifier_claim` rows, which the code
  calls a claim group. Every `identity` observation of the snapshot is a candidate, including those
  marked `duplicate_of_task`.
- Rationale: claims are only ever written during `find`, one batch per `identity` observation
  (`store.WriteObservation`), so the observation is the natural unit and it is what carries the target
  address and the collection time an entity needs. Including duplicates is FR-008: 001 writes a second
  `collected` identity observation for every other address the same device answered on, and dropping
  those would lose the addresses a device was reachable at.
- Alternatives: grouping individual claims (loses the "these were read from one device at one moment"
  fact, which is the only thing that makes the grouping sound); grouping targets (a device with two
  addresses is two targets, which is the problem this feature exists to solve).

## R3. The grouping algorithm

- Decision: read the snapshot's claim groups into memory and run union-find over shared strong
  identifiers, in Go, in one pass, with claim groups ordered by observation id so the result never
  depends on how PostgreSQL returned the rows.
- Rationale: this is a few hundred devices and a few thousand claims per snapshot at this tool's
  scale. Union-find is about thirty lines and can be read and fixed without a diagram. The grouping
  also has to consult decisions and detect contradictions as it goes, which is far clearer in Go than
  in SQL.
- Alternatives: a recursive CTE computing connected components in the database (one clever query that
  nobody wants to debug at 23:00, and it still cannot apply the decisions); a temporary table plus
  iterative UPDATEs (slower and harder to read than the in-memory version); pairwise matching like
  002's `compare` (no transitive closure, which is exactly what FR-002 asks for).

## R4. What counts as a contradiction, and what happens to it

- Decision: after the naive connected component is computed, the component is checked for a strong
  kind carrying more than one distinct value inside it (two serials, two chassis MACs). A component
  that holds one does not merge at all: every claim group in it becomes its own entity, and one
  `identity_conflict` finding names the component's device keys and the kind that contradicts.
- Rationale: a wrong merge is invisible in the result and corrupts every edge and diff built on top of
  it; an over-split is visible as two entities plus a finding, and an operator merge decision fixes it
  permanently. Refusing to merge the whole component, rather than trying to find the minimum set of
  links to cut, keeps the rule explainable in one sentence: "a component that disagrees with itself is
  not merged." Components are two or three claim groups in practice, so the blast radius of that
  bluntness is small.
- Alternatives: merging anyway and recording the conflict as an attribute (the merge is exactly what
  must not happen); cutting the minimum number of links to make the component consistent (a small
  graph problem whose answer is often not unique, so FR-013 reproducibility would rest on a tie-break
  nobody can predict); merging on the strongest kind only, by a declared kind priority (pushes a
  vendor-shaped ordering into the resolver, which Principle V keeps out of the engine).

## R5. The device key, and why it is minted rather than hashed

- Decision: a device key is a text token `<kind>:<value>`, taken from the anchor identifier of the
  claim group set that first minted the device: the lexicographically smallest `kind:value` among its
  strong identifiers. A device with no strong identifier keys on the address it answered on,
  `addr:<address>` (clarification of 2026-09-24): a hostname is not unique, two chassis can carry the
  same one, and a hostname key would collide inside one snapshot and need an address tie-break anyway.
  The key lives in a small registry, `device` plus `device_identifier`, scoped to a perimeter name, so
  two perimeters never combine a device whatever identifier they share. A later snapshot's claim group set is matched to a device by **any** of its strong
  identifiers, not by recomputing the anchor, and new identifiers observed on a known device are added
  to the registry.
- Rationale: the key has one job, which is to be the same token for the same box in two runs (FR-021,
  SC-007). A key computed from the identifier set on each run fails that job the first time a run
  reads a chassis MAC but not a serial, because the set changed and so would the hash. Matching on any
  shared identifier is also what 002's `compare` already does, so the registry keeps 003 at least as
  good as the ad-hoc matching it is meant to replace rather than worse. The key stays text, derived
  from an observed identifier rather than from a sequence, so wiping every computed row and replaying
  the snapshots in closing order mints the same keys again (SC-008).
- Alternatives: hashing the sorted set of strong identifiers (changes whenever any identifier is
  missing from a run, which is the common case with SNMP-only devices); the anchor alone recomputed per
  snapshot with a declared kind priority (same fragility, plus a vendor-shaped priority list in the
  engine); a `bigserial` device id (re-issued when the registry is rebuilt, so every recorded decision
  would point at the wrong device after a wipe, breaking FR-010).
- Cost, stated plainly: this is one small table more than the alternatives, it is cross-snapshot state,
  and a full rebuild has to replay snapshots in closing order to mint the same keys. That order is the
  same one the 002 sweep already uses.

## R6. A claim group set that matches two known devices

- Decision: attach it to the device with the lowest key among the matches, add nothing of the other
  device to the registry, and raise an `identity_conflict` finding naming both keys and the identifiers
  that bridged them. Only an operator `merge` decision makes the two one device.
- Rationale: FR-023 is explicit that re-attaching two keys is an operator decision and never something
  the system concludes on its own. This is the chassis-swap case: the same box now carries the old
  chassis MAC and a new serial that already minted its own device. The conservative answer is stable
  (the lowest key wins every run, so the result does not flap), visible (a finding), and reversible
  with one recorded decision.
- Alternatives: merging the two registry devices automatically (fast, and the one case where the system
  silently rewrites history an operator may have been reading); minting a third device (loses both
  histories); refusing to resolve the snapshot (a data quality problem must not stop the pipeline, which
  is the rule 001 already follows for a parse failure).

## R7. Decisions: three kinds, replayed, append only

- Decision: one `entity_decision` table, append only, holding `merge` (two device keys are one device),
  `split` (one device key loses one named identifier, which then mints or matches its own device) and
  `never_merge` (two device keys are never combined, and the identifier that would bridge them is
  ignored rather than reported). Every resolution reads the whole table for its perimeter and applies
  it, ordered by id; on the same subjects, the last one recorded governs (FR-011).
- Rationale: FR-009 requires replay rather than in-place repair, and the three kinds map one to one on
  what an operator can actually know: "these two are the same box", "this identifier does not belong to
  this box", "stop suggesting these two are the same". Expressing `split` as detaching a named
  identifier, rather than as an opaque instruction to cut a group in two, is what makes it replayable:
  the next run knows precisely what to ignore, which is FR-012.
- Alternatives: `split` as a list of which claim groups go where (the claim group ids change on the next
  run, so the decision would not survive one); a single `override` kind with a free-form payload (one
  table, three meanings, and no constraint to check); applying decisions by editing the entities they
  concern (forbidden by Principle II).

## R8. One current entity set per snapshot

- Decision: a `resolution` row per snapshot (`UNIQUE (snapshot_id)`), carrying `resolver_version` and
  `computed_at`, upserted; the snapshot's entities are deleted and rewritten in the same transaction,
  which takes `pg_advisory_xact_lock` on the snapshot id first.
- Rationale: the sweep needs a marker that tells an unresolved snapshot from one that legitimately
  resolved to zero entities (the empty snapshot edge case), and the transaction is what gives FR-015 its
  "never a partially written set". The advisory lock is the same idiom 001 uses for claims and settles
  the concurrent sweep-and-operator case in the edge cases.
- Alternatives: stacking resolutions with a partial unique index the way 002 stacks judgements (a
  judgement is a verdict people acted on and must never be rewritten, an entity set is derived data the
  spec says is replaced wholesale, so keeping every historical set would be storage for nothing);
  inferring "resolved" from the presence of entities (wrong for an empty snapshot); a boolean column on
  `snapshot` (blocked by the `snapshot_closed_is_final` trigger from 001).

## R9. `resolver_version`

- Decision: an integer constant in the package, stored on every `resolution` row, bumped by hand when a
  change to the grouping alters results.
- Rationale: it is the same device as `gate_version` (002, R9) and for the same reason: after a fix, the
  affected snapshots can be found and re-resolved instead of re-crawled, which is Principle II's whole
  argument. The lab run that produced 002's version 2 is the precedent.
- Alternatives: deriving a version from a hash of the source (opaque and it churns on comments); no
  version at all (a corrected grouping becomes unfindable).

## R10. Reading only the active parse generation

- Decision: every claim read joins its observation and filters on the snapshot's active
  `parse_generation`.
- Rationale: FR-019. A replay writes a new generation over the same raw output, and mixing two
  generations' claims would group a device with its own superseded reading of itself.
- Alternatives: reading every generation and preferring the newest per observation (the same answer by a
  longer route, and wrong the day a replay covers only part of a snapshot).

## R11. Where the findings go

- Decision: the existing `finding` table, `domain = 'data_quality'`, with a new category
  `identity_conflict`. The migration extends the category `CHECK`. `subject_ref` holds the device keys
  involved, evidence rows cite the `identity` observations behind each side.
- Rationale: 001 already made `finding` the single surface for this kind of report, and
  `store.RaiseFinding` already writes it with its evidence. An identity contradiction is a data quality
  problem in the same sense a parse failure is.
- These findings are part of the resolution's output, so they are replaced with the entity set: a
  re-resolution deletes the `identity_conflict` findings it previously raised for that snapshot, with
  their `finding_evidence` rows, and raises those that still hold. A conflict settled by a decision
  therefore stops being reported, and ten re-resolutions leave one finding per live conflict rather than
  ten (clarification of 2026-09-24, FR-015). Findings raised by anything else, the collector's included,
  are never touched: the delete is restricted to `category = 'identity_conflict'` and to the snapshot
  being resolved.
- Alternatives: a dedicated conflict table (a second findings surface, which the C4 component list
  explicitly refuses); `domain = 'compliance'` (it is not a policy question); raising idempotently and
  never deleting (no `DELETE` grant needed, but a settled conflict stays open for ever with nothing able
  to close it); letting them accumulate one set per resolution (noise, and FR-018 could no longer say
  which findings describe the current set).

## R12. Grants

- Decision: `netmapper_engine` gains `SELECT, INSERT, UPDATE, DELETE` on `resolution`, `entity`,
  `entity_claim`, `device` and `device_identifier`, `INSERT` and `DELETE` on `finding` and
  `finding_evidence`, and `USAGE` on the new sequences. The `DELETE` is what R11's replace rule costs;
  it is unrestricted in SQL and restricted in code to `identity_conflict` rows of the snapshot being
  resolved. `netmapper_operator` gains `SELECT, INSERT` on `entity_decision` and
  `SELECT` on the rest. No role but the owner gains `UPDATE` or `DELETE` on `entity_decision`.
- Rationale: the engine owns the computed zone and must be able to replace it, which is what `DELETE`
  here means; the decisions are the append-only part, so they get 002's treatment. 001 gave the engine
  only `SELECT` on `finding`, so raising one needs an explicit grant, the same gap 002 hit with
  sequences.
- Alternatives: a `SECURITY DEFINER` write function like `judge_snapshot` (002 needed one because a
  supersede is an `UPDATE` on an append-only table; here the append-only table is never updated at all,
  so the function would guard nothing); giving the operator `DELETE` on decisions (a recorded decision
  is evidence of a human judgement, and FR-009 says recording one never modifies an earlier one).

## R13. The operator surface

- Decision: two subcommands. `netmapper resolve <snapshot-id>` re-resolves one snapshot.
  `netmapper decide merge|split|never-merge ...` records a decision. Both print one line.
- Rationale: FR-017 needs the first and FR-009 needs something to write a decision, since no API exists.
  `netmapper judge` (002) is the shape to copy: parse, connect, call the package, print, exit code.
- Alternatives: a `--resolve` flag on `netmapper run` (conflates a crawl with a recomputation); recording
  decisions by hand in SQL (an undocumented contract, and no validation that the subjects exist).

## R15. Attribute values when grouped observations disagree

- Decision: an entity's weak attributes (`hostname`, `platform`) come from the observation that stands
  for the device itself, the one not marked `duplicate_of_task`. Every other value stays readable
  through `entity_claim`.
- Rationale: without a rule the attribute depends on row order and two recomputations of one snapshot
  can disagree, which breaks FR-013. The winning observation is already the notion 001 and 002 use to
  mean the device rather than one of its addresses, so this adds no new concept.
- Alternatives: keeping every value as a list (hides nothing, but every consumer then has to choose, so
  the problem moves one level up); raising a finding on disagreement (a short name against an FQDN is
  the normal case, not a defect, and it would fire on every run).

## R14. Scale and cost

- Decision: no performance work. One read of a snapshot's identity observations and claims, an in-memory
  grouping, one write transaction, once per closed snapshot on the existing tick.
- Rationale: the lab is two switches and a real perimeter here is tens of devices. 002 measured nothing
  for the same reason and that was right.
- Revisit when: a snapshot holds more than a few thousand devices, at which point the claim read and the
  entity rewrite are the two places to look, in that order.
