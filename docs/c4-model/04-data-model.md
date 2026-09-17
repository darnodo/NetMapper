# Data model and contracts

| | |
|---|---|
| Status | Draft for review |
| Date | 2026-08-31 |
| Note | Not a C4 level. C4 stops at components; this is the schema and the Go interfaces |

![Map of the schema](diagrams/data-model.svg)

Four zones, and the boundary between the second and the third is the one that matters: everything on the right is rebuildable from everything on the left. Losing it costs time, never data.

## Control plane

| Table | Key columns | Notes |
|---|---|---|
| `config_version` | `id`, `posted_at`, `posted_by`, `document` | the YAML as posted, kept whole |
| `perimeter` | `id`, `config_version`, `name`, `include[]`, `exclude[]` | derived from the document, indexed for lookup |
| `credential_set` | `id`, `name`, `kind`, `username`, `secret_ref`, `perimeter_ids[]` | never a value, only a reference |
| `seed_set` | `id`, `name`, `targets[]`, `credential_set_id` | |
| `schedule` | `id`, `cron`, `job_type`, `parameters`, `enabled` | misfire policy is skip |
| `api_token` | `id`, `name`, `hash`, `scopes[]`, `created_at`, `last_used_at` | the value is never stored |
| `job` | `id`, `type`, `state`, `requested_by`, `parameters`, `snapshot_id`, `config_version`, timestamps, `error` | a run records the config version it used |
| `task` | `id`, `job_id`, `kind` (`find`, `scrape`), `target`, `state`, `claimed_by`, `lease_expires`, `attempts` | the frontier. Partitioned by job, claimed with `FOR UPDATE SKIP LOCKED` |
| `audit_log` | `id`, `at`, `actor`, `action`, `target`, `result` | API calls and device commands, append only |

## What was collected

Immutable, append only, and the only zone a `collector` writes.

| Table | Key columns | Notes |
|---|---|---|
| `snapshot` | `id`, `job_id`, `opened_at`, `closed_at`, `state` (`open`, `published`, `degraded`, `quarantined`, `evicted`), `coverage`, `pinned` | an evicted snapshot keeps its row as a tombstone |
| `observation` | `id`, `snapshot_id`, `collected_at`, `collector_id`, `target`, `transport`, `recipe_id`, `fact_family`, `status`, `parse_generation_id`, `parsed` | partitioned by snapshot range, so eviction is a partition drop |
| `observation_raw` | `observation_id`, `step_id`, `hash` | one row per command, since a family may need several |
| `raw_object` | `hash`, `bytes`, `stored_at`, `refcount` | the object store holds the bytes, this holds the accounting |
| `parse_generation` | `id`, `snapshot_id`, `parser_versions`, `created_at`, `active` | exactly one active per snapshot |
| `identifier_claim` | `id`, `kind`, `subtype`, `value`, `strength`, `snapshot_id`, `observation_id`, `first_seen`, `last_seen` | written by the collector during a `find`, before any entity exists |

`status` is one of `collected`, `empty`, `unsupported`, `parse_failed`, `unreachable`, `denied`. Everything downstream depends on that column, so it is never nullable.

`identifier_claim` has no `entity_id`. It is the deduplication index during a crawl and the input to resolution afterwards, which is why it belongs to this zone and not the next.

## What was computed

Rebuilt from the two zones on the left. Every row carries the snapshot it belongs to and the observations it came from.

| Table | Key columns | Notes |
|---|---|---|
| `entity` | `id`, `snapshot_id`, `kind` (`device`, `endpoint`, `module`, `prefix`, `vlan_instance`, `routing_instance`), `attributes`, `first_seen`, `last_seen` | |
| `entity_claim` | `entity_id`, `identifier_claim_id` | which claims were merged into this entity |
| `entity_decision` | `id`, `kind` (`merge`, `split`, `never_merge`, `promote`, `demote`), `subjects[]`, `actor`, `at` | operator decisions, replayed on every recomputation |
| `interface` | `id`, `entity_id`, `canonical_name`, `if_index`, `kind`, `parent_interface_id`, `mac`, `admin_state`, `oper_state`, `speed`, `mtu`, `description` | identity is the pair entity plus canonical name |
| `interface_alias` | `interface_id`, `spelling`, `source` | every raw spelling ever seen |
| `edge` | `id`, `snapshot_id`, `type`, `from_ref`, `to_ref`, `confidence`, `attributes`, `first_seen`, `last_seen` | types from the domain list, `l1_link`, `attached`, `has_address`, `protocol_adjacency` and the rest |
| `edge_evidence` | `edge_id`, `observation_id` | how the edge is known, and how a diff explains itself |
| `l2domain` | `id`, `snapshot_id`, `key_type` (`vlan`, `vni`), `key_id`, `label`, `confidence`, `pinned_name` | |
| `l2domain_member` | `l2domain_id`, `entity_id`, `local_vlan_id` | the local VLAN, which may differ per device |
| `l2domain_link` | `l2domain_id`, `edge_id` | what the component was built from |
| `site` | `id`, `name`, `aliases[]` | matched between sources on the alias set |

## Reported

| Table | Key columns | Notes |
|---|---|---|
| `finding` | `id`, `snapshot_id`, `domain`, `category`, `severity`, `subject_ref`, `detail`, `state` | one surface for data quality and network state |
| `finding_evidence` | `finding_id`, `observation_id` | |
| `intent_source` | `id`, `kind`, `location`, `imported_at`, `version` | |
| `intent_record` | `id`, `source_id`, `kind`, `natural_key`, `attributes` | kept verbatim |
| `intent_match` | `intent_record_id`, `entity_id`, `snapshot_id`, `result` | matched, observed only, intended only, drift, ambiguous |

An intent version referenced by a retained snapshot is retained with it.

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

## Still open

- whether `entity` and `edge` carry a row per snapshot or a validity interval across snapshots. Per snapshot is simpler to reason about and heavier to store, and the choice is easier to make once a real snapshot has been measured
- the exact partition granularity for `observation`, which depends on the same measurement
