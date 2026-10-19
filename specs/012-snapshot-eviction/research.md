# Research: Snapshot eviction and pinning

All findings come from reading the code and migrations on `main` (e1790dd, 2026-10-19). Nothing in
this feature talks to a device; no lab session was needed.

## R1. Who may delete the collected zone

**Decision**: the database part of eviction, pinning and unpinning runs in `SECURITY DEFINER`
functions owned by `netmapper_owner`, with `EXECUTE` granted to `netmapper_operator`. The operator
gets no `DELETE` on any collected table and no `UPDATE` on `snapshot`.

**Rationale**: 0004 states that no role but the owner may update or delete the collected zone or
`audit_log`, and only the owner of a partitioned table can drop its partitions. The repo already
solves "the operator needs one owner-only action" this way: `create_snapshot_partitions`,
`create_task_partition` and `judge_snapshot` are owner functions with a narrow `EXECUTE` grant. A
function also keeps every check and every delete in one transaction.

**Alternatives considered**: granting `DELETE` on the collected tables to the operator (breaks the
0004 rule for every operator command, not only eviction); running `evict` as the owner login (the
owner's DSN is the migration DSN and is not meant to be handed to day-to-day commands).

## R2. Preview and eviction share one function

**Decision**: one function, `evict_snapshot(p_snapshot_id bigint, p_apply boolean)`. It takes the
locks, runs every refusal check and counts what would go. With `p_apply = false` it returns the
counts and writes nothing. With `p_apply = true` it does the database part of the eviction and
returns the same counts.

**Rationale**: the preview must announce exactly what the eviction removes (FR-014, FR-015). Two
code paths would drift. The operator also has no read access to `observation_raw`, `raw_object` or
the computed tables it would need to count on its own.

**Alternatives considered**: a preview computed in Go from the operator's grants (needs new read
grants and duplicates the rules); a preview that runs the eviction and rolls back (takes the
locks as an eviction and writes partition DDL for nothing).

## R3. Order of the database part

**Decision**, inside the one transaction of `evict_snapshot(..., true)`:

1. Locks (R6), then the refusal checks (R7).
2. Record the stored outputs to delete in a work list, `eviction_object` (R5).
3. Delete the computed zone: `l2domain` (members and links cascade), `entity` (interfaces, aliases,
   edges and their evidence, `entity_claim` and `l2domain_member` cascade or are deleted
   explicitly where they do not cascade), `projection`, `resolution`, `finding_evidence`,
   `finding`.
4. Detach and drop the snapshot's partitions of `observation_raw`, `identifier_claim`,
   `observation`, and the crawl's partition of `task`. Delete its `parse_generation` rows.
5. Delete the `raw_object` rows of the hashes in the work list.
6. Registry cleanup (R8).
7. Set the snapshot to `evicting`, with `evicted_at`.

The job row and the snapshot's `snapshot_judgement` rows stay; so do the judgements of later
snapshots that name it as baseline (FR-013).

**Rationale**: every table that references `observation` or `identifier_claim` by foreign key has
to be emptied of the snapshot's rows before its partition can go, and `parse_generation` is
referenced by `observation`. Dropping the partitions rather than deleting rows is what
`specs/001-crawl-loop/data-model.md` planned and costs the same on a small snapshot and far less
on a large one. `task` is partitioned by job, not by snapshot, as the issue assumed; one job has
exactly one snapshot, so the job's partition is the snapshot's task list.

Detaching a partition takes an `ACCESS EXCLUSIVE` lock on its parent table until the transaction
commits, so API and engine reads of `observation`, `observation_raw`, `identifier_claim` and `task`
wait for the eviction's database part. At homelab scale that is well under a second; it is not
worth `DETACH ... CONCURRENTLY`, which cannot run inside a transaction or a function.

The exact delete order is checked by the integration test on a fully projected snapshot with
findings and L2 domains: a missed table fails the partition drop on a foreign key, loudly.

**Alternatives considered**: `DELETE` on the partitioned parents (works, but leaves empty
partitions behind for every evicted snapshot); `ON DELETE CASCADE` added on every foreign key to
`observation` (a schema-wide change whose effect reaches every future delete, not only eviction).

## R4. Raw output is shared, and the collector relies on it before recording it

**Finding**: `RawStore.Put` (`internal/store/raw.go`) checks whether the object already exists and
skips the upload if it does. The `raw_object` and `observation_raw` rows are written later, in
`WriteObservation`. Between the two, a running crawl relies on an object that no row ties to it
yet. An eviction that deletes that object in this window leaves the new observation pointing at
nothing.

**Decision**: no eviction while a crawl runs, and no crawl starts while an eviction is unfinished
(clarification of 2026-10-19). The candidates for deletion are the hashes in the evicted snapshot's
`observation_raw` that no other snapshot's `observation_raw` references. Outputs already orphaned
before the eviction are not touched.

**Rationale**: with no crawl running, the only references are rows, and a single query decides what
is shared. No coordination with the collector is needed.

**Alternatives considered**: a grace period on object age (does not cover the reuse case, the
object is old); marking objects as reserved by the collector (changes the collector's hot path for
an operator command used a few times a year).

## R5. Resuming an interrupted eviction

**Decision**: the database part ends with the snapshot in a new state, `evicting`, and the hashes to
delete listed in `eviction_object (snapshot_id, hash)`. The object store part then deletes each
object and its `eviction_object` row, one at a time. When the list is empty, a second owner
function, `finish_snapshot_eviction(id)`, sets the state to `evicted`. Running `evict --yes` on an
`evicting` snapshot skips the database part and resumes the object store part. On an `evicted`
snapshot it does nothing and exits 0 (FR-006).

An object already missing from the store counts as deleted, so a crash between the delete and the
row removal is harmless.

**Rationale**: once the database rows are gone, nothing else says which objects the snapshot used.
The list has to be written in the same transaction that removes them. A durable `evicting` state is
also what blocks new crawls until the eviction ends (R6), even across a crash of the `evict`
process.

**Alternatives considered**: deleting objects first, then rows (a crash leaves rows pointing at
deleted objects, which breaks evidence served by the API: worse than an orphan); an advisory lock
held for the whole command (released by a crash, so a crawl could start between the two parts and
reuse an object still on the list).

## R6. Keeping a crawl and an eviction apart, and the engine away from an evicted snapshot

**Decision**:

- One transaction-scoped advisory lock, the "crawl gate", taken by `jobrunner.Start` before it
  inserts the job, and by `evict_snapshot` (preview and apply). `Start` then refuses when any
  snapshot is `evicting`. `evict_snapshot` refuses when any job is `running` or `cancelling`.
- `evict_snapshot` also takes the locks the engine already uses: the perimeter lock of the
  resolver (`pg_advisory_xact_lock(hashtextextended(perimeter, 0))`, `internal/entity/entity.go`)
  and the projection lock of the snapshot (`projectionLockClass`, `internal/graph/graph.go`), and
  locks the snapshot row `FOR UPDATE`.
- The resolver and the projector check the snapshot's state today before their transaction and
  before their lock. They re-read it after taking the lock and stop when it is no longer `closed`.
- `judge_snapshot` locks the snapshot row `FOR SHARE` and raises when the snapshot, or the baseline
  it names, is no longer `closed`. The next sweep picks a new baseline (FR-011).

**Rationale**: the two checks (no evicting snapshot, no running job) under one lock make the
"never at the same time" rule hold in both directions. The engine sweeps already select only
`closed` snapshots, so after the state change they skip evicted ones without further change; the
re-checks close the window of a sweep that read `closed` just before the eviction committed.

**Alternatives considered**: `LOCK TABLE job` in eviction (blocks every reader of `job`, the API's
job endpoint planned in `Start`'s comment included); relying on the sweep's selection alone (leaves
the window above open).

## R7. Refusal checks, in order

1. Unknown id: invalid input, exit 2.
2. `evicted`: nothing to do, exit 0. `evicting`: resume (R5).
3. `open`: refused.
4. Pinned: refused.
5. A job `running` or `cancelling`, any perimeter: refused, naming the job and its snapshot.
6. Another snapshot of the same perimeter `closed` with no `resolution` row: refused, naming it
   (clarification of 2026-10-19). Open snapshots of the perimeter are covered by check 5.

A refusal exits 2, like `netmapper cancel` on a job that is not running. The preview exits 3.

**Rationale**: the order puts the cheapest, most specific message first. Exit 2 already means "your
request cannot be done as asked" in this CLI. The spec only requires the preview code to differ
from the others.

## R8. Registry cleanup

**Decision**: after the snapshot's entities are deleted, delete every `device` row of the evicted
snapshot's perimeter that no entity of a non-evicted snapshot of that perimeter carries
(`entity.device_key`), with its `device_identifier` rows first. The perimeter of a snapshot is
`perimeter.name` through `job.parameters->>'perimeter_id'`, as in `selectBaseline`.

Entries still referenced keep everything, including `minted_from`, which may now point at a
tombstone. The `snapshot` row stays, so the foreign key holds.

**Rationale**: this is the clarified rule ("no remaining snapshot references it, regardless of which
snapshot created it"). It runs under the resolver's perimeter lock (R6), so no resolution reads the
registry while it shrinks. Check 6 of R7 ensures every remaining snapshot of the perimeter has its
entities, so "carried by an entity" is the complete set of references.

**Alternatives considered**: matching on strong identifiers from claims of unresolved snapshots
(option C of the clarification, rejected there).

## R9. The pin flag and closed-snapshot immutability

**Decision**: `snapshot.pinned boolean NOT NULL DEFAULT false`, changed only by an owner function,
`set_snapshot_pinned(id, pinned)`, which refuses an `evicting` or `evicted` snapshot. The
`snapshot_closed_is_final` trigger is replaced by one that allows, on a closed snapshot, only:
a change of `pinned` alone, or the move to `evicting` with `evicted_at`. `evicting` may only move to
`evicted`. `evicted` is final.

**Rationale**: Principle II asks that a snapshot be immutable once closed. The trigger today
enforces that by refusing any update of a closed row. A PATCH amendment, approved on 2026-10-19,
scopes the rule to content: the pin flag, the `evicting` and `evicted` states and `evicted_at` are
lifecycle metadata; content is never modified, only removed as a whole by eviction. The trigger
enforces that boundary. A separate `snapshot_pin` table was considered and rejected with the
amendment.

## R10. Object store access for the operator

**Decision**: `netmapper snapshot evict --yes` reads the same `NETMAPPER_S3_*` variables as the
collector, through the existing `rawStore()` helper (`cmd/netmapper/collector.go`), with its own
key. The deploy guide's default becomes three keys: read and write for the collector, read only for
the API, and a dedicated operator key that can read and delete. The collector's key gains no right
for this feature. The preview needs no object store access.

On stores with per-action policies (AWS S3, MinIO), the operator key is `GetObject`,
`DeleteObject` and `ListBucket`, and the collector key needs no `DeleteObject`. Garage's bucket
permissions are `read`, `write` and `owner`, and `write` includes delete, so on Garage the operator
key is `read` + `write` and the collector's existing `write` already allows deleting; that is
Garage's model and is stated in the guide, not changed here. The dev stack (`deploy/compose.yaml`)
creates the operator key next to the existing one, so tests run with the documented split.

**Rationale**: a dedicated key keeps the right to remove evidence with the role that is supposed to
do it, and can be revoked without stopping collection. The S3 key opens no device session and
resolves no secret, so Principle III is untouched.

**Alternatives considered**: reusing the collector's key (simplest, rejected on 2026-10-19: the
operator would hold the collector's write key, and deletes would be indistinguishable from the
collector's in the store's logs).

## R11. Audit trail

**Decision**: `audit_log` gains three actions, `snapshot.evict`, `snapshot.pin`, `snapshot.unpin`.
For them `target` is null (the check becomes "target is not null unless the action starts with
`snapshot.`"), `command` is `snapshot <id>`, and `result` is `ok`, `noop`, `incomplete: <reason>`
or `refused: <reason>`. One row per command, written by the Go command after the function returns,
with the OS user as actor, as `decide` does (`osUser()`). The operator gets `INSERT` on
`audit_log`, nothing more. A preview writes nothing (spec edge case).

**Rationale**: the spec asks for the audit trail; `audit_log` is it, append only for every role but
the owner. A second audit table would split the trail in two.

## R12. Read API

**Decision**: `SnapshotRef` gains `pinned` and `evicted_at`. `pickSnapshot` and
`observationScope` answer `410 {"error": "snapshot_evicted", "snapshot": {…}}` when the named
snapshot is `evicting` or `evicted`. `GET /v1/snapshots/{id}` keeps serving any state, tombstone
included. The judgement view gains `baseline_state`, so a reader sees the baseline was evicted
without a second request. The default snapshot and the observation lookup without `?snapshot=`
need no change: the first already selects `closed` only, and the second finds nothing because the
rows are gone (404 `no_such_observation`).

**Rationale**: 410 says "existed, gone on purpose", distinct from 404 `no_such_snapshot` and 409
`not_projected` (FR-010). The API role gains no grant (FR-016); it already reads `snapshot` and
`snapshot_judgement`.

## R13. Out of scope, recorded

- Sweeping objects orphaned outside eviction: listed under "Still open" in
  `docs/c4-model/04-data-model.md` with the maintenance component of `03-components.md`.
- Retention policy run by the engine.
- Grafana panels reading `snapshot` directly: they will see the new states; no panel is changed.
