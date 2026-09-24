---
description: "Task list for identity resolution (claims into device entities)"
---

# Tasks: Identity resolution (claims into device entities)

**Input**: Design documents from `/specs/003-identity-resolution/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Included. [quickstart.md](quickstart.md) section 1 lists the cases this feature must pass,
and FR-013 (same inputs, same grouping) and SC-008 (wipe and replay) are only meaningful as tests.

**Organization**: Tasks are grouped by user story so each story can be built and tested on its own.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3 from spec.md
- Paths are relative to the repository root. Same module and layout as 001 and 002: `cmd/` plus
  `internal/`, tests next to the code (plan.md, Project Structure).

Integration tests use `internal/testutil` and skip when `NETMAPPER_TEST_DSN` is unset. This feature
touches no object store, so no test needs `NETMAPPER_TEST_S3_ENDPOINT`.

---

## Phase 1: Setup

**Purpose**: The package this feature lives in, and what the tests need to build the device shapes it
has to tell apart

- [x] T001 Create the package skeleton in internal/entity/entity.go: the `Entity` struct mirroring the
  `entity` columns of data-model.md (`SnapshotID`, `DeviceKey`, `Weak`, `Attributes`, `FirstSeen`,
  `LastSeen`), a `ClaimGroup` struct holding one `identity` observation with its claims, the sentinel
  errors `ErrNotFound` and `ErrNotClosed` following internal/gate/gate.go, and
  `const resolverVersion = 1` with a comment stating it is bumped by hand when a change to the
  grouping alters results (research R9)
- [x] T002 [P] Add an identifier-shaping variant of `FakeOS` to internal/testutil/lab.go: a builder
  that prints only the `Serial:` line, only the `MAC:` line, both, or neither, so a test can produce a
  device with one strong identifier, two, or none at all. The fakeos pack in
  internal/pack/testdata/fakeos/pack.yaml already declares `serial` and `chassis_mac` as strong and
  `hostname` as weak, so no pack change is needed. Eleven tests from T012 to T022 need these shapes, so
  the helper earns its place

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema and grants. No story work can begin until this phase is complete

**⚠️ CRITICAL**: The engine gains its first `DELETE` outside the computed zone here (on `finding`,
for the replace rule of FR-015). The grant is wide in SQL and narrow in code; T011 is what proves the
rest of the matrix still holds

- [x] T003 Create migrations/0006_entity.sql with the `device` registry table exactly as in
  data-model.md: `perimeter_name text NOT NULL`, `key text NOT NULL`,
  `weak boolean NOT NULL DEFAULT false`, `first_seen timestamptz NOT NULL`,
  `last_seen timestamptz NOT NULL`, `minted_from bigint NOT NULL REFERENCES snapshot`, and
  `PRIMARY KEY (perimeter_name, key)`. The perimeter is named, not referenced by id, for the reason
  002 established in its research R1
- [x] T004 Add the `device_identifier` table to migrations/0006_entity.sql: `perimeter_name text NOT NULL`,
  `kind text NOT NULL`, `value text NOT NULL`, `key text NOT NULL`, `first_seen timestamptz NOT NULL`,
  `PRIMARY KEY (perimeter_name, kind, value)` and
  `FOREIGN KEY (perimeter_name, key) REFERENCES device`. The primary key is the invariant "one
  identifier belongs to one device"; a conflict on it is the R6 case to handle, never an error to
  swallow
- [x] T005 [P] Add the `resolution` table to migrations/0006_entity.sql:
  `snapshot_id bigint PRIMARY KEY REFERENCES snapshot`, `resolver_version integer NOT NULL`,
  `decisions_applied bigint NOT NULL`, `entities integer NOT NULL`,
  `computed_at timestamptz NOT NULL DEFAULT now()`. The primary key alone is FR-015's "exactly one
  current entity set", and the row is what tells an unresolved snapshot from one that resolved to
  nothing (research R8)
- [x] T006 Add the `entity` table to migrations/0006_entity.sql: `id bigserial PRIMARY KEY`,
  `snapshot_id bigint NOT NULL REFERENCES snapshot`, `kind text NOT NULL CHECK (kind = 'device')`,
  `device_key text NOT NULL`, `weak boolean NOT NULL`, `attributes jsonb NOT NULL DEFAULT '{}'`,
  `first_seen timestamptz NOT NULL`, `last_seen timestamptz NOT NULL`, and
  `UNIQUE (snapshot_id, device_key)`. That unique index is the whole of FR-024, and the narrow `kind`
  check is deliberate: the day the graph projector adds its kinds is a migration, not a silent
  widening (FR-026)
- [x] T007 [P] Add the `entity_claim` table to migrations/0006_entity.sql:
  `entity_id bigint NOT NULL REFERENCES entity ON DELETE CASCADE`, `snapshot_id bigint NOT NULL`,
  `identifier_claim_id bigint NOT NULL`,
  `PRIMARY KEY (entity_id, snapshot_id, identifier_claim_id)` and
  `FOREIGN KEY (snapshot_id, identifier_claim_id) REFERENCES identifier_claim (snapshot_id, id)`.
  The composite foreign key is how a row reaches the partitioned table, as 001 does everywhere
- [x] T008 [P] Add the `entity_decision` table to migrations/0006_entity.sql:
  `id bigserial PRIMARY KEY`, `perimeter_name text NOT NULL`,
  `kind text NOT NULL CHECK (kind IN ('merge','split','never_merge'))`, `subjects text[] NOT NULL`,
  `identifier jsonb NULL`, `actor text NOT NULL`, `at timestamptz NOT NULL DEFAULT now()`,
  `note text NULL`, plus `CHECK ((kind = 'split') = (identifier IS NOT NULL))` and
  `CHECK (array_length(subjects, 1) = CASE WHEN kind = 'split' THEN 1 ELSE 2 END)`
- [x] T009 [P] Widen the findings category in migrations/0006_entity.sql:
  `ALTER TABLE finding DROP CONSTRAINT finding_category_check;` then re-add it as
  `CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict'))`
- [x] T010 Add the grants and owners to migrations/0006_entity.sql: every new table
  `OWNER TO netmapper_owner`; `SELECT, INSERT, UPDATE, DELETE` on `resolution`, `entity`,
  `entity_claim`, `device`, `device_identifier` plus `USAGE` on `entity_id_seq` to `netmapper_engine`;
  `SELECT` on `entity_decision` to `netmapper_engine`; `INSERT, DELETE` on `finding` and
  `finding_evidence` to `netmapper_engine`; `SELECT, INSERT` on `entity_decision` and `USAGE` on
  `entity_decision_id_seq` to `netmapper_operator`; `SELECT` plus the same five write rights on the
  computed tables to `netmapper_operator`, because `netmapper resolve` runs the resolver in the
  operator's process. No role but the owner gets `UPDATE` or `DELETE` on `entity_decision`
  (research R12)
- [x] T011 Grant test in internal/store/roles_test.go: connected as `netmapper_engine`, `UPDATE` and
  `DELETE` on `entity_decision` both fail while `SELECT` succeeds; `DELETE` on `entity` succeeds;
  `INSERT` and `DELETE` on `finding` succeed; `UPDATE` and `DELETE` on `observation` and
  `identifier_claim` still fail, so the new grants did not widen the collected zone (FR-009, FR-014)

**Checkpoint**: Schema ready. Story work can begin.

---

## Phase 3: User Story 1 - One device, one entity, however many ways it was reached (Priority: P1) 🎯 MVP

**Goal**: Every closed snapshot ends with exactly one entity set, one entity per device, each carrying
its claims and a device key that means the same thing in the next run.

**Independent Test**: Close a fake-transport snapshot where one device is reached through two
addresses and a chain of claims links three claim groups only transitively; resolving gives one entity
with all of them. On the lab, quickstart.md sections 2 and 3.

**Scope note**: a component that contradicts itself on a strong kind is already left unmerged here,
because merging it would be wrong and US2 must not have to undo a shipped behaviour. What US2 adds is
the reporting, not the refusal.

### Tests for User Story 1

> Write these first and confirm they fail.

- [x] T012 [P] [US1] Shared-identifier test in internal/entity/group_test.go: two claim groups
  carrying the same strong `(kind, value)` resolve to one entity that names both groups' claims
  through `entity_claim` (FR-002, US1-1)
- [x] T013 [P] [US1] Transitive test in internal/entity/group_test.go: group A carries a serial also
  on C, C carries a chassis MAC also on B, A and B share nothing; all three resolve to one entity
  (FR-002, US1-2)
- [x] T014 [P] [US1] Duplicate test in internal/entity/group_test.go: an `identity` observation the
  crawl marked `duplicate_of_task` hangs off the entity of the task it duplicated, and that task's
  address appears in `attributes->'targets'` (FR-008, US1-3, SC-002)
- [x] T015 [P] [US1] Weak-identifier test in internal/entity/group_test.go: two devices printing the
  same hostname and nothing else in common stay two entities (FR-003, US1-4)
- [x] T016 [P] [US1] Weak-device test in internal/entity/registry_test.go: a device whose output
  carries no `Serial:` and no `MAC:` line resolves to an entity with `weak = true` and
  `device_key = 'addr:<its address>'`, never a hostname-derived key (FR-004, FR-022, US1-5)
- [x] T017 [P] [US1] Empty-snapshot test in internal/entity/entity_test.go: a closed snapshot holding
  no `identity` observation resolves to zero entities and still writes a `resolution` row with
  `entities = 0`, so the sweep does not pick it up again (US1-6, research R8)
- [x] T018 [P] [US1] Key stability test in internal/entity/registry_test.go: the same device resolved
  in two consecutive snapshots of one perimeter carries the same `device_key` (US1-7, SC-007, FR-021)
- [x] T019 [P] [US1] Renumbering test in internal/entity/registry_test.go: a device whose address
  changed between two snapshots but whose strong identifiers are unchanged keeps its key, and
  `device` gains no second row (research R5)
- [x] T020 [P] [US1] Partial-identifier test in internal/entity/registry_test.go: a device that
  reported a serial and a chassis MAC in the first run and only the chassis MAC in the second keeps
  its key, which is the case a hashed key would fail (research R5)
- [x] T020a [P] [US1] New-key test in internal/entity/registry_test.go: a device whose serial and chassis
  MAC both changed between two snapshots, sharing no identifier with what the registry knows, mints a new
  key and a second `device` row, and nothing bridges the two entities that the system concluded on its
  own (FR-023)
- [x] T021 [P] [US1] Perimeter scope test in internal/entity/registry_test.go: two perimeters whose
  devices share a strong identifier resolve to two devices with their own keys, and neither
  perimeter's registry reaches into the other (FR-021, clarification of 2026-09-24)
- [x] T022 [P] [US1] Attribute tie-break test in internal/entity/group_test.go: two observations
  grouped into one entity reporting different hostnames (a short name against an FQDN) yield the
  value of the observation not marked `duplicate_of_task`, twice in a row (FR-003, FR-013,
  research R15)
- [x] T023 [P] [US1] Parse generation test in internal/entity/entity_test.go: claims belonging to a
  superseded `parse_generation` are ignored, and only the active generation's claims are grouped
  (FR-019, research R10)
- [x] T024 [P] [US1] Reproducibility test in internal/entity/entity_test.go: resolving the same
  snapshot twice with no decision recorded in between produces the same entities, the same keys and
  the same claim membership (FR-013, SC-004)
- [x] T025 [P] [US1] Replacement test in internal/entity/write_test.go: re-resolving replaces the set
  rather than adding to it, leaves exactly one `resolution` row for the snapshot, and never leaves
  orphan `entity_claim` rows (FR-015)
- [x] T026 [P] [US1] Concurrency test in internal/entity/write_test.go: two goroutines resolving the
  same snapshot at once leave one complete entity set, the loser waiting on the advisory lock rather
  than interleaving with the winner (research R8, spec edge case)
- [x] T027 [P] [US1] Sweep test in internal/jobrunner/resolve_test.go: a snapshot closed directly
  against the database with no runner running is resolved on the next `Tick` (FR-016, SC-001)
- [x] T028 [P] [US1] Refusal test in internal/entity/entity_test.go: resolving a snapshot in state
  `open` returns `ErrNotClosed` and writes nothing (FR-001)
- [x] T029 [P] [US1] Verdict independence test in internal/entity/entity_test.go: a snapshot the gate
  classified `quarantined` is resolved like any other, and resolving writes no `snapshot_judgement`
  row (FR-025)
- [x] T030 [P] [US1] Immutability test in internal/entity/entity_test.go: resolving changes no row of
  `observation`, `observation_raw`, `identifier_claim` or `snapshot`, checked by comparing a digest of
  those tables before and after (FR-014, FR-020, Principle II)
- [x] T031 [P] [US1] Replay test in internal/entity/entity_test.go: resolve two snapshots, delete
  every row of `entity`, `entity_claim`, `resolution`, `device` and `device_identifier`, resolve both
  again in closing order, and get the same keys and the same entity sets (SC-008, FR-020)
- [x] T031a [P] [US1] Freshness test in internal/entity/entity_test.go: an entity built from observations
  collected minutes apart carries the earliest and the latest `collected_at` of its own evidence in
  `first_seen` and `last_seen`, never the resolution time (FR-006, Principle I)

### Implementation for User Story 1

- [x] T032 [US1] Implement claim reading in internal/entity/group.go: one query returning every
  `identity` observation of the snapshot with its `identifier_claim` rows, joined to the snapshot's
  active `parse_generation`, ordered by observation id so the grouping never depends on row order
  (research R2, R10)
- [x] T033 [US1] Implement union-find over shared strong identifiers in internal/entity/group.go,
  including the transitive closure, with weak claims recorded as attributes and never used as a link
  (FR-002, FR-003, research R3)
- [x] T034 [US1] Detect a contradicting component in internal/entity/group.go: a component holding
  more than one distinct value for one strong kind is not merged, and every claim group in it becomes
  its own entity. No finding is raised yet, that is US2 (research R4)
- [x] T035 [US1] Implement the attribute merge in internal/entity/group.go: `hostname` and `platform`
  come from the observation not marked `duplicate_of_task`; `targets` lists every address the group
  answered on, that observation's first; `identifiers` is a flat copy of the strong claims
  (FR-003, FR-008, research R15)
- [x] T036 [US1] Implement the registry in internal/entity/registry.go: look each component's strong
  identifiers up in `device_identifier` scoped to the perimeter name; no match mints a device keyed on
  the lexicographically smallest `<kind>:<value>` of the component, one match reuses that key and
  inserts the identifiers it did not yet know, and a component with no strong identifier mints
  `addr:<address>` with `weak = true` (FR-021, FR-022, research R5)
- [x] T037 [US1] Handle the two-device match in internal/entity/registry.go: a component whose
  identifiers match more than one known device attaches to the lowest key among them and adds no
  identifier to the others, so the outcome is the same on every run. The finding is US2 (FR-023,
  research R6)
- [x] T038 [US1] Implement the write path in internal/entity/write.go: one transaction that takes
  `pg_advisory_xact_lock(snapshot_id)`, deletes the snapshot's entities, inserts the new ones with
  their `entity_claim` rows, upserts `resolution` with `resolver_version`, `decisions_applied` and the
  entity count, and carries the registry's timestamps forward with
  `first_seen = LEAST(device.first_seen, ...)` and `last_seen = GREATEST(device.last_seen, ...)`, so
  resolving an older snapshot after a newer one never moves them backwards (FR-015, research R8)
- [x] T039 [US1] Implement `Resolve(ctx, db, snapshotID)` in internal/entity/entity.go: refuse a
  snapshot that is not `closed`, read the perimeter name the way internal/gate/baseline.go does, then
  group, resolve keys and write. This is the single entry point the sweep, both subcommands and the
  tests use, which is what makes FR-013 testable (plan.md, Structure Decision)
- [x] T040 [US1] Add the resolving step to internal/jobrunner/resolve.go and call it from `Tick` in
  internal/jobrunner/runner.go, after `judgeStep`: select closed snapshots with no `resolution` row
  ordered by `closed_at, id` ascending and call `entity.Resolve` for each; a failure is logged and
  retried on the next tick rather than written half-way. Oldest first is what makes the registry
  replayable (FR-016, research R1, R5)
- [x] T040a [US1] Return the entity count, the weak count and the conflict count from `Resolve`, so
  cmd/netmapper/resolve.go can print the line contracts/cli.md specifies. T034 already detects the
  contradictions in US1; US2 only adds the findings, so the count is truthful from the start
- [x] T041 [US1] Wire `netmapper resolve <snapshot-id>` in cmd/netmapper/resolve.go per
  contracts/cli.md: print `<entities> entities, <weak> weakly identified, <conflicts> conflicts`;
  exit 2 with `snapshot <id> not found` or `snapshot <id> is not closed`; resolve a never-resolved
  snapshot normally; never cascade to other snapshots (FR-017)
- [x] T042 [US1] Add the `resolve` subcommand to the dispatch in cmd/netmapper/main.go

**Checkpoint**: User Story 1 works on its own: run quickstart.md sections 2 and 3.

---

## Phase 4: User Story 2 - A contradicted identity is reported, never guessed (Priority: P2)

**Goal**: A collision an operator can act on, raised where the other data quality problems already
are, and always describing the current entity set.

**Independent Test**: Close a snapshot with two devices deliberately sharing one strong identifier and
disagreeing on another; resolving gives two entities plus one finding naming both, with no operator
input. On the lab, quickstart.md section 4.

### Tests for User Story 2

- [x] T043 [P] [US2] Within-snapshot conflict test in internal/entity/conflict_test.go: two claim
  groups sharing a chassis MAC and carrying different serials produce two entities and one `finding`
  with `domain = 'data_quality'`, `category = 'identity_conflict'` and
  `detail->>'conflict' = 'within_snapshot'` naming the kind and both values (FR-007, US2-1, SC-005)
- [x] T044 [P] [US2] Evidence test in internal/entity/conflict_test.go: that finding's
  `finding_evidence` rows lead to the `identity` observations behind each side, and through them to
  `observation_raw` (FR-005, US2-2, SC-003)
- [x] T045 [P] [US2] Across-devices conflict test in internal/entity/conflict_test.go: a component
  matching two known devices attaches to the lowest key and raises a finding with
  `detail->>'conflict' = 'across_devices'` naming both keys and the bridging identifiers
  (FR-023, research R6)
- [x] T046 [P] [US2] Finding replacement test in internal/entity/conflict_test.go: re-resolving a
  snapshot whose conflict still holds leaves one finding, not two; re-resolving after the conflict is
  settled leaves none (FR-015, research R11)
- [x] T047 [P] [US2] Foreign finding test in internal/entity/conflict_test.go: a `parse_failed`
  finding the collector raised on the same snapshot survives a re-resolution untouched, including its
  `finding_evidence` rows (FR-015, research R11)

### Implementation for User Story 2

- [x] T048 [US2] Collect the within-snapshot conflict in internal/entity/group.go: a component holding
  more than one distinct value for one strong kind is returned as a conflict carrying
  `{"conflict": "within_snapshot", "kind": ..., "values": [...], "keys": [...]}` and the `identity`
  observations of the component as its evidence. group.go raises nothing itself (FR-007)
- [x] T049 [US2] Collect the across-devices conflict in internal/entity/registry.go the same way, with
  `"conflict": "across_devices"` and the bridging identifiers in its detail. registry.go raises nothing
  itself (FR-023, research R6)
- [x] T050 [US2] Write the conflicts in internal/entity/write.go, inside the transaction of T038 and in
  this order: delete the snapshot's `finding_evidence` rows then its `finding` rows, restricted to
  `category = 'identity_conflict'` and that `snapshot_id`, then raise the ones T048 and T049 collected
  through `store.RaiseFinding` with `domain = 'data_quality'` and `subject_ref` holding the device keys.
  Raising outside this transaction would leave findings describing an entity set that was never written.
  Nothing else in `finding` is touched (FR-007, FR-015, research R11)

**Checkpoint**: User Stories 1 and 2 both work: run quickstart.md section 4.

---

## Phase 5: User Story 3 - An operator decision survives every recomputation (Priority: P3)

**Goal**: Three decisions an operator can record once, replayed on every resolution, never applied in
place.

**Independent Test**: Record a merge on two entities, recompute the snapshot from its stored claims
alone, and find the merge applied with no further operator action. On the lab, quickstart.md
section 5.

### Tests for User Story 3

- [x] T052 [P] [US3] Merge test in internal/entity/decisions_test.go: a `merge` on two device keys
  makes every snapshot containing both resolve them to one entity carrying the first key (US3-1,
  FR-009)
- [x] T053 [P] [US3] Never-merge test in internal/entity/decisions_test.go: a `never_merge` on two
  keys that share a strong identifier keeps two entities under two distinct device keys, so
  `UNIQUE (snapshot_id, device_key)` is satisfied by the grouping and not by luck, and raises no
  `identity_conflict` finding for that pair (US3-2, FR-009, FR-024)
- [x] T054 [P] [US3] Split persistence test in internal/entity/decisions_test.go: a `split` detaching
  an identifier keeps the devices apart on the next run where that identifier is observed again, each
  under its own key (US3-3, FR-012, FR-024, SC-006)
- [x] T055 [P] [US3] Late decision test in internal/entity/decisions_test.go: a decision recorded
  after a snapshot was resolved applies when that snapshot is resolved again, and `decisions_applied`
  on the `resolution` row names it (US3-4, FR-017)
- [x] T056 [P] [US3] Absent subject test in internal/entity/decisions_test.go: a decision whose
  subjects appear in no snapshot of the perimeter is skipped without failing the resolution and still
  applies later when they do appear (US3-5)
- [x] T057 [P] [US3] Ordering test in internal/entity/decisions_test.go: a `merge` then a
  `never_merge` on the same pair resolves as two entities, the reverse order as one, and both rows
  stay readable (FR-011)
- [x] T058 [P] [US3] CLI validation test in cmd/netmapper/decide_test.go: exit 2 for an unknown
  subject key, for a merge naming the same key twice, and for a split whose identifier is not
  attributed to the named device (contracts/cli.md)

### Implementation for User Story 3

- [x] T059 [US3] Implement decision reading in internal/entity/decisions.go: every
  `entity_decision` of the perimeter ordered by `id`, with the rule that the last decision on the same
  unordered subject pair governs and earlier ones stay readable (FR-011)
- [x] T060 [US3] Apply `merge` in internal/entity/decisions.go and internal/entity/registry.go: the
  second key's identifiers resolve to the first key, and entities carry the first (FR-009)
- [x] T061 [US3] Apply `split` in internal/entity/decisions.go: the named identifier is removed from
  the named device before lookup, so a claim group carrying it matches or mints another device
  (FR-012, research R7)
- [x] T062 [US3] Apply `never_merge` in internal/entity/group.go and internal/entity/registry.go: an
  identifier observed on both subjects is ignored as a link and raises no conflict finding for that
  pair (FR-009, US3-2)
- [x] T063 [US3] Record `decisions_applied` on the `resolution` row in internal/entity/write.go: the
  highest `entity_decision.id` read for the perimeter, 0 when it has none, whether or not every one of
  them applied, so a result can be explained afterwards
- [x] T064 [US3] Implement `netmapper decide merge|split|never-merge` in cmd/netmapper/decide.go per
  contracts/cli.md: `--perimeter`, `--keys` or `--key` plus `--identifier kind=value`, `--note`,
  `--actor` defaulting to the OS user; validate the subjects exist in that perimeter; print
  `decision <id> recorded` and then the snapshots now stale as `resolve <ids> to apply`
- [x] T065 [US3] Add the `decide` subcommand to the dispatch in cmd/netmapper/main.go

**Checkpoint**: All three stories work: run quickstart.md section 5.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T066 [P] Carry the deltas of plan.md into docs/c4-model/04-data-model.md: the six new tables,
  the closing of the "row per snapshot or validity interval" question for entities, `identity_conflict`
  in the findings categories, and the engine's new grants
- [x] T067 [P] Note in docs/c4-model/04-data-model.md that 002's `compare` will have a simpler source
  once entities carry a device key, and that changing it is a separate change with its own
  `gate_version` bump, not part of this feature
- [ ] T068 Run quickstart.md sections 2 to 5 against the containerlab lab and record every divergence
  in its "Divergences recorded during implementation" section, the way 002 did. A divergence is a
  finding about the design, not a detail to fix silently.
  **Partly done**: containerlab is not installed on the dev machine, so sections 2 to 5 have not run.
  Section 1 is green over three parallel runs, and the eight divergences the integration suite
  surfaced are recorded. This task stays open for the lab run itself
- [x] T069 Review `resolverVersion` in internal/entity/entity.go before merging: if the grouping changed after the first snapshots
  were resolved during development, bump it, and say so in the commit. 002's lab run showed the bump
  is easy to forget exactly when it matters (research R9)
- [x] T070 Run `go test ./...` with `NETMAPPER_TEST_DSN` set and confirm the whole suite passes, 001
  and 002 included: nothing in this feature may change a crawl outcome or a verdict

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: needs Setup; blocks every story, because every test writes to the new
  tables
- **User Story 1 (Phase 3)**: needs Foundational. The MVP
- **User Story 2 (Phase 4)**: needs US1's grouping and registry, since a conflict is raised from
  inside them. It adds reporting to decisions US1 already takes
- **User Story 3 (Phase 5)**: needs US1. Independent of US2: decisions apply whether or not conflicts
  are reported, and the never-merge case only touches US2's finding when both are present
- **Polish (Phase 6)**: needs the stories that are being shipped

### Within Each Story

- Tests first, failing, then implementation
- Reading (group.go) before keys (registry.go) before the write (write.go) before the entry point
- Entry point before the sweep and the subcommands

### Parallel Opportunities

- T005, T007, T008, T009 touch different parts of one migration file and can be written in parallel,
  but land in one file: merge them in order
- Every test task inside a phase is marked [P]: they are separate test files or separate functions
- T066 and T067 are documentation and can run alongside anything

## Parallel Example: User Story 1

```bash
# The grouping tests, all in internal/entity/group_test.go, written together:
T012 shared identifier, T013 transitive, T014 duplicate, T015 weak identifier, T022 attribute tie-break

# The registry tests, all in internal/entity/registry_test.go:
T016 weak device, T018 key stability, T019 renumbering, T020 partial identifiers, T020a new key,
T021 perimeter scope
```

## Implementation Strategy

### MVP (User Story 1 only)

1. Phase 1, then Phase 2 (schema and grants)
2. Phase 3
3. Stop and validate: quickstart.md sections 2 and 3, plus `go test ./...`
4. At this point a snapshot has entities with stable keys and evidence, which is what the graph
   projector needs. Conflicts are already not merged, they are simply not reported yet

### Incremental delivery

1. Setup and Foundational
2. US1 → validate → the computed zone exists
3. US2 → validate → collisions are visible
4. US3 → validate → an operator can correct the resolver without editing data

### Notes

- [P] means a different file and no dependency on an unfinished task
- Commit after each task or logical group
- A divergence found while implementing belongs in quickstart.md, not in a silent edit of the plan

---

## Phase 7: Convergence

- [x] T071 Make a `split` keep two claim groups of one snapshot apart. The detached identifier is
  consulted only in internal/entity/registry.go (`loadRegistry`), so internal/entity/group.go still
  links two groups carrying it: they merge into one entity and the second device never gets a key of
  its own. Suppress the detached identifier as a link for the named device's groups, the way a
  never-merge suppresses a bridging one, and add the case quickstart.md section 1 lists as "a
  never-merge and a split leave two entities under two distinct keys" for the split half
  per FR-012, FR-024 and the spec edge case "kept apart by a split or a never-merge decision
  although they share a strong identifier" (partial)
- [x] T072 Add the third-run test quickstart.md section 1 lists as "a split survives, and the merge
  keeps applying, on a third run": crawl the perimeter a third time with a split and a merge already
  recorded, and confirm both still apply with no further operator action. T054 and T055 only
  re-resolve one snapshot, so the "every later run of that perimeter" half of SC-006 is untested
  per SC-006 and FR-012 (missing)

---

## Phase 8: Convergence

- [x] T073 Name the registry as the exception in docs/c4-model/04-data-model.md: the "What was
  computed" section opens with "Every row carries the snapshot it belongs to and the observations it
  came from", which `device` and `device_identifier` do not, being the one cross-snapshot state of the
  zone. Say so in the preamble rather than leaving two rows contradicting it
  per plan: documentation deltas (partial)
