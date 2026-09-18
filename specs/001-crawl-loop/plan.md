# Implementation Plan: Crawl loop (find + scrape)

**Branch**: `001-crawl-loop` | **Date**: 2026-09-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/001-crawl-loop/spec.md`

## Summary

Build the collector's core loop in Go. A run starts from `netmapper run`, which validates the
configuration and puts one `find` task per seed on a PostgreSQL frontier. Collectors claim tasks with
`FOR UPDATE SKIP LOCKED` under a lease. A `find` checks the perimeter, fingerprints the device over
SNMP then SSH with no transaction open, then in a short transaction takes an advisory lock per strong
identifier and writes its identifier claims, which are the crawl's deduplication index. It then reads
the neighbour table and, in a second short transaction, enqueues a `find` per in-perimeter neighbour,
a `skipped` task per out-of-perimeter one, and a `scrape` for the device. A `scrape` runs the pack recipes for that platform and version, stores raw output in Garage
by SHA-256, and writes one observation per fact family with a non-null status. The engine's job runner
does one final retry pass when the queue empties, then closes the snapshot. Details and trade-offs are
in [research.md](research.md).

## Technical Context

**Language/Version**: Go, latest stable (1.26 or later)

**Primary Dependencies**: pgx/v5, goose/v3, scrapligo, gosnmp, gotextfsm, minio-go/v7,
hashicorp/vault/api, go.yaml.in/yaml/v3. Standard library for everything else (`net/netip`,
`log/slog`, `flag`, `crypto/sha256`).

**Storage**: PostgreSQL (control plane, frontier, observations, findings); Garage over S3 (raw output)

**Testing**: `go test`; integration tests against PostgreSQL and Garage from `deploy/compose.yaml`;
fake `Transport` replaying recorded output; containerlab with two Arista cEOS nodes for end to end

**Target Platform**: Linux containers (amd64, arm64)

**Project Type**: single Go binary with role subcommands (service + operator CLI)

**Performance Goals**: none measured yet. Topology discovery must not wait on collection time
(FR-005). Concurrency, lease, batch size, poll interval, attempts, step idle timeout and step deadline are
bounded and configurable, with defaults that are starting points rather than measured values.
`max_attempts_per_device` has no default, because a wrong value locks accounts (research R16)

**Constraints**: no packet outside the perimeter; no secret at rest; no crash state in collector memory;
a per step idle timeout and deadline so one device cannot stall a run (no output cap: a truncated table would read as disappearances); one step's output held in memory once, a documented known limit; no lock held across network I/O

**Scale/Scope**: one pack shipped (`arista_eos`), three fact families (`identity`, `neighbours`,
`interfaces`), four subcommands plus `migrate`. Shipping only Arista cEOS is a deliberate narrowing of
the v1 scope for this slice, chosen because it runs in containerlab and ntc-templates covers it. It is
not the final platform list; further platforms arrive as packs

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle / constraint                                        | Status               | How this plan holds it                                                                                                                                                                                                                                                                     |
| ------------------------------------------------------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| I. Evidence travels with the answer                           | Pass (partial scope) | No interface serves graph data here. Every observation carries target, command or OID, time, raw hash and parser version (FR-008), which is what later answers will cite                                                                                                                   |
| II. Observations immutable, rest rebuildable                  | Pass                 | Collected zone is insert only, enforced by role grants. `raw_object.refcount` and `identifier_claim.last_seen` are not updated in place; both become derived. `task` is control plane; a `skipped` task is also rederivable from the `neighbours` observation and the job's config version |
| III. Credentials and reach stay in the collector              | Pass                 | Only `collector` loads packs, resolves `secret_ref` and dials. `run`, `cancel`, `engine`, `migrate` never do. One dial function applies the perimeter check. Config rejects literal secrets                                                                                                |
| IV. Read only, outward                                        | Pass                 | SNMP client exposes no SET; SSH sends only recipe commands; pack loader enforces the pack's `read_only` prefixes and tests lint shipped packs                                                                                                                                              |
| V. Vendor specifics are data                                  | Pass                 | Fingerprint rules, identifier extraction, scrapligo platform definitions, recipes, templates and interface naming live in `packs/`. Fact family schemas are neutral. A test pack in `internal/pack/testdata` proves a platform can be added without code change                            |
| One binary, three roles                                       | Pass                 | `collector`, `engine`, plus operator subcommands `migrate`, `run`, `cancel` (no new role, no network listener)                                                                                                                                                                             |
| Frontier in PostgreSQL, SKIP LOCKED, no crash state in memory | Pass                 | Lease, attempts and per-set credential counters are all task columns                                                                                                                                                                                                                       |
| Raw output in object store by hash                            | Pass                 |                                                                                                                                                                                                                                                                                            |
| Status enum closed and non-null                               | Pass                 | The six statuses are unchanged. An out-of-perimeter target is a `skipped` task with no observation, since no packet was sent (research R8)                                                                                                                                                 |
| Config YAML posted whole, versioned, recorded on run          | Pass, sequencing     | Stored whole as `config_version` by `netmapper run` until the API config service exists; `job.config_version` set on every run                                                                                                                                                             |
| Workflow: open questions go to `docs/`                        | Action               | The deltas listed below must be carried into `docs/c4-model/04-data-model.md` in this branch                                                                                                                                                                                               |

Post-design re-check: still passes. The design added `task.cred_attempts`, `task.skip_reason`,
`observation.detail`, `observation_raw.command`, the `claim_lock_key` SQL function and a `closed`
snapshot state. None crosses the
collected/computed boundary or moves a credential.

Principles touched by this feature: II, III, IV, V.

## Documentation deltas to carry into `docs/c4-model/04-data-model.md`

- `snapshot.state` gains `closed` (closed, not yet judged by the gate).
- Invariant on `identifier_claim`: every writer of strong claims takes the sorted advisory locks in
  the inserting transaction, with no network I/O inside it. **Done** in this branch.
- `claim_lock_key(snapshot_id, kind, value)` SQL function, the single definition of the lock key.
- `task` gains `cred_attempts`, `last_error`, `parent_task_id`, `platform`, `target_name`,
  `skip_reason`.
- `credential_set` gains `config_version`, `position`, `max_attempts_per_device`; `seed_set` loses
  `credential_set_id`.
- `observation` gains `task_id`, `platform`, `detail`; `observation_raw` gains `command`.
- `raw_object.refcount` is derived, not stored; `raw_object` gains `size`.
- `identifier_claim.first_seen`/`last_seen` replaced by `collected_at`.

## Project Structure

### Documentation (this feature)

```text
specs/001-crawl-loop/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── cli.md
│   ├── config.md
│   ├── pack-format.md
│   └── fact-families.md
└── tasks.md              # /speckit-tasks
```

### Source Code (repository root)

```text
cmd/netmapper/main.go         # subcommand dispatch: migrate, run, cancel, collector, engine

internal/
├── config/                   # YAML parse and validation, FR-001 checks, config_version write
├── perimeter/                # netip include/exclude, the shared Dial guard
├── frontier/                 # enqueue, claim, lease renew, complete, fail (SQL)
├── collector/                # worker pool, find path, scrape path, credential ordering
├── transport/
│   ├── ssh/                  # scrapligo session, platform def from pack
│   ├── snmp/                 # gosnmp get/walk, no SET
│   └── fake/                 # recorded-output transport for tests
├── secret/                   # SecretBackend: env, vault
├── pack/                     # registry, loader, read-only lint
├── parse/                    # textfsm, SNMP column mapping, field mapping, name normalisation
├── fact/                     # fact family schemas (identity, neighbours, interfaces)
├── store/                    # observations, claims, findings (pgx batch); audit rows before and after each command; raw objects (minio-go)
└── jobrunner/                # engine: final retry pass, failed sweep, snapshot close, cancel

migrations/                   # goose SQL, embedded
packs/arista_eos/             # the one shipped pack
deploy/compose.yaml           # PostgreSQL + Garage for dev and integration tests
test/lab/                     # containerlab topology and lab config
```

**Structure Decision**: one Go module at the repo root, `cmd/` plus `internal/`. Tests sit next to the
code (`_test.go`); lab assets live in `test/lab/`. Interfaces are limited to the five in the data model
doc (`Transport`, `Session`, `Parser`, `SecretBackend`, `PackRegistry`); everything else is concrete.

## Known test debt

| Behaviour | Why it is untested | Closed when |
| --------- | ------------------ | ----------- |
| A reclaimed `scrape` skips the families it already wrote, so the device is not asked the same command twice (SC-004) | The shipped families leave `scrape` with one family (`interfaces`), so no scrape can stop after writing one family and before finishing. Deliberately removing the skip leaves every test green. The `UNIQUE (snapshot_id, target, fact_family, task_id)` constraint still prevents a second row; what goes unchecked is the repeated read-only command | A second scrape family exists (for example `mac_table`): add it to the `fakeos` test pack and extend `internal/collector/resume_test.go` with a crash after the first family is written, asserting that its command reaches the device once |

## Complexity Tracking

| Addition                                                    | Why needed                                                                                                                  | Simpler alternative rejected because                                                                                                                 |
| ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Advisory lock on `identifier_claim` instead of a constraint | a device reached on several addresses is identified once, across collectors and restarts, while the table stays append only | a unique index breaks append only; a separate key table duplicates the index the data model already names; an in-memory set breaks FR-012 and FR-013 |
| `netmapper run` / `cancel` writing to the database directly | a run must be triggerable before the API exists                                                                             | waiting on the API feature blocks every test of this one; the API will call the same function                                                        |
