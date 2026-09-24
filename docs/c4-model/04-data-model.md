# Data model and contracts

|        |                                                                                  |
| ------ | -------------------------------------------------------------------------------- |
| Status | Draft for review                                                                 |
| Date   | 2026-08-31                                                                       |
| Note   | Not a C4 level. C4 stops at components; this is the schema and the Go interfaces |

![Map of the schema](diagrams/data-model.svg)

Four zones, and the boundary between the second and the third is the one that matters: everything on the right is rebuildable from everything on the left. Losing it costs time, never data.

## Control plane

| Table            | Key columns                                                                                                                                                                                        | Notes                                                                                                                                                                                                                                                                                                                                       |
| ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `config_version` | `id`, `posted_at`, `posted_by`, `document`                                                                                                                                                         | the YAML as posted, kept whole                                                                                                                                                                                                                                                                                                              |
| `perimeter`      | `id`, `config_version`, `name`, `include[]`, `exclude[]`, `degraded_at`, `quarantined_below`                                                                                                       | derived from the document, indexed for lookup. The two thresholds are optional fractions the coverage gate reads; null means the documented defaults. A perimeter's identity across config versions is its **name**: every run posts the document whole, so `id` changes even when nothing did                                              |
| `credential_set` | `id`, `config_version`, `name`, `position`, `kind`, `username`, `secret_ref`, `max_attempts_per_device`, `perimeter_ids[]`                                                                         | never a value, only a reference. `position` is the order sets are tried in; the attempt budget is per device and has no default                                                                                                                                                                                                             |
| `seed_set`       | `id`, `config_version`, `name`, `targets[]`                                                                                                                                                        | sets cover perimeters, not seeds                                                                                                                                                                                                                                                                                                            |
| `schedule`       | `id`, `cron`, `job_type`, `parameters`, `enabled`                                                                                                                                                  | misfire policy is skip                                                                                                                                                                                                                                                                                                                      |
| `api_token`      | `id`, `name`, `hash`, `scopes[]`, `created_at`, `last_used_at`                                                                                                                                     | the value is never stored                                                                                                                                                                                                                                                                                                                   |
| `job`            | `id`, `type`, `state`, `requested_by`, `parameters`, `snapshot_id`, `config_version`, timestamps, `error`                                                                                          | a run records the config version it used                                                                                                                                                                                                                                                                                                    |
| `task`           | `id`, `job_id`, `kind` (`find`, `scrape`), `target`, `target_name`, `platform`, `state`, `claimed_by`, `lease_expires`, `attempts`, `cred_attempts`, `last_error`, `parent_task_id`, `skip_reason` | the frontier. Partitioned by job, key `(job_id, id)`, claimed with `FOR UPDATE SKIP LOCKED`. `cred_attempts` holds the per device, per set attempt counts; `last_error` starts with its kind (`lease_expired`, `error`, `deadline`, `credential_unresolved`, `credential_partial`); `skip_reason` is `out_of_perimeter` on a `skipped` task |
| `audit_log`      | `id`, `ref`, `at`, `actor`, `action`, `target`, `command`, `result`                                                                                                                                | API calls and device commands, append only. A `sent` row before each command leaves, then a result row with the same `ref`                                                                                                                                                                                                                  |

## What was collected

Immutable, append only, and the only zone a `collector` writes.

| Table              | Key columns                                                                                                                                                                        | Notes                                                                                                                                                                                                          |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `snapshot`         | `id`, `job_id`, `opened_at`, `closed_at`, `state` (`open`, `closed`, `published`, `degraded`, `quarantined`, `evicted`), `coverage`, `pinned`                                      | an evicted snapshot keeps its row as a tombstone. `closed`: the run ended, the gate has not judged it yet                                                                                                      |
| `observation`      | `id`, `snapshot_id`, `collected_at`, `collector_id`, `task_id`, `target`, `transport`, `platform`, `recipe_id`, `fact_family`, `status`, `detail`, `parse_generation_id`, `parsed` | partitioned by snapshot, key `(snapshot_id, id)`, so eviction is a partition drop. `detail` is a machine-readable reason (`unknown_platform`, `timeout`); `recipe_id` is `<pack>/<recipe>@<pack version hash>` |
| `observation_raw`  | `snapshot_id`, `observation_id`, `step_id`, `command`, `hash`                                                                                                                      | one row per command, since a family may need several. Partitioned like `observation`                                                                                                                           |
| `raw_object`       | `hash`, `size`, `stored_at`                                                                                                                                                        | the object store holds the bytes, this holds the accounting. The reference count is derived from `observation_raw`, never stored                                                                               |
| `parse_generation` | `id`, `snapshot_id`, `parser_versions`, `created_at`, `active`                                                                                                                     | exactly one active per snapshot                                                                                                                                                                                |
| `identifier_claim` | `id`, `kind`, `subtype`, `value`, `strength`, `snapshot_id`, `observation_id`, `collected_at`                                                                                      | written by the collector during a `find`, before any entity exists. First and last seen are computed at resolution                                                                                             |

`status` is one of `collected`, `empty`, `unsupported`, `parse_failed`, `unreachable`, `denied`. Everything downstream depends on that column, so it is never nullable.

`identifier_claim` has no `entity_id`. It is the deduplication index during a crawl and the input to resolution afterwards, which is why it belongs to this zone and not the next.

Invariant: the table is append only, so no constraint can make a strong identifier unique. Every path that writes strong claims, the collector's `find` and identity resolution alike, takes `pg_advisory_xact_lock(claim_lock_key(snapshot_id, kind, value))` (the SQL function `claim_lock_key` is the single definition of the key) for each strong identifier it is about to write, with the keys sorted, inside the transaction that inserts them, and checks for an existing strong claim before deciding. That transaction does no network I/O and holds no lock while waiting on a device. A path that writes strong claims without the lock breaks deduplication for every other path.

## What was computed

Rebuilt from the two zones on the left. Every row carries the snapshot it belongs to and the observations it came from.

| Table             | Key columns                                                                                                                                          | Notes                                                                                               |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `entity`          | `id`, `snapshot_id`, `kind` (`device`, `endpoint`, `module`, `prefix`, `vlan_instance`, `routing_instance`), `attributes`, `first_seen`, `last_seen` |                                                                                                     |
| `entity_claim`    | `entity_id`, `identifier_claim_id`                                                                                                                   | which claims were merged into this entity                                                           |
| `entity_decision` | `id`, `kind` (`merge`, `split`, `never_merge`, `promote`, `demote`), `subjects[]`, `actor`, `at`                                                     | operator decisions, replayed on every recomputation                                                 |
| `interface`       | `id`, `entity_id`, `canonical_name`, `if_index`, `kind`, `parent_interface_id`, `mac`, `admin_state`, `oper_state`, `speed`, `mtu`, `description`    | identity is the pair entity plus canonical name                                                     |
| `interface_alias` | `interface_id`, `spelling`, `source`                                                                                                                 | every raw spelling ever seen                                                                        |
| `edge`            | `id`, `snapshot_id`, `type`, `from_ref`, `to_ref`, `confidence`, `attributes`, `first_seen`, `last_seen`                                             | types from the domain list, `l1_link`, `attached`, `has_address`, `protocol_adjacency` and the rest |
| `edge_evidence`   | `edge_id`, `observation_id`                                                                                                                          | how the edge is known, and how a diff explains itself                                               |
| `l2domain`        | `id`, `snapshot_id`, `key_type` (`vlan`, `vni`), `key_id`, `label`, `confidence`, `pinned_name`                                                      |                                                                                                     |
| `l2domain_member` | `l2domain_id`, `entity_id`, `local_vlan_id`                                                                                                          | the local VLAN, which may differ per device                                                         |
| `l2domain_link`   | `l2domain_id`, `edge_id`                                                                                                                             | what the component was built from                                                                   |
| `site`            | `id`, `name`, `aliases[]`                                                                                                                            | matched between sources on the alias set                                                            |

## Reported

| Table                | Key columns                                                                                                                                                                                  | Notes                                                                                                                                                                                                                                                                    |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `finding`            | `id`, `snapshot_id`, `domain`, `category`, `severity`, `subject_ref`, `detail`, `state`                                                                                                      | one surface for data quality and network state                                                                                                                                                                                                                           |
| `finding_evidence`   | `finding_id`, `snapshot_id`, `observation_id`                                                                                                                                                | composite key to the partitioned `observation`                                                                                                                                                                                                                           |
| `snapshot_judgement` | `id`, `snapshot_id`, `baseline_snapshot_id`, `classification`, `coverage`, `baseline_devices`, `carried_over`, `reached`, `breakdown`, `thresholds`, `gate_version`, `active`, `computed_at` | the coverage verdict on a closed snapshot: `published`, `degraded` or `quarantined`. Append only, several rows per snapshot with a partial unique index keeping exactly one `active`. Written only through `judge_snapshot`, so a verdict is superseded, never rewritten |
| `intent_source`      | `id`, `kind`, `location`, `imported_at`, `version`                                                                                                                                           |                                                                                                                                                                                                                                                                          |
| `intent_record`      | `id`, `source_id`, `kind`, `natural_key`, `attributes`                                                                                                                                       | kept verbatim                                                                                                                                                                                                                                                            |
| `intent_match`       | `intent_record_id`, `entity_id`, `snapshot_id`, `result`                                                                                                                                     | matched, observed only, intended only, drift, ambiguous                                                                                                                                                                                                                  |

An intent version referenced by a retained snapshot is retained with it.

`snapshot_judgement.breakdown` says which devices of the baseline did not come back and why:
`unreachable`, `denied`, `unsupported`, `parse_failed`, or `not_attempted` for a device that left
discovery without ever being tried, which no outcome recorded inside the snapshot can reveal.
`judge_snapshot(...)` is `SECURITY DEFINER` and owned by `netmapper_owner`; `netmapper_engine` holds
`EXECUTE` on it plus `INSERT` on the table, and no role but the owner may `UPDATE` or `DELETE` a
verdict. This is the first table outside the collected zone the engine writes at all.

## The Go contracts that matter

Five interfaces carry every extension point. Everything else is concrete.

```go
// A protocol. Rare, one implementation each.
type Transport interface {
    Open(ctx context.Context, target Target, cred Credential) (Session, error)
}

type Session interface {
    Run(ctx context.Context, step Step) (RawOutput, error)
    Close() error
}

// Turns raw output into rows matching a fact family schema.
type Parser interface {
    Parse(raw RawOutput, spec ParserSpec) ([]Row, error)
}

// Where secrets come from. Vault, OpenBao, or the built-in store.
type SecretBackend interface {
    Resolve(ctx context.Context, ref string) (Secret, error)
}

// Everything vendor-specific, loaded as data.
type PackRegistry interface {
    Fingerprint(evidence FingerprintEvidence) (Platform, bool)
    Recipe(platform Platform, family FactFamily, version OSVersion) (Recipe, bool)
    Normalise(platform Platform, spelling string) (string, bool)
}
```

The shape of the first four is unremarkable, and that is the point: a new transport or a new parser format is one implementation, and nothing above it changes. `PackRegistry` is the one that decides whether the project keeps its promise, because the day something vendor-specific leaks outside it, adding a platform stops being a directory of data.

Tables referencing the partitioned `observation` carry `snapshot_id` and use composite foreign keys, and partitions are created per snapshot and per job by `SECURITY DEFINER` functions owned by the schema owner (`create_snapshot_partitions`, `create_task_partition`).

## Still open

- `PackRegistry` and `Parser` are concrete in the first slice (`pack.Registry`, `parse.Parse`), since each has one implementation. They become interfaces when a second implementation appears, not before

- whether `entity` and `edge` carry a row per snapshot or a validity interval across snapshots. Per snapshot is simpler to reason about and heavier to store, and the choice is easier to make once a real snapshot has been measured
- the exact partition granularity for `observation`, which depends on the same measurement
