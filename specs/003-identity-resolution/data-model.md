# Data model: Identity resolution

What this feature adds to the schema 001 and 002 left behind, in migration `0006_entity.sql`. Nothing
here changes the collected zone. The only change to an existing table is one widened `CHECK` on
`finding.category`, plus grants. Decisions and their alternatives are in [research.md](research.md).

Everything below is the computed zone except `entity_decision`, which is append only like the
observations it corrects the reading of.

## New table: `device` (the registry)

One row per device ever seen in a perimeter. This is the only cross-snapshot table the feature adds, and
the reason a device key means the same thing in two runs (R5).

| Column           | Type                                  | Notes                                                                                                           |
| ---------------- | ------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `perimeter_name` | `text NOT NULL`                       | perimeters are identified by name across config versions, as 002 established (002, R1)                          |
| `key`            | `text NOT NULL`                       | `<kind>:<value>` of the anchor identifier at mint time, or `addr:<address>` for a weakly identified device (R5) |
| `weak`           | `boolean NOT NULL DEFAULT false`      | true when the device was minted without a strong identifier (FR-022)                                            |
| `first_seen`     | `timestamptz NOT NULL`                | earliest evidence across every snapshot that reached it                                                         |
| `last_seen`      | `timestamptz NOT NULL`                | latest                                                                                                          |
| `minted_from`    | `bigint NOT NULL REFERENCES snapshot` | the snapshot that first produced this key, so a key can be explained                                            |

`PRIMARY KEY (perimeter_name, key)`.

## New table: `device_identifier`

Every strong identifier ever attributed to a device. The lookup that turns a claim group set into a
known device, and the table a `split` decision removes a row from.

| Column           | Type                   | Notes                                                         |
| ---------------- | ---------------------- | ------------------------------------------------------------- |
| `perimeter_name` | `text NOT NULL`        |                                                               |
| `kind`           | `text NOT NULL`        | as the pack declared it, never interpreted here (Principle V) |
| `value`          | `text NOT NULL`        |                                                               |
| `key`            | `text NOT NULL`        | the device it belongs to                                      |
| `first_seen`     | `timestamptz NOT NULL` |                                                               |

`PRIMARY KEY (perimeter_name, kind, value)`, `FOREIGN KEY (perimeter_name, key) REFERENCES device`.

The primary key is the invariant: one identifier belongs to one device. A claim group set carrying an
identifier already attributed elsewhere is the R6 case, not an insert conflict to swallow.

## New table: `resolution`

One row per resolved snapshot. The sweep's marker and the record of which calculation produced the
current entity set.

| Column              | Type                                     | Notes                                                                                                 |
| ------------------- | ---------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `snapshot_id`       | `bigint PRIMARY KEY REFERENCES snapshot` | one current set per snapshot, by the primary key alone (FR-015)                                       |
| `resolver_version`  | `integer NOT NULL`                       | which grouping produced it (R9)                                                                       |
| `decisions_applied` | `bigint NOT NULL`                        | highest `entity_decision.id` read for the perimeter, 0 when it has none, so a result can be explained |
| `entities`          | `integer NOT NULL`                       | how many, so "resolved to nothing" reads as a fact and not a gap                                      |
| `computed_at`       | `timestamptz NOT NULL DEFAULT now()`     |                                                                                                       |

Upserted on a re-resolve, in the transaction that rewrites the snapshot's entities. There is no history
of superseded entity sets: they are derived data, replaced wholesale (R8).

## New table: `entity`

One row per device in one snapshot.

| Column        | Type                                    | Notes                                                                     |
| ------------- | --------------------------------------- | ------------------------------------------------------------------------- |
| `id`          | `bigserial PRIMARY KEY`                 | re-issued on every resolution; never referenced from outside the snapshot |
| `snapshot_id` | `bigint NOT NULL REFERENCES snapshot`   |                                                                           |
| `kind`        | `text NOT NULL CHECK (kind = 'device')` | the closed enum opens when the graph projector adds its kinds (FR-026)    |
| `device_key`  | `text NOT NULL`                         | the stable name (FR-021)                                                  |
| `weak`        | `boolean NOT NULL`                      | weakly identified (FR-004, FR-022)                                        |
| `attributes`  | `jsonb NOT NULL DEFAULT '{}'`           | hostname, platform, the addresses it answered on; see below               |
| `first_seen`  | `timestamptz NOT NULL`                  | earliest `collected_at` of its evidence in this snapshot (FR-006)         |
| `last_seen`   | `timestamptz NOT NULL`                  | latest                                                                    |

`UNIQUE (snapshot_id, device_key)` is the whole of FR-024: two entities of one snapshot can never carry
the same key, so a split that keeps two devices apart has to give them different keys or fail loudly.
`ON DELETE CASCADE` from `snapshot` is not used; eviction deletes entities with the snapshot's other
computed rows.

### `attributes` shape

```json
{
  "hostname": "sw1",
  "platform": "arista_eos",
  "targets": ["172.20.20.2", "10.0.0.2"],
  "identifiers": {"serial": "FGE1234", "chassis_mac": "00:1c:73:aa:bb:cc"}
}
```

`targets` is every address the device answered on in this snapshot, the winning observation's first
(FR-008). `identifiers` is a flat copy of the strong claims for reading; `entity_claim` is the
authoritative link. `hostname` and `platform` are taken from the winning observation, the one not marked
`duplicate_of_task`, so grouped observations that disagree (a short name against an FQDN) resolve the same
way on every recomputation (R15, FR-003).

## New table: `entity_claim`

| Column                | Type                                                  | Notes                                              |
| --------------------- | ----------------------------------------------------- | -------------------------------------------------- |
| `entity_id`           | `bigint NOT NULL REFERENCES entity ON DELETE CASCADE` |                                                    |
| `snapshot_id`         | `bigint NOT NULL`                                     | carried so the composite key reaches the partition |
| `identifier_claim_id` | `bigint NOT NULL`                                     |                                                    |

`PRIMARY KEY (entity_id, snapshot_id, identifier_claim_id)`,
`FOREIGN KEY (snapshot_id, identifier_claim_id) REFERENCES identifier_claim (snapshot_id, id)`.

This is FR-005: from an entity, its claims; from a claim, its observation; from the observation,
`observation_raw` and the bytes in the object store. The chain the constitution's first principle asks
for already exists, this table is its first link.

## New table: `entity_decision`

Append only. The only table in this feature no role but the owner may update or delete.

| Column           | Type                                                            | Notes                                                                  |
| ---------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `id`             | `bigserial PRIMARY KEY`                                         | the order decisions are applied and the tie-break of FR-011            |
| `perimeter_name` | `text NOT NULL`                                                 | decisions do not cross perimeters                                      |
| `kind`           | `text NOT NULL CHECK (kind IN ('merge','split','never_merge'))` |                                                                        |
| `subjects`       | `text[] NOT NULL`                                               | device keys: two for `merge` and `never_merge`, one for `split`        |
| `identifier`     | `jsonb NULL`                                                    | `{"kind": "...", "value": "..."}`, required by `split`, null otherwise |
| `actor`          | `text NOT NULL`                                                 | who recorded it                                                        |
| `at`             | `timestamptz NOT NULL DEFAULT now()`                            |                                                                        |
| `note`           | `text NULL`                                                     | why, in the operator's words                                           |

Constraints: `CHECK ((kind = 'split') = (identifier IS NOT NULL))`,
`CHECK (array_length(subjects, 1) = CASE WHEN kind = 'split' THEN 1 ELSE 2 END)`.

What each kind means at resolution time (R7):

| Kind          | Effect                                                                                                                    |
| ------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `merge`       | the two keys name one device; the second key's identifiers are read as the first's, and entities carry the first key      |
| `split`       | the named identifier is removed from the named device; a claim group carrying it matches or mints another device          |
| `never_merge` | the two devices are never combined; an identifier observed on both is ignored for matching and raises no conflict finding |

## Changed table: `finding`

```sql
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict'));
```

`domain` stays `data_quality`. `subject_ref` holds the device keys involved, comma separated the way 001
writes a target. `detail` carries the contradicting kind and the values:

```json
{"conflict": "within_snapshot", "kind": "serial", "values": ["FGE1234", "FGE9999"], "keys": ["serial:FGE1234", "serial:FGE9999"]}
```

`conflict` is `within_snapshot` for the R4 case (one component disagreeing with itself) and
`across_devices` for the R6 case (a claim group set matching two known devices).

These findings belong to the resolution that raised them. Re-resolving a snapshot deletes its
`identity_conflict` rows and their `finding_evidence` rows inside the same transaction that rewrites the
entities, then raises the conflicts that still hold (R11, FR-015). The delete is restricted in code to
`category = 'identity_conflict'` and to that snapshot, so the collector's findings are never touched.

## Grants added (migration `0006_entity.sql`)

| Role                  | Gains                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `netmapper_engine`    | `SELECT, INSERT, UPDATE, DELETE` on `resolution`, `entity`, `entity_claim`, `device`, `device_identifier`; `SELECT` on `entity_decision`; `INSERT, DELETE` on `finding`, `finding_evidence`; `USAGE` on `entity_id_seq` and on `finding_id_seq`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `netmapper_operator`  | `SELECT, INSERT` on `entity_decision`; `USAGE` on its sequence; `SELECT, INSERT, UPDATE, DELETE` on the five computed tables and `USAGE` on `entity_id_seq`; `SELECT, INSERT, DELETE` on `finding`, `finding_evidence`. The write rights and the findings rights are both there for the same reason: `netmapper resolve` runs the resolver in the operator's own process, and a resolution that finds a collision raises one whichever process ran it. `SELECT` is not optional: the resolver's delete reads `category` in its `WHERE` clause and `store.RaiseFinding` returns the new id, and PostgreSQL requires `SELECT` for both. Also `SELECT` on `observation` and `identifier_claim`, which 001 gave only to the collector and the engine: the resolver reads the claims before it writes anything |
| `netmapper_collector` | nothing. It never reads an entity                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |

Two of those lines are not what this document first planned, and the difference is deliberate:
`netmapper_engine` needs `USAGE` on `finding_id_seq` because 001 granted `ALL SEQUENCES` to the operator
and the collector only, and `netmapper_operator` needs the findings rights because `netmapper resolve`
is a second path into the same resolver. Both were found while implementing and are recorded as
divergences 1 and 2 in [quickstart.md](quickstart.md).

No role but `netmapper_owner` gains `UPDATE` or `DELETE` on `entity_decision`. That is what makes FR-009
a constraint rather than a convention, the same argument 002 made for verdicts.

## Reading: how a snapshot resolves

1. Read the snapshot's perimeter name and its active `parse_generation` (R10).
2. Read every `identity` observation of that generation with its claims, ordered by observation id.
3. Union-find over shared strong identifiers, decisions applied as the links are considered (R3, R7).
4. Reject any component that contradicts itself, one entity per claim group instead, one finding (R4).
5. For each surviving component, look its strong identifiers up in `device_identifier`: no match mints a
   device, one match uses it and records the identifiers it did not yet know, more than one attaches to
   the lowest key and raises a finding (R5, R6).
6. In one transaction, under `pg_advisory_xact_lock` on the perimeter name: delete the snapshot's entities and its
   `identity_conflict` findings, insert the new entities with their claims, raise the conflicts that still
   hold, upsert `resolution`, update `device.last_seen` (R8, R11).

## What this feature does not touch

- No table of the collected zone, in any way. No device, no secret, no pack.
- `snapshot`, `snapshot_judgement` and the `snapshot_closed_is_final` trigger are read only here. A
  verdict does not gate a resolution and a resolution does not change a verdict (FR-025).
- No `interface`, `interface_alias`, `edge`, `edge_evidence` or `l2domain` row. Those are the graph
  projector's and the `entity.kind` `CHECK` is deliberately narrow so the day they arrive is a migration,
  not a silent widening (FR-026).
