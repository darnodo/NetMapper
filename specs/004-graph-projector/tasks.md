---
description: "Task list for the graph projector (interfaces and edges)"
---

# Tasks: Graph projector (interfaces and edges)

**Input**: Design documents from `/specs/004-graph-projector/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Included. [quickstart.md](quickstart.md) section 1 lists the cases this feature must pass,
and FR-017 (same inputs, same result) and SC-008 (wipe and recompute) are only meaningful as tests.

**Organization**: Tasks are grouped by user story so each story can be built and tested on its own.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3 from spec.md
- Paths are relative to the repository root. Same module and layout as 001, 002 and 003: `cmd/` plus
  `internal/`, tests next to the code (plan.md, Project Structure).

Integration tests use `internal/testutil`. Every test that builds a snapshot through `NewLab` needs
both `NETMAPPER_TEST_DSN` and `NETMAPPER_TEST_S3_ENDPOINT` and skips without them, because the crawl
that produces the snapshot stores raw output in the object store. The projector itself opens no object
store connection.

---

## Phase 1: Setup

**Purpose**: The package this feature lives in, and the test pack data the interface cases cannot be
written without

- [X] T001 Create the package skeleton in internal/graph/graph.go: `Project(ctx, db, reg, snapshotID)`
  returning a `Result`, an `Interface` struct mirroring the `interface` columns of data-model.md, an
  `Edge` struct mirroring the `edge` columns, the sentinel errors `ErrNotFound`, `ErrNotClosed` and
  `ErrNotResolved` following internal/entity/entity.go, and `const projectorVersion = 1` with a comment
  stating it is bumped by hand when a change alters results (research R12)
- [X] T002 [P] Teach the fakeos test pack to report a far-end port: add `Value REMOTE (\S+)` and a
  sixth column to the `Start` rule of internal/pack/testdata/fakeos/templates/display_neighbours.textfsm,
  and `remote_interface: REMOTE` to the `map` of
  internal/pack/testdata/fakeos/recipes/neighbours.yaml. The pack already declares
  `interface_names: [{ match: '^p(\d+)$', replace: 'port$1' }]`, so a report spelling a port `p1` while
  the device itself spells it `port1` is exactly the alias case US1 has to prove. This is pack data,
  which is the point: no Go change teaches the projector a vendor's spelling (FR-003, Principle V)
- [X] T003 Extend `FakeOSShaped` in internal/testutil/lab.go so a neighbour entry is
  `"<local port> <name> <address> [<remote port>]"`, printing the remote port as the sixth field the
  T002 template now reads and defaulting to `-` when the entry omits it. Keep the existing three-field
  form working: every neighbour string in 001's and 003's tests uses it, and this feature may not
  change a crawl outcome

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, grants and the transaction shell every story writes through. No story work can
begin until this phase is complete

**⚠️ CRITICAL**: The engine reads platform packs for the first time here (T016), which contradicts a
comment in cmd/netmapper/engine.go that T079 corrects. T014 is what proves the rest of the grant matrix
still holds, and it is the check the constitution added because 003 shipped a command its own role
could not run

- [X] T004 Create migrations/0007_graph.sql with the `projection` table exactly as in data-model.md:
  `snapshot_id bigint PRIMARY KEY REFERENCES snapshot`, `projector_version integer NOT NULL`,
  `resolution_at timestamptz NOT NULL`, `interfaces integer NOT NULL`, `edges integer NOT NULL`,
  `computed_at timestamptz NOT NULL DEFAULT now()`. The primary key alone is FR-018's "exactly one
  current set", and `resolution_at` is what makes a re-resolution repair itself (research R12)
- [X] T005 Add the `interface` table to migrations/0007_graph.sql: `id bigserial PRIMARY KEY`,
  `entity_id bigint NOT NULL REFERENCES entity ON DELETE CASCADE`,
  `snapshot_id bigint NOT NULL REFERENCES snapshot`, `canonical_name text NOT NULL`,
  `source text NOT NULL CHECK (source IN ('device','neighbour'))`, `description text NULL`,
  `admin_state text NULL CHECK (admin_state IN ('up','down'))`,
  `oper_state text NULL CHECK (oper_state IN ('up','down','other'))`, `speed_bps bigint NULL`,
  `mtu integer NULL`, `mac text NULL`, `first_seen timestamptz NOT NULL`,
  `last_seen timestamptz NOT NULL`, and `UNIQUE (entity_id, canonical_name)`. That unique index is the
  whole of FR-002. The two state checks are the `interfaces` fact family's own enums verbatim, so a
  value the parser accepted cannot be rejected here. Everything but the name is nullable, because a
  port a neighbour revealed has no operational facts at all
- [X] T006 [P] Add the `interface_alias` table to migrations/0007_graph.sql:
  `interface_id bigint NOT NULL REFERENCES interface ON DELETE CASCADE`, `spelling text NOT NULL`,
  `source text NOT NULL CHECK (source IN ('device','neighbour'))`, `snapshot_id bigint NOT NULL`,
  `observation_id bigint NOT NULL`, `PRIMARY KEY (interface_id, spelling)` and
  `FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)`
- [X] T007 [P] Add the `interface_evidence` table to migrations/0007_graph.sql:
  `interface_id bigint NOT NULL REFERENCES interface ON DELETE CASCADE`, `snapshot_id bigint NOT NULL`,
  `observation_id bigint NOT NULL`, `PRIMARY KEY (interface_id, observation_id)` and the same composite
  `FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)`. This is FR-007
  for interfaces: the first link of the chain to `observation_raw` and the bytes
- [X] T008 Add the `edge` table to migrations/0007_graph.sql: `id bigserial PRIMARY KEY`,
  `snapshot_id bigint NOT NULL REFERENCES snapshot`,
  `type text NOT NULL CHECK (type IN ('l1_link','has_address'))`, `from_ref text NOT NULL`,
  `to_ref text NOT NULL`,
  `name text GENERATED ALWAYS AS (type || ':' || from_ref || '|' || to_ref) STORED`,
  `from_entity_id bigint NULL REFERENCES entity ON DELETE CASCADE`,
  `to_entity_id bigint NULL REFERENCES entity ON DELETE CASCADE`,
  `from_interface_id bigint NULL REFERENCES interface ON DELETE CASCADE`,
  `to_interface_id bigint NULL REFERENCES interface ON DELETE CASCADE`,
  `confidence text NOT NULL CHECK (confidence IN ('both_ends','one_end','direct'))`,
  `attributes jsonb NOT NULL DEFAULT '{}'`, `first_seen timestamptz NOT NULL`,
  `last_seen timestamptz NOT NULL`, and `UNIQUE (snapshot_id, name)`. Generating the name in the
  database is deliberate: it cannot drift from the columns it is built from, and FR-009's "one link,
  not two" then rests on a key rather than on a code path (research R6)
- [X] T009 Add the two named constraints to the `edge` table in migrations/0007_graph.sql:
  `CONSTRAINT edge_l1_link_is_ordered CHECK (type <> 'l1_link' OR from_ref COLLATE "C" < to_ref COLLATE "C")`
  and `CONSTRAINT edge_from_is_resolved CHECK (from_entity_id IS NOT NULL)`. The `C` collation is not
  optional: Go compares strings by byte, the database's own collation does not, and a reference
  contains `:` and `/`, so without it the two disagree on some pairs of port names and the projector
  fails an insert months later (research R7)
- [X] T010 [P] Add the `edge_evidence` table to migrations/0007_graph.sql:
  `edge_id bigint NOT NULL REFERENCES edge ON DELETE CASCADE`, `snapshot_id bigint NOT NULL`,
  `observation_id bigint NOT NULL`, `side text NOT NULL CHECK (side IN ('from','to'))`,
  `PRIMARY KEY (edge_id, observation_id, side)` and the composite
  `FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)`. `side` is what
  makes "both ends agreed" readable from the evidence and not only from a column
- [X] T011 Widen the findings category in migrations/0007_graph.sql the way 0006 did: drop
  `finding_category_check` and recreate it as
  `CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict', 'link_disagreement'))`,
  with the matching narrowing in the `+goose Down` section
- [X] T012 Add the owner and the grants to migrations/0007_graph.sql: `ALTER TABLE ... OWNER TO
  netmapper_owner` for the six tables, then `GRANT SELECT, INSERT, UPDATE, DELETE` on `projection`,
  `interface`, `interface_alias`, `interface_evidence`, `edge` and `edge_evidence` to
  `netmapper_engine` and to `netmapper_operator`, plus `GRANT USAGE ON SEQUENCE interface_id_seq,
  edge_id_seq` to both. No new right on `finding` is needed: 003 already gave both roles
  `SELECT, INSERT, DELETE` and the sequence. The collector gains nothing, because it never reads an
  interface or an edge
- [X] T013 Write the `+goose Down` section of migrations/0007_graph.sql: restore the previous
  `finding_category_check` and `DROP TABLE edge_evidence, edge, interface_evidence, interface_alias,
  interface, projection` in dependency order
- [X] T014 Role test in internal/store/roles_test.go following the existing
  `TestCollectorCannotRewriteCollectedZone`: with `testutil.As(t, db, "netmapper_collector")` confirm
  the collector can read neither `interface` nor `edge`; with `netmapper_engine` and again with
  `netmapper_operator` confirm both can insert into and delete from all six tables. This is the
  constitution's "a path assigned to a role is tested under that role", and it exists because 003
  granted the operator writes without the reads and nothing noticed until a security review
- [X] T015 Implement the read side in internal/graph/graph.go: `describe` reading the snapshot state
  and refusing one that is not `closed` (`ErrNotClosed`) or carries no `resolution` row
  (`ErrNotResolved`), then a query reading the entity set with `device_key`, `attributes`,
  `first_seen`, `last_seen` and the `find` task ids behind each entity, through `entity_claim` joined
  to `identifier_claim` joined to the `identity` observation. Order by `device_key`, then by task id,
  so the whole projection runs in one fixed order (FR-017, research R2)
- [X] T016 Read the two fact families in internal/graph/graph.go: one query for `interfaces` and one
  for `neighbours` observations of the snapshot, both joining `parse_generation` on
  `active` (FR-019, research R13) and both restricted to `status = 'collected'`. A `neighbours`
  observation carries the same `task_id` as the `identity` one; an `interfaces` observation is found
  through `task.parent_task_id`. Order by observation id then by row index in `parsed`
- [X] T017 Implement the transaction shell in internal/graph/write.go: `pg_advisory_xact_lock` on the
  snapshot id, then delete the snapshot's `edge` rows, its `interface` rows, and its
  `link_disagreement` findings with their evidence, then write the new set, then upsert the
  `projection` row with `projector_version` and the `resolution.computed_at` read in T015. The lock is
  on the snapshot and not on the perimeter, because this feature adds no cross-snapshot state
  (research R12). The delete on `finding` is restricted in code to that category and that snapshot: a
  finding the collector or the resolver raised is not this projection's to remove

**Checkpoint**: schema, grants and the write path exist. Story work can begin

---

## Phase 3: User Story 1 - A device has ports, and they are called one thing (Priority: P1) 🎯 MVP

**Goal**: every resolved device carries its ports under one canonical name, with every spelling ever
seen readable against it and the evidence behind each reachable. No edges.

**Independent Test**: close a fake-transport snapshot with one device reporting several interfaces and
a neighbour reporting one of them under another spelling; project it; confirm each port appears once
under its canonical name, with the neighbour's spelling recorded as an alias traceable to its
observation, and no edge written.

### Tests for User Story 1

> Write these first and confirm they fail before implementing.

- [X] T018 [P] [US1] Interfaces test in internal/graph/interfaces_test.go: a device whose `interfaces`
  observation listed several ports gets one row each, under the canonical name the fakeos
  `interface_names` rule produces (`p1` becomes `port1`), carrying the description, admin and oper
  states, MTU and MAC that were collected (US1-1, FR-002, FR-003, FR-006)
- [X] T019 [P] [US1] Alias test in internal/graph/interfaces_test.go: a neighbour reporting the far end
  as `p1` while the device itself spells it `port1` yields one interface and two `interface_alias`
  rows, one `source = 'device'` and one `source = 'neighbour'`, each naming its own observation
  (US1-2, FR-004, SC-002)
- [X] T020 [P] [US1] Unmatched spelling test in internal/graph/interfaces_test.go: a spelling no naming
  rule matches still produces an interface, named by that spelling (edge case, FR-005)
- [X] T021 [P] [US1] Evidence test in internal/graph/interfaces_test.go: from an interface, through
  `interface_evidence`, reach the observation, its `observation_raw` row and its `collected_at`
  (US1-3, FR-007, SC-003)
- [X] T022 [P] [US1] Missing recipe test in internal/graph/interfaces_test.go: a device whose
  `interfaces` observation is `unsupported` or `parse_failed` keeps its entity and simply has no
  interfaces, and the projection succeeds (US1-4, FR-001)
- [X] T023 [P] [US1] Same-name test in internal/graph/interfaces_test.go: two devices each with a port
  spelled the same way give two interfaces, because `UNIQUE (entity_id, canonical_name)` scopes the
  name to one device (US1-5, FR-002)
- [X] T024 [P] [US1] Neighbour-revealed port test in internal/graph/interfaces_test.go: a device
  reporting a neighbour on a port its own `interfaces` observation never listed gets that port, with
  `source = 'neighbour'` and no operational facts (edge case, FR-006)
- [X] T025 [P] [US1] Two-address test in internal/graph/interfaces_test.go: a device reached on two
  addresses, whose second `find` task ended `duplicate`, gets its interfaces once. The duplicate task
  enqueues no scrape, so this proves the task lineage of research R2 rather than a deduplication rule
  (edge case)
- [X] T026 [P] [US1] Alias tie-break test in internal/graph/interfaces_test.go, run with inputs that
  actually tie: the same spelling arriving from two observations keeps the lowest observation id, and
  two runs agree. This is the constitution's "a rule that settles a tie is tested with inputs that
  tie" (research R5)
- [X] T027 [P] [US1] Chassis MAC test in internal/graph/interfaces_test.go: an interface whose MAC is
  also a device's chassis MAC, which resolution used as a strong identifier, produces an interface and
  no entity and no edge (edge case, research R14)
- [X] T028 [P] [US1] Empty projection test in internal/graph/graph_test.go: a snapshot with an entity
  set but no `interfaces` and no `neighbours` observation projects to nothing and gets a `projection`
  row, so it reads as projected rather than pending (edge case, FR-018)
- [X] T029 [P] [US1] Unresolved snapshot test in internal/graph/graph_test.go: a closed snapshot with
  no `resolution` row returns `ErrNotResolved` and writes nothing; the sweep does not select it and
  treats it as neither work nor error (edge case, FR-001)
- [X] T030 [P] [US1] Replacement test in internal/graph/graph_test.go: project, change which interfaces
  the observation yields, project again, and confirm the result is the new set complete with no remnant
  of the old one and exactly one `projection` row (edge case, FR-018)
- [X] T031 [P] [US1] Concurrency test in internal/graph/graph_test.go: two `Project` calls on one
  snapshot at once end with one set, never two half-written ones (edge case, research R12)
- [X] T032 [P] [US1] Reproducibility test in internal/graph/graph_test.go: projecting the same snapshot
  three times from the same parse generation and entity set gives byte-identical interfaces, aliases
  and edges, compared as sorted rows (FR-017, SC-005)
- [X] T033 [P] [US1] Parse generation test in internal/graph/graph_test.go: an observation of an
  inactive parse generation is not read (FR-019, research R13)
- [X] T034 [P] [US1] Sweep test in internal/jobrunner/project_test.go: a snapshot closed and resolved
  while nothing was projecting is projected on the next `Tick`, with no command run to make it so
  (FR-021, SC-001)
- [X] T035 [P] [US1] Re-resolution test in internal/jobrunner/project_test.go: re-resolving a projected
  snapshot cascades its interfaces away and leaves `projection.resolution_at` stale, and the next
  `Tick` projects it again (FR-021, research R12)
- [X] T036 [P] [US1] Operator role test in cmd/netmapper/project_test.go: `netmapper project` runs to
  completion on a connection as `netmapper_operator`, and returns exit 2 with the contract's wording
  for a snapshot that is missing, not closed, or has no entity set (contracts/cli.md, research R15)
- [X] T037 [P] [US1] Read-only test in internal/graph/graph_test.go: projecting changes no row of
  `observation`, `observation_raw`, `identifier_claim`, `entity`, `entity_claim`, `device`,
  `device_identifier` or `entity_decision`, compared by checksum before and after (FR-020,
  Principle II)

### Implementation for User Story 1

- [X] T038 [US1] Build interfaces from the `interfaces` family in internal/graph/interfaces.go: one
  `Interface` per row, keyed on `(entity, canonical_name)`, `source = 'device'`, carrying
  `description`, `admin_state`, `oper_state`, `speed_bps`, `mtu` and `mac` from the row, with
  `first_seen`/`last_seen` from the observation's `collected_at`. The `name` field is already canonical
  because the family marks it `Canonical` and the parser normalised it at collection time
- [X] T039 [US1] Resolve a report's far end to an entity in internal/graph/links.go: match
  `remote_chassis_id` against the strong identifier values in each entity's `attributes.identifiers`
  first, then `remote_mgmt_address` against `attributes.targets` when
  `remote_mgmt_address_type` is `ipv4` or `ipv6` and against the identifier values when it is `mac`.
  A `remote_system_name` is recorded but never resolves an endpoint, because a hostname is weak in
  every pack and two devices may share one. A value matching more than one entity matches none: the far
  end stays unresolved (FR-012, clarified 2026-09-25, research R8)
- [X] T040 [US1] Add the ports a `neighbours` row names in internal/graph/interfaces.go: the local
  port, already canonical because the family marks `local_interface` `Canonical`; and the far-end port
  when the report's far end resolved to an entity, canonicalised with
  `reg.Normalise(<that entity's attributes.platform>, spelling)`. Create what is missing with
  `source = 'neighbour'`; a port the device also described keeps `source = 'device'`. This is the only
  place a pack is read, and it is why `Project` takes a `*pack.Registry` (FR-003, research R3, R4)
- [X] T041 [US1] Record every spelling in internal/graph/interfaces.go: one `interface_alias` per
  distinct spelling seen for a port, including one that already equals the canonical name, with the
  source kind and the observation it came from, keeping the lowest observation id when the same
  spelling arrives twice (FR-004, research R5)
- [X] T042 [US1] Write interfaces, aliases and evidence in internal/graph/write.go, inside the T017
  transaction, in `(device_key, canonical_name)` order, collecting the returned ids so the edge writer
  can reference them
- [X] T043 [US1] Add the projecting step in internal/jobrunner/project.go and call it from `Tick` in
  internal/jobrunner/runner.go, after `resolveStep`: select closed snapshots joined to `resolution` and
  left-joined to `projection` where the `projection` row is absent or its `resolution_at` differs from
  `resolution.computed_at`, `ORDER BY s.closed_at, s.id`, and attempt every one of them, joining the
  errors rather than stopping at the first, the way `resolveStep` does (FR-021, contracts/cli.md)
- [X] T044 [US1] Give the engine its packs: add `--packs` defaulting to `packs` to
  cmd/netmapper/engine.go, load with `pack.LoadRoot` and refuse to start if the packs do not load, the
  way cmd/netmapper/collector.go does, and thread the registry through `jobrunner.Run` and `Tick` to
  `graph.Project`
- [X] T045 [US1] Implement `netmapper project <snapshot-id>` in cmd/netmapper/project.go per
  contracts/cli.md: the `--packs` flag, the stdout line
  `<interfaces> interfaces, <edges> edges, <disagreements> disagreements`, and exit 2 with the
  contract's wording for `ErrNotFound`, `ErrNotClosed` and `ErrNotResolved`, following
  cmd/netmapper/resolve.go
- [X] T046 [US1] Add the `project` subcommand to the dispatch and the usage line in
  cmd/netmapper/main.go

**Checkpoint**: a projected snapshot has ports with aliases and evidence, reachable by SQL, and the
sweep and the subcommand both produce them. No edges yet

---

## Phase 4: User Story 2 - A cable both ends agree on is a link (Priority: P2)

**Goal**: two reports of one cable become one link between two interfaces, marked as agreed by both
ends and citing the observation from each side.

**Independent Test**: close a snapshot with two devices reporting each other over LLDP, project it, and
confirm one link between the two interfaces, marked `both_ends`, citing both observations.

### Tests for User Story 2

- [X] T047 [P] [US2] Agreement test in internal/graph/links_test.go: two devices that each reported the
  other naming the same pair of ports give one `l1_link`, not two, with `confidence = 'both_ends'`
  (US2-1, FR-009, SC-004)
- [X] T048 [P] [US2] Evidence test in internal/graph/links_test.go: that link has one `edge_evidence`
  row per `side`, each reaching its own observation and its `observation_raw` (US2-2, FR-007, FR-008)
- [X] T049 [P] [US2] Chassis attachment test in internal/graph/links_test.go: a report naming the
  remote device only by a chassis identifier the entity set accounts for lands the link on that entity
  (US2-3, FR-012)
- [X] T050 [P] [US2] Address attachment test in internal/graph/links_test.go: a report naming the
  remote device only by a management address the entity set accounts for lands the link on that entity
  (US2-3, FR-012)
- [X] T051 [P] [US2] Two-cable test in internal/graph/links_test.go: two devices cabled on two ports
  give two links, because the name carries the ports and not just the devices (US2-4, FR-011)
- [X] T052 [P] [US2] Two-protocol test in internal/graph/links_test.go: one device reporting one cable
  under two protocols gives one edge whose `attributes.protocols` holds both, citing both observations
  (FR-011, clarified 2026-09-25)
- [X] T053 [P] [US2] Orientation test in internal/graph/links_test.go: the link carries the same name
  whichever device's report is read first, and the `edge_l1_link_is_ordered` constraint is never
  violated by a name the projector builds
- [X] T054 [P] [US2] Self-report tie-break test in internal/graph/links_test.go, run with inputs that
  actually tie: a report whose two endpoint references are equal, a port claiming to see itself, is
  dropped and the projection still succeeds. This is the second of the constitution's tie-break tests
  (research R6)
- [X] T055 [P] [US2] Shared medium test in internal/graph/links_test.go: three devices whose ports all
  report each other give one link per pair, with none discarded and no finding raised (edge case,
  research R10)
- [X] T056 [P] [US2] Stability test in internal/graph/links_test.go: two consecutive snapshots of one
  perimeter, with the same cable, carry the same `edge.name` (FR-015, SC-006)

### Implementation for User Story 2

- [X] T057 [US2] Build endpoint references in internal/graph/links.go: `if:<device_key>/<canonical>`
  for a resolved port, `dev:<device_key>` for a device, `addr:<address>` for an address, and
  `unknown:<kind>=<value>[/<port>]` for a far end no entity accounts for, choosing the identifier in the
  order `remote_chassis_id`, `remote_mgmt_address`, `remote_system_name` (data-model.md, research R6)
- [X] T058 [US2] Pair the reports in internal/graph/links.go: a report from A about B and a report from
  B about A become one `both_ends` edge when each names the other's local port after canonicalisation.
  Orient by putting the lower reference first, compared as bytes so the Go side and the
  `edge_l1_link_is_ordered` constraint agree. Drop a report whose two references are equal (FR-009,
  FR-011, research R6, R7, R8)
- [X] T059 [US2] Collapse the protocols in internal/graph/links.go: two rows from one device describing
  the same cable under different protocols are one edge, with `attributes.protocols` holding the sorted
  set and every row's observation cited. The protocol is evidence, not identity (FR-011)
- [X] T060 [US2] Fill the edge attributes in internal/graph/links.go per data-model.md:
  `protocols`, `from_spelling` and `to_spelling` as each side actually wrote them
- [X] T061 [US2] Write edges and their evidence in internal/graph/write.go, inside the T017
  transaction, in `name` order, with one `edge_evidence` row per side, resolving
  `from_interface_id`/`to_interface_id` and `from_entity_id`/`to_entity_id` from the ids T042 collected
- [X] T062 [US2] Return the counts in the `Result` of internal/graph/graph.go so T045's stdout line and
  the `projection` row both report interfaces, edges and disagreements

**Checkpoint**: the topology a lab agrees on is readable, with its evidence. One-sided cables are still
missing

---

## Phase 5: User Story 3 - A link only one side saw is still worth having, and says so (Priority: P3)

**Goal**: a cable only one end reported is kept and marked as such, the addresses a device answered on
become their own edges, and two devices that contradict each other about ports are reported rather than
reconciled.

**Independent Test**: close a snapshot where one device reports a neighbour that was never reached,
project it, and confirm the link exists, is marked one-sided, and names what little is known about the
far end.

### Tests for User Story 3

- [X] T063 [P] [US3] Unresolved far end test in internal/graph/links_test.go: a report whose remote
  device no entity matches gives a link from the reporting interface, `confidence = 'one_end'`, a
  `to_ref` starting `unknown:`, and `attributes` carrying the system name, chassis identifier,
  management address and port spelling the report gave (US3-1, FR-010)
- [X] T064 [P] [US3] Silent far end test in internal/graph/links_test.go: a report whose remote device
  did resolve but which never reported back connects the two entities and stays `one_end` rather than
  `both_ends` (US3-2, FR-010)
- [X] T065 [P] [US3] Address test in internal/graph/graph_test.go: each address in an entity's
  `attributes.targets` gives one `has_address` edge from `dev:<key>` to `addr:<address>`, with
  `confidence = 'direct'`, cited by the identity observation collected on that address (US3-3, FR-013)
- [X] T066 [P] [US3] Later agreement test in internal/graph/links_test.go: a one-sided link that both
  ends report in a later snapshot is `both_ends` there, and the earlier snapshot's row is unchanged
  (US3-4, FR-016)
- [X] T067 [P] [US3] Empty far end test in internal/graph/links_test.go: a report saying nothing at all
  about the far end yields the local interface and no edge, because there is no endpoint to connect to
  (research R6)
- [X] T068 [P] [US3] Ambiguous identifier test in internal/graph/links_test.go: a chassis identifier
  that two entities of the snapshot both carry, which resolution produces when it refuses to merge a
  contradicting component, attaches the link to neither and leaves it `one_end` (edge case, FR-012,
  clarified 2026-09-25)
- [X] T069 [P] [US3] Disagreement test in internal/graph/links_test.go: two reports about one pair of
  devices that name one port in common and a different port opposite it produce no `both_ends` link,
  keep each side's own report as a `one_end` link, and raise exactly one `link_disagreement` finding
  (FR-014, SC-007, clarified 2026-09-25)
- [X] T070 [P] [US3] Disagreement evidence test in internal/graph/links_test.go: that finding names
  both device keys and what each side said, and cites the `neighbours` observation behind each
  (FR-014, Principle I)
- [X] T071 [P] [US3] Finding replacement test in internal/graph/graph_test.go: re-projecting replaces
  the `link_disagreement` findings, leaving none duplicated and none stale, and leaves the collector's
  and the resolver's own findings untouched (FR-018, FR-020, research R9)
- [X] T072 [P] [US3] Wipe and recompute test in internal/graph/graph_test.go: project a snapshot,
  delete every row of `edge_evidence`, `edge`, `interface_alias`, `interface_evidence`, `interface` and
  `projection`, project again, and compare row for row. This is what proves Principle II for this
  feature, so it runs the whole sequence rather than a sample (SC-008, FR-023)

### Implementation for User Story 3

- [X] T073 [US3] Build the one-sided links in internal/graph/links.go: a report with no matching
  counterpart becomes a `one_end` edge from the reporting interface, with the far end as an `if:`
  reference when it resolved and an `unknown:` one when it did not, and one `edge_evidence` row for the
  reporting side (FR-010)
- [X] T074 [US3] Carry the far end's details in internal/graph/links.go: when the far end resolved to
  nothing, put `remote_system_name`, `remote_chassis_id`, `remote_mgmt_address` and
  `remote_mgmt_address_type` in the edge attributes, and omit them when they add nothing to a resolved
  endpoint (FR-010, data-model.md)
- [X] T075 [US3] Build the `has_address` edges in internal/graph/graph.go: one per address in each
  entity's `attributes.targets`, `dev:<device_key>` to `addr:<address>`, `confidence = 'direct'`,
  `attributes` holding the address, cited by the identity observation collected on it. Read the
  addresses from the entity attributes and not from `observation.target` again, so this projects what
  resolution decided rather than recomputing it (FR-013, research R11)
- [X] T076 [US3] Detect the disagreements in internal/graph/links.go: for each pair of resolved
  devices, two reports that name one port in common and a different port opposite it contradict each
  other. Require the shared port, so a shared medium stays several agreeing pairs rather than a
  contradiction (FR-014, research R9, R10)
- [X] T077 [US3] Raise the findings in internal/graph/write.go through `store.RaiseFinding`, inside the
  T017 transaction and after its delete: `domain = 'data_quality'`,
  `category = 'link_disagreement'`, `severity = 'warning'`, `subject_ref` the lower of the two device
  keys as `device:<key>`, `detail` naming both devices and what each report said, citing both
  observations (FR-014, data-model.md)

**Checkpoint**: every case the spec names produces either a row that says how well it is known, or a
finding that says why there is none

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T078 [P] Carry the deltas of plan.md into docs/c4-model/04-data-model.md: the six new tables with
  the columns that actually ship, the closing of the "row per snapshot or validity interval" question
  for edges, `link_disagreement` in the findings categories, the engine's new grants, and the note that
  only `l1_link` and `has_address` are built because no fact family feeds the other two
- [X] T079 [P] Correct the statement that the engine reads no packs, in the comment at the top of
  `cmdEngine` in cmd/netmapper/engine.go and wherever docs/c4-model/02-containers.md and
  docs/c4-model/03-components.md repeat it. The constitution asks for the document and the behaviour to
  be corrected in the same change, and this is the one this feature makes false
- [X] T080 [P] Record in docs/c4-model/04-data-model.md that `interface.if_index`, `interface.kind` and
  `interface.parent_interface_id` are deliberately not built: no recipe collects an interface index and
  nothing describes a subinterface, so they would be three always-null columns
- [X] T081 Review `projectorVersion` in internal/graph/graph.go before merging: if the projection
  changed after the first snapshots were projected during development, bump it and say so in the
  commit. 002 and 003 both showed the bump is easy to forget exactly when it matters.
  **Done**: stays at 1. The projection did change three times during development, but every snapshot
  projected by the earlier versions lived in a throwaway test schema that its own test dropped. No
  stored set was produced by a version other than this one, so there is nothing for a bump to identify
- [X] T082 Run quickstart.md sections 2 to 7 against the containerlab lab and record every divergence
  in its "Divergences recorded during implementation" section, the way 002 and 003 did. A divergence is
  a finding about the design, not a detail to fix silently.
  **Done**: sections 2 to 7 pass against four cEOS nodes, two crawls, no error in the engine log. Five
  more divergences recorded, 8 to 12. Two of them matter. The lab turns out not to exercise aliasing at
  all, because every Arista command a recipe reads prints full interface names, so US1 stays covered by
  the fakeos tests alone. And an `unknown:` endpoint reference keeps the vendor's raw chassis spelling
  while the matcher normalises it, which is stable within one vendor and would split one cable in two
  across vendors; that one wants its own change
- [X] T083 Run `go test ./...` with `NETMAPPER_TEST_DSN` and `NETMAPPER_TEST_S3_ENDPOINT` set and
  confirm the whole suite passes, 001, 002 and 003 included: nothing in this feature may change a crawl
  outcome, a verdict or an entity set.
  **Done**: every package passes. Two failures had to be fixed first, and both were this feature
  reaching into an older one. `internal/parse` TestOutcomes fed the fakeos neighbours template a
  five-column line after T002 gave it a sixth; the fixture now carries a far-end port and asserts that
  the parser keeps its spelling verbatim, which is the half of FR-003 the parser cannot do.
  `cmd/netmapper` could not load its packs because the test built the pack root out of symlinks and
  `pack.LoadRoot` lists a directory with `os.ReadDir`, where a symlink is not a directory; the packs
  are copied now

> **Every task is done.** The whole `go test ./...` suite passes against PostgreSQL and Garage from
> `deploy/compose.yaml`, and quickstart.md sections 2 to 7 pass against the four-node cEOS topology.
> Divergences 1 to 7 came out of the implementation, 8 to 12 out of the lab run.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies. T002 and T003 are one change in two files and are easiest done
  together, even though only T002 is marked [P]
- **Foundational (Phase 2)**: depends on Setup. Blocks every story. Inside it, T004 to T013 are one
  migration file and are sequential by nature except the tables marked [P], which touch no shared
  constraint; T014 depends on the migration; T015 to T017 depend on nothing but T001
- **User stories (Phase 3 to 5)**: all depend on Phase 2
- **Polish (Phase 6)**: depends on the stories being complete, except T079 which can be done any time

### User Story Dependencies

- **US1 (P1)**: independent once Phase 2 is done. It is the only story that can be delivered alone
- **US2 (P2)**: needs US1's interfaces to attach a link to, and US1's far-end resolution (T039) to know
  which entity a report is about. It is not independent of US1, and the spec says so: an edge attaches
  to a port
- **US3 (P3)**: needs US2's endpoint references and pairing (T057, T058) to know what a report failed to
  pair with. Its `has_address` half (T065, T075) depends on US1 alone and could be pulled forward if a
  consumer needed addresses before topology

### Within Each Story

- Tests before implementation, failing first
- Reading before building, building before writing: T038 to T041 before T042, T057 to T060 before T061
- The story's own checkpoint before the next priority

### Parallel Opportunities

- T002 and T003 alongside T001
- The [P] tables of the migration, T006, T007 and T010, which reference nothing the others define
- Every test inside one story's test section: they are one file per story and independent cases, so
  they parallelise as authoring work rather than as separate files
- The three polish documentation tasks T078, T079 and T080
- US2 and US3 cannot be parallelised against US1, for the reason above

---

## Parallel Example: User Story 1

```bash
# The interface cases, all in internal/graph/interfaces_test.go:
Task: "T018 interfaces from the family, canonical names and fields"
Task: "T019 a neighbour's spelling lands as an alias"
Task: "T024 a port only a neighbour named, marked neighbour"
Task: "T026 the alias tie-break, with inputs that tie"

# The projection-level cases, all in internal/graph/graph_test.go:
Task: "T028 an entity set with nothing to project"
Task: "T030 re-projecting replaces, leaving no remnant"
Task: "T032 the same inputs give the same result"
```

---

## Implementation Strategy

### MVP (User Story 1 only)

1. Phase 1: Setup
2. Phase 2: Foundational
3. Phase 3: User Story 1
4. Stop and validate: an engineer can ask what a device's ports are, what state they are in, and what
   else calls them something else, with the evidence behind each. That is useful on its own, which is
   why the spec made it P1
5. The `edge` table exists and is empty, which is a correct state, not a half-built one

### Incremental delivery

1. Setup and Foundational
2. US1: ports and aliases, validated against the lab (quickstart section 2)
3. US2: the cables both ends agree on (quickstart sections 3 and 4)
4. US3: the one-sided cables, the addresses and the disagreements (quickstart sections 5 and 6)
5. Each story adds rows to tables the previous one created, and none changes what the previous one
   wrote

### Notes

- [P] means a different file with no dependency on an incomplete task
- Commit after each task or logical group
- Every test here builds a snapshot, so every one of them needs both test environment variables. That
  is the crawl's requirement, not the projector's
- The three tie-break tests the constitution asks for are T026, T054 and, by refusing rather than
  breaking the tie, T068. The third is not a tie-break any more, which is itself worth asserting
- No task adds a vendor name outside a pack. T002 is pack data and T040 is the only call into it

---

## Phase 7: Convergence

Found by `/speckit-converge` after the implementation pass. Each item names the artifact it traces to
and the kind of gap it closes.

- [X] T084 **CRITICAL** Reconcile the documented `l1_link` attributes with what the projector writes,
  per Constitution Development Workflow "a document that describes behaviour MUST be corrected in the
  same change as the behaviour" (contradicts). data-model.md's "attributes shape" presents
  `from_spelling` and `to_spelling` as part of every `l1_link` and scopes only the four `remote_*`
  fields to a one-sided edge; `link` in internal/graph/links.go writes the spellings for a `one_end`
  edge alone, so an agreed link carries `{"protocols": [...]}` and nothing else. Decide which is right
  and make both say it. The case for the code as it stands: on an agreed link each side's spelling of
  its own port is the canonical name, and what each side called the *other* side's port is already in
  `interface_alias`, so the two fields would duplicate the alias table. The case for the document: its
  own example is exactly the interesting case, sw1 writing `Ethernet1` where sw2 writes `Et1`
- [X] T085 Give each `has_address` edge the collection range of the observation it cites, per FR-008
  (partial). `addAddressEdges` in internal/graph/graph.go sets `first`/`last` from the entity's
  `first_seen`/`last_seen`, which spans every identity observation the entity was built from. A device
  reached on two addresses therefore gets two edges that each cite one observation and both claim the
  whole range, which is not "when its evidence was first and last collected". `readEntities` already
  reads the observation per address into `answeredOn`; it needs that observation's `collected_at`
  beside the id. Add a test with a device answering on two addresses asserting each edge's range equals
  its own observation's `collected_at`.
  **Done**: `answeredOn` now holds an `answer{obs, at}` per address and `addAddressEdges` takes the
  range from it. `TestAddressEdgeCarriesItsOwnEvidenceRange` was run against the old code first and
  failed as `addr:10.0.0.1 true false`, so it is not a test that would have passed either way
- [X] T086 Add the quarantined-snapshot test quickstart.md names, per the spec's Assumptions
  (missing). "A snapshot the coverage gate quarantined is projected like any other, the way resolution
  treats it." The behaviour holds today only by construction, because the gate records its verdict in
  `snapshot_judgement` and leaves `snapshot.state` at `closed`, and no test pins that: a future change
  that moved the verdict onto the snapshot row would silently stop quarantined snapshots being
  projected. Build a snapshot the gate quarantines, the way internal/gate's tests do, and assert it
  gets a projection like any other.
  **Done**: `TestQuarantinedSnapshotIsProjected` builds the 50% coverage shape internal/gate uses,
  asserts the verdict really is `quarantined`, then asserts the snapshot carries a current projection
  with interfaces and edges
- [X] T087 Record in docs/c4-model/04-data-model.md that the graph projector shipped without adding an
  entity kind, per the plan's Documentation deltas (partial). The `entity` row still reads "the
  projector's kinds are a migration, not a widening", written when the projector was still ahead. It
  needed none: an unresolved far end is an `unknown:` reference and some attributes, not an entity, and
  `entity.kind` keeps its `CHECK (kind = 'device')`.
  **Done**: the `entity` row in docs/c4-model/04-data-model.md now says the projector shipped without
  needing another kind, and that a future one is a migration rather than a silent widening

---

## Phase 8: Convergence

Found by `/speckit-converge` after the lab run. The feature has no functional gap left; these are a
document that still misdescribes the code, a question the lab answered, and one latent defect.

- [X] T088 **CRITICAL** Finish the correction T084 started, per Constitution Development Workflow "a
  document that describes behaviour MUST be corrected in the same change as the behaviour"
  (contradicts). data-model.md's "attributes shape" now shows two shapes where `link` in
  internal/graph/links.go produces three: an agreed link carries `protocols` alone, a one-sided link
  whose far end resolved carries `protocols` plus both spellings, and a one-sided link whose far end
  resolved to nothing carries those plus the four `remote_*` fields. T084 documented the first and the
  third. The prose is also wrong in one place: it says the far spelling is kept because "on a one-sided
  link whose far end resolved to nothing there is no port row to hang the far spelling off", but
  links.go keeps `to_spelling` on every one-sided link whatever the far end resolved to. The lab
  produced the undocumented shape on sw4, whose links are one-sided because the crawl deduplicated it
  against sw1's pinned serial. Add the third example and fix that sentence.
  **Done**: three shapes now, with the two conditions stated separately. Both spellings go on every
  one-sided link, because such a link has one report and the edge is the only place recording how that
  report worded things; the four `remote_*` fields go on only the subset whose far end resolved to
  nothing, because that is the case where no entity or interface row holds them
- [X] T089 Record in plan.md's "Known open points" what the lab answered about a shared medium, per
  plan: known open points (partial). The row said the question closes when "the lab runs long enough to
  say whether it happens, at which point it is a query, not a rule". It happened on the first run: the
  containerlab management bridge puts every node on one segment, so five of the seven links in a
  four-node lab are that segment and one is the actual cable. Nothing is wrong with the result, and the
  note should now say so rather than leaving the question open, because a reader counting links will
  otherwise think something is duplicated. The neighbouring row about two ports of one device cabled
  together stays open: what the lab produced instead is a device seeing *itself* on one port, which the
  equal-references rule drops.
  **Done**: the shared-medium row is closed with what the lab measured, and the loopback row stays open
  with a note that the lab produced a self-report rather than a self-cable
- [X] T090 Normalise the identifier in an `unknown:` endpoint reference the way `resolveFar` already
  does for the lookup, per FR-011 as clarified on 2026-09-25 (partial). `farRef` in
  internal/graph/links.go builds `unknown:chassis_id=<value>` from the raw string the device reported,
  so the lab names sw3 `unknown:chassis_id=001c.7374.a126` in Arista's dotted form while the matcher
  three functions away reads it as `00:1c:73:74:a1:26`. Nothing is broken today, because only LLDP is
  collected anywhere and one pack spells a chassis identifier one way. It breaks the moment a pack
  reports the same cable under two protocols that spell it differently: the two rows would land in two
  groups and one cable would become two edges, which is exactly what the clarification says must not
  happen. Passing the value through `pack.NormaliseMAC` is a one-line change, and the reason it was not
  folded into the lab run is that it renames every `unknown:` edge, so it wants its own test: two
  reports of one cable whose chassis identifier is spelled two ways collapse to one edge.
  **Done**: `farRef` passes the identifier through `pack.NormaliseMAC`, which leaves a hostname and an
  IP address untouched. `TestUnresolvedFarEndIdentifierIsNormalised` was run against the old code first
  and produced two links for one cable, so it is not a test that would have passed either way

---

## Phase 9: Convergence

Found by `/speckit-converge` after Phase 8. No functional gap: the suite passes and the lab passes.
All three are documentation precision, and all three sit in the two paragraphs describing the
`unknown:` endpoint reference and the edge attributes. That is the third pass in a row to find drift
there, which is noted at the end of this phase.

- [X] T091 **CRITICAL** Define `<kind>` in the `unknown:` endpoint reference, per Constitution
  Development Workflow "a document that describes behaviour MUST be corrected in the same change as
  the behaviour" (contradicts). data-model.md's reference table and research R6 both write the form as
  `unknown:<kind>=<value>[/<port>]` and then name the three sources as `remote_chassis_id`,
  `remote_mgmt_address` and `remote_system_name`, which are the fact family's field names.
  internal/graph/links.go trims the `remote_` prefix, so what is actually written is
  `unknown:chassis_id=…`, `unknown:mgmt_address=…` or `unknown:system_name=…`. `<kind>` is defined
  nowhere, so an engineer writing `WHERE to_ref LIKE 'unknown:remote_chassis_id=%'` straight from the
  document gets no rows and no hint why. Name the three literal values in both files; the lab's own
  output, `unknown:chassis_id=00:1c:73:74:a1:26/Management0`, is a good example to quote.
  **Done**: both files now name `chassis_id`, `mgmt_address` and `system_name` as the literal values and
  say the `remote_` prefix is dropped. data-model.md quotes the lab's reference and notes the
  observation behind it still records `001c.7374.a126`, which is the difference between the collected
  and the computed zone in one line
- [X] T092 Record in research R6 that the identifier in an `unknown:` reference is normalised, per
  Constitution Development Workflow and research R6 (partial). R6 sets out how the reference is built,
  including the order the three identifiers are tried, and T090 changed that construction without
  touching it. Nothing in R6 is false; what is missing is the fact that makes the rule work, which is
  that the value goes through `pack.NormaliseMAC` exactly as `resolveFar` normalises it to look for an
  entity, so one box reported under two spellings of its chassis MAC is one endpoint. Say it there,
  with the reason T090 gives: without it, one device reporting one cable under two protocols that
  spell a chassis MAC differently would produce two edges for one cable.
  **Done**: R6 now records the normalisation, why it exists, that anything which is not a MAC passes
  through, and the one thing that deliberately is *not* normalised: the far-end port, because
  canonicalising a port needs a platform and an unresolved device has none, which is FR-005 applied to
  the far end
- [X] T093 Qualify the spellings sentence in data-model.md's attributes section (partial). T088 wrote
  "Both spellings are carried by every one-sided link", and `link` in internal/graph/links.go writes
  `to_spelling` only when the report named a far-end port. A one-sided link to a device whose report
  named no port at all carries `from_spelling` alone, which is a fourth shape the section does not
  mention. Either qualify the sentence or say plainly that `to_spelling` follows the report.
  **Done**: the sentence now separates the two, and says the `remote_*` set holds only the fields the
  report supplied rather than always four. Both statements were checked by reading the key sets out of
  a real projection covering all four cases, not by reasoning about the code

> **A note for whoever does these.** T084 corrected this section, T088 corrected that correction, and
> T091 and T093 correct this one. Every fix was right and every fix left something, because the
> section is hand-written prose describing a string that code builds. It would drift less as a short
> grammar with a test asserting the shapes it produces. That is a change to how the artifact is
> written rather than a gap in this feature, so it is recorded here rather than made a task.

---

## Phase 10: Convergence

Found by `/speckit-converge` after two code-review passes and their fixes. F1 is the first plain
requirement violation this feature has produced; everything before it was documentation drift.

- [X] T094 Make one cable produce one link when only one side names a far-end port, per FR-009
  (partial). `farRef` in internal/graph/links.go returns `dev:<key>` when the far end resolved to an
  entity but the report named no port, because FR-011 identifies a link by its interfaces and there is
  no interface to name. The consequence is that the two ends of one cable build different references
  and land in different groups: A reporting B with a port keys `if:A/pA|if:B/port1`, while B reporting
  A without one keys `dev:A|if:B/port1`. Two `one_end` edges for one cable, where FR-009 says two
  reports describing the same cable from opposite ends must produce one link marked as agreed by both
  ends. `remote_interface` is optional in internal/fact/fact.go:41, so this fires on any platform that
  omits a far-end port id, which CDP and some LLDP implementations do.
  This is a change to the endpoint model, not a patch: a `dev:` endpoint has to be able to merge into
  an `if:` group of the same device once another report names the port, which means grouping cannot
  stay a plain map keyed on the sorted reference pair. Weigh it against simply recording the
  limitation: a `one_end` edge to `dev:B` is not wrong, it is less precise, and FR-009's "not two" is
  what it breaks. Whichever way it goes, the test is two reports of one cable where one names the
  far-end port and the other does not
- [X] T095 Decide what `from_spelling` is for, then make the code and data-model.md agree (contradicts).
  `readReports` in internal/graph/links.go sets `fromPort: local, fromSpelling: local` from one value,
  `local_interface`, which internal/fact/fact.go marks `Canonical: true` and the parser has already
  normalised. So `from_spelling` on a one-sided edge is always byte-identical to the name inside
  `from_ref`, and the sentence in data-model.md, "a one-sided link has exactly one report, so the edge
  is the only place recording how that report worded things", is true of `to_spelling` and false of
  `from_spelling`. Two ways out: drop the attribute, which is the smaller change and loses nothing
  since the canonical name is already in the reference and in `interface_alias`; or add a
  pre-canonical local spelling to the `neighbours` fact family so the parser keeps what the device
  actually printed, which is the only way the attribute earns its name. The second is a pack and
  schema change reaching back into collection, so it is a real decision rather than a cleanup
