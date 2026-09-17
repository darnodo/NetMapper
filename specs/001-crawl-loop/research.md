# Research: Crawl loop (find + scrape)

Most of the stack is already fixed by the constitution and `docs/c4-model/`. This file settles what
those documents leave open for this feature. Each entry gives the decision, why, and what else was
considered.

## R1. Language and toolchain

- Decision: Go, latest stable release at the time of implementation (1.26 or later), one module at
  the repo root, one binary `netmapper`.
- Rationale: fixed by the constitution. Standard `log/slog` for logs, `flag` for subcommands.
- Alternatives: cobra for the CLI. Rejected: five subcommands do not need a framework.

## R2. PostgreSQL access and migrations

- Decision: `github.com/jackc/pgx/v5` with `pgxpool`. Plain SQL migrations embedded with `embed.FS`
  and applied by `github.com/pressly/goose/v3` from `netmapper migrate`.
- Rationale: pgx is the default Go driver for PostgreSQL and supports `COPY` for bulk observation
  writes. goose runs embedded SQL files with no code generation.
- Alternatives: sqlc (adds a generation step for a handful of queries), golang-migrate (equivalent,
  no reason to prefer it), an ORM (hides `FOR UPDATE SKIP LOCKED`, which is the point of the frontier).

## R3. Frontier claim, lease and reclaim

- Decision: one `task` table. A worker claims with
  `UPDATE task SET state='claimed', claimed_by=$1, lease_expires=now()+$2, attempts=attempts+1
  WHERE id IN (SELECT id FROM task WHERE job_id=ANY($3) AND kind=ANY($4) AND
  (state='pending' OR (state='claimed' AND lease_expires < now())) ORDER BY id
  LIMIT $5 FOR UPDATE SKIP LOCKED) RETURNING ...`.
  The worker renews its lease every lease/3. A task whose `attempts` reaches the job's
  `max_attempts` (default 3) moves to `failed` with `last_error` instead of being claimed again.
- Rationale: an expired lease is how a killed collector gives its work back (FR-012, US3). SKIP LOCKED
  keeps two collectors off the same row (FR-013). Counting attempts at claim time counts a crash as an
  attempt, which is what bounds a device that crashes the worker.
- Alternatives: advisory locks (lost on disconnect with no record of attempts), LISTEN/NOTIFY for
  wake-up (skipped: polling every second is enough; add it when idle polling shows up in load).

## R4. One device, several addresses (FR-004, US1 scenario 2)

- Decision: a `crawl_key` table in the control plane, `UNIQUE (job_id, kind, value)`. A `find` task
  that has fingerprinted a device inserts one row per strong identifier (serial, chassis MAC, as the
  pack defines them) in the same transaction that writes its identifier claims and enqueues work. If
  any insert conflicts, the transaction rolls back to a savepoint, the claims and the `identity`
  observation are still written, and the task ends as `duplicate` without queuing a scrape or
  neighbours. Before contacting a neighbour at all, the find checks whether the chassis ID the
  neighbour table already reported is present in `crawl_key`, and skips the packet when it is.
- Rationale: `identifier_claim` is append only and holds many rows per identifier, so it cannot carry
  the uniqueness itself. A separate key table gives an atomic "first find wins" in PostgreSQL rather
  than in memory. "Identified once" means one identity and one collection per device. A second
  address may still be contacted once when no neighbour gave its chassis ID in advance.
- Alternatives: a unique index on `identifier_claim` (breaks append only), in-memory seen-set (lost on
  restart, not shared across collectors), dedup on a single "primary" identifier (breaks when SNMP and
  SSH expose different identifiers).

## R5. Find reads neighbours; scrape reads the rest

- Decision: a `find` task fingerprints the target, then runs the `neighbours` fact family in the same
  session and enqueues a `find` per in-perimeter neighbour and one `scrape` for the device, all in one
  transaction. `scrape` runs every other family the pack defines for that platform.
- Rationale: FR-005 requires neighbours to be queued as soon as the device is identified and not to
  wait on the full collection. Topology then moves at find speed.
- Alternatives: neighbours inside scrape (topology blocked behind the slowest MAC table).

## R6. Fingerprinting and transports

- Decision: SNMP first (`sysObjectID`, `sysDescr` via `github.com/gosnmp/gosnmp`, v2c and v3, GET,
  GETNEXT and GETBULK only), SSH second (`github.com/scrapli/scrapligo`, one fingerprint command per
  pack rule, tried in pack order). A transport that is silent is not a device failure while another
  transport answered. The target is `unreachable` only when every transport was silent, and `denied`
  when at least one transport answered and every credential set was rejected on all transports that
  answered.
- Rationale: matches the container doc. scrapligo handles prompts, paging and platform-neutral
  command sends without shipping vendor logic into our code (vendor prompt patterns come from the
  pack as scrapligo platform definitions, which are YAML). gosnmp is the standard Go SNMP client.
- Alternatives: raw `golang.org/x/crypto/ssh` (we would rewrite prompt and pager handling),
  netmiko over a Python sidecar (second runtime, breaks the one-binary rule).

## R7. Credentials, order and attempt budgets (FR-017 to FR-022)

- Decision: credential sets covering the target's perimeter are tried in the order they appear in the
  configuration document. Each set has `max_attempts_per_device` (default 1). Per-device, per-set
  attempt counts live in `task.cred_attempts jsonb`, updated before each attempt, so a restart never
  resets a budget. A reference that resolves to nothing moves to the next set and is logged, not
  counted as a device attempt. Secrets are resolved just before `Open` and dropped when the session
  closes. `SecretBackend` has two implementations: `vault` (Vault and OpenBao KV v2 via
  `github.com/hashicorp/vault/api`) and `env` (reference `env:NAME`, for labs and tests).
- Rationale: counting before the attempt errs on the safe side of an account lockout. Storing counts
  in the task row keeps crash state out of process memory, as the constitution requires.
- Alternatives: a per-device credential cache across runs (out of scope, and it is state that would
  outlive the run).

## R8. Perimeter check (FR-002, SC-002)

- Decision: `perimeter.Allowed(netip.Addr) bool` using `net/netip` prefixes, include then exclude.
  It is called inside the single dial function that every transport uses, after DNS resolution and
  before the socket opens. Transports have no other way to dial. A refused target gets an observation
  with the `identity` family, status `unreachable`, and `detail = out_of_perimeter`, and the task ends
  as `skipped`.
- Rationale: one chokepoint that a test can assert on. `netip` is stdlib and allocation free.
- Alternatives: checking at enqueue time only (misses hostname seeds that resolve late and future
  callers).

Open point: FR-009 lists six statuses and has no "skipped". Recording an out-of-perimeter target as
`unreachable` with a detail keeps the enum closed. The alternative is a seventh status, which needs a
constitution amendment. The plan takes the detail approach.

## R9. Parsing

- Decision: TextFSM templates via `github.com/sirikothe/gotextfsm`, followed by a pack-declared field
  mapping into the fact family schema. SNMP tables map columns to fields directly, with no template.
  A template that yields zero rows on non-empty output is `parse_failed`; empty output is `empty`; a
  family the pack has no recipe for on this platform and version is `unsupported`.
- Rationale: TextFSM is the de facto format for network CLI parsing and lets packs reuse
  ntc-templates. Keeping the mapping in the pack satisfies principle V.
- Alternatives: regex-only recipes (reinvents TextFSM), TTP (Python only).

Parse failure detection has a ceiling: a template that matches partially still reports `collected`.
Replay (FR-011) is the correction path.

## R10. Raw output storage (FR-007, FR-011)

- Decision: SHA-256 of the exact bytes, stored at key `raw/sha256/<hex>` through
  `github.com/minio/minio-go/v7` against Garage. `PutObject` is skipped when `StatObject` finds the
  key. `raw_object` is inserted with `ON CONFLICT (hash) DO NOTHING`. The collector does not maintain
  `refcount`: it would need updates on an append-only zone. The count is derivable from
  `observation_raw` and belongs to the orphan sweep.
- Rationale: content addressing gives dedup for free. minio-go is a small, S3-compatible client.
- Alternatives: aws-sdk-go-v2 (heavier, same result), bytes in PostgreSQL (forbidden by the
  constitution).

Consequence for `docs/c4-model/04-data-model.md`: drop `raw_object.refcount` or mark it computed. Also
`identifier_claim.first_seen` and `last_seen` become per-row collection times, with ranges computed at
resolution.

## R11. Immutability enforcement (FR-010)

- Decision: a dedicated PostgreSQL role `netmapper_collector` with `SELECT, INSERT` on the collected
  zone, `finding`, `finding_evidence` and `audit_log`, and `SELECT, INSERT, UPDATE` on `task` and `crawl_key`. It has no
  `UPDATE` or `DELETE` on `observation`, `observation_raw`, `raw_object` or `identifier_claim`.
  The engine role gets `UPDATE` on `snapshot` and `job` only.
- Rationale: a grant is one line and makes a bug fail loudly instead of rewriting history.

## R12. Run lifecycle and closing the snapshot (FR-016, FR-025)

- Decision: `netmapper engine` runs a job runner loop (the only engine component this feature
  builds). Every few seconds, for each running job with no `pending` or `claimed` task: if the final
  retry pass has not run, requeue `failed` tasks with `attempts` reset and mark the pass done; else
  set `snapshot.closed_at`, `snapshot.state = 'closed'`, `job.state = 'succeeded'`. Cancelling sets
  `job.state = 'cancelling'`; workers stop claiming, in-flight tasks finish their current step and
  write what they have, pending tasks become `cancelled`, the snapshot closes, and the job ends
  `cancelled`.
- Rationale: matches the component doc, where the job runner closes snapshots. The final pass runs
  once, which bounds run time.
- Consequence: `snapshot.state` needs a `closed` value meaning "closed, not yet judged", since the
  gate is the next feature. Update the data model doc accordingly.
- Bound on one device (edge case "very large table"): per step timeout (default 120 s) and per step
  output cap (default 64 MiB). Exceeding either records the family as `parse_failed` with detail
  `truncated` or times out the step, and the scrape continues with the next family.

## R13. Starting a run without the API

- Decision: two operator subcommands in the same binary: `netmapper migrate` and
  `netmapper run --config <file> [--seed-set name]`. `run` validates the YAML, stores it as a
  `config_version`, checks FR-001 (perimeter present, seed inside it, a covering credential set) and
  inserts `job`, `snapshot` and one `find` task per seed. `netmapper cancel <job-id>` sets the job to
  `cancelling`. None of them opens a device session or resolves a secret.
- Rationale: the API, scheduler and config service are separate features. This is the smallest way to
  trigger and test a run. When the API lands, its job endpoint calls the same Go function.

## R14. Read-only guarantee (FR-024, principle IV)

- Decision: SNMP code exposes no SET. The SSH session only sends commands taken from a loaded recipe
  (no config mode API is called). The pack loader rejects a pack whose CLI commands do not match that
  pack's declared `read_only` prefix list, and `go test ./packs/...` lints every shipped pack.
- Ceiling: the prefix list is declared by the pack author. Review of pack changes remains the real
  control.

## R15. Testing and lab

- Decision: `go test` only. Unit tests for perimeter, credential ordering, frontier state transitions
  and parsing (fixtures from recorded device output). Integration tests against PostgreSQL and Garage
  started by `docker compose` (`deploy/compose.yaml`), skipped when `NETMAPPER_TEST_DSN` is unset.
  Crawl tests use a fake `Transport` that replays recorded outputs per address, which covers loops,
  duplicates, denied, unreachable, unknown platform and crash-resume without real devices.
  End-to-end validation runs on a containerlab topology of two Arista cEOS nodes (`test/lab/`).
- Rationale: the `Transport` interface already exists, so the fake costs one file. containerlab plus
  cEOS is the common free network lab, and ntc-templates covers EOS.
- Alternatives: testcontainers-go (fine, but compose also serves the quickstart), Nokia SR Linux
  (freely pullable, but no ntc-templates coverage).

## R16. Scale assumptions

Not in the spec, set here so the design has targets: up to 10,000 devices per run, 64 concurrent
sessions per collector by default (`--workers`), claim batch of 16, lease 5 minutes. Observations are
written with `COPY` per task.
