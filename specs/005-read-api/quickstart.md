# Quickstart: validating the read interface

Run guide for proving the feature end to end. Schema and grants are in [data-model.md](data-model.md),
the endpoints in [contracts/rest.md](contracts/rest.md), the decisions in [research.md](research.md).

## Prerequisites

Same as [004's quickstart](../004-graph-projector/quickstart.md): Go, Docker with compose, and for the
lab scenarios containerlab with a cEOS image imported as `ceos:latest`. Its "Running the lab" section
covers deploying the four-node topology and starting the engine and collector; nothing here changes it.

This feature adds no service. It adds one listener, and it reads the object store, so the collector's
`NETMAPPER_S3_*` variables are needed by `netmapper api` too.

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d
export NETMAPPER_TEST_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
go test ./...
```

Expected: all pass. The interface tests build snapshots with the fake transport from 001, serve them
through `httptest`, and must include at least these cases.

**Every endpoint, under every token state.** This is the table that matters, because a suite that only
ever presents a valid token proves one of five cases, and the constitution added its rule about
testing under the right role precisely because 003 shipped a command its own role could not run.

| Test                                                                            | Proves                     |
| ------------------------------------------------------------------------------- | -------------------------- |
| every endpoint with no header, and with a malformed one, returns the same refusal | FR-010, R12                |
| every endpoint with an unknown token returns that same refusal                  | FR-010, R12                |
| every endpoint with a revoked token returns that same refusal                   | FR-010, US2-4              |
| the four refusals above are byte-identical, and name nothing about the subject  | FR-010, SC-004, R12        |
| a token whose scopes are empty or undefined is refused, not treated as permissive | FR-011, SC-005, R13      |
| that refusal is distinguishable from an unknown token                           | FR-011, US2-2              |
| a refusal on an existing subject and on a missing one are identical             | SC-004                     |
| revoking a token refuses it on the next request, with nothing to expire         | FR-021, US2-4, R5          |

**The evidence contract.**

| Test                                                                            | Proves                     |
| ------------------------------------------------------------------------------- | -------------------------- |
| every element of every answer carries its observations and their collection time | FR-002, SC-002, US1-1     |
| an answer containing one element without evidence fails the suite               | SC-002                     |
| every answer carries its snapshot and that snapshot's coverage verdict          | FR-017, FR-020, SC-009     |
| an agreed link and a one-sided one are distinguishable without opening evidence | FR-003, SC-003, US1-2      |
| an interface says whether its device described it or a neighbour revealed it    | FR-005                     |
| an interface carries the other spellings it is known by                         | FR-005                     |
| a weakly identified device says so                                              | FR-006, US1-4              |
| following an observation to its raw output returns the stored bytes             | FR-004, SC-006a, US1-3     |
| the raw endpoint serves the bytes itself rather than a reference                | FR-004, FR-004a            |

**Naming, listing and choosing a snapshot.**

| Test                                                                            | Proves                     |
| ------------------------------------------------------------------------------- | -------------------------- |
| a device is reachable by its device key                                         | FR-001a                    |
| the same device is reachable by its hostname                                    | FR-001a, SC-001            |
| the same device is reachable by an address it answered on                       | FR-001a                    |
| a hostname naming two devices is refused with both named, run with inputs that tie | FR-001a, SC-001a, tie-break |
| every answer states the device key it resolved to                               | FR-001b                    |
| the device list carries key, hostname, addresses and weak marker, and no ports  | FR-001c, SC-001b           |
| a device on a flat segment returns every edge on its port, uncapped             | FR-001d                    |
| naming no snapshot answers from the most recently closed one carrying a graph   | FR-019                     |
| a snapshot closed but not yet projected is not the default, and says so if named | FR-019, FR-009, edge case |
| a quarantined snapshot is served, and its verdict is in the answer              | FR-019, US3-3, SC-009      |
| naming a snapshot explicitly never answers from another                         | FR-018                     |
| a device absent from the named snapshot is a plain absence, not an empty graph  | FR-009, edge case          |

**The boundary.**

| Test                                                                            | Proves                     |
| ------------------------------------------------------------------------------- | -------------------------- |
| no response on any endpoint, at any scope, contains a credential value          | FR-014, SC-006             |
| no response contains an object-store access key                                 | FR-014, SC-006             |
| `netmapper_api` cannot read `credential_set` at all                             | data-model.md              |
| `netmapper_api` cannot insert or delete in any zone                             | FR-016, SC-008, R3         |
| `netmapper_api` can update `api_token` and no other table                       | R6                         |
| serving every endpoint leaves every row of all four zones unchanged, by checksum | FR-016, SC-008            |
| a handler attempting a write fails against the read-only transaction            | FR-016, R7                 |
| no endpoint answers without a token, including any health or version path       | FR-010, contracts/rest.md  |

**The token subcommand.**

| Test                                                                            | Proves                     |
| ------------------------------------------------------------------------------- | -------------------------- |
| `token create` prints a value once and stores only its hash                     | FR-013, R4                 |
| no command and no endpoint returns a token value afterwards                     | FR-013                     |
| `token list` never prints a value                                               | FR-013                     |
| `token revoke` on an unknown name exits 2                                       | contracts/rest.md          |
| a scope the interface does not define is refused at creation                    | R13                        |
| the subcommands run as `netmapper_operator`, and the server as `netmapper_api`  | contracts/cli, R3          |

The role cases go in `internal/store/roles_test.go` beside the three that are there, and the token
states in `internal/api`. The read-only transaction case is the one that needs a deliberately bad
handler to prove, so it lives with the server rather than with the endpoints.

## 2. Lab run: a device, named the way an engineer knows it

With the four-node lab deployed, a crawl completed, and the engine and collector running as in 004:

```sh
export NETMAPPER_S3_ENDPOINT=localhost:3900 NETMAPPER_S3_BUCKET=netmapper NETMAPPER_S3_INSECURE=1 \
       NETMAPPER_S3_ACCESS_KEY=GK0123456789abcdef01234567 \
       NETMAPPER_S3_SECRET_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
TOKEN=$(./netmapper token create --name lab --scope read)
./netmapper api --listen :8080 &
```

```sh
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/devices | jq '.devices[] | {device_key, hostname, targets}'
```

Expected: three devices, sw1, sw2 and sw4. sw3 is absent because it refuses every credential set and
so has no entity, which is the crawl's answer and not this interface's.

```sh
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw1 | jq '{resolved: .device_key, ports: [.interfaces[].canonical_name]}'
```

Expected: `Ethernet1` and `Management0`, and `resolved` carrying the chassis MAC key, since the request
named a hostname and the answer says what it resolved to.

## 3. The evidence, followed to the bytes (US1-3, SC-006a)

```sh
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/devices/sw1 \
  | jq '.interfaces[0].evidence[0]'
```

Take the `observation_id` from that, then:

```sh
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/observations/<id> | jq '.commands'
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/observations/<id>/raw/<step>
```

Expected: the last command returns the actual `show interfaces status | no-more` output the switch
printed. That is the whole point of the feature in one request: from a port, to the observation, to
the bytes, without leaving the interface and without holding any credential beyond the token.

## 4. The verdict travels (FR-020, SC-009)

Make the lab quarantine a snapshot the way 004's `TestQuarantinedSnapshotIsProjected` does, by crawling
twice with the second run reaching fewer devices, then:

```sh
curl -sH "Authorization: Bearer $TOKEN" localhost:8080/v1/devices | jq '.snapshot'
```

Expected: the default answers from that snapshot, and the envelope carries
`"verdict": "quarantined"` with its coverage figure. A caller learns the graph is suspect from the same
answer that gave them the graph.

## 5. The refusals (US2)

```sh
curl -si localhost:8080/v1/devices | head -1
curl -siH "Authorization: Bearer nm_wrong" localhost:8080/v1/devices | head -1
./netmapper token revoke --name lab
curl -siH "Authorization: Bearer $TOKEN" localhost:8080/v1/devices | head -1
```

Expected: three identical 401s with identical bodies. Then confirm the same refusal is returned for a
device that exists and one that does not, so a caller cannot use the refusal to probe.

## 6. The boundary (FR-016, SC-008)

Take a checksum of all four zones, serve every endpoint, take it again.

```sql
SELECT md5(string_agg(x, '|' ORDER BY x)) FROM (
  SELECT id || status || fact_family AS x FROM observation
  UNION ALL SELECT id || device_key || weak::text FROM entity
  UNION ALL SELECT id::text || canonical_name || source FROM interface
  UNION ALL SELECT id::text || name || confidence FROM edge
) t;
```

Expected: unchanged. The role makes this hard to get wrong and the read-only transaction makes it hard
to get wrong twice, but the checksum is what proves it.

## Divergences recorded during implementation

Recorded here as they are found, the way 002, 003 and 004 did, so the reference and the behaviour stay
corrected in the same change.

- **FR-009's evicted case has nothing to read.** No eviction or tombstone exists in the schema:
  `snapshot.state` is `open` or `closed`. The interface answers `open`, closed-but-unprojected and
  stale-projection snapshots with `409 not_projected`; the evicted answer arrives with the feature that
  evicts. Also recorded as a known open point in plan.md.
- **Two grants data-model.md's first draft left out.** `entity_claim` and `identifier_claim` carry a
  device's evidence. The migration grants them and data-model.md now lists them.
- **`UPDATE` on `api_token` is column-level**, `last_used_at` only, narrower than the draft's table
  grant.
- **A device's evidence also includes the observations its address edges cite.** The projector matches
  an identity observation that produced no claim to a device by address; reading claims alone would
  have left such a device without evidence, and FR-002 then refuses to serve it.
- **Refusals that name a device say which snapshot was searched.** `404 no_such_device`,
  `409 ambiguous` and `404 no_such_interface` carry the `snapshot` envelope, since a caller who named
  no snapshot cannot otherwise tell what was looked at (FR-017).
- **`/v1/interfaces/{device}/{name}` takes the rest of the path as the port name**, so `Ethernet1/1`
  works written as it is as well as `Ethernet1%2F1`.
- **A method other than `GET` with a valid token is `405`**, which the standard library's mux gives for
  free and a test now pins (FR-012).
- **Every element carries `confidence`** from a fixed set per kind (spec FR-002a), found missing by
  `/speckit-analyze` against constitution Principle I.
- **Lab run, 2026-09-25 (tasks.md T051).** Sections 2, 3, 5 and 6 matched against the four-node cEOS lab,
  with `token` run as `netmapper_operator` and `api` as `netmapper_api`. Three differences, none in the
  interface:
  - Section 3: `interfaces[0].evidence[0]` was the LLDP observation (`show lldp neighbors detail`),
    not `show interfaces status`, because evidence is ordered by collection time and LLDP came first.
    The interface listing is the port's second evidence entry. The chain from port to bytes holds
    either way.
  - Section 4: excluding two switches from the perimeter does not quarantine, since the gate measures
    against the current perimeter, and `docker pause` does not make a switch unreachable, since the
    paused container's network stack still accepts the connection and the session hangs until
    unpause. What worked: `docker exec clab-netmapper-sw2 ip link set eth0 down` (and sw4), crawl, then
    `ip link set eth0 up`. That gave `quarantined` at 0.5, and `/v1/devices` with no snapshot named
    answered from it with the verdict in the envelope.
  - Section 6: the checksum query as first written did not run, because its subquery never named the
    column `x`, and a shell comparing two empty results would have reported "unchanged". The query
    above now names it (`AS x`); with that, the checksum was identical before and after serving every
    endpoint under a valid, an absent and an unknown token.
