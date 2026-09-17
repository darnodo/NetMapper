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
  `max_attempts` moves to `failed` with `last_error` instead of being claimed again. Lease duration,
  batch size and `max_attempts` come from configuration (see R16).
- Rationale: an expired lease is how a killed collector gives its work back (FR-012, US3). SKIP LOCKED
  keeps two collectors off the same row (FR-013). Counting attempts at claim time counts a crash as an
  attempt, which is what bounds a device that crashes the worker.
- Alternatives: advisory locks (lost on disconnect with no record of attempts), LISTEN/NOTIFY for
  wake-up (skipped: polling at a configured interval is enough; add it when idle polling shows up
  in load).

## R4. One device, several addresses (FR-004, US1 scenario 2)

- Decision: `identifier_claim` is the deduplication index, as `docs/c4-model/04-data-model.md` intends.
  Uniqueness comes from a transaction-scoped advisory lock per strong identifier, not from a
  constraint. A `find` runs in three steps, and the lock is never held across network I/O:
  1. Network, no transaction open: fingerprint the target and extract its identifiers.
  2. Short transaction, no network: compute the lock key of every strong identifier with the SQL
     function `claim_lock_key(snapshot_id, kind, value)` (defined once in a migration), sort the
     keys, take `pg_advisory_xact_lock` on each in that order, then look for a strong claim with the
     same kind and value in this snapshot whose `identity` observation is `collected` and belongs to
     another task. Write the `identity` observation and this find's claims in either case. If such a
     claim exists, mark the task `duplicate` and commit: no neighbours, no scrape. Otherwise commit;
     the claims now mark the device as taken by this task. The locks are released at commit.
  3. Network again, then a second short transaction: read the neighbour table, write the
     `neighbours` observation, enqueue neighbour finds and the scrape, and mark the task `done`.
  A find reclaimed after a crash between steps 2 and 3 redoes step 1, finds its own claims in step 2
  (same task, so not a duplicate; the observation insert is `ON CONFLICT DO NOTHING`), and continues.
- Rationale: holding a lock while waiting on a slow device would serialize every find that shares a
  hash bucket with it and hold a pool connection for a device timeout. Sorting keys avoids deadlocks
  when two devices share one identifier out of several. A hash collision in `claim_lock_key` only
  serializes two unrelated finds; it never merges them, since the check compares kind and value.
- Invariant, recorded in `docs/c4-model/04-data-model.md`: every path that writes strong identifier
  claims takes these locks, through `claim_lock_key`, in sorted order, in the transaction that inserts.
  Identity resolution will write claims too and is bound by the same rule.
- "Identified once" means one identity and one collection per device. A second address of the same
  device still receives the fingerprint of step 1 before step 2 can recognise it.
- Alternatives: a unique index on `identifier_claim` (breaks append only); a separate key table with a
  unique constraint (works, but duplicates the index the data model already names); an in-memory
  seen-set (lost on restart, not shared across collectors); dedup on one "primary" identifier (breaks
  when SNMP and SSH expose different identifiers).

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
  configuration document. Each set has a required `max_attempts_per_device` (at least 1, no default, because a wrong value
  locks accounts on the central authentication service). Per-device, per-set
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
  It is applied in two places:
  - when a find enqueues neighbours: an address outside the perimeter becomes a `find` task inserted
    directly in state `skipped` with `skip_reason = 'out_of_perimeter'` and `parent_task_id` pointing
    to the find whose neighbour table reported it;
  - inside the single dial function that every transport uses, after DNS resolution and before the
    socket opens. Transports have no other way to dial. A target refused there ends `skipped` the
    same way.
  No observation is written for a skipped target. No packet was sent, so there is nothing to observe.
  The six observation statuses stay as they are.
- Rationale: one chokepoint that a test can assert on, plus an early check so out-of-perimeter
  neighbours never enter the queue as work. The skip stays traceable: the `neighbours` observation
  holds the row that named the address, and the job records the config version whose perimeter
  refused it.
- Alternatives: an `identity` observation with status `unreachable` and a detail (a false statement,
  since nothing was attempted); a seventh status (needs a constitution amendment for something that is
  not an observation outcome); recording the neighbour's identifiers as claims (the spec defines a
  claim as what a device reports about itself).

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
  zone, `finding`, `finding_evidence` and `audit_log`, and `SELECT, INSERT, UPDATE` on `task`. It has no
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
- Bound on one device (edge case "very large table"): no output size cap. A cap would store a cut-off
  table under a status that claims something false, and a truncated MAC table marked `collected` would
  show up as disappearances in the next diff. Two time bounds instead:
  - an idle timeout per step (`step_idle_timeout`): no byte received for that long. The device stopped
    answering in the middle of a session, so the family is recorded `unreachable` with detail
    `timeout`, and the scrape moves on to the next family. A large table that keeps arriving never
    trips it.
  - a total deadline per step (`step_deadline`), which keeps the run bounded (FR-016). If it fires, the
    operator setting was too short for that device, which is a collector-side failure: the task ends
    under FR-014 with `last_error` starting `deadline:` and no observation for that family. None of
    the six statuses describes "the device was still sending when we stopped", and the case is
    expected to be rare, so no seventh status is added. If it happens in the lab or in production,
    that occurrence is the evidence for a constitution amendment.
- Known limit: the whole output of one step is held in memory once. scrapligo returns the output of a
  command as one string, and TextFSM parses a whole string, so streaming the raw bytes to the object
  store would not lower the peak. The same buffer is hashed, uploaded and parsed. Upgrade path, if a
  real table proves too large: read at the scrapligo channel level, stream to the object store while
  hashing, and parse from a re-read.

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

## R16. Scale and limits

The spec gives no throughput or size target, and nothing has been measured yet, so the project commits
to no performance figure. The design is bounded, every bound is configurable, and the operational
bounds ship with defaults so a first crawl can start without tuning. The defaults are starting points
chosen by judgement, not measured values, and an operator overrides them after running the tool:

| Bound | Setting | Default |
|---|---|---|
| concurrent device sessions per collector | `--workers` | 64 |
| lease duration | `discovery.lease` | 5m |
| tasks claimed per batch | `discovery.claim_batch` | 16 |
| frontier poll interval | `discovery.poll_interval` | 1s |
| attempts per task | `discovery.max_task_attempts` | 3 |
| idle timeout per step (no byte received) | `discovery.step_idle_timeout` | 60s |
| total deadline per step | `discovery.step_deadline` | 30m |

`credential_sets[].max_attempts_per_device` has no default and must be stated. A wrong value locks
accounts on a central authentication service, a consequence that lands outside the tool, so the
operator has to choose it (R7).

Observations are written with `COPY` per task.
