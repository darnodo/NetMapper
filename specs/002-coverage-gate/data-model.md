# Data model: Coverage gate

What this feature adds to the schema 001 left behind. Nothing here changes a table in the collected
zone; the two changes to existing tables are two optional columns on `perimeter` (control plane) and
two grants. Decisions and their alternatives are in [research.md](research.md).

## New table: `snapshot_judgement` (reported zone)

One row per verdict. A snapshot accumulates rows over time and exactly one of them is active.

| Column                 | Type                                                                             | Notes                                                                                       |
| ---------------------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `id`                   | `bigserial PRIMARY KEY`                                                          |                                                                                             |
| `snapshot_id`          | `bigint NOT NULL REFERENCES snapshot`                                            | the snapshot being judged                                                                   |
| `baseline_snapshot_id` | `bigint NULL REFERENCES snapshot`                                                | null when no baseline existed (R10)                                                         |
| `classification`       | `text NOT NULL CHECK (classification IN ('published','degraded','quarantined'))` | FR-002, closed enum                                                                         |
| `coverage`             | `numeric(5,4) NULL`                                                              | fraction in `[0,1]`; null only when `baseline_snapshot_id IS NULL`                          |
| `baseline_devices`     | `integer NOT NULL`                                                               | devices in the baseline after perimeter filtering (R8); 0 when no baseline                  |
| `carried_over`         | `integer NOT NULL`                                                               | baseline devices reached again in this snapshot                                             |
| `reached`              | `integer NOT NULL`                                                               | devices this snapshot reached, baseline or not; the figure FR-004 falls back on             |
| `breakdown`            | `jsonb NOT NULL DEFAULT '{}'`                                                    | per-reason counts and the addresses behind them, see below                                  |
| `thresholds`           | `jsonb NOT NULL`                                                                 | `{"degraded_at": 0.9, "quarantined_below": 0.9, "source": "default"\|"perimeter"}` (FR-005) |
| `gate_version`         | `integer NOT NULL`                                                               | which calculation produced this row (R9)                                                    |
| `active`               | `boolean NOT NULL DEFAULT true`                                                  | exactly one true per snapshot                                                               |
| `computed_at`          | `timestamptz NOT NULL DEFAULT now()`                                             | FR-010                                                                                      |

Constraints:

- `CREATE UNIQUE INDEX snapshot_judgement_active ON snapshot_judgement (snapshot_id) WHERE active;`
  is the whole of FR-001's "exactly one active judgement" (R5).
- `CHECK ((baseline_snapshot_id IS NULL) = (coverage IS NULL))` keeps the no-baseline case honest:
  a row either has a baseline and a coverage figure, or neither.
- `CHECK (carried_over <= baseline_devices)`.
- No `UPDATE` or `DELETE` grant to any role but the owner (FR-009).

### `breakdown` shape

```json
{
  "no_baseline": false,
  "missing": {
    "unreachable":  {"count": 1, "targets": ["172.20.20.3"]},
    "denied":       {"count": 0, "targets": []},
    "unsupported":  {"count": 0, "targets": []},
    "parse_failed": {"count": 0, "targets": []},
    "not_attempted":{"count": 1, "targets": ["172.20.20.7"]}
  },
  "perimeter_filtered": {"count": 2, "targets": ["10.0.0.4", "10.0.0.5"]}
}
```

`missing` sums to `baseline_devices - carried_over`. `perimeter_filtered` records baseline devices
dropped because the newer snapshot's perimeter no longer covers them (R8); they are excluded from
`baseline_devices`, so they can never read as a loss, but they stay visible so a narrowing is
explainable. `not_attempted` is the reason this feature exists (SC-006).

## New function: `judge_snapshot(...)` (`SECURITY DEFINER`, owned by `netmapper_owner`)

Signature mirrors the table's writable columns. In one transaction it sets `active = false` on the
snapshot's current active row (if any) and inserts the new row with `active = true`.

The engine is granted `EXECUTE` on it and is granted no direct write on the table, so the only
reachable transitions are "insert the first verdict" and "supersede the current verdict". Rewriting a
verdict in place is not expressible (R4, R5). Same device as `create_task_partition` and
`create_snapshot_partitions` in 001.

## Changed table: `perimeter` (control plane)

| Column              | Type                                                                         | Notes                                          |
| ------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------- |
| `degraded_at`       | `numeric(5,4) NULL CHECK (degraded_at > 0 AND degraded_at <= 1)`             | coverage at or above this is at worst degraded |
| `quarantined_below` | `numeric(5,4) NULL CHECK (quarantined_below > 0 AND quarantined_below <= 1)` | coverage below this is quarantined             |

Both null means the documented defaults (`degraded_at = 0.9`, `quarantined_below = 0.9`, published
only at `1.0`). Written by `netmapper run` from the configuration document alongside the include and
exclude ranges, so they are pinned to the config version the snapshot ran under (R13). A
`CHECK (degraded_at IS NULL OR quarantined_below IS NULL OR quarantined_below <= degraded_at)` keeps
the two from crossing.

## Grants added (migration `0005_judgement.sql`)

```sql
GRANT SELECT, INSERT ON snapshot_judgement TO netmapper_engine;
GRANT USAGE ON SEQUENCE snapshot_judgement_id_seq TO netmapper_engine;
GRANT EXECUTE ON FUNCTION judge_snapshot(...) TO netmapper_engine;
GRANT SELECT ON snapshot_judgement TO netmapper_operator, netmapper_collector;
```

001 gave the engine `SELECT` on the collected zone and `SELECT, UPDATE` on `job`, `task` and
`snapshot`, and gave sequence usage to the operator and collector only. Both gaps are closed here and
only here; the engine still cannot write anything in the collected zone.

## Reading: how a device is matched and a reason attributed

Two definitions carry the whole feature (R2, R3).

**Reached, in a given snapshot**: there is an `observation` row with `fact_family = 'identity'` and
`status = 'collected'` for that device. Its `target` is one of that device's addresses.

**Same device, across two snapshots**: the two `identity` observations have at least one
`identifier_claim` with `strength = 'strong'` in common, matched on `(kind, value)`. Devices with no
strong claim fall back to matching on `target`.

**Reason a baseline device did not carry over**: look in the newer snapshot for an `identity`
observation on any address that device was known by in the baseline. Its `status` is the reason;
no such observation at any of those addresses means `not_attempted`.

Addresses a baseline device was known by = the `target` of its `identity` observation, plus the
targets of tasks completed `duplicate` against its claims in that snapshot (001 writes one `identity`
observation for the winning task and marks the other addresses' tasks `duplicate`).

## Baseline selection

```sql
SELECT s.id
FROM snapshot s
JOIN job j ON j.snapshot_id = s.id
JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
WHERE p.name = $1                    -- the perimeter name, not its id (R1)
  AND s.state = 'closed'
  AND (s.closed_at, s.id) < ($2, $3) -- strictly before the snapshot being judged
  AND EXISTS (SELECT 1 FROM snapshot_judgement sj WHERE sj.snapshot_id = s.id AND sj.active)
ORDER BY s.closed_at DESC, s.id DESC
LIMIT 1;
```

The perimeter is joined by **name**, because `perimeter.id` changes on every `netmapper run`: the
table is `UNIQUE (config_version, name)` and each run posts the document whole. Comparing by id would
give every snapshot an empty history (R1). This is the single most consequential line in the feature.

## Sweep

```sql
SELECT s.id
FROM snapshot s
WHERE s.state = 'closed'
  AND NOT EXISTS (SELECT 1 FROM snapshot_judgement sj WHERE sj.snapshot_id = s.id AND sj.active)
ORDER BY s.closed_at, s.id;
```

Ascending, so a predecessor is judged before its successor looks for a baseline (R7). A snapshot
whose predecessor is closed but still unjudged is left for a later pass rather than compared against
an older run.

## What this feature does not touch

`observation`, `observation_raw`, `raw_object`, `identifier_claim`, `finding`, `task`, `job` and the
`snapshot` row itself are read only here. The `snapshot_closed_is_final` trigger from 001 stays as it
is: a verdict never touches the snapshot row, which is why it lives in its own table (R4).
