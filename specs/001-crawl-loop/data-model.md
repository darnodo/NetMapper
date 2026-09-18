# Data model: Crawl loop

Tables this feature creates, as a subset of `docs/c4-model/04-data-model.md`. Only columns this
feature reads or writes are listed. Differences from that document are marked **(delta)** and must be
carried back into it.

## Control plane

### config_version

| Column    | Type         | Rule                                    |
| --------- | ------------ | --------------------------------------- |
| id        | bigserial PK |                                         |
| posted_at | timestamptz  | default now()                           |
| posted_by | text         | OS user running `netmapper run` for now |
| document  | text         | YAML exactly as given, never rewritten  |

### perimeter

| Column         | Type         | Rule                        |
| -------------- | ------------ | --------------------------- |
| id             | bigserial PK |                             |
| config_version | bigint FK    |                             |
| name           | text         | unique per config_version   |
| include        | cidr[]       | at least one entry (FR-001) |
| exclude        | cidr[]       | may be empty                |

### credential_set

| Column                  | Type         | Rule                                                                                   |
| ----------------------- | ------------ | -------------------------------------------------------------------------------------- |
| id                      | bigserial PK |                                                                                        |
| config_version          | bigint FK    | **(delta)** sets are versioned with the document                                       |
| name                    | text         |                                                                                        |
| position                | int          | **(delta)** order in the document, used for FR-018                                     |
| kind                    | text         | `ssh`, `snmp_v2c`, `snmp_v3`                                                           |
| username                | text null    | not a secret                                                                           |
| secret_ref              | text         | a reference such as `vault:kv/net/ro#password` or `env:LAB_PW`. Never a value (FR-017) |
| max_attempts_per_device | int          | **(delta)** required, >= 1 (FR-019)                                                    |
| perimeter_ids           | bigint[]     | at least one                                                                           |

### seed_set

| Column         | Type         | Rule                                                                   |
| -------------- | ------------ | ---------------------------------------------------------------------- |
| id             | bigserial PK |                                                                        |
| config_version | bigint FK    |                                                                        |
| name           | text         |                                                                        |
| targets        | text[]       | address or hostname, each must resolve inside a perimeter at run start |

`credential_set_id` from the docs is dropped **(delta)**: sets cover perimeters, not seeds (spec
assumptions).

### job

| Column                           | Type         | Rule                                                          |
| -------------------------------- | ------------ | ------------------------------------------------------------- |
| id                               | bigserial PK |                                                               |
| type                             | text         | `discovery` in this feature                                   |
| state                            | text         | see transitions                                               |
| config_version                   | bigint FK    | recorded on every run                                         |
| snapshot_id                      | bigint FK    |                                                               |
| parameters                       | jsonb        | `perimeter_id`, `seed_set`, `max_attempts`, `final_pass_done` |
| requested_by                     | text         |                                                               |
| created_at, started_at, ended_at | timestamptz  |                                                               |
| error                            | text null    | why a run refused to start or ended in error                  |

State transitions:

```text
running -> succeeded      (queue empty after the final retry pass)
running -> cancelling -> cancelled
running -> failed         (engine-side error only; device failures never fail a job)
```

A run that fails FR-001 is never inserted: `netmapper run` exits non-zero and names the missing piece.

### task (the frontier)

| Column         | Type             | Rule                                                                                                    |
| -------------- | ---------------- | ------------------------------------------------------------------------------------------------------- |
| id             | bigserial        | primary key is `(job_id, id)`, see Partitioning below                                                   |
| job_id         | bigint FK        | partition key (list partition per job)                                                                  |
| kind           | text             | `find` or `scrape`                                                                                      |
| target         | inet             | resolved address; inside the perimeter unless the task is `skipped`                                     |
| target_name    | text null        | hostname or neighbour-reported name                                                                     |
| platform       | text null        | set on `scrape`, copied from the find                                                                   |
| state          | text             | see transitions                                                                                         |
| claimed_by     | text null        | collector id                                                                                            |
| lease_expires  | timestamptz null |                                                                                                         |
| attempts       | int              | incremented at claim                                                                                    |
| cred_attempts  | jsonb            | **(delta)** `{"<credential_set_id>": {"n": int, "ok": bool}}`, per device per set (FR-019, research R7) |
| last_error     | text null        |                                                                                                         |
| parent_task_id | bigint null      | **(delta)** which find reported this neighbour, same job; no FK                                         |
| skip_reason    | text null        | **(delta)** `out_of_perimeter`; set only on `skipped` tasks                                             |

`UNIQUE (job_id, kind, target)` so the same address queued as seed and as neighbour becomes one task.

State transitions:

```text
(insert)          -> skipped   (neighbour address outside the perimeter; never claimed)
pending -> claimed -> done | duplicate | skipped
claimed -> claimed        (lease expired, reclaimed, attempts+1)
claimed -> pending        (Fail with kind `error`, attempts < max_task_attempts)
claimed -> failed         (lease expired or `error` with attempts = max_task_attempts;
                           `deadline`, `credential_unresolved` or `credential_partial` at once;
                           last_error kept)
failed  -> pending        (final retry pass, once, attempts reset, cred_attempts kept)
pending -> cancelled      (job cancelling)
```

`done` means the task wrote its outcomes, whatever those outcomes were. A denied or unreachable device
is a `done` task with a `denied` or `unreachable` observation. `failed` is kept for errors that stopped
the task before it could record an outcome (crash, timeout of the whole task). A `failed` task gets no
observation. Its failure says something about the collector, not about the device: a lease that
expired three times means a worker died or hung, and the device may have answered every time. Writing
`unreachable` for it would make the same false statement rejected for out-of-perimeter targets.
`last_error` carries the reason with a kind prefix, so the run shows each failed target and why
(FR-014). The kind also sets the retry rule:

| Kind                                                               | Meaning                                                                                                                                                               | Retry within the run                                                                              |
| ------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `lease_expired: <n> attempts`                                      | the collector died or hung; the device may be fine                                                                                                                    | reclaimed until `max_task_attempts`, then once more in the final pass                             |
| `error: <message>`                                                 | the task returned an error it could not turn into an outcome                                                                                                          | requeued until `max_task_attempts`, then once more in the final pass                              |
| `deadline: <family> after <duration>`                              | the step deadline fired while the step was running; the setting was too short (research R12)                                                                          | not requeued: repeating it costs another full deadline on the same device; once in the final pass |
| `credential_unresolved: <set names>`                               | no covering set resolved; nothing was presented to the device (research R6)                                                                                           | not requeued; once in the final pass, in case the secret was fixed                                |
| `credential_partial: rejected <set names>; unresolved <set names>` | the device refused every set presented, and at least one set never resolved; check this one first, it may be a device outside the authentication regime (research R6) | not requeued; once in the final pass, in case the secret was fixed                                |

Every kind gets exactly one retry in the final pass, which keeps the run bounded (FR-016). Observations the task wrote
before failing, such as an `identity` written in find step 2, stay as they are.

A `skipped` task has no observation: no packet was sent, so nothing was attempted and FR-009 does not
apply. The task row is the record that the target was skipped (US1 scenario 3). Task rows live as
long as their job, and the skip can also be rederived from the `neighbours` observation that named
the address and the perimeter of the job's `config_version`.

### audit_log

| Column  | Type         | Rule                                                                                   |
| ------- | ------------ | -------------------------------------------------------------------------------------- |
| id      | bigserial PK |                                                                                        |
| ref     | uuid         | **(delta)** pairs the `sent` row with its result row                                   |
| at      | timestamptz  |                                                                                        |
| actor   | text         | `collector:<id>`                                                                       |
| action  | text         | `ssh.command`, `snmp.get`, `snmp.walk`, `ssh.auth`, `snmp.auth`                        |
| target  | inet         |                                                                                        |
| command | text null    | **(delta)** CLI command or OID; username for auth actions; never a credential value    |
| result  | text         | `sent`, then `ok`, `timeout`, `auth_failed`, `error: ...`. Never contains a credential |

Append only (FR-023). Each command produces two rows, each in its own statement outside any task
transaction: `sent` before the command leaves, the result after. A crash in between leaves a `sent`
row with no result, which is what happened. Buffering the rows until the task's batch would lose the
record of commands a device already received (research R6).

## What was collected

### snapshot

| Column    | Type             | Rule                                                           |
| --------- | ---------------- | -------------------------------------------------------------- |
| id        | bigserial PK     |                                                                |
| job_id    | bigint FK        |                                                                |
| opened_at | timestamptz      |                                                                |
| closed_at | timestamptz null | set once                                                       |
| state     | text             | `open` then `closed` **(delta: new value, awaiting the gate)** |

### observation

| Column              | Type          | Rule                                                                                                  |
| ------------------- | ------------- | ----------------------------------------------------------------------------------------------------- |
| id                  | bigserial     | primary key is `(snapshot_id, id)`, see Partitioning below                                            |
| snapshot_id         | bigint FK     | partition key                                                                                         |
| collected_at        | timestamptz   |                                                                                                       |
| collector_id        | text          |                                                                                                       |
| task_id             | bigint        | **(delta)** link to the task, for resume and audit                                                    |
| target              | inet          |                                                                                                       |
| transport           | text null     | `ssh` or `snmp`; null when nothing answered                                                           |
| platform            | text null     | null for `identity` on an unidentified device                                                         |
| recipe_id           | text null     | `<pack>/<recipe id>@<pack version hash>`; the record of which pack version produced `parsed` (FR-011) |
| fact_family         | text          | `identity`, `neighbours`, or a pack family                                                            |
| status              | text NOT NULL | `collected`, `empty`, `unsupported`, `parse_failed`, `unreachable`, `denied` (CHECK)                  |
| detail              | text null     | **(delta)** machine-readable reason: `unknown_platform`, `timeout` (idle timeout mid-session)         |
| parse_generation_id | bigint        | generation 1 is created with the snapshot; FK `(snapshot_id, parse_generation_id)`                    |
| parsed              | jsonb null    | rows in the fact family schema, null unless `collected`                                               |

`UNIQUE (snapshot_id, target, fact_family, task_id)` so a task retried after a crash that already
committed its batch does not write twice.

### observation_raw

| Column         | Type   | Rule                                             |
| -------------- | ------ | ------------------------------------------------ |
| snapshot_id    | bigint | **(delta)** partition key                        |
| observation_id | bigint | FK `(snapshot_id, observation_id)`               |
| step_id        | text   | position of the step in the recipe               |
| command        | text   | **(delta)** the CLI command or OID sent (FR-008) |
| hash           | bytea  | SHA-256, FK to raw_object                        |

### raw_object

| Column    | Type        | Rule                 |
| --------- | ----------- | -------------------- |
| hash      | bytea PK    | SHA-256 of the bytes |
| size      | bigint      | **(delta)**          |
| stored_at | timestamptz |                      |

`refcount` is not written **(delta)**. See research R10.

### parse_generation

| Column          | Type         | Rule                                                 |
| --------------- | ------------ | ---------------------------------------------------- |
| id              | bigserial PK |                                                      |
| snapshot_id     | bigint FK    |                                                      |
| parser_versions | jsonb null   | **(delta)** not written in this slice, see below     |
| created_at      | timestamptz  |                                                      |
| active          | bool         | exactly one true per snapshot (partial unique index) |

`parser_versions` stays empty in this slice. It would be filled by `netmapper run`, which never loads
packs, and several collectors may run different pack versions against one snapshot. The pack version
is recorded per observation in `recipe_id` instead, which is exact in both cases. The replay feature,
which creates generation 2 and later from one known parser set, is the first writer of this column.

### identifier_claim

| Column         | Type         | Rule                                                           |
| -------------- | ------------ | -------------------------------------------------------------- |
| id             | bigserial PK |                                                                |
| snapshot_id    | bigint FK    |                                                                |
| observation_id | bigint       | the `identity` observation; FK `(snapshot_id, observation_id)` |
| kind           | text         | `serial`, `chassis_mac`, `hostname`, `mgmt_address`, ...       |
| subtype        | text null    | e.g. `sysName` vs CLI hostname                                 |
| value          | text         |                                                                |
| strength       | text         | `strong` or `weak`, from the pack                              |
| collected_at   | timestamptz  | **(delta)** replaces first_seen/last_seen at write time        |

Rows are written for every find that identified a device, duplicates included. Two devices with the
same hostname produce two sets of claims with different strong identifiers; nothing merges them here.

This table is also the deduplication index during a crawl (research R4). No constraint enforces
uniqueness, since the table is append only. Instead, **invariant**: every path that writes strong
claims takes `pg_advisory_xact_lock(claim_lock_key(snapshot_id, kind, value))` for each strong
identifier, keys sorted, inside the transaction that inserts, and checks for an existing strong claim
before deciding. The transaction does no network I/O: the fingerprint is complete before it opens.
Index: `(snapshot_id, kind, value) WHERE strength = 'strong'`.

## Partitioning and keys in the collected zone

`observation` is partitioned by `snapshot_id` so that eviction is a partition drop
(`docs/c4-model/04-data-model.md`). PostgreSQL requires every primary key and unique constraint on a
partitioned table to include the partition key, so `observation.id` alone cannot be a primary key and
a foreign key cannot point at it. Three ways out were considered.

- **Chosen: composite keys.** `observation` has primary key `(snapshot_id, id)`. Every table that
  references it carries `snapshot_id` and uses a composite foreign key: `observation_raw`,
  `identifier_claim`, `finding_evidence`. `observation_raw` and `identifier_claim` are partitioned by
  `snapshot_id` the same way, so a snapshot's rows in all three tables sit in partitions that are
  dropped together, children first. `parse_generation` gets primary key `(snapshot_id, id)` for the
  same reason. `raw_object` is shared across snapshots, not partitioned, and keeps `hash` as its key;
  its orphans are the object store sweep's job.
- Rejected: no foreign keys, integrity left to the writer and a test. The collected zone is the one
  thing the system cannot rebuild (principle II). A dangling `observation_raw` or a claim pointing at
  nothing would silently break provenance, and the database is the cheapest place to refuse it.
- Rejected: no partitioning in this slice. Partitioning a populated append-only table later means
  rewriting all of it, and eviction by partition drop is already the documented design.

Cost: one extra `snapshot_id` column on `observation_raw` and `finding_evidence`. `identifier_claim`
already had it, and every row needs it anyway for the partition drop to remove it.

Partitions are created per snapshot by `create_snapshot_partitions(snapshot_id)` and per job by
`create_task_partition(job_id)`. Both are `SECURITY DEFINER` functions owned by `netmapper_owner`,
since only the owner of a parent table can add partitions to it (research R11). One partition per
snapshot is the choice for this slice; the final granularity is still an open question in the docs
and depends on measuring a real snapshot.

`task` follows the same rule: primary key `(job_id, id)`. `parent_task_id` has no foreign key; it
always points into the same job and is kept for audit only.

## Reported

### finding

| Column      | Type         | Rule                                                       |
| ----------- | ------------ | ---------------------------------------------------------- |
| id          | bigserial PK |                                                            |
| snapshot_id | bigint FK    |                                                            |
| domain      | text         | `data_quality` or `compliance`                             |
| category    | text         | `unknown_platform`, `parse_failed`, `credential_denied`    |
| severity    | text         | `warning` for data quality, `high` for `credential_denied` |
| subject_ref | text         | `target:<address>` (no entity exists yet)                  |
| detail      | jsonb        |                                                            |
| state       | text         | `open`                                                     |

### finding_evidence

`finding_id`, `snapshot_id`, `observation_id`, with FK `(snapshot_id, observation_id)`. A `credential_denied` finding cites the `denied` observation, whose
raw output holds the banner or authentication response proving the device answered (FR-022).

## Validation rules from the spec

| Rule                                        | Where enforced                                                                                       |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| FR-001 perimeter, seed inside, covering set | `netmapper run` before inserting the job                                                             |
| FR-002 perimeter before any packet          | neighbour enqueue and the shared dial function (research R8); refused targets become `skipped` tasks |
| FR-009 status never absent                  | NOT NULL + CHECK; tasks that could not record an outcome carry `last_error` (FR-014)                 |
| FR-010 immutable facts                      | role grants (research R11)                                                                           |
| FR-017 no secret stored                     | only `secret_ref` columns exist; a test greps the schema and audit rows for known lab secrets        |
| No double collection                        | advisory lock + strong claim check (R4), `task` unique target, `observation` unique per task         |
