# Implementation Plan: Snapshot eviction and pinning

**Branch**: `012-snapshot-eviction` | **Date**: 2026-10-19 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/012-snapshot-eviction/spec.md`

## Summary

A new operator command, `netmapper snapshot evict|pin|unpin --id N`, backed by three owner-owned
`SECURITY DEFINER` functions, the way `create_snapshot_partitions` and `judge_snapshot` already give
the operator one owner-only action each (research R1). The operator role gains no `DELETE` on the
collected zone.

`evict_snapshot(id, apply)` takes the locks, runs the refusal checks, and counts what would go; the
preview (no `--yes`, exit 3) and the eviction share it, so they cannot disagree (R2). With `apply`
it lists the snapshot's unshared outputs in a work list, `eviction_object`, deletes the computed
zone, drops the snapshot's partitions and its crawl's task partition, deletes the registry entries
no remaining entity of the perimeter carries, and leaves the snapshot `evicting` (R3, R8). The Go
command then deletes the listed objects from the store, one row at a time, and
`finish_snapshot_eviction` moves the snapshot to `evicted`. A crash or an unreachable store leaves
it `evicting`; the same command resumes (R5).

Crawls and evictions are kept apart by one advisory lock taken by `jobrunner.Start` and by
`evict_snapshot`: a crawl does not start while a snapshot is `evicting`, an eviction does not start
while a job is `running` or `cancelling` (R4, R6). That closes the gap the collector's
skip-if-present upload opens. The resolver and the projector re-check the snapshot's state after
their lock, and `judge_snapshot` refuses a snapshot or baseline that is no longer `closed`, so the
engine never writes for an evicted snapshot (R6).

`snapshot.pinned` and `evicted_at` are new columns; the closed-snapshot trigger is replaced by one
that allows only the pin toggle and the eviction transitions (R9). `audit_log` takes the three new
actions with a null target (R11). The read API answers 410 `snapshot_evicted` for a named evicted
snapshot and shows `pinned`, `evicted_at` and the baseline's state (R12).

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `pgx/v5`, `minio-go/v7` (`RemoveObject`), goose. No new
dependency.

**Storage**: PostgreSQL, one migration (`0013_snapshot_eviction.sql`); the object store loses
objects, gains nothing.

**Testing**: `go test ./...` against the dev stack (`NETMAPPER_TEST_DSN`,
`NETMAPPER_TEST_S3_ENDPOINT`); integration tests in `cmd/netmapper` under `netmapper_operator`,
role grants in `internal/store/roles_test.go`, API tests in `internal/api`. No lab, no fixture
recording.

**Target Platform**: Linux container (one binary, three roles) plus the operator CLI.

**Project Type**: single Go service.

**Performance Goals**: none set. Partition drops are constant time; object deletes are one request
per unshared output, a few dozen per lab snapshot.

**Constraints**: tests pass with no lab up. The live check of the quickstart needs two ordinary
crawls; no device configuration changes, and the "broken" crawl contacts no device.

**Scale/Scope**: homelab and trial: tens of snapshots, hundreds of observations each.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | No answer loses its evidence: a remaining snapshot keeps every observation and output it references (R4). An evicted snapshot is never answered from; naming it gives 410, not an empty answer. Verdicts keep their figures and show the baseline's state. |
| II. Observations immutable, rest rebuildable | yes | Eviction is the exception the principle names. Pinning and the eviction states change a closed snapshot's lifecycle metadata, never its content; the new trigger allows those changes alone (R9). See note below. Entity decisions are kept, so a replay of the remaining snapshots gives the same result. |
| III. Credentials and reach stay in the collector | yes | No device credential anywhere new. The operator gets its own object store key that can delete outputs (R10); it opens no device session. The collector's key gains no right, `engine` gains nothing, the API keeps its read-only key and gains no grant. |
| IV. Read only, outward | no | Nothing reaches a device. |
| V. Vendor specifics are data | no | No vendor term. |

Note on Principle II: "Snapshots MUST be immutable once closed" is clarified by a PATCH amendment
in the same change, scoped to lifecycle metadata. Proposed wording, appended to Principle II:

> A snapshot's lifecycle metadata (its pin flag, the `evicting` and `evicted` states and the time
> of eviction) is not its content. Content is never modified after closing; it is only removed, as
> a whole, by eviction.

The new trigger enforces exactly that: on a closed snapshot, only `pinned`, or the move to
`evicting` with `evicted_at`; nothing else (R9). Pinning stays a column on `snapshot`.

Workflow rules: the README (operator commands), `docs/how-to/deploy.md` (a third,
dedicated operator key with read and delete as the default, the operator's S3 variables, the
`snapshot` commands), `docs/c4-model/04-data-model.md` (states, `pinned`, `evicted_at`,
`eviction_object`, registry cleanup, "Still open": orphan sweep), the 001 CLI contract,
the 005 REST contract and the Bruno snapshot requests are updated in the same change (FR-017). Every
path is tested under the role that runs it: eviction and pinning under `netmapper_operator`, the
410 under `netmapper_api`, the re-checks under `netmapper_engine`.

Gate: pass, with the Principle II note above. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/012-snapshot-eviction/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── cli.md      # merged into specs/001-crawl-loop/contracts/cli.md
│   └── rest.md     # merged into specs/005-read-api/contracts/rest.md
└── tasks.md        # /speckit-tasks
```

### Source Code (repository root)

```text
migrations/0013_snapshot_eviction.sql   # snapshot columns and trigger, eviction_object, audit_log
                                        # actions, evict_snapshot, finish_snapshot_eviction,
                                        # set_snapshot_pinned, judge_snapshot state check, grants
cmd/netmapper/snapshot.go               # new: snapshot evict|pin|unpin, output, exit codes, audit
cmd/netmapper/snapshot_test.go          # new: the cmd/netmapper tests of quickstart section 1
cmd/netmapper/main.go                   # "snapshot" command, usage line, exit code 3
cmd/netmapper/collector.go              # rawStore() reused as is
internal/store/raw.go                   # RawStore.Delete (missing object is not an error)
internal/store/roles_test.go            # operator and api grants
internal/jobrunner/start.go             # crawl gate lock, refuse while a snapshot is evicting
internal/entity/entity.go               # re-read state after the perimeter lock
internal/graph/graph.go                 # re-read state after the projection lock
internal/api/snapshot.go                # SnapshotRef.Pinned, EvictedAt; 410 in pickSnapshot;
                                        # baseline_state in the judgement view
internal/api/raw.go                     # 410 in observationScope
internal/api/*_test.go                  # TestEvictedSnapshot
bruno/snapshots/                        # list and get show the new fields; an evicted request
specs/001-crawl-loop/contracts/cli.md, specs/005-read-api/contracts/rest.md
README.md, docs/how-to/deploy.md, docs/c4-model/04-data-model.md
.specify/memory/constitution.md         # PATCH: lifecycle metadata is not content (Constitution Check)
deploy/compose.yaml                     # garage-init creates the dedicated operator key
```

**Structure Decision**: the existing layout. One new command file with its test, one migration;
everything else extends files in place. The crawl gate's lock key lives in one place, a SQL
function in the migration (`crawl_gate_key()`), called by `Start` and `evict_snapshot`, as
`claim_lock_key` already is for claims.

## Implementation order

1. Migration: columns, trigger, `eviction_object`, `audit_log` changes, grants, the three functions,
   the `judge_snapshot` check. Role tests first, then a SQL-level test of `evict_snapshot` on a
   seeded snapshot, preview then apply.
2. `RawStore.Delete`; `cmd/netmapper/snapshot.go` with preview, eviction, resume, pin, unpin and
   audit rows; the `cmd/netmapper` tests.
3. Crawl gate in `jobrunner.Start`; state re-checks in the resolver and the projector, with their
   tests.
4. API: envelope fields, 410, `baseline_state`; Bruno requests.
5. Documents and contracts; operator key in the dev stack; constitution PATCH.
6. Quickstart section 2 on a running stack.

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
| ---- | ---------- | ------------------------------------ |
| `evicting` state and `eviction_object` work list | an eviction must survive a crash between the database and the object store, and must keep crawls out until it ends (R5) | deleting objects first breaks served evidence on a crash; a lock held by the command is released by a crash |
| Owner functions instead of grants | 0004: only the owner deletes the collected zone or drops partitions (R1) | granting `DELETE` to the operator widens every operator command |
