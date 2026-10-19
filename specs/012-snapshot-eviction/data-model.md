# Data model: Snapshot eviction and pinning

One migration, `migrations/0013_snapshot_eviction.sql`. It adds no zone: the work list is control
plane, the rest changes existing tables.

## snapshot (changed)

| Column       | Change                                                                     |
| ------------ | -------------------------------------------------------------------------- |
| `state`      | check widened to `open`, `closed`, `evicting`, `evicted`                   |
| `pinned`     | new, `boolean NOT NULL DEFAULT false`                                      |
| `evicted_at` | new, `timestamptz NULL`, set when the state leaves `closed` for `evicting` |

Checks: `(state IN ('evicting', 'evicted')) = (evicted_at IS NOT NULL)`; the existing
`(state = 'closed') = (closed_at IS NOT NULL)` becomes `(state = 'open') = (closed_at IS NULL)`,
since a tombstone keeps its close time.

### State transitions

```text
open ──close──> closed ──evict_snapshot(apply)──> evicting ──finish_snapshot_eviction──> evicted
                  │                                   ▲
                  └── pinned toggled, nothing else    └── evict --yes resumes here
```

Enforced by the trigger that replaces `snapshot_closed_is_final` (research R9):

- `open`: any update, as today (the job runner closes it).
- `closed`: either only `pinned` changes, or `state` becomes `evicting` with `evicted_at` set and
  nothing else changes.
- `evicting`: only `state` to `evicted`.
- `evicted`: no update.

`pinned` cannot change on `evicting` or `evicted` (refused by `set_snapshot_pinned`, and by the
trigger).

## eviction_object (new)

The stored outputs an eviction still has to delete from the object store (research R5).

| Column        | Type                                  |
| ------------- | ------------------------------------- |
| `snapshot_id` | `bigint NOT NULL REFERENCES snapshot` |
| `hash`        | `bytea NOT NULL`                      |
| `size`        | `bigint NOT NULL`, for the report of a resumed eviction (added in implementation) |

Primary key `(snapshot_id, hash)`. Written by `evict_snapshot` in the same transaction that drops
the rows; a row is deleted once its object is gone. No foreign key to `raw_object`: that row is
deleted in the same transaction. Empty for every snapshot that is not `evicting`.

Owner `netmapper_owner`. Operator: `SELECT, DELETE`. No other role.

## audit_log (changed)

- `action` check gains `snapshot.evict`, `snapshot.pin`, `snapshot.unpin`.
- `target` becomes nullable, with `CHECK (target IS NOT NULL OR action LIKE 'snapshot.%')`.
- For the new actions: `command` = `snapshot <id>`, `result` one of `ok`, `noop`,
  `incomplete: <reason>`, `refused: <reason>`.
- Operator: `INSERT` (new). Still no `UPDATE` or `DELETE` for any role but the owner.

## Functions (new, owner, `SECURITY DEFINER`, `EXECUTE` to `netmapper_operator` only)

### evict_snapshot(p_snapshot_id bigint, p_apply boolean)

Returns one row:

| Column              | Meaning                                                                  |
| ------------------- | ------------------------------------------------------------------------ |
| `outcome`           | `preview`, `evicting`, `resume`, `noop`, `refused`, `unknown`            |
| `reason`            | refusal reason: `open`, `pinned`, `crawl_running`, `unresolved_sibling`  |
| `blocking_snapshot` | the snapshot behind `crawl_running` or `unresolved_sibling`, else null   |
| `blocking_job`      | the job behind `crawl_running`, else null                                |
| `observations`      | observations of the snapshot                                             |
| `objects_deleted`   | stored outputs only this snapshot references                             |
| `bytes_deleted`     | their total size                                                         |
| `objects_kept`      | stored outputs it references that another snapshot also references       |
| `devices_deleted`   | registry entries no remaining snapshot of the perimeter would reference  |

Locks: crawl gate, perimeter lock, projection lock, snapshot row `FOR UPDATE` (research R6); with
`p_apply`, the 14 tables it locks exclusively, all at once with `NOWAIT` and retries, before any
delete (research R3).
Checks and their order: research R7. Steps with `p_apply`: research R3. On `evicting`, it returns
`resume` and changes nothing; the counts are then the remaining `eviction_object` rows.

### finish_snapshot_eviction(p_snapshot_id bigint)

Sets `evicting` to `evicted` when no `eviction_object` row is left for it; raises otherwise.

### set_snapshot_pinned(p_snapshot_id bigint, p_pinned boolean)

Returns `ok`, `noop` or `refused` (`evicting`, `evicted`), or `unknown`. Allowed on `open` and
`closed`.

### judge_snapshot (changed)

Locks the snapshot row `FOR SHARE` and raises when the snapshot or `p_baseline_snapshot_id` is not
`closed`.

## Rows removed by an eviction

Per snapshot: `observation`, `observation_raw`, `identifier_claim` partitions; `task` partition of
its job; `parse_generation`; `finding`, `finding_evidence`; `resolution`, `entity`, `entity_claim`;
`projection`, `interface`, `interface_alias`, `interface_evidence`, `edge`, `edge_evidence`;
`l2domain`, `l2domain_member`, `l2domain_link`. Shared: `raw_object` rows of hashes only it
referenced; `device` and `device_identifier` rows of its perimeter no remaining entity carries.

Kept: `snapshot` (tombstone), `job`, `snapshot_judgement` (its own and those naming it as baseline),
`config_version`, `perimeter`, `credential_set`, `seed_set`, `entity_decision`, `audit_log`,
`api_token`.

## Read API shapes (changed)

`SnapshotRef` gains `opened_at` (time, kept on a tombstone), `pinned` (boolean) and `evicted_at`
(time or null). The judgement view gains
`baseline_state` (the baseline's `state`, or null without a baseline). See
[contracts/rest.md](contracts/rest.md).
