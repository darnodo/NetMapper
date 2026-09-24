# Quickstart: validating identity resolution

Run guide for proving the feature end to end. Schema is in [data-model.md](data-model.md), commands in
[contracts/cli.md](contracts/cli.md), decisions in [research.md](research.md).

## Prerequisites

Same as [001's quickstart](../001-crawl-loop/quickstart.md): Go, Docker with compose, and for the lab
scenarios containerlab with a cEOS image. This feature adds no new dependency and no new service.

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d
export NETMAPPER_TEST_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
go test ./...
```

Expected: all pass. The resolution tests build snapshots with the fake transport from 001 and must
include at least these cases:

| Test                                                                     | Proves               |
| -------------------------------------------------------------------------- | ---------------------- |
| two claim groups sharing one strong identifier become one entity          | US1-1, FR-002          |
| A shares a serial with C, C a chassis MAC with B: one entity              | US1-2, FR-002          |
| a duplicate-marked observation lands on the entity it duplicated          | US1-3, FR-008          |
| two chassis sharing only a hostname stay two entities                     | US1-4, FR-003          |
| a device with no strong identifier resolves, marked weak                  | US1-5, FR-004, FR-022  |
| a snapshot with no claims resolves to zero entities, marked resolved      | US1-6, R8              |
| the same device in two snapshots carries the same device key              | US1-7, SC-007, FR-021  |
| two devices contradicting on a serial: two entities, one finding          | US2-1, FR-007          |
| that finding cites the observations behind each side                      | US2-2, FR-005          |
| a claim group set matching two known devices: lowest key, one finding     | R6, FR-023             |
| a merge decision makes two keys one device                               | US3-1, FR-009          |
| a never-merge decision suppresses the conflict finding for that pair      | US3-2, FR-009          |
| a split survives the next resolution where the identifier reappears       | US3-3, FR-012          |
| a decision recorded after a resolution applies on the next one            | US3-4, FR-017          |
| a decision whose subjects are absent is skipped, not an error             | US3-5, edge case       |
| merge then never-merge on the same pair: the later one governs            | FR-011, edge case      |
| resolution reads only the active parse generation                         | FR-019, R10            |
| re-resolving with the same inputs yields the same grouping                | FR-013, SC-004         |
| re-resolving replaces the set, never leaves two sets or a partial one     | FR-015, R8             |
| two resolutions of one snapshot at once produce one set                   | edge case, R8          |
| a snapshot closed while the engine was down is resolved on restart        | FR-016, SC-001         |
| an open snapshot is refused                                               | FR-001                 |
| a quarantined snapshot is resolved like any other                         | FR-025                 |
| resolution writes nothing in the collected zone                           | FR-014, Principle II   |
| the engine cannot UPDATE or DELETE an entity_decision                     | FR-009, R12            |
| wiping every computed row and replaying restores the same keys and set    | SC-008, FR-020         |

The last one is the test that actually proves Principle II for this feature, so it runs the whole
sequence: resolve two snapshots, delete `entity`, `entity_claim`, `resolution`, `device` and
`device_identifier`, resolve both again in closing order, compare.

## 2. Lab run: one device, one entity

With the two-switch lab from 001 deployed and the engine and a collector running:

```sh
JOB=$(./netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds)
```

Wait for the job to succeed, then:

```sql
SELECT e.device_key, e.weak, e.attributes->>'hostname', e.attributes->'targets', e.first_seen, e.last_seen
FROM entity e JOIN snapshot s ON s.id = e.snapshot_id JOIN job b ON b.snapshot_id = s.id
WHERE b.id = :JOB ORDER BY e.device_key;
-- two rows, sw1 and sw2, neither weak, each with its serial as the key
```

Expected: two entities for two switches, whatever the crawl's task count was, and
`SELECT entities FROM resolution WHERE snapshot_id = :SNAP` returning 2.

Check the evidence chain reaches the bytes (FR-005, Principle I):

```sql
SELECT e.device_key, c.kind, c.value, o.target, o.collected_at, r.command
FROM entity e
JOIN entity_claim ec ON ec.entity_id = e.id
JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
JOIN observation o ON o.snapshot_id = c.snapshot_id AND o.id = c.observation_id
JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
WHERE e.snapshot_id = :SNAP ORDER BY e.device_key, c.kind;
```

## 3. The same device across two runs (SC-007)

Run the perimeter a second time with nothing changed:

```sql
SELECT device_key, count(*) FROM entity WHERE snapshot_id IN (:SNAP1, :SNAP2) GROUP BY device_key;
-- two keys, two rows each
```

The keys must be identical across the two snapshots. That identity is the whole point of the registry
and the thing the diff will hang off.

Then renumber a switch's management address in the lab and run again: same keys, a new address in
`attributes->'targets'`, no new device in `device`.

## 4. A contradiction (US2)

Give the lab two devices with the same serial. The cheapest way is a second cEOS node cloned from the
same image with its serial unchanged, on an address inside the perimeter, seeded directly:

```sql
SELECT subject_ref, detail FROM finding WHERE category = 'identity_conflict' AND snapshot_id = :SNAP;
-- {"conflict": "within_snapshot", "kind": "serial", "values": [...], "keys": [...]}
SELECT count(*) FROM entity WHERE snapshot_id = :SNAP;
-- still one entity per physical box: the contradicting component did not merge
```

Then settle it and re-resolve:

```sh
./netmapper decide never-merge --perimeter lab --keys <key-a>,<key-b> --note "cloned lab image"
./netmapper resolve :SNAP
```

Expected: the same two entities, and no `identity_conflict` finding this time (FR-009, US3-2).

## 5. Decisions surviving a recomputation (US3)

```sh
./netmapper decide merge --perimeter lab --keys serial:AAA,serial:BBB --note "warranty swap"
./netmapper resolve :SNAP
```

```sql
SELECT id, kind, subjects, actor, at, note FROM entity_decision ORDER BY id;
SELECT resolver_version, decisions_applied, entities FROM resolution WHERE snapshot_id = :SNAP;
```

Expected: the decision row is untouched by the resolution, `decisions_applied` names it, and the two
keys now produce one entity. Record a contradicting `never-merge` on the same pair and re-resolve: two
entities again, both decisions still readable (FR-011).

## 6. Recovery (FR-016)

Covered by an integration test rather than the lab, for the same reason 002's was: closing a snapshot
requires the engine, so there is no way to close one while the engine is down by hand. The test closes
a snapshot directly against the database with no engine running, starts the runner, and asserts the
snapshot ends with exactly one `resolution` row and a complete entity set.

## Divergences recorded during implementation

To be filled in during `/speckit-implement`, the way 002's were. Anything the lab contradicts belongs
here rather than in a silent edit of the plan.
