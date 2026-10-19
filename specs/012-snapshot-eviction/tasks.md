---
description: "Task list for snapshot eviction and pinning"
---

# Tasks: Snapshot eviction and pinning

**Input**: Design documents from `/specs/012-snapshot-eviction/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/cli.md,
contracts/rest.md, quickstart.md

**Tests**: Included. The constitution asks that a path be tested under the role that runs it, and
quickstart section 1 names the tests that prove each success criterion.

**Organization**: by user story. US1 (evict, P1) is the MVP and carries most of the work: the
eviction function, the crawl gate, the engine re-checks, the command and the 410. US2 (pin, P2)
adds the setter and the commands; the pinned refusal itself is in US1's function, since the column
exists from Phase 2. US3 (history, P3) adds what a reader needs to understand a tombstone.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code.
- No task needs a lab or the `NetLab` VM. Integration tests need the dev stack
  (`NETMAPPER_TEST_DSN`, `NETMAPPER_TEST_S3_ENDPOINT`); check `docker ps` for `netmapper-trial`
  first, it publishes the same ports.
- Command tests follow `cmd/netmapper/token_test.go:29`: a `testutil.NewLab` on a fake network,
  `l.Crawl(doc)`, then `NETMAPPER_DSN` set to the test DSN with
  `options=-c search_path=<schema> -c role=netmapper_operator`, so the command runs under the
  operator role. Not `decide_test.go`, whose DSN has no role and runs as superuser.

---

## Phase 1: Setup

- [ ] T001 Create `migrations/0013_snapshot_eviction.sql` with empty `-- +goose Up` and
  `-- +goose Down` sections and a header comment naming feature 012 and research R1, R3, R5, R9,
  R11; check `store.Migrate` still runs with `go test ./internal/store/`

---

## Phase 2: Foundational (blocks every story)

- [ ] T002 In `migrations/0013_snapshot_eviction.sql`, change `snapshot`: state check to
  `state IN ('open', 'closed', 'evicting', 'evicted')`; add `pinned boolean NOT NULL DEFAULT false`
  and `evicted_at timestamptz NULL`; replace `CHECK ((state = 'closed') = (closed_at IS NOT NULL))`
  with `CHECK ((state = 'open') = (closed_at IS NULL))`; add
  `CHECK ((state IN ('evicting', 'evicted')) = (evicted_at IS NOT NULL))` (data-model.md). Down
  section restores the 0002 checks and drops the columns.
- [ ] T003 Append to Principle II in `.specify/memory/constitution.md` the wording of plan.md
  "Note on Principle II" (approved 2026-10-19), before the trigger that relies on it; bump the
  version to 1.2.2 (PATCH) and set "Last Amended" to the change date
- [ ] T004 In `migrations/0013_snapshot_eviction.sql`, replace `snapshot_closed_is_final()` with a
  trigger function that allows: any update on `open`; on `closed`, either only `pinned` changing,
  or `state` to `evicting` with `evicted_at` set and nothing else changing; on `evicting`, only
  `state` to `evicted`; nothing on `evicted` (data-model.md "State transitions", research R9).
  Owner `netmapper_owner`. Down restores the 0002 function.
- [ ] T005 In `migrations/0013_snapshot_eviction.sql`, change `audit_log`: action check gains
  `snapshot.evict`, `snapshot.pin`, `snapshot.unpin`; `target` drops `NOT NULL` and gains
  `CHECK (target IS NOT NULL OR action LIKE 'snapshot.%')`; `GRANT INSERT ON audit_log TO
  netmapper_operator` (research R11). Down deletes the `snapshot.%` rows first, then restores.
- [ ] T006 In `migrations/0013_snapshot_eviction.sql`, add `crawl_gate_key()`, an `IMMUTABLE` SQL
  function returning the advisory lock key of the crawl gate, owned by `netmapper_owner`, `EXECUTE`
  to `netmapper_operator` (plan "Structure Decision", research R6)
- [ ] T007 [P] Add a `snapshot` entry to the `commands` map and the usage line in
  `cmd/netmapper/main.go`, and `exitPreview = 3` next to the other exit codes; create
  `cmd/netmapper/snapshot.go` with `cmdSnapshot` dispatching `evict`, `pin`, `unpin` like
  `cmdToken`, each subcommand reading `--id` (missing or non-positive: exit 2), and a helper that
  writes one `audit_log` row (`ref` random uuid, `actor` from `osUser()`, `action`, `target` null,
  `command` `snapshot <id>`, `result`) (contracts/cli.md)
- [ ] T008 [P] Add to `internal/store/roles_test.go` `TestSnapshotLifecycleTrigger`: as owner, on a
  closed snapshot, `UPDATE snapshot SET pinned = true` succeeds, changing `job_id` or `closed_at`
  fails, `closed` to `open` fails, `evicted` row accepts no update; and
  `TestOperatorCannotTouchCollectedZone`: under `netmapper_operator`, `DELETE` on `observation`,
  `observation_raw`, `identifier_claim`, `raw_object`, `UPDATE snapshot`, `UPDATE audit_log`,
  `DELETE FROM audit_log` are all refused, `INSERT INTO audit_log` with action `snapshot.pin` and a
  null target succeeds, and `INSERT` with action `ssh.command` and a null target fails; and
  `TestAPIGainsNothingFromEviction`: under `netmapper_api`, `SELECT evict_snapshot(…)`,
  `finish_snapshot_eviction(…)`, `set_snapshot_pinned(…)`, any access to `eviction_object`,
  `INSERT INTO audit_log` and `UPDATE snapshot` are all refused (FR-016)

**Checkpoint**: migrations apply, the role tests pass, `netmapper snapshot` prints its usage.

---

## Phase 3: User Story 1 - Remove a broken crawl from the history (Priority: P1) MVP

**Goal**: `netmapper snapshot evict --id N [--yes]` removes a snapshot's data and its unshared
outputs, leaves a tombstone, and the API stops answering from it.

**Independent Test**: crawl twice on a fake network, evict the second, check database, object store
and API (quickstart section 1, US1 rows; section 2 steps 3 to 8).

### Tests for User Story 1

- [ ] T009 [P] [US1] `TestEvictProjectedSnapshot` in `cmd/netmapper/snapshot_test.go`: on a
  resolved, projected snapshot with at least one finding and one L2 domain, preview exits 3 and
  changes no row; `--yes` exits 0; a count over every per-snapshot table of data-model.md "Rows
  removed by an eviction" is 0 for its id; the `snapshot` row is `evicted` with `evicted_at` and
  its `snapshot_judgement` rows remain; one `snapshot.evict` audit row with result `ok` (SC-001)
- [ ] T010 [P] [US1] `TestEvictSharedOutputs` in `cmd/netmapper/snapshot_test.go`: two crawls of the
  same fake devices share outputs, plus one output stored in the bucket that no row references;
  after evicting the first, every hash of the second's `observation_raw` is readable through
  `RawStore.Get`, the hashes only the first used are gone from the bucket and from `raw_object`,
  the pre-existing orphan is still there; the printed `objects_deleted`, `objects_kept` and
  `bytes_deleted` match (SC-002, FR-015)
- [ ] T011 [P] [US1] `TestEvictRefusals` in `cmd/netmapper/snapshot_test.go`: open snapshot,
  unknown id, a job `running` in another perimeter, a sibling of the same perimeter `closed`
  without `resolution`, and `--yes` with `NETMAPPER_S3_BUCKET` unset: each exits 2 with the
  message of contracts/cli.md, and the row counts of the database and the object list of the
  bucket are unchanged; each `--yes` refusal of an existing snapshot (open, crawl running,
  unresolved sibling) writes one `refused: <reason>` audit row; the unknown id and the missing
  variable are invalid input and write none; a preview of a refused snapshot exits 2 and writes
  none (SC-005)
- [ ] T012 [P] [US1] `TestEvictResume` in `cmd/netmapper/snapshot_test.go`: with
  `NETMAPPER_S3_ENDPOINT` pointing at a closed port, `--yes` exits 1, prints the "incomplete"
  message, leaves the snapshot `evicting` with its `eviction_object` rows; `jobrunner.Start` is
  refused; with the endpoint back, the same command exits 0, the snapshot is `evicted`,
  `eviction_object` is empty, one `incomplete:` and one `ok` audit row (SC-004). Before the
  rerun, a preview on the `evicting` snapshot exits 3, prints `result: resume` and lists the hash
  of every object still to delete, and writes no audit row
- [ ] T013 [P] [US1] `TestEvictTwice` in `cmd/netmapper/snapshot_test.go`: a second `--yes` and a
  preview on an `evicted` snapshot both exit 0 with `nothing to do`; the `--yes` writes a `noop`
  audit row, the preview none
- [ ] T014 [P] [US1] `TestEvictRegistry` in `cmd/netmapper/snapshot_test.go`: snapshot A sees
  devices a and b, snapshot B sees a only; evicting A deletes b's `device` and `device_identifier`
  rows and keeps a's with the same `key`, `first_seen`, `last_seen` and `minted_from`; then a
  device row left over in the perimeter with no entity anywhere is deleted by the next eviction;
  `devices_deleted` matches (SC-007, FR-012)
- [ ] T015 [P] [US1] `TestStartRefusedWhileEvicting` in `internal/jobrunner/start_test.go`: with a
  snapshot set to `evicting` (as owner), `Start` returns an error naming it and inserts no job
- [ ] T016 [P] [US1] In `internal/entity/entity_test.go` and `internal/graph/graph_test.go`: a
  resolution and a projection whose snapshot is moved to `evicting` after their state check and
  before their lock write nothing and return `ErrNotClosed`; run under `netmapper_engine`
- [ ] T017 [P] [US1] `TestJudgeRefusesEvicted` in `internal/gate/gate_test.go`: `judge_snapshot` under
  `netmapper_engine` raises when the snapshot or the baseline it names is `evicting` or `evicted`
- [ ] T018 [P] [US1] `TestEvictedSnapshot` in `internal/api/snapshot_test.go`: under
  `netmapper_api`, `?snapshot=<evicted>` and `?snapshot=<evicting>` on the devices, device,
  interface, facts, findings, observation and observation raw endpoints answer 410
  `{"error": "snapshot_evicted", "snapshot": {…}}`; without `?snapshot=`, the default is the most
  recent `closed` snapshot with a graph, or 409 `not_projected` with `snapshot: null` when none is
  left; `GET /v1/observations/{id}` of an evicted observation without `?snapshot=` is 404
  `no_such_observation`; after evicting one of two snapshots that share outputs, `GET
  /v1/observations/{id}/raw` for observations of the remaining snapshot answers 200 with the
  stored bytes, for every step whose hash the evicted snapshot also used (SC-006, US1 scenario 2)

### Implementation for User Story 1

- [ ] T019 [US1] In `migrations/0013_snapshot_eviction.sql`, create `eviction_object (snapshot_id
  bigint NOT NULL REFERENCES snapshot, hash bytea NOT NULL, PRIMARY KEY (snapshot_id, hash))`,
  owner `netmapper_owner`, `GRANT SELECT, DELETE` to `netmapper_operator`, no other role
  (data-model.md)
- [ ] T020 [US1] In `migrations/0013_snapshot_eviction.sql`, create `evict_snapshot(p_snapshot_id
  bigint, p_apply boolean)`, `SECURITY DEFINER SET search_path FROM CURRENT`, owner
  `netmapper_owner`, `EXECUTE` to `netmapper_operator` only, returning the row of data-model.md
  (`outcome`, `reason`, `blocking_snapshot`, `blocking_job`, `observations`, `objects_deleted`,
  `bytes_deleted`, `objects_kept`, `devices_deleted`). In order:
  locks of research R6 (crawl gate, perimeter lock `hashtextextended(perimeter, 0)`, projection
  lock `(4, id)` (`projectionLockClass` in `internal/graph/graph.go`; a comment there names the SQL copy),
  snapshot row `FOR UPDATE`); checks of research R7 in their order; counts; and with `p_apply`, the
  steps of research R3 (work list, computed zone, partitions `observation_raw_<id>`,
  `identifier_claim_<id>`, `observation_<id>` detached and dropped, `task_<job_id>` detached and
  dropped, `parse_generation`, `raw_object` rows of the work list, registry cleanup of research R8,
  state `evicting` with `evicted_at = now()`). On `evicting` it returns `resume` with the
  remaining work list counted. Down drops it.
- [ ] T021 [US1] In `migrations/0013_snapshot_eviction.sql`, create
  `finish_snapshot_eviction(p_snapshot_id bigint)`: raises unless the snapshot is `evicting` and
  has no `eviction_object` row, then sets `state = 'evicted'`; owner, `EXECUTE` to the operator
- [ ] T022 [US1] In `migrations/0013_snapshot_eviction.sql`, replace `judge_snapshot` with the same
  signature and body plus a `SELECT ... FOR SHARE` on the snapshot and its baseline that raises
  when either is not `closed` (research R6); keep its grants
- [ ] T023 [P] [US1] Add `RawStore.Delete(ctx, hash)` in `internal/store/raw.go`: `RemoveObject` on
  `RawKey(hash)`; a missing object is not an error (research R5)
- [ ] T024 [US1] In `internal/jobrunner/start.go`, inside the existing transaction and before the
  job insert, take `pg_advisory_xact_lock(crawl_gate_key())` and refuse with an `Invalid` entry
  `snapshot N is being evicted: finish it with netmapper snapshot evict --id N --yes` when any
  snapshot is `evicting` (contracts/cli.md)
- [ ] T025 [P] [US1] In `internal/entity/entity.go` `resolveIn`, after the perimeter lock, re-read
  the snapshot's state and return `ErrNotClosed` unless it is `closed` (research R6)
- [ ] T026 [P] [US1] In `internal/graph/graph.go` `projectIn`, after the projection lock, re-read
  the snapshot's state and return `ErrNotClosed` unless it is `closed` (research R6); add to the
  `projectionLockClass` comment that `evict_snapshot` uses the same value in SQL
- [ ] T027 [US1] Implement `snapshot evict` in `cmd/netmapper/snapshot.go`: call
  `evict_snapshot(id, false)` without `--yes`; print the `key: value` block of contracts/cli.md
  (`snapshot`, `closed_at`, `verdict`, `pinned`, the counts, `result`), exit 3 on `preview`, 2 on
  `refused` or `unknown` with the stderr messages of contracts/cli.md, 0 on `noop`; on `resume`,
  print `result: resume` and one `pending: <hex hash>` line per `eviction_object` row, exit 3. With `--yes`:
  check the `NETMAPPER_S3_*` variables through `rawStore()` before any database call (exit 2 when
  missing), call `evict_snapshot(id, true)`, then for each `eviction_object` row of the snapshot
  `RawStore.Delete` and `DELETE` the row, then `finish_snapshot_eviction`; on a store error exit 1
  with the "incomplete" message. Write one audit row per `--yes` run on an existing snapshot:
  `ok`, `noop`, `refused: <reason>` or `incomplete: <error>`. Write none for a preview, an unknown
  id or missing `NETMAPPER_S3_*` variables: those are invalid input (contracts/cli.md).
- [ ] T028 [US1] In `internal/api/snapshot.go`, `pickSnapshot`: a named snapshot in `evicting` or
  `evicted` answers 410 `{"error": "snapshot_evicted", "snapshot": {…}}`, before any
  `not_projected` check (contracts/rest.md)
- [ ] T029 [US1] In `internal/api/raw.go`, `observationScope` (or the handlers that call it, since
  it has no transaction): a named snapshot in `evicting` or `evicted` answers the same 410
  (contracts/rest.md)

**Checkpoint**: T009 to T018 pass; quickstart section 2 steps 3 to 8 can be run without the pin
steps.

---

## Phase 4: User Story 2 - Protect a snapshot from eviction (Priority: P2)

**Goal**: `netmapper snapshot pin|unpin --id N`, idempotent, and the flag shown by the API.

**Independent Test**: pin, evict (refused), unpin, evict (done) (quickstart section 2 steps 2, 4, 9).

### Tests for User Story 2

- [ ] T030 [P] [US2] `TestPinUnpin` in `cmd/netmapper/snapshot_test.go`: pin prints `pinned`, pin
  again `nothing to do`, evict `--yes` exits 2 `snapshot N is pinned: unpin it first` and changes
  nothing, unpin prints `unpinned`, unpin again `nothing to do`, evict succeeds; pin on an open
  snapshot succeeds; pin on an `evicted` snapshot exits 2; one audit row per pin and unpin run
  (`ok`, `noop` or `refused: <reason>`)
- [ ] T031 [P] [US2] In `internal/api/snapshot_test.go`, `/v1/snapshots` and `/v1/snapshots/{id}`
  show `pinned` for every snapshot, `false` by default

### Implementation for User Story 2

- [ ] T032 [US2] In `migrations/0013_snapshot_eviction.sql`, create `set_snapshot_pinned(p_snapshot_id
  bigint, p_pinned boolean)` returning `ok`, `noop`, `refused` or `unknown`; refuses `evicting` and
  `evicted`; owner, `SECURITY DEFINER`, `EXECUTE` to `netmapper_operator` only (data-model.md)
- [ ] T033 [US2] Implement `snapshot pin` and `snapshot unpin` in `cmd/netmapper/snapshot.go` on
  `set_snapshot_pinned`: output and exit codes of contracts/cli.md, one audit row each
- [ ] T034 [P] [US2] Add `Pinned bool json:"pinned"` to `SnapshotRef` and `s.pinned` to
  `snapshotColumns` and `scanSnapshot` in `internal/api/snapshot.go`

**Checkpoint**: T030 and T031 pass; US1 tests still pass.

---

## Phase 5: User Story 3 - Know what happened to a snapshot (Priority: P3)

**Goal**: a tombstone and the verdicts around it explain themselves.

**Independent Test**: evict a baseline, read the later verdict, the tombstone and the audit trail.

### Tests for User Story 3

- [ ] T035 [P] [US3] `TestEvictBaseline` in `cmd/netmapper/snapshot_test.go`: three crawls A, B, C
  of one perimeter; evict B (C's baseline); C's active judgement keeps `baseline_snapshot_id = B`
  and its `coverage`; `netmapper judge` on C then records a new judgement with baseline A (FR-013,
  FR-011)
- [ ] T036 [P] [US3] In `internal/api/snapshot_test.go`, `GET /v1/snapshots/{id}` of an evicted
  snapshot answers 200 with `state: evicted`, `evicted_at`, `closed_at` and its verdict; the
  judgement of C shows `baseline_state: evicted`; `baseline_state` is null without a baseline
- [ ] T037 [P] [US3] `TestEvictAuditTrail` in `cmd/netmapper/snapshot_test.go`: after a pin, a
  refused eviction, an unpin and an eviction, `audit_log` holds exactly four `snapshot.%` rows in
  order, each with actor, time, `command = 'snapshot <id>'` and its result

### Implementation for User Story 3

- [ ] T038 [US3] Add `EvictedAt *time.Time json:"evicted_at"` to `SnapshotRef` with
  `s.evicted_at` in `snapshotColumns` and `scanSnapshot`, and `BaselineState *string
  json:"baseline_state"` to `judgement` read from the baseline's `snapshot` row, in
  `internal/api/snapshot.go` (contracts/rest.md)

**Checkpoint**: every test of quickstart section 1 passes.

---

## Phase 6: Polish & cross-cutting

- [ ] T039 [P] In `deploy/compose.yaml` `garage-init`, import a second key for the operator with
  `read` and `write` on the bucket, and state in the header comment which key is which
- [ ] T040 [P] In `docs/how-to/deploy.md`: three keys as the default (collector read and write, API
  read only, a dedicated operator key that can read and delete, with the per-action list for
  AWS S3 or MinIO and the Garage note of research R10); the operator's `NETMAPPER_S3_*` for
  `snapshot evict --yes`; the three `snapshot` commands with an example; exit code 3 ("preview
  only, nothing done") next to 1 and 2 in the exit code sentence of the introduction
- [ ] T041 [P] In `README.md`, add the `snapshot` commands to the operator commands and to the
  usage line of `cmd/netmapper/main.go`'s package comment
- [ ] T042 [P] In `docs/c4-model/04-data-model.md`: snapshot states `open`, `closed`, `evicting`,
  `evicted` (the verdict lives in `snapshot_judgement`, not in the state), `pinned`, `evicted_at`,
  `eviction_object`, the registry cleanup rule, the tombstone; under "Still open", the sweep of
  outputs orphaned outside eviction and a retention policy
- [ ] T043 [P] Merge `specs/012-snapshot-eviction/contracts/cli.md` into
  `specs/001-crawl-loop/contracts/cli.md` and `contracts/rest.md` into
  `specs/005-read-api/contracts/rest.md` (error table: the 410 row; envelope: `pinned`,
  `evicted_at`; judgement: `baseline_state`)
- [ ] T044 [P] In `bruno/snapshots/`, add `get-evicted-snapshot.bru` asserting the 410 on
  `/v1/devices?snapshot={{evicted_snapshot}}`, and the `pinned` and `evicted_at` fields to the
  assertions of `list-snapshots.bru` and `get-snapshot.bru`
- [ ] T045 Run `go test -count=1 ./...` against the dev stack: everything passes, nothing skipped
- [ ] T046 Quickstart section 2 on a running stack (ask before using `netmapper-trial`), and record
  the result in the PR description

---

## Dependencies

- T001 before everything. Phase 2 (T002 to T008) before any story. T003 (constitution PATCH)
  before T004 (the trigger it allows).
- US1: T019 before T020; T020 before T021, T027; T023 before T027; T006 before T020 and T024. Tests
  T009 to T018 are written first and fail until T019 to T029 land.
- US2 needs Phase 2 only for T032 to T034; T030's "evict refused" assertion needs US1's T020 and
  T027.
- US3 needs US1 (an eviction to look at) and US2's T034 for the shared `snapshotColumns` edit; T038
  edits the same file as T028 and T034, so it follows them.
- Phase 6 after the stories it documents; T045 and T046 last.

Story order: US1, then US2 and US3 (US3 after US2 because of `internal/api/snapshot.go`).

## Parallel examples

- Phase 2: T007 and T008 alongside the migration tasks T002, T004 to T006 (different files).
- US1 tests: T009 to T014 are separate test functions in one new file and can be written together;
  T015, T016, T017, T018 are in four other packages.
- US1 implementation: T023, T025, T026 in parallel with the migration tasks T019 to T022.
- Phase 6: T039 to T044 all touch different files.

## Implementation strategy

MVP is US1: once Phase 3 passes, a broken crawl can be removed and the API answers correctly. Pins
(US2) are the guard that makes eviction safe to leave in a script; ship it in the same PR unless
US1 alone is urgent. US3 adds no behaviour, only what a reader sees, and is small enough to land
with the other two.
