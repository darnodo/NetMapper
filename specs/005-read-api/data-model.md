# Data model: The read API

Phase 1 of [plan.md](plan.md). This feature adds **one table and one role**. It computes nothing and
stores nothing about the graph: everything it serves was written by 001 to 004 and is read as it
stands. One migration, `0008_api.sql`.

## New table: `api_token`

The control plane gains its first credential record. The value itself is never stored: what is kept is
the SHA-256 of the string a caller presents, which is what the lookup matches on
([research R4](research.md)).

| Column         | Type            | Notes                                                                 |
| -------------- | --------------- | --------------------------------------------------------------------- |
| `id`           | `bigserial` PK  |                                                                       |
| `name`         | `text`          | what an operator calls it, unique, so a token can be revoked by name  |
| `hash`         | `bytea`         | SHA-256 of the presented token, unique; the index authentication uses |
| `scopes`       | `text[]`        | `NOT NULL`; `read` is the only value this feature defines             |
| `created_at`   | `timestamptz`   | defaults to `now()`                                                   |
| `last_used_at` | `timestamptz` null | set on each successful authentication (R6)                         |
| `revoked_at`   | `timestamptz` null | set by `netmapper token revoke`; a non-null value refuses the token |

Constraints:

- `UNIQUE (hash)` is what makes authentication one indexed equality rather than a scan.
- `UNIQUE (name)` so an operator revokes by the thing they know.
- `CHECK (cardinality(scopes) > 0)`, following the shape `entity_decision` uses in 0006: an empty array
  would authenticate and authorise nothing, which is a token that exists and cannot be used, and is
  better refused at write time than discovered at request time.

The scope values are deliberately **not** a database `CHECK`. The interface refuses a value it does not
define (R13), and pinning the set in the schema would make adding the second scope a migration before
it is a decision.

## New role: `netmapper_api`

Created the way 0004 creates the other three, inside the same `DO` block pattern so parallel test
schemas do not collide on it.

```
netmapper_api  SELECT   snapshot, snapshot_judgement, projection, resolution,
                        entity, entity_claim, identifier_claim,
                        interface, interface_alias, interface_evidence,
                        edge, edge_evidence,
                        observation, observation_raw, raw_object,
                        finding, finding_evidence,
                        api_token
               UPDATE   api_token (last_used_at)
```

That is the whole grant. No `INSERT` anywhere, no `DELETE` anywhere, and `UPDATE` on exactly one
column of one table. `entity_claim` and `identifier_claim` are there because a device's own evidence
runs through them (see "The evidence chain" below); an earlier draft of this list left them out. The
`UPDATE` is column-level so the role can never un-revoke a token, change its scopes or replace its
hash.
The role is what bounds a compromise of the only component anyone can reach, so it is the part of this
feature worth reading twice.

Two absences are deliberate:

- **No grant on `credential_set`, `config_version`, `perimeter` or `seed_set`.** The interface serves
  the graph, not the configuration, and `credential_set` carries the secret references FR-014 forbids
  it from returning. It cannot return what it cannot read.
- **No grant on `task`, `job`, `entity_decision` or `audit_log`.** Nothing this feature serves needs
  them, and the operator's decisions are not the exposed component's business.

`netmapper_operator` gains `SELECT, INSERT, UPDATE` on `api_token` and `USAGE` on its sequence, for
`netmapper token create|list|revoke`. The `UPDATE` is what revocation is; no role may `DELETE` a token,
so a revoked one stays visible and a name is never quietly reused.

## What this feature does not add

- No fact family, no recipe, no pack change.
- No column on `entity`, `interface`, `edge`, `observation` or any other row it serves.
- No change to `audit_log`. The constitution attaches its audit obligation to a mutating call, and
  FR-012 leaves this interface none. Its action check still admits only `ssh.*` and `snmp.*` against a
  non-null `inet` target, and widening it is the business of the feature that brings mutation.
- No change to the four zones or to the boundary between them.

## Reading: how a request is answered

1. The token is read from the `Authorization` header, hashed, and looked up. Absent, unknown or revoked
   are one refusal; a token whose scopes do not cover the call is a different one (R12).
2. `last_used_at` is set. This is the only write in the request, and it happens before the read
   transaction opens (R6, R7).
3. A `READ ONLY`, `REPEATABLE READ` transaction opens. Every query the handler runs happens inside
   it, so an accidental write fails against the database rather than against a review, and every
   query of one answer sees the same snapshot of the data, even if a resolution or projection commits
   in the middle (R7).
4. The snapshot is chosen: the one the caller named, or the most recently closed one carrying a current
   projection (R9).
5. The handler reads what it serves and attaches, to every element, the observations behind it and when
   they were collected. An element that cannot carry its evidence is a defect, not a partial answer
   (FR-002).

## The evidence chain, end to end

This is what the whole feature exists to expose, and every link of it already exists:

```
device (entity)      → entity_claim → identifier_claim → observation
interface            → interface_evidence               → observation
edge                 → edge_evidence                    → observation
finding              → finding_evidence                 → observation
observation          → observation_raw → raw_object     → the bytes in the object store
```

The interface walks it and serves the last step itself, read-only, through the `RawStore.Get` that 001
already wrote ([contracts/rest.md](contracts/rest.md)).
