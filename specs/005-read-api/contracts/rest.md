# Contract: the read interface

Eight endpoints, all `GET`, all requiring a token. Versioned under `/v1` from the first day, because
the agent consumer that arrives later will hold URLs this one hands out.

## Authentication

Every request carries `Authorization: Bearer nm_<token>`. There is no unauthenticated endpoint, not
even a health check: FR-010 says every call presents a token, and a liveness probe that bypasses it
would be the one door nobody remembers to close. A process supervisor can watch the port instead.

| Situation                                        | Status | Body                                  |
| ------------------------------------------------ | ------ | ------------------------------------- |
| No header, malformed header, unknown or revoked   | 401    | `{"error": "unauthenticated"}`, nothing more |
| Authenticated, scopes do not cover the call       | 403    | `{"error": "forbidden", "need": "read"}` |

The four cases behind 401 are deliberately indistinguishable ([research R12](../research.md)): telling
a caller their token is known but revoked confirms that a guessed value was once real. 403 is
deliberately distinguishable, because a caller with a valid token needs to know the problem is their
scope.

Neither ever reveals whether the path's subject exists. A 401 on `/v1/devices/sw1` and on
`/v1/devices/nonesuch` are the same response.

## Common shape

Every answer carries the snapshot it was built from:

```json
{
  "snapshot": {
    "id": 12,
    "closed_at": "2026-09-25T10:44:58Z",
    "state": "closed",
    "verdict": "quarantined",
    "coverage": 0.5
  },
  "...": "the answer itself"
}
```

`verdict` is present on every answer, not only on the snapshot endpoints (FR-020), because the default
snapshot can be a quarantined one and a caller must not need a second request to learn that.

Every element that rests on evidence carries it:

```json
"evidence": [
  {"observation_id": 3, "collected_at": "2026-09-25T10:44:58Z", "family": "neighbours", "target": "172.20.20.2"}
]
```

An element without `evidence` and without a collection time is a defect of this feature, not a sparse
answer (FR-002, SC-002).

## Endpoints

### `GET /v1/snapshots`

Every snapshot, newest first: id, state, `closed_at`, verdict, coverage, and whether it carries a
current graph. This is how a caller discovers what they can ask about.

### `GET /v1/snapshots/{id}`

One snapshot with its verdict, the figure the verdict rests on, what it was compared against, and the
breakdown of what did not come back (FR-007).

### `GET /v1/devices`

The devices of a snapshot: device key, hostname, platform, the addresses it answered on, whether it is
weakly identified, and its evidence. No interfaces and no edges (FR-001c). `?snapshot=<id>` names one;
without it, the default of FR-019 applies.

There is no filtering and no search. It is a decision, not an omission.

### `GET /v1/devices/{name}`

One device with its interfaces and every edge touching them, uncapped (FR-001d).

`{name}` is a device key, a hostname, or an address it answered on, tried in that order (FR-001a). The
answer states the `device_key` it resolved to, whatever was asked for (FR-001b).

| Situation                              | Status | Body                                                        |
| -------------------------------------- | ------ | ----------------------------------------------------------- |
| Resolved                               | 200    | the device                                                   |
| Nothing matches in that snapshot       | 404    | `{"error": "no_such_device"}`                                |
| A hostname or address matching several | 409    | `{"error": "ambiguous", "candidates": ["chassis_mac:…", …]}` |
| The snapshot carries no graph          | 409    | `{"error": "not_projected", "snapshot": {…}}`                |

404 and "the snapshot has no graph" are different answers on purpose (FR-009): absent and empty must
not read the same.

Each interface carries its canonical name, whether the device described it or a neighbour revealed it,
the spellings it is known by, its operational fields, and its evidence (FR-005). Each edge carries its
type, its two endpoint references, its confidence, its attributes and the evidence for each side
(FR-003).

### `GET /v1/findings`

The findings raised against a snapshot, each with its category, severity, subject and the observations
it cites (FR-008). `?snapshot=<id>` as above.

### `GET /v1/observations/{id}`

One observation: its family, status, target, transport, recipe, collection time, and the commands
behind it with the hash of each.

### `GET /v1/observations/{id}/raw/{step}`

The bytes, `application/octet-stream`, served by the interface itself from the object store (FR-004,
FR-004a). `{step}` names which command of the observation, since a family may need several.

This is the end of the evidence chain and the reason the interface holds read-only object-store
access. It writes nothing there and returns no access key.

### `GET /v1/interfaces/{device}/{name}`

One interface with its aliases, its evidence and the edges touching it. A convenience over
`/v1/devices/{name}` for a caller who already knows which port they care about, and the one endpoint
that could be dropped without losing a requirement.

## netmapper api (new subcommand)

```sh
netmapper api [--listen :8080]
```

- Connects as `netmapper_api`, which holds `SELECT` on what it serves and `UPDATE` on `api_token`
  alone.
- Reads `NETMAPPER_S3_*` for read-only object-store access. It is the second component after the
  collector to read the object store, and it never writes there.
- Opens no device session and resolves no secret reference (FR-015).
- Exit 1 if the database or the object store is unreachable at startup, the way `collector` does.

## netmapper token create|list|revoke (new subcommand)

Runs as `netmapper_operator`. The interface exposes no call that changes anything, so it cannot manage
its own credentials ([research R8](../research.md)).

```sh
netmapper token create --name grafana-reader --scope read
netmapper token list
netmapper token revoke --name grafana-reader
```

- `create` prints the token value on stdout **once** and never again. Only its SHA-256 is stored.
- `list` prints name, scopes, created, last used and revoked. Never a value.
- `revoke` sets `revoked_at`; the next request with that token is refused. No row is ever deleted, so a
  name is never quietly reused.
- Exit 2 for a duplicate name, an unknown name on revoke, or a scope the interface does not define.

## netmapper run / cancel / judge / resolve / decide / project / collector / engine / migrate

Unchanged. `netmapper migrate` applies `0008_api.sql` like any other.
