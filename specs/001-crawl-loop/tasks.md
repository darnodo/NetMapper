---
description: "Task list for the crawl loop (find + scrape)"
---

# Tasks: Crawl loop (find + scrape)

**Input**: Design documents from `/specs/001-crawl-loop/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: Included. [quickstart.md](quickstart.md) lists the test cases the feature must pass, and
the constitution requires parser and recipe changes to be verifiable over stored output.

**Organization**: Tasks are grouped by user story so each story can be built and tested on its own.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3 from spec.md
- Paths are relative to the repository root. One Go module, `cmd/` plus `internal/`, tests next to
  the code (plan.md, Project Structure).

Integration tests use `internal/testutil` and skip when `NETMAPPER_TEST_DSN` (PostgreSQL) or
`NETMAPPER_TEST_S3_ENDPOINT` (Garage) is unset.

---

## Phase 1: Setup

**Purpose**: Module, binary skeleton and local infrastructure

- [X] T001 Create `go.mod` with module path `github.com/darnodo/NetMapper` (matches the `origin` remote https://github.com/darnodo/NetMapper; Go module paths are case sensitive) and require pgx/v5, pressly/goose/v3, scrapli/scrapligo, gosnmp/gosnmp, sirikothe/gotextfsm, minio/minio-go/v7, hashicorp/vault/api, go.yaml.in/yaml/v3 in go.mod
- [X] T002 [P] Create subcommand dispatch for `migrate`, `run`, `cancel`, `collector`, `engine` using stdlib `flag`, with a JSON `log/slog` handler on stderr and exit codes 0 success, 1 runtime error, 2 invalid input (contracts/cli.md); each subcommand is a stub returning exit 1 "not implemented" in cmd/netmapper/main.go
- [X] T003 [P] Create PostgreSQL and Garage services for dev and integration tests, with a one-shot Garage bootstrap that creates the bucket and an access key, in deploy/compose.yaml and deploy/garage.toml

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, configuration, perimeter, transports, packs, parser and writers used by every story

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

### Schema

- [X] T004 Create control plane migration: `config_version` (`document` text, "YAML exactly as given, never rewritten"), `perimeter` (`include cidr[]` "at least one entry", `exclude cidr[]`, name "unique per config_version"), `credential_set` (`config_version`, `position int`, `kind` in `ssh`/`snmp_v2c`/`snmp_v3`, `username text null`, `secret_ref text`, `max_attempts_per_device int NOT NULL CHECK (>= 1)`, `perimeter_ids bigint[]` "at least one"), `seed_set` (`targets text[]`, no `credential_set_id`), `job` (`state` in `running`/`cancelling`/`cancelled`/`succeeded`/`failed`, `config_version`, `snapshot_id`, `parameters jsonb`, `requested_by`, `created_at`/`started_at`/`ended_at`, `error text null`), `task` LIST partitioned by `job_id` with primary key `(job_id, id)` (`kind` in `find`/`scrape`, `target inet`, `target_name text null`, `platform text null`, `state` in `pending`/`claimed`/`done`/`duplicate`/`skipped`/`failed`/`cancelled`, `claimed_by`, `lease_expires`, `attempts int default 0`, `cred_attempts jsonb default '{}'` shaped `{"<credential_set_id>": {"n": int, "ok": bool}}`, `last_error text null`, `parent_task_id bigint null` with no FK, `skip_reason text null` "set only on `skipped` tasks", `UNIQUE (job_id, kind, target)`), `audit_log` (`ref uuid`, `at`, `actor`, `action`, `target inet`, `command text null` "never a credential value", `result` starting with `sent` or a result), and `SECURITY DEFINER` function `create_task_partition(job_id bigint)` owned by `netmapper_owner` (data-model.md, Partitioning and keys) in migrations/0001_control_plane.sql
- [X] T005 Create collected zone migration: `snapshot` (`state` in `open`/`closed`, `closed_at` "set once"), `parse_generation` with primary key `(snapshot_id, id)`, `observation` partitioned by `snapshot_id` with primary key `(snapshot_id, id)` (`status text NOT NULL CHECK (status IN ('collected','empty','unsupported','parse_failed','unreachable','denied'))`, `detail text null`, `task_id`, `target inet`, `transport text null`, `platform text null`, `recipe_id text null`, `fact_family`, `parse_generation_id` with FK `(snapshot_id, parse_generation_id)`, `parsed jsonb null`, `UNIQUE (snapshot_id, target, fact_family, task_id)`), `observation_raw` partitioned by `snapshot_id` (`snapshot_id`, `observation_id` with FK `(snapshot_id, observation_id)`, `step_id`, `command`, `hash bytea` FK to `raw_object`), `raw_object` (`hash bytea PK`, `size bigint`, `stored_at`, no `refcount`, not partitioned), `parse_generation` columns (`parser_versions jsonb null` "not written in this slice", `active bool`, partial unique index "exactly one true per snapshot"), `identifier_claim` partitioned by `snapshot_id` with FK `(snapshot_id, observation_id)` (`kind`, `subtype text null`, `value`, `strength` in `strong`/`weak`, `observation_id`, `collected_at`) with index `(snapshot_id, kind, value) WHERE strength = 'strong'`, and SQL function `claim_lock_key(snapshot_id bigint, kind text, value text) RETURNS bigint` as `hashtextextended(snapshot_id::text || '/' || kind || '/' || value, 0)`, and `SECURITY DEFINER` function `create_snapshot_partitions(snapshot_id bigint)` creating that snapshot's partitions of `observation`, `observation_raw` and `identifier_claim`, in migrations/0002_collected.sql
- [X] T006 [P] Create reported zone migration: `finding` (`domain` in `data_quality`/`compliance`, `category` in `unknown_platform`/`parse_failed`/`credential_denied`, `severity`, `subject_ref`, `detail jsonb`, `state`) and `finding_evidence (finding_id, snapshot_id, observation_id)` with FK `(snapshot_id, observation_id)` in migrations/0003_reported.sql
- [X] T007 [P] Create roles migration implementing the grant matrix of research R11 exactly: roles `netmapper_owner`, `netmapper_operator`, `netmapper_collector`, `netmapper_engine`; no role but the owner has `UPDATE` or `DELETE` on the collected zone or `audit_log`; `EXECUTE` on the partition functions for the operator only and on `claim_lock_key` for collector and engine, in migrations/0004_roles.sql
- [X] T008 Embed the migrations with `embed.FS` in migrations/embed.go and apply them with goose from `netmapper migrate` in internal/store/migrate.go, wired in cmd/netmapper/main.go
- [X] T009 [P] Create test helpers: open a pgx pool from `NETMAPPER_TEST_DSN` on a fresh schema with migrations applied, a minio client on a fresh bucket from `NETMAPPER_TEST_S3_ENDPOINT`, `t.Skip` when either is unset, in internal/testutil/testutil.go

### Configuration and perimeter

- [X] T010 [P] Parse and validate the configuration document per contracts/config.md in internal/config/config.go: `include` non-empty with valid CIDRs; `secret_ref` must start with `env:` or `vault:`, else error `secret_ref must be a reference, not a value`; `perimeters` must name existing perimeters; `kind: ssh` requires `username`; `snmp_v3` requires `username`; names unique per list; `max_attempts_per_device` required and at least 1; `discovery` keys optional with defaults `max_task_attempts: 3`, `step_idle_timeout: 60s`, `step_deadline: 30m`, `lease: 5m`, `claim_batch: 16`, `poll_interval: 1s` (research R16); keep the raw document bytes unchanged
- [X] T011 [P] Table tests for every validation rule and every default in internal/config/config_test.go
- [X] T012 [P] Implement `perimeter.Perimeter` with `Allowed(netip.Addr) bool` (include then exclude, `net/netip`) and `Resolve(ctx, host) (netip.Addr, error)` returning `ErrOutOfPerimeter` when the resolved address is refused; this is the only function transports may use to obtain an address to dial (research R8), in internal/perimeter/perimeter.go
- [X] T013 [P] Unit tests for include, exclude, IPv4, IPv6, hostname resolving outside, empty include in internal/perimeter/perimeter_test.go

### Contracts shared by all paths

- [X] T014 [P] Define fact family schemas `identity`, `neighbours`, `interfaces` exactly as in contracts/fact-families.md, with `Validate(family string, rows []map[string]any) error` rejecting unlisted fields and missing required ones, in internal/fact/fact.go
- [X] T015 [P] Define `SecretBackend` (`Resolve(ctx, ref) (Secret, error)`), a `Resolver` dispatching on the `env:` and `vault:` schemes, and the `env` backend; `Secret` must not implement `String`/`GoString`/`MarshalJSON` returning the value; in internal/secret/secret.go with tests in internal/secret/secret_test.go
- [X] T016 [P] Implement the `vault` backend for KV v2 references `vault:<kv v2 path>#<field>` using `VAULT_ADDR`/`VAULT_TOKEN`, in internal/secret/vault.go
- [X] T017 Define `Transport`, `Session`, `Target`, `Credential`, `Step`, `RawOutput` as in docs/c4-model/04-data-model.md, plus error kinds `ErrSilent` (no answer) and `ErrAuth` (answered, credential rejected, carrying the banner or auth response bytes as evidence), and an `Audit` hook with `Sent(action, target, command) (ref)` called before a command or request leaves and `Result(ref, result)` called after, each writing its own `audit_log` row immediately in its own statement, never buffered (research R6, FR-023), in internal/transport/transport.go
- [X] T018 [P] Implement a fake transport replaying recorded output per address and command from a directory, supporting per address: silent, auth failure for named credentials, answers on one transport only, and blocking until context cancel; record every Open for assertions, in internal/transport/fake/fake.go
- [X] T019 [P] Implement the SNMP transport with gosnmp (v2c and v3; GET, GETNEXT, GETBULK only, no SET anywhere), setting `Target` to the address returned by `perimeter.Resolve`, calling `Audit.Sent` before and `Audit.Result` after each request, with actions `snmp.get`/`snmp.walk`/`snmp.auth`, and sending only OIDs given by the pack registry (the `_base` probe or a recipe), in internal/transport/snmp/snmp.go
- [X] T020 [P] Implement the SSH transport with scrapligo, loading the platform definition from the pack's `scrapli.yaml`, dialing only the address returned by `perimeter.Resolve`, sending only `Step.Command` (no config mode call), with no output size cap, enforcing a per step idle timeout (no byte received for `step_idle_timeout`, returned as `ErrIdleTimeout`) and a per step deadline (`step_deadline`, returned as `ErrDeadline`); scrapligo's operation timeout is a total, so verify whether it can express an idle timeout and otherwise run the idle timer around its channel reads; calling `Audit.Sent` before and `Audit.Result` after each authentication and command, with `ssh.auth`/`ssh.command`, in internal/transport/ssh/ssh.go

### Packs and parsing

- [X] T021 [P] Implement the pack loader and `PackRegistry` (`Probe` returning the `_base` pack's SNMP probe, `Fingerprint`, `Recipe`, `Normalise`, plus identifier extraction rules) per contracts/pack-format.md, refusing to load when a CLI `command` does not start with an entry of the pack's `read_only`, a template is missing or does not compile, a `map` target is not a field of the family schema, `name` is duplicated, or not exactly one pack named `_base` declares `probe`; expose the pack version hash used in `observation.recipe_id` as `<pack>/<recipe id>@<pack version hash>` (FR-011), in internal/pack/registry.go
- [X] T022 [P] Create a minimal test pack `fakeos` (fingerprint, identifiers, `neighbours` and `interfaces` recipes, templates) with no Go code in internal/pack/testdata/fakeos/ and loader tests covering every refusal rule in internal/pack/registry_test.go
- [X] T023 [P] Implement the parser: TextFSM via gotextfsm, SNMP column mapping, pack field mapping with `'=literal'` values, interface name canonicalisation, `merge_on`; return outcome `empty` for empty output and `parse_failed` when a template yields zero rows on non-empty output (research R9), in internal/parse/parse.go with fixture tests in internal/parse/parse_test.go
- [X] T024 [P] Create the base pack packs/_base/pack.yaml exactly as in contracts/pack-format.md (probe OIDs `1.3.6.1.2.1.1.2.0` and `1.3.6.1.2.1.1.1.0`, no recipes), and the `arista_eos` pack: pack.yaml (fingerprint via sysObjectID prefix `1.3.6.1.4.1.30065` and `show version`, identifiers `serial` strong, `chassis_mac` strong, `hostname` weak, `read_only: ["show "]`), scrapli.yaml, recipes/neighbours.yaml, recipes/interfaces.yaml, templates taken from ntc-templates (keep its Apache 2.0 notice in packs/arista_eos/NOTICE), and parser fixtures copied from the ntc-templates test directory for those templates (same Apache 2.0 notice) in packs/arista_eos/testdata/; lab-recorded outputs are added later by T045, so this task needs no lab
- [X] T025 Lint every directory under packs/ with the loader from T021 and parse every recorded output with its template, in packs/packs_test.go

### Writers and frontier

- [X] T026 [P] Implement the raw store: SHA-256 of the exact bytes, key `raw/sha256/<hex>`, `StatObject` before `PutObject`, in internal/store/raw.go
- [X] T027 Implement the observation writer in one pgx batch per call: `raw_object` with `ON CONFLICT (hash) DO NOTHING`, `observation` with `ON CONFLICT (snapshot_id, target, fact_family, task_id) DO NOTHING`, `observation_raw` with `snapshot_id` and `command`, identifier claims, all insert only and all carrying `snapshot_id` for the composite keys; audit rows are not part of this batch (T017); validate `parsed` with internal/fact before writing, in internal/store/observations.go
- [X] T028 Implement the frontier: `Enqueue` (`ON CONFLICT (job_id, kind, target) DO NOTHING`), `InsertSkipped(job, target, parent, reason)` inserting state `skipped` directly, `Claim(collectorID, kinds, batch, lease)` with the `FOR UPDATE SKIP LOCKED` query from research R3 restricted to jobs in state `running`, `Complete(task, state)`, `Fail(task, kind, message)` applying the retry rule per kind from data-model.md (`error` requeued to `pending` while `attempts < max_task_attempts`, else `failed`; `deadline`, `credential_unresolved` and `credential_partial` go to `failed` at once; `last_error` set to `<kind>: <message>`), in internal/frontier/frontier.go with integration tests in internal/frontier/frontier_test.go

**Checkpoint**: Foundation ready. User story work can begin.

---

## Phase 3: User Story 1 - Discover a network from a seed (Priority: P1) 🎯 MVP

**Goal**: A run starts from a seed, identifies each device once, follows neighbours inside the perimeter, collects every pack family, and leaves a closed snapshot with provenance on every fact.

**Independent Test**: With the fake transport, a seed and two neighbours that report each other end in a `closed` snapshot where each device has exactly one `identity`, `neighbours` and `interfaces` observation, and each observation links to retrievable raw bytes. On the lab, quickstart.md section 2 for sw1 and sw2.

### Tests for User Story 1

> Write these first and confirm they fail.

- [X] T029 [P] [US1] Crawl test: seed plus two neighbours reporting each other and themselves, assert no loop, one observation per family per device, snapshot `closed`, job `succeeded`, in internal/collector/crawl_test.go
- [X] T030 [P] [US1] Dedup test: one device on two addresses queued at once, run two finds concurrently, assert exactly one task `done` and one `duplicate`, one `scrape` enqueued, claims written by both, in internal/collector/dedup_test.go
- [X] T031 [P] [US1] Perimeter test: a neighbour reported outside the perimeter becomes a `find` task in state `skipped` with `skip_reason = 'out_of_perimeter'` and `parent_task_id` set, the fake transport records zero Open calls for it, and no observation exists for that address, in internal/collector/perimeter_test.go
- [X] T032 [P] [US1] Provenance test: every `collected` observation has `observation_raw` rows with `command` and `hash`, and the object at `raw/sha256/<hex>` equals the fake device output byte for byte; identical output from two devices is stored once, in internal/collector/provenance_test.go
- [X] T033 [P] [US1] Start test: `Start` refuses with the exact messages of contracts/cli.md for missing perimeter, empty include, seed outside, unresolvable seed, no covering credential set, and inserts no `job` row, in internal/jobrunner/start_test.go
- [X] T034 [P] [US1] SC-006 test: crawl a network of `fakeos` devices using only internal/pack/testdata/fakeos, in internal/collector/newpack_test.go

### Implementation for User Story 1

- [X] T035 [US1] Implement `Start(ctx, pool, doc, perimeterName, seedSetName)`: validate with internal/config, check FR-001 with the messages of contracts/cli.md, then in one transaction insert `config_version` (document verbatim), perimeters, credential sets with `position` in document order, seed sets, `snapshot` (`open`), `parse_generation` (`active`, `parser_versions` left null), `job` (`running`, `config_version`, `parameters` with `perimeter_id` and `seed_set`), then call `create_snapshot_partitions(snapshot_id)` and `create_task_partition(job_id)`, and insert one `find` per seed; runs as `netmapper_operator`, in internal/jobrunner/start.go
- [X] T036 [US1] Wire `netmapper run --config --perimeter --seed-set`: print only the job id on stdout, exit 2 with one line per problem on stderr, in cmd/netmapper/run.go
- [X] T037 [US1] Implement find step 1 (no transaction open): SNMP probe with the OIDs from `PackRegistry.Probe`, then SSH fingerprint commands from the loaded packs, matched through `PackRegistry.Fingerprint`, extract identifiers, using the first covering credential set that opens a session, in internal/collector/find.go
- [X] T038 [US1] Implement find step 2 exactly as research R4: short transaction with no network I/O, `pg_advisory_xact_lock(claim_lock_key(...))` for each strong identifier in sorted key order, check for a strong claim with same kind and value in the snapshot whose `identity` observation is `collected` and whose `task_id` differs, write the `identity` observation and claims in both cases, set `duplicate_of_task` and complete `duplicate` when found; a reclaimed task finding its own claims continues, in internal/collector/claims.go
- [X] T039 [US1] Implement find step 3: run the `neighbours` recipe, then in one short transaction write the `neighbours` observation, `Enqueue` a `find` per neighbour address inside the perimeter, `InsertSkipped` per address outside it, enqueue nothing for rows whose address is not typed `ipv4` or `ipv6` (names are never resolved), enqueue one `scrape` with `platform`, and complete `done`, in internal/collector/find.go
- [X] T040 [US1] Implement scrape: for each family the pack defines except `identity` and `neighbours`, select the recipe by platform, version and transport, run steps in one session, parse, write one observation with outcome `collected`, `empty` or `unsupported`; `ErrIdleTimeout` records that family `unreachable` with detail `timeout` and continues with the next family; `ErrDeadline` ends the task through `Fail` with `last_error` `deadline: <family> after <duration>` and no observation for that family (research R12); hold each step's output in memory once for hashing, upload and parsing, with a `ponytail:` comment naming the limit and the channel-level streaming upgrade path, in internal/collector/scrape.go
- [X] T041 [US1] Implement the worker pool: `--workers` goroutines (default 64), `--consume find,scrape`, claim with `claim_batch` and `lease` every `poll_interval`, dispatch to find or scrape, wire `AuditFunc` to the writer, in internal/collector/pool.go
- [X] T042 [US1] Wire `netmapper collector [--id] [--workers 64] [--consume] [--packs]`, reading `NETMAPPER_S3_*` and exiting 2 when a pack fails to load or lint, in cmd/netmapper/collector.go
- [X] T043 [US1] Implement the job runner close: for each `running` job with no `pending` or `claimed` task, set `snapshot.closed_at`, `snapshot.state = 'closed'`, `job.state = 'succeeded'`, `job.ended_at`, in internal/jobrunner/runner.go
- [X] T044 [US1] Wire `netmapper engine` running the job runner loop; it must not read `NETMAPPER_S3_*`, `VAULT_*` or packs, in cmd/netmapper/engine.go
- [ ] T045 [US1] Create the two-switch containerlab topology (sw1, sw2 on cEOS, LLDP between them, SNMP and SSH enabled) and the lab configuration with perimeter `lab` and seed set `lab-seeds`, in test/lab/two-switch.clab.yaml and test/lab/netmapper.yaml; record the `show version`, `show lldp neighbors detail` and `show interfaces status` outputs of both nodes into packs/arista_eos/testdata/lab/ so T025 also parses real cEOS output
  - Done: topology, node configs and lab configuration. Open: recording cEOS output, which needs containerlab and a cEOS image (not available on the implementation machine).

**Checkpoint**: User Story 1 works on its own: run quickstart.md section 2 for sw1 and sw2.

---

## Phase 4: User Story 2 - Account for what could not be collected (Priority: P2)

**Goal**: Every attempted target ends with an outcome. Unreachable, denied, unidentified platform and parse failure are distinct, and the last three raise findings.

**Independent Test**: With the fake transport, one silent address, one device refusing every credential and one device matching no pack each appear with a distinct `identity` status, and the right findings exist. On the lab, quickstart.md section 2 for the unused address and the wrong-password host.

### Tests for User Story 2

- [X] T046 [P] [US2] Outcome test: silent address gives `identity` `unreachable`; SNMP silent but SSH answering is not a failure; device matching no pack gives `unsupported` with detail `unknown_platform` plus a `data_quality`/`unknown_platform` finding, and the crawl continues elsewhere, in internal/collector/outcome_test.go
- [X] T047 [P] [US2] Credential test: sets tried in `position` order; no set exceeds its `max_attempts_per_device` on a device, including across a task reclaim (counts read back from `task.cred_attempts`); an `env:` reference resolving to nothing moves to the next set without counting an attempt; a device rejecting every covering set gives `denied`; a device where no covering set resolved ends `failed` with `last_error` starting `credential_unresolved:`; a device that rejected set A while set B never resolved ends `failed` with `last_error` = `credential_partial: rejected A; unresolved B`, raises no finding, and its rejection of A is in `audit_log` with result `auth_failed`; neither case writes an `identity` observation; a task that opened a session with set A, then crashed, retries with A even when A's `n` has reached its budget, and does not record `denied`, in internal/collector/creds_test.go
- [X] T048 [P] [US2] Denied finding test: a `compliance`/`credential_denied` finding with severity `high` and `subject_ref` `target:<address>` has a `finding_evidence` row pointing at the `denied` observation, whose raw output holds the auth response bytes, in internal/collector/denied_test.go
- [X] T049 [P] [US2] Parse drift test: output not matching its template gives `parse_failed`, raw output still stored, and a `data_quality`/`parse_failed` finding, in internal/collector/parsefail_test.go

### Implementation for User Story 2

- [X] T050 [US2] Implement credential iteration: covering sets in `position` order, try first any set with `ok = true` in `task.cred_attempts` without consuming budget (on failure set `ok = false` and increment `n`); for other sets increment `n` in its own statement before each attempt and skip a set whose `n` reached `max_attempts_per_device`; set `ok = true` when a session opens (research R7); resolve the secret immediately before `Open` and drop it when the session closes, log and skip an unresolvable reference, in internal/collector/creds.go, replacing the first-covering-set logic in internal/collector/find.go
- [X] T051 [US2] Classify find outcomes per research R6: `unreachable` only when every transport returned `ErrSilent`; `denied` only when at least one transport answered and every covering set was presented and failed with `ErrAuth`, storing the auth evidence as raw output; when any covering set never resolved, write no observation and call `Fail(task, credential_unresolved, <unresolved sets>)` if no set was presented, or `Fail(task, credential_partial, "rejected <sets>; unresolved <sets>")` if at least one was presented and rejected, logging the latter at warn level with the target and set names; `unsupported` with detail `unknown_platform` when a transport answered and no pack matched, in internal/collector/find.go
- [X] T052 [P] [US2] Implement `RaiseFinding(snapshot, domain, category, severity, subjectRef, detail, evidenceObservationIDs)` in internal/store/findings.go
- [X] T053 [US2] Raise findings: `data_quality`/`unknown_platform` (severity `warning`) from find, `data_quality`/`parse_failed` (severity `warning`) from scrape and find step 3, `compliance`/`credential_denied` (severity `high`) citing the `denied` observation, in internal/collector/find.go and internal/collector/scrape.go
- [X] T054 [US2] Record why a task failed without writing any observation for it: `Fail` and the expired-lease path set `last_error` with a kind prefix, `lease_expired: <attempts> attempts` (the collector died or hung, the device may be fine), `deadline: <family> after <duration>` (step deadline too short), `credential_unresolved: <set names>` (nothing was presented), `credential_partial: rejected <sets>; unresolved <sets>` (some sets rejected, others never presented) or `error: <message>` (the task returned an error it could not turn into an outcome), with the retry rule per kind from data-model.md; the job runner writes nothing for `failed` tasks when closing, and observations already written by the task stay as they are, in internal/frontier/frontier.go and internal/jobrunner/runner.go
- [X] T055 [US2] Extend the lab with an unused address inside the perimeter and a host whose credentials reject the lab sets, in test/lab/two-switch.clab.yaml and test/lab/netmapper.yaml

**Checkpoint**: User Stories 1 and 2 both pass their independent tests.

---

## Phase 5: User Story 3 - Survive an interruption (Priority: P2)

**Goal**: A restart during a run loses nothing and visits no device twice; several collectors share one run; a run can be cancelled and keeps what it collected.

**Independent Test**: With the fake transport, stop a worker mid-scrape, let the lease expire, start another, and assert the final observations equal those of an uninterrupted run with no duplicate row. On the lab, quickstart.md sections 3 and 5.

### Tests for User Story 3

- [X] T056 [P] [US3] Resume test: cancel a worker's context while a scrape blocks, use a short lease, start a second worker, assert the job succeeds and `SELECT target, fact_family, count(*) ... HAVING count(*) > 1` returns zero rows; repeat with the crash between find steps 2 and 3, in internal/collector/resume_test.go
- [X] T057 [P] [US3] Attempts test: a task that keeps crashing reaches `max_task_attempts`, becomes `failed` with `last_error`, is requeued once by the final retry pass, and if it fails again stays `failed` with `last_error` starting `lease_expired:` and has no observation beyond those it wrote before crashing, in internal/jobrunner/retry_test.go
- [X] T058 [P] [US3] Two collectors test: two pools with different ids on one job, every task handled by exactly one of them, both ids present in `task.claimed_by`, in internal/collector/multi_test.go
- [X] T059 [P] [US3] Cancel test: `cancel` during a run leaves no `pending` task, keeps every observation written before, closes the snapshot and ends the job `cancelled`, in internal/jobrunner/cancel_test.go

### Implementation for User Story 3

- [X] T060 [US3] Add lease renewal every lease/3 while a task runs; when renewal fails because the task was reclaimed, cancel the task context and write nothing more, in internal/frontier/lease.go and internal/collector/pool.go
- [X] T061 [US3] Before each claim, mark expired `claimed` tasks whose `attempts` reached `max_task_attempts` as `failed` with `last_error = 'lease_expired: <attempts> attempts'` (same format as T054), in internal/frontier/frontier.go
- [X] T062 [US3] Add the final retry pass: when a job has no `pending` or `claimed` task and `parameters.final_pass_done` is not set, reset every `failed` task, whatever its `last_error` kind, to `pending` with `attempts = 0` and `cred_attempts` kept, set `final_pass_done`, and close only on the next empty check, in internal/jobrunner/runner.go
- [X] T063 [US3] Implement cancellation: `Cancel(job)` sets `cancelling` (error when the job is not `running`); the runner sets `pending` tasks to `cancelled`, waits for `claimed` tasks to finish or expire, closes the snapshot and ends the job `cancelled`, in internal/jobrunner/cancel.go
- [X] T064 [US3] Wire `netmapper cancel <job-id>` with exit 2 when the job is not `running`, in cmd/netmapper/cancel.go
- [X] T065 [US3] Handle SIGTERM in the collector: stop claiming, let in-flight steps finish for up to the lease duration, then exit, in cmd/netmapper/collector.go

**Checkpoint**: All three stories pass their independent tests.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T066 [P] Immutability test: connected as `netmapper_collector`, `UPDATE` and `DELETE` on `observation`, `observation_raw`, `raw_object` and `identifier_claim` fail (FR-010), in internal/store/roles_test.go
- [X] T067 [P] Secret leak test: run a crawl with known lab secret values, then search every text and jsonb column of every table, the audit log and captured slog output for them and assert zero hits (FR-017, SC-007), in internal/collector/secrets_test.go
- [X] T068 [P] Audit test: every command the fake transport received has a `sent` row written before the fake saw it (the fake checks the row exists when the command arrives) and a result row with the same `ref`; killing the worker while a command blocks leaves the `sent` row in place (FR-023), in internal/collector/audit_test.go
- [X] T069 [P] Carry the documentation deltas listed in plan.md into docs/c4-model/04-data-model.md (`closed` snapshot state, `claim_lock_key`, task and credential set columns, `observation.detail`, `observation_raw.command`, derived `refcount`, `collected_at` on claims)
- [ ] T070 Run quickstart.md sections 1 to 5 against the lab and record any divergence in specs/001-crawl-loop/quickstart.md
  - Divergences recorded; sections 1, 4 and 5 checked without the lab. Open: sections 2 and 3 on the lab.

---

## Dependencies & Execution Order

### Phase dependencies

- Setup (Phase 1): none
- Foundational (Phase 2): after Setup; blocks every story
- US1 (Phase 3): after Foundational
- US2 (Phase 4): after US1. It changes find.go and scrape.go written in US1 (credential iteration, outcome classification, findings)
- US3 (Phase 5): after US1. Independent of US2, and can run in parallel with it except for internal/jobrunner/runner.go (T054 and T062) and internal/frontier/frontier.go (T054 and T061): do those pairs in sequence
- Polish (Phase 6): after the stories it tests; T066 needs only Foundational

### Within Phase 2

- T004 → T005 → T008; T006, T007 after T005
- T017 before T018, T019, T020
- T021 before T022, T024, T025; T014 before T021 and T027
- T026 before T027; T027 and T028 after T008

### Within each story

- Tests first and failing, then implementation
- US1: T035 → T036; T037 → T038 → T039; T040 after T039; T041 after T039 and T040; T042 after T041; T043 → T044
- US2: T050 → T051 → T053; T052 before T053
- US3: T060 and T061 before T062; T063 → T064; T065 after T060

---

## Parallel Examples

### Phase 2

```text
T010 config.go          T012 perimeter.go       T014 fact.go
T015 secret.go          T016 vault.go           T023 parse.go
T026 raw.go             T009 testutil.go
```

### User Story 1 tests

```text
T029 crawl_test.go      T030 dedup_test.go      T031 perimeter_test.go
T032 provenance_test.go T033 start_test.go      T034 newpack_test.go
```

### User Story 2 and 3 tests (once US1 is done, two people)

```text
T046 outcome_test.go    T047 creds_test.go      T048 denied_test.go     T049 parsefail_test.go
T056 resume_test.go     T057 retry_test.go      T058 multi_test.go      T059 cancel_test.go
```

---

## Implementation Strategy

### MVP (User Story 1 only)

1. Phase 1 and Phase 2
2. Phase 3
3. Stop and validate: quickstart.md section 1 (US1 rows) and section 2 for sw1 and sw2

### Incremental delivery

1. Foundation, then US1: a crawl that discovers and collects with provenance
2. US2: every silence explained, findings raised
3. US3: restarts, several collectors, cancellation
4. Polish: constitution checks as tests, documentation deltas, full quickstart run

### Two developers after US1

- Developer A: US2
- Developer B: US3, coordinating on internal/jobrunner/runner.go

---

## Notes

- [P] means a different file and no dependency on an incomplete task
- Commit after each task or logical group, stating the constitution principle it touches
- No vendor name outside packs/ and internal/pack/testdata/ (principle V)
