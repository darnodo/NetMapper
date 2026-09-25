# Data model: Graph projector

Phase 1 of [plan.md](plan.md). Everything here lives in the computed zone: rebuilt from the collected
zone and the entity set alone, replaced wholesale, never patched (Principle II, FR-023). One migration,
`0007_graph.sql`.

All of it is per snapshot. Unlike 003, this feature adds no cross-snapshot table: an edge is named by
its endpoints, so there is no registry to keep and nothing two snapshots share ([research R6](research.md),
[R12](research.md)).

## New table: `projection`

One row per projected snapshot. The primary key alone is FR-018's "exactly one current set", and the
row is what tells an unprojected snapshot from one that projected to nothing.

| Column              | Type          | Notes                                                                            |
| ------------------- | ------------- | -------------------------------------------------------------------------------- |
| `snapshot_id`       | `bigint` PK   | references `snapshot`                                                            |
| `projector_version` | `integer`     | bumped by hand when a change alters results, so old sets can be found and replayed |
| `resolution_at`     | `timestamptz` | the `resolution.computed_at` this projection was built from (R12)                 |
| `interfaces`        | `integer`     | how many were written                                                            |
| `edges`             | `integer`     | how many were written                                                            |
| `computed_at`       | `timestamptz` | defaults to `now()`                                                              |

`resolution_at` is what makes a re-resolution repair itself: `interface` cascades from `entity`, so
re-resolving a snapshot destroys its projection, and a stale `projection` row would otherwise claim a
set that is gone. The sweep takes a snapshot whose `resolution_at` no longer matches.

## New table: `interface`

One port of one device entity. Computed, never collected.

| Column           | Type            | Notes                                                        |
| ---------------- | --------------- | ------------------------------------------------------------ |
| `id`             | `bigserial` PK  |                                                              |
| `entity_id`      | `bigint`        | references `entity` `ON DELETE CASCADE`                      |
| `snapshot_id`    | `bigint`        | references `snapshot`; carried for the evidence foreign keys |
| `canonical_name` | `text`          | from the pack's naming rules, or the spelling itself (FR-005) |
| `source`         | `text`          | `device` or `neighbour` (FR-006); `device` wins when both apply |
| `description`    | `text` null     | from the `interfaces` family                                 |
| `admin_state`    | `text` null     | `up` or `down`                                               |
| `oper_state`     | `text` null     | `up`, `down` or `other`                                      |
| `speed_bps`      | `bigint` null   |                                                              |
| `mtu`            | `integer` null  |                                                              |
| `mac`            | `text` null     | lowercase colon form, as the parser normalised it            |
| `first_seen`     | `timestamptz`   | earliest `collected_at` among its evidence                   |
| `last_seen`      | `timestamptz`   | latest                                                       |

`UNIQUE (entity_id, canonical_name)` is the whole of FR-002: a name identifies a port inside one
device and nowhere else, so two devices may each have a `port1` (acceptance scenario 5). The state
columns are the `interfaces` fact family verbatim, with the same closed enums the family declares, so
a value the parser accepted cannot be rejected here.

Nullable columns everywhere but the name: a port revealed by a neighbour has no operational facts at
all, and a device whose interfaces recipe failed still keeps its ports through the reports about them
(acceptance scenario 4).

## New table: `interface_alias`

One spelling of an interface, with where it came from. The record of why two reports of one cable are
one cable.

| Column           | Type     | Notes                                                    |
| ---------------- | -------- | -------------------------------------------------------- |
| `interface_id`   | `bigint` | references `interface` `ON DELETE CASCADE`               |
| `spelling`       | `text`   | as the source wrote it                                   |
| `source`         | `text`   | `device` or `neighbour`                                  |
| `snapshot_id`    | `bigint` | with `observation_id`, the composite key to `observation` |
| `observation_id` | `bigint` |                                                          |

Primary key `(interface_id, spelling)`. Every spelling seen is recorded, including one that already
equals the canonical name, so a reader never has to infer the canonical spelling from a missing row
(FR-004, [research R5](research.md)). The same spelling from two observations keeps the lowest
observation id, which is a tie-break and is tested with inputs that tie.

## New table: `interface_evidence`

| Column           | Type     | Notes                                   |
| ---------------- | -------- | ----------------------------------------- |
| `interface_id`   | `bigint` | references `interface` `ON DELETE CASCADE` |
| `snapshot_id`    | `bigint` |                                           |
| `observation_id` | `bigint` |                                           |

Primary key `(interface_id, observation_id)`, composite foreign key to the partitioned `observation`.
This is FR-007 for interfaces: from a port to its observations, and from there to `observation_raw`,
the bytes and `collected_at`.

## New table: `edge`

A relationship between two endpoints in one snapshot.

| Column              | Type            | Notes                                                             |
| ------------------- | --------------- | ------------------------------------------------------------------- |
| `id`                | `bigserial` PK  |                                                                     |
| `snapshot_id`       | `bigint`        | references `snapshot`; FR-016, an edge belongs to exactly one       |
| `type`              | `text`          | `l1_link` or `has_address`                                          |
| `from_ref`          | `text`          | endpoint reference, see below                                       |
| `to_ref`            | `text`          | endpoint reference                                                  |
| `name`              | `text`          | generated, stored: `type \|\| ':' \|\| from_ref \|\| '\|' \|\| to_ref` |
| `from_entity_id`    | `bigint`        | references `entity` `ON DELETE CASCADE`                             |
| `to_entity_id`      | `bigint` null   | null when the far end resolved to nothing, and for `has_address`    |
| `from_interface_id` | `bigint` null   | references `interface` `ON DELETE CASCADE`; null for `has_address`  |
| `to_interface_id`   | `bigint` null   | null when the far end resolved to nothing, and for `has_address`    |
| `confidence`        | `text`          | `both_ends`, `one_end` or `direct`                                  |
| `attributes`        | `jsonb`         | what the reports said, see below                                    |
| `first_seen`        | `timestamptz`   | earliest `collected_at` among its evidence (FR-008)                 |
| `last_seen`         | `timestamptz`   | latest                                                              |

### Endpoint references and the edge name

| Form                              | Means                                                                         |
| --------------------------------- | ------------------------------------------------------------------------------- |
| `if:<device_key>/<canonical>`     | a port of a resolved device                                                     |
| `dev:<device_key>`                | a resolved device as a whole                                                    |
| `addr:<address>`                  | an address a device answered on                                                 |
| `unknown:<kind>=<value>[/<port>]` | a far end no entity accounts for, named by `remote_chassis_id`, else `remote_mgmt_address`, else `remote_system_name` |

`UNIQUE (snapshot_id, name)` is FR-009 and FR-011 together: one cable is one row, and two cables
between the same two switches are two rows, because the name carries the ports and not just the
devices. FR-015's stability across runs is inherited, without a registry: a device key is stable
because 003 made it so, and a canonical name is stable because the pack's rules are.

### Constraints

- `edge_l1_link_is_ordered`: `type <> 'l1_link' OR from_ref COLLATE "C" < to_ref COLLATE "C"`.
  One cable has one name whichever end is read. The `C` collation is deliberate: the projector orders
  in Go, which compares bytes, and the database's own collation does not ([research R7](research.md)).
  A report whose two references are equal, a port claiming to see itself, is dropped rather than
  written, which is the tie this rule has to be tested against.
- `edge_from_is_resolved`: `from_entity_id IS NOT NULL`. Both types start at a device the snapshot
  resolved. For `l1_link` this holds by construction, since `if:` sorts before `unknown:`; for
  `has_address` the type fixes the direction as device then address.
- `type` and `confidence` are `NOT NULL` with a `CHECK`, like every closed enum in the schema.

### `attributes` shape

For an agreed `l1_link`, where both endpoints are resolved ports:

```json
{ "protocols": ["lldp"] }
```

For a one-sided one whose far end no entity accounts for:

```json
{
  "protocols": ["lldp"],
  "from_spelling": "Ethernet1",
  "to_spelling": "Et1",
  "remote_system_name": "sw2",
  "remote_chassis_id": "aa:bb:cc:00:00:02",
  "remote_mgmt_address": "10.0.0.2",
  "remote_mgmt_address_type": "ipv4"
}
```

`protocols` is the set of discovery protocols that reported this cable, sorted. It is a set and not a
value because the protocol is not part of an edge's identity: one device reporting one cable over both
LLDP and CDP produces one edge citing both observations (FR-011).

Everything else is carried by a one-sided link only. The four `remote_*` fields are what FR-010 calls
"whatever the report said about the far end", and they exist because there is no endpoint row to hold
them; on an agreed link the two endpoints already say all of it. The spellings go with them for the
same reason: `interface_alias` is where a spelling lives, keyed to the port it names and to the
observation that used it, and repeating one on an agreed edge would give the same fact two homes that
can disagree. On a one-sided link whose far end resolved to nothing there is no port row to hang the
far spelling off, so the edge keeps it.

This was the other way round until a convergence pass found the document describing spellings the
projector writes only for a one-sided link (T084).

For a `has_address`: `{"address": "10.0.0.1"}`.

## New table: `edge_evidence`

| Column           | Type     | Notes                                        |
| ---------------- | -------- | ---------------------------------------------- |
| `edge_id`        | `bigint` | references `edge` `ON DELETE CASCADE`          |
| `snapshot_id`    | `bigint` |                                                |
| `observation_id` | `bigint` |                                                |
| `side`           | `text`   | `from` or `to`: which end this observation is  |

Primary key `(edge_id, observation_id, side)`, composite foreign key to `observation`. `side` is what
makes "both ends agreed" readable from the evidence and not only from the `confidence` column: a
`both_ends` edge has a row per side, a `one_end` edge has one.

## Changed table: `finding`

`category` gains `link_disagreement`, alongside `unknown_platform`, `parse_failed`,
`credential_denied` and `identity_conflict`.

- `domain` is `data_quality`, `severity` is `warning`.
- `subject_ref` is the lower of the two device keys involved, as `device:<key>`.
- `detail` names both sides and what each said:
  `{"devices": ["k1", "k2"], "reports": [{"from": "if:k1/Ethernet1", "says": "Ethernet3"}, ...]}`.
- Evidence cites the `neighbours` observation behind each side.

Like an `identity_conflict`, a `link_disagreement` is part of a projection's output and is replaced
with it: the delete is restricted in code to that category and the snapshot being projected, so a
finding the collector or the resolver raised is never touched.

## Grants (migration `0007_graph.sql`)

```
netmapper_engine    SELECT, INSERT, UPDATE, DELETE  projection, interface, interface_alias,
                                                    interface_evidence, edge, edge_evidence
                    USAGE                           interface_id_seq, edge_id_seq
netmapper_operator  the same set, because `netmapper project` runs the projector in its own process
netmapper_collector nothing: it never reads an interface or an edge
```

No new right on `finding` is needed: 003 already gave both roles `SELECT, INSERT, DELETE` on it and
on `finding_evidence`, plus `USAGE` on `finding_id_seq`. The reads this feature needs on `observation`,
`entity`, `entity_claim`, `identifier_claim` and `resolution` are already held by both roles.

## Reading: how a snapshot projects

1. `Project(ctx, db, reg, snapshotID)` checks the snapshot is closed and carries a `resolution` row,
   then opens one transaction and takes `pg_advisory_xact_lock` on the snapshot id.
2. Read the entity set with its `device_key`, `attributes` and the find task ids behind it, through
   `entity_claim` and `identifier_claim` ([research R2](research.md)).
3. Read the `interfaces` and `neighbours` observations of the active parse generation, mapped to
   entities through those task ids.
4. Build interfaces from the `interfaces` rows, then from every port a `neighbours` row names on
   either end, creating what is missing and recording every spelling as an alias.
5. Pair the neighbour reports: `both_ends` when each names the other's port, `one_end` otherwise,
   a `link_disagreement` finding when two reports about the same device pair contradict each other.
6. Add one `has_address` edge per address in each entity's `attributes.targets`.
7. Delete the snapshot's interfaces, edges and `link_disagreement` findings; write the new set; upsert
   the `projection` row. Same transaction, so no consumer ever sees half of it.

## What this feature does not touch

- No observation, raw object, identifier claim, judgement, entity, registry row or operator decision
  is written or deleted (FR-020).
- `entity.kind` keeps its `CHECK (kind = 'device')`. An unresolved far end is a reference and some
  attributes, not an entity ([research R14](research.md)).
- No device is contacted and no secret is resolved. The packs are read as data, for their naming rules
  alone.
- `attached`, `protocol_adjacency`, `l2domain` and the intent tables stay unbuilt: neither fact family
  they would need is collected (spec Assumptions, FR-025).
