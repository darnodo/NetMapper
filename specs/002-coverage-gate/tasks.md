---
description: "Task list for the coverage gate (judge a closed snapshot)"
---

# Tasks: Coverage gate (judge a closed snapshot)

**Input**: Design documents from `/specs/002-coverage-gate/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Included. [quickstart.md](quickstart.md) section 1 lists the cases this feature must pass,
and FR-007/FR-012 (same inputs, same verdict) are only meaningful as tests.

**Organization**: Tasks are grouped by user story so each story can be built and tested on its own.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3 from spec.md
- Paths are relative to the repository root. Same module and layout as 001: `cmd/` plus `internal/`,
  tests next to the code (plan.md, Project Structure).

Integration tests use `internal/testutil` and skip when `NETMAPPER_TEST_DSN` is unset. This feature
touches no object store, so no test needs `NETMAPPER_TEST_S3_ENDPOINT`.

---

## Phase 1: Setup

**Purpose**: The package this feature lives in, and the test fixture every test needs

- [X] T001 Create the package skeleton in internal/gate/gate.go: the `Judgement` struct mirroring the
  `snapshot_judgement` columns of data-model.md, the classification constants `published`, `degraded`,
  `quarantined`, and `const gateVersion = 1` with a comment stating it is bumped by hand when the
  coverage calculation changes in a way that alters results (research R9)
- [X] T002 [P] ~~Add a snapshot fixture builder to internal/testutil/snapshot.go~~ **Dropped**:
  `testutil.Lab` from 001 already produces closed snapshots by running real crawls against the fake
  network, and calling `l.Crawl(Doc)` twice with the network changed in between gives a baseline and
  a successor with far more fidelity than hand-inserted rows. Hand-built observations would also have
  let a wrong assumption about what 001 actually writes pass unnoticed. No fixture code was added.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, function and grants. No story work can begin until this phase is complete

**⚠️ CRITICAL**: The engine has no `INSERT` right anywhere in 001's grant matrix and no sequence
usage; both gaps are closed here (research R4)

- [X] T003 Create migrations/0005_judgement.sql with the `snapshot_judgement` table exactly as in
  data-model.md: `id bigserial PRIMARY KEY`, `snapshot_id bigint NOT NULL REFERENCES snapshot`,
  `baseline_snapshot_id bigint NULL REFERENCES snapshot`,
  `classification text NOT NULL CHECK (classification IN ('published','degraded','quarantined'))`,
  `coverage numeric(5,4) NULL`, `baseline_devices integer NOT NULL`, `carried_over integer NOT NULL`,
  `reached integer NOT NULL`, `breakdown jsonb NOT NULL DEFAULT '{}'`, `thresholds jsonb NOT NULL`,
  `gate_version integer NOT NULL`, `active boolean NOT NULL DEFAULT true`,
  `computed_at timestamptz NOT NULL DEFAULT now()`, plus
  `CHECK ((baseline_snapshot_id IS NULL) = (coverage IS NULL))` and
  `CHECK (carried_over <= baseline_devices)`
- [X] T004 Add `CREATE UNIQUE INDEX snapshot_judgement_active ON snapshot_judgement (snapshot_id) WHERE active;`
  to migrations/0005_judgement.sql: this index is the whole of FR-001's "exactly one active judgement"
  (research R5)
- [X] T005 Add the `judge_snapshot` `SECURITY DEFINER` function owned by `netmapper_owner` to
  migrations/0005_judgement.sql: in one transaction it sets `active = false` on the snapshot's current
  active row if there is one, then inserts the new row with `active = true`, and returns its id. It is
  the only write path to the table, so "rewrite a verdict in place" is not expressible (FR-009)
- [X] T006 [P] Add to migrations/0005_judgement.sql the two `perimeter` columns:
  `degraded_at numeric(5,4) NULL CHECK (degraded_at > 0 AND degraded_at <= 1)` and
  `quarantined_below numeric(5,4) NULL CHECK (quarantined_below > 0 AND quarantined_below <= 1)`,
  plus `CHECK (degraded_at IS NULL OR quarantined_below IS NULL OR quarantined_below <= degraded_at)`
- [X] T007 Add the grants to migrations/0005_judgement.sql: `SELECT, INSERT` on `snapshot_judgement`
  and `USAGE` on `snapshot_judgement_id_seq` to `netmapper_engine`, `EXECUTE` on `judge_snapshot` to
  `netmapper_engine`, `SELECT` on the table to `netmapper_operator` and `netmapper_collector`. No role
  but the owner gets `UPDATE` or `DELETE`
- [X] T008 Immutability test in internal/store/roles_test.go: connected as `netmapper_engine`,
  `UPDATE` and `DELETE` on `snapshot_judgement` both fail, while `judge_snapshot` succeeds (FR-009)

**Checkpoint**: Schema ready. Story work can begin.

---

## Phase 3: User Story 1 - Trust a snapshot before acting on it (Priority: P1) 🎯 MVP

**Goal**: Every closed snapshot ends with exactly one active verdict, computed against the right
baseline, reproducible, and repaired by a sweep if the engine dies.

**Independent Test**: Build two snapshots of one perimeter with the fixture, the second missing a
device the first reached; the second ends `quarantined` with coverage 0.5 and the first ends
`published` with no baseline. On the lab, quickstart.md sections 2 and 3.

### Tests for User Story 1

> Write these first and confirm they fail.

- [X] T009 [P] [US1] First-snapshot test in internal/gate/baseline_test.go: a perimeter's first
  closed snapshot is judged `published` with `baseline_snapshot_id` null and `coverage` null, and its
  breakdown records `no_baseline` (FR-004, research R10)
- [X] T010 [P] [US1] Unchanged-run test in internal/gate/gate_test.go: a second snapshot reaching the
  same devices as the first is `published` with `coverage = 1.0000` and `carried_over = baseline_devices`
  - Covered by `TestBaselineMatchesPerimeterByName` in internal/gate/baseline_test.go rather than its
    own test: that test already runs the same network twice and asserts the stored row reads
    `published 1 <baseline> 1 default`.
- [X] T011 [P] [US1] Loss test in internal/gate/gate_test.go: a baseline of two devices and a snapshot
  reaching one is `quarantined` with `coverage = 0.5000` under default thresholds
  - Covered by `TestDeviceThatLeftDiscovery` in internal/gate/compare_test.go.
- [X] T012 [P] [US1] Renumbering test in internal/gate/compare_test.go: a device whose address changed
  between the two snapshots but whose strong claim is unchanged counts as carried over, not as one
  loss plus one arrival (research R2)
- [X] T013 [P] [US1] Perimeter identity test in internal/gate/baseline_test.go: two snapshots whose
  perimeters share a name but belong to different `config_version` rows, and therefore have different
  `perimeter.id` values, are compared against each other. Without this the feature is silently inert
  (research R1)
- [X] T014 [P] [US1] Baseline order test in internal/gate/baseline_test.go: with three closed
  snapshots, the middle one's baseline is the oldest, not the newest; re-judging the middle one after
  the newest closed resolves the same baseline (FR-015, FR-012)
- [X] T015 [P] [US1] Perimeter narrowing test in internal/gate/compare_test.go: a baseline device
  whose addresses all fall outside the newer snapshot's perimeter is dropped from `baseline_devices`
  and does not count as a loss (research R8, spec edge case)
- [X] T016 [P] [US1] Sweep test in internal/jobrunner/judge_test.go: a snapshot closed directly
  against the database with no runner running is judged on the next `Tick`, ending with exactly one
  active judgement (FR-014, US1-6)
- [X] T017 [P] [US1] Re-judge test in internal/gate/gate_test.go: judging an already judged snapshot
  writes a new active row, leaves the previous row readable with `active = false` and its original
  figures, and reproduces the same classification and coverage (FR-009, FR-012, SC-003)
- [X] T018 [P] [US1] Refusal test in internal/gate/gate_test.go: judging a snapshot in state `open`
  returns an error and writes no row (FR-001)
- [X] T019 [P] [US1] Concurrency test in internal/gate/gate_test.go: two goroutines judging the same
  snapshot at once leave exactly one active row, the loser failing on the partial unique index
  (research R12)

### Implementation for User Story 1

- [X] T020 [US1] Implement baseline selection in internal/gate/baseline.go: resolve the snapshot's
  perimeter name through `job.parameters->>'perimeter_id'` joined to `perimeter`, then select the
  snapshot of that perimeter **name** with the greatest `(closed_at, id)` strictly earlier than the
  one being judged, among those carrying an active judgement; return "no baseline" when there is none
  (research R1, R7)
- [X] T021 [US1] Implement the comparison in internal/gate/compare.go as one SQL statement: match
  baseline devices to newer-snapshot devices on shared `identifier_claim` rows with
  `strength = 'strong'` joined on `(kind, value)`, falling back to `observation.target` for baseline
  devices with no strong claim; a device is reached when it has an `identity` observation with
  `status = 'collected'` (research R2, R11)
- [X] T022 [US1] Apply perimeter filtering in internal/gate/compare.go: drop from the baseline set
  every device whose addresses all fall outside the include/exclude ranges of the perimeter row of
  the judged snapshot's own config version, reusing `perimeter.Allowed` from 001 (research R8)
  - Divergence: the include-then-exclude test is expressed in SQL (`target <<= ANY(include) AND NOT
    (target <<= ANY(exclude))`) rather than by loading the perimeter and calling `perimeter.Allowed`.
    Same semantics, and it keeps the comparison in the single statement R11 asked for. Filtered
    devices leave the denominator and are listed under `breakdown.perimeter_filtered`.
- [X] T023 [US1] Implement classification in internal/gate/thresholds.go with the defaults only:
  `published` at coverage `1.0`, `degraded` at `>= 0.9`, `quarantined` below, and `published` with a
  null coverage when there is no baseline; record the applied values and `"source": "default"` in the
  `thresholds` column (FR-002, FR-005 defaults, research R10)
- [X] T024 [US1] Implement `Judge(ctx, db, snapshotID)` in internal/gate/gate.go: refuse a snapshot
  that is not `closed`, select the baseline, run the comparison, classify, then call `judge_snapshot`
  in one short transaction. This is the single entry point the sweep, the CLI and the tests all use,
  which is what makes FR-012 testable (plan.md, Structure Decision)
- [X] T025 [US1] Add the judging step to internal/jobrunner/runner.go `Tick`, independent of its loop
  over active jobs: select closed snapshots with no active judgement ordered by `closed_at, id`
  ascending, and call `gate.Judge` for each; a snapshot whose predecessor is closed but unjudged is
  left for a later tick, and a failing judgement is logged and retried next tick rather than written
  wrong (FR-014, research R6, R7)
- [X] T026 [US1] Wire `netmapper judge <snapshot-id>` in cmd/netmapper/judge.go per contracts/cli.md:
  print `<classification> <carried_over>/<baseline_devices> (baseline snapshot <id>)` or
  `<classification> no baseline` on stdout; exit 2 with `snapshot <id> not found` or
  `snapshot <id> is not closed`; judge a never-judged snapshot normally; never cascade to successors
  (FR-013)
- [X] T027 [US1] Add the `judge` subcommand to the dispatch in cmd/netmapper/main.go

**Checkpoint**: User Story 1 works on its own: run quickstart.md sections 2 and 3.

---

## Phase 4: User Story 2 - Understand the gap, not just the verdict (Priority: P2)

**Goal**: A verdict carries the figures that explain it, and a device that silently left discovery is
named as such rather than folded into a generic count.

**Independent Test**: Two snapshots judged degraded for different reasons (one device gone
unreachable, one never attempted) each carry a breakdown naming their own cause. On the lab,
quickstart.md section 3.

### Tests for User Story 2

- [X] T028 [P] [US2] Breakdown test in internal/gate/compare_test.go: a baseline device that is
  `unreachable` in the newer snapshot, one that is `denied`, and one with no `identity` observation at
  any of its baseline addresses produce counts under `unreachable`, `denied` and `not_attempted`
  respectively, each with the addresses behind it, and `missing` sums to
  `baseline_devices - carried_over` (FR-006)
- [X] T029 [P] [US2] Not-attempted test in internal/gate/compare_test.go: a device dropped from
  discovery entirely, with no task and no observation in the newer snapshot, is reported as
  `not_attempted` and not as `unreachable`. This is the shape of the LLDP bug found in 001 and the
  reason the feature exists (SC-006, US2-2)
- [X] T030 [P] [US2] Multi-address test in internal/gate/compare_test.go: a baseline device known by
  two addresses, one of which is retried and fails in the newer snapshot, takes that failure's status
  as its reason rather than `not_attempted` (research R3)

### Implementation for User Story 2

- [X] T031 [US2] Extend the comparison in internal/gate/compare.go to attribute a reason to every
  baseline device that did not carry over: look for an `identity` observation in the newer snapshot on
  any address the device was known by in the baseline (its observation's `target` plus the targets of
  tasks completed `duplicate` against its claims); the reason is that observation's `status`, and
  `not_attempted` when none exists (research R3)
  - Done. Writing T030 first exposed a real defect in the T029 version: 001 writes a second, equally
    `collected` identity observation for each further address a device answered on, marked with
    `duplicate_of_task`, so counting identity observations counted such a device twice and inflated
    both the denominator and `reached`. A device is now its winning observation, with the duplicates'
    addresses kept as further handles on it.
- [X] T032 [US2] Write the `breakdown` jsonb in internal/gate/gate.go exactly as shaped in
  data-model.md: `no_baseline`, `missing` with the five reasons each carrying `count` and `targets`,
  and `perimeter_filtered` with the devices dropped by T022 (FR-006)
- [X] T033 [US2] Record the remaining figures in internal/gate/gate.go: `baseline_devices`,
  `carried_over`, `reached`, `computed_at` and `gate_version`, so a reader can answer "why not
  published" from the row alone (FR-006, FR-010, SC-002)

**Checkpoint**: User Stories 1 and 2 both pass their independent tests.

---

## Phase 5: User Story 3 - Configure how strict a perimeter's gate is (Priority: P3)

**Goal**: A perimeter declares its own thresholds in the configuration document, pinned to the config
version its snapshot ran under.

**Independent Test**: The same coverage figure classified `degraded` under one perimeter's declared
thresholds and `quarantined` under the defaults. On the lab, quickstart.md section 4.

### Tests for User Story 3

- [X] T034 [P] [US3] Threshold test in internal/gate/thresholds_test.go: coverage `0.5` is
  `quarantined` under the defaults and `degraded` under a perimeter declaring `degraded_at: 0.5`, and
  the judgement records `"source": "perimeter"` instead of `"default"` (FR-005, US3)
- [X] T035 [P] [US3] Config validation test in internal/config/config_test.go: the three messages of
  contracts/config.md are produced for out-of-range and crossed values, and a perimeter declaring one
  key takes the default for the other
- [X] T036 [P] [US3] Pinning test in internal/gate/thresholds_test.go: changing the thresholds in the
  document and posting a new config version does not change an earlier snapshot's judgement, and a
  re-judge of that earlier snapshot still uses the thresholds of its own config version (FR-005,
  SC-005)

### Implementation for User Story 3

- [X] T037 [US3] Parse and validate `degraded_at` and `quarantined_below` per perimeter in
  internal/config/config.go: both optional, both fractions in `(0, 1]`, `quarantined_below` not
  greater than `degraded_at`, with the exact messages of contracts/config.md
- [X] T038 [US3] Write both columns from the document in internal/jobrunner/start.go, alongside the
  include and exclude ranges of the `perimeter` row it already inserts
- [X] T039 [US3] Read the judged snapshot's own perimeter row in internal/gate/thresholds.go and use
  its values when present, the defaults when null, recording which was used in the `thresholds` column
  (FR-005, research R13)

**Checkpoint**: All three stories pass their independent tests.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T040 [P] Carry the documentation deltas listed in plan.md into docs/c4-model/04-data-model.md:
  the `snapshot_judgement` table and its partial unique index, `judge_snapshot` as the only write
  path, the two `perimeter` columns, the engine's new `INSERT` and sequence grants, and the invariant
  that a perimeter's identity across config versions is its name
- [X] T041 [P] Extend specs/001-crawl-loop/contracts/cli.md's successor in
  specs/002-coverage-gate/contracts/cli.md if the implemented flags drift from it, and note any
  divergence rather than letting the contract go stale
- [X] T042 Run quickstart.md sections 1 to 6 against the lab and record any divergence in
  specs/002-coverage-gate/quickstart.md
  - Sections 1 to 5 run on the lab; section 6 stays an integration test. Section 4 found a real
    defect in the threshold pairing, fixed here, and its correction was applied to the affected
    snapshot through `netmapper judge` rather than a re-crawl.

---

## Dependencies & Execution Order

### Phase dependencies

- Setup (Phase 1): none
- Foundational (Phase 2): after Setup; blocks every story
- US1 (Phase 3): after Foundational
- US2 (Phase 4): after US1. It extends internal/gate/compare.go and internal/gate/gate.go written in
  US1
- US3 (Phase 5): after US1. Independent of US2 except that both touch internal/gate/gate.go for the
  columns they record: do T033 and T039 in sequence
- Polish (Phase 6): after the stories it documents

### Within Phase 2

- T003 → T004 → T005 → T007; T006 is independent of the table and can land in parallel
- T008 after T007

### Within each story

- Tests first and failing, then implementation
- US1: T020 → T021 → T022 → T023 → T024; T025 and T026 after T024; T027 after T026
- US2: T031 → T032 → T033
- US3: T037 → T038; T039 after T037

---

## Parallel Examples

### User Story 1 tests

```text
T009 baseline_test.go    T010 gate_test.go        T011 gate_test.go
T012 compare_test.go     T013 baseline_test.go    T014 baseline_test.go
T015 compare_test.go     T016 judge_test.go       T017 gate_test.go
T018 gate_test.go        T019 gate_test.go
```

### User Story 2 and 3 tests (once US1 is done, two people)

```text
T028 compare_test.go     T029 compare_test.go     T030 compare_test.go
T034 thresholds_test.go  T035 config_test.go      T036 thresholds_test.go
```

---

## Implementation Strategy

### MVP (User Story 1 only)

1. Phase 1 and Phase 2
2. Phase 3
3. Stop and validate: quickstart.md section 1 (US1 rows) and sections 2 and 3 on the lab

At that point every closed snapshot carries a verdict and a coverage figure. What is missing is the
per-reason breakdown (US2) and per-perimeter tuning (US3), both of which are additive.

### Incremental delivery

1. Foundation, then US1: a verdict on every snapshot, with the baseline chosen correctly
2. US2: the figures that explain a verdict, including `not_attempted`
3. US3: per-perimeter thresholds
4. Polish: documentation deltas, full quickstart run

### Two developers after US1

- Developer A: US2
- Developer B: US3, coordinating on internal/gate/gate.go (T033 and T039)

---

## Notes

- [P] means a different file and no dependency on an incomplete task
- Commit after each task or logical group, stating the constitution principle it touches
- This feature contacts no device and reads no pack: any task that needs a transport, a secret or an
  S3 client is a sign the scope has drifted
- `gateVersion` (T001) is bumped by hand when the calculation changes; nothing detects it for you
