# Quickstart: validating the graph projector

Run guide for proving the feature end to end. Schema is in [data-model.md](data-model.md), commands in
[contracts/cli.md](contracts/cli.md), decisions in [research.md](research.md).

## Prerequisites

Same as [003's quickstart](../003-identity-resolution/quickstart.md): Go, Docker with compose, and for
the lab scenarios containerlab with a cEOS image. This feature adds no dependency and no service. It
does need the pack directory readable by the engine and by `netmapper project`, which is the `--packs`
flag both now take.

The `fakeos` test pack needs two additions before the interface tests can say anything: a
`remote_interface` field in its `neighbours` recipe and template, and an `interface_names` rule the
far-end spelling actually exercises. Both are pack data, which is the point.

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d
export NETMAPPER_TEST_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
go test ./...
```

Expected: all pass. The projection tests build snapshots with the fake transport from 001 and must
include at least these cases:

| Test                                                                           | Proves                    |
| ------------------------------------------------------------------------------ | ------------------------- |
| a device's collected interfaces become one row each under the canonical name   | US1-1, FR-002, FR-003     |
| each carries description, states, speed, MTU and MAC from the family           | US1-1, FR-006             |
| a neighbour's spelling of a port lands as an alias on the same interface       | US1-2, FR-004, SC-002     |
| a spelling matching no naming rule names the interface itself                  | edge case, FR-005         |
| every alias names the observation it came from                                 | US1-2, FR-004             |
| an interface reaches its observation, its raw output and its collection time   | US1-3, FR-007, SC-003     |
| a device whose interfaces were never collected keeps its entity, with no ports | US1-4, FR-001             |
| two devices with a port of the same name get two interfaces                    | US1-5, FR-002             |
| a port only a neighbour named is created, marked `neighbour`                   | edge case, FR-006         |
| a device reached on two addresses does not get its interfaces twice            | edge case, R2             |
| two devices reporting each other produce one link, marked `both_ends`          | US2-1, FR-009, SC-004     |
| that link cites the observation from each side, with the side recorded         | US2-2, FR-007, FR-008     |
| a link attaches to the entity a chassis identifier resolves to                 | US2-3, FR-012             |
| a link attaches to the entity a management address resolves to                 | US2-3, FR-012             |
| two devices cabled on two ports produce two links                              | US2-4, FR-011             |
| a report whose far end matches no entity is a `one_end` link carrying it       | US3-1, FR-010             |
| a report whose far end resolved but never reported back stays `one_end`        | US3-2, FR-010             |
| each address an entity answered on is a `has_address` edge                     | US3-3, FR-013             |
| a one-sided link agreed in a later snapshot leaves the earlier one unchanged   | US3-4, FR-016             |
| two devices disagreeing on which ports are cabled: no link invented, one finding | FR-014, SC-007          |
| that finding names both sides, what each said, and cites both observations     | FR-014, Principle I       |
| three devices reporting one port produce a link per pair, none discarded       | edge case, R10            |
| a report naming nothing about the far end yields the port and no edge          | R6                        |
| a port reporting itself is dropped, not written, and the run still succeeds    | edge case, R6, tie-break  |
| a chassis identifier matching two entities attaches to neither, link stays `one_end` | FR-012, R8          |
| one device reporting one cable under two protocols produces one edge citing both | FR-011, R8              |
| the same spelling from two observations keeps the lowest observation id        | R5, tie-break             |
| an interface's MAC equal to a chassis MAC creates no entity and no device edge | edge case, R14            |
| a snapshot with an entity set but no interface and no neighbour projects to nothing, and is recorded as projected | edge case, FR-018 |
| a snapshot with no entity set is not projected and is not an error             | edge case, FR-001         |
| projecting again after a parser fix leaves no remnant of the old set           | edge case, FR-018         |
| two projections of one snapshot at once produce one set                        | edge case, R12            |
| re-projecting the same inputs yields the same interfaces and edges             | FR-017, SC-005            |
| a link keeps the same name in two consecutive snapshots of one perimeter       | FR-015, SC-006            |
| projection reads only the active parse generation                              | FR-019, R13               |
| a snapshot closed while the engine was down is projected on restart            | FR-021, SC-001            |
| a quarantined snapshot is projected like any other                             | spec Assumptions          |
| re-resolving a snapshot makes the sweep project it again                       | FR-021, R12               |
| `netmapper project` on an unchanged snapshot replaces its set                  | FR-022                    |
| re-projecting replaces the disagreement findings, none duplicated, none stale  | FR-018, R9                |
| re-projecting leaves the collector's and the resolver's findings untouched     | FR-020, R9                |
| projection writes nothing in the collected zone and no entity                  | FR-020, Principle II      |
| wiping every projected row and recomputing restores the same set               | SC-008, FR-023            |
| projection works as `netmapper_operator`, not only as the engine               | contracts/cli.md, R15     |

Two of those are the tie-breaks the constitution's workflow rule is about, and each has to be run with
inputs that actually tie: two endpoint references that are equal, and one spelling arriving from two
observations. The identifier matching two entities is no longer one of them: it is settled by refusing
to attach rather than by breaking the tie, which is a rule to test rather than an order to fix.

The wipe-and-recompute case is what proves Principle II here, so it runs the whole sequence: project a
snapshot, delete `edge_evidence`, `edge`, `interface_alias`, `interface_evidence`, `interface` and
`projection`, project again, compare row for row.

The role case is what the constitution added after 003 shipped a command its own role could not run:
`netmapper project` is tested on a connection as `netmapper_operator`, and the sweep on one as
`netmapper_engine`.

## 2. Lab run: ports and one cable

With the two-switch lab from 001 deployed, and the engine started with `--packs packs`:

```sh
JOB=$(./netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds)
```

Wait for the job to succeed, then find the snapshot and read its ports:

```sql
SELECT e.device_key, i.canonical_name, i.source, i.admin_state, i.oper_state, i.mtu, i.mac
FROM interface i JOIN entity e ON e.id = i.entity_id
WHERE i.snapshot_id = :SNAP ORDER BY e.device_key, i.canonical_name;
```

Expected: every port each switch listed, once, under the Arista pack's canonical spelling
(`Ethernet1`, not `Et1`), with `source = 'device'` for the ones the switch described itself.

The spellings that lead to one of them (SC-002):

```sql
SELECT e.device_key, i.canonical_name, a.spelling, a.source, a.observation_id
FROM interface_alias a
JOIN interface i ON i.id = a.interface_id
JOIN entity e ON e.id = i.entity_id
WHERE i.snapshot_id = :SNAP ORDER BY e.device_key, i.canonical_name, a.spelling;
```

Expected: the port on each side of the inter-switch cable carries at least two rows, its own spelling
with `source = 'device'` and the neighbour's with `source = 'neighbour'`.

## 3. The cable, and its evidence (US2, SC-003, SC-004)

```sql
SELECT g.name, g.confidence, g.first_seen, g.last_seen, g.attributes
FROM edge g WHERE g.snapshot_id = :SNAP AND g.type = 'l1_link' ORDER BY g.name;
```

Expected: exactly one row for the one cable between sw1 and sw2, `confidence = 'both_ends'`, and a
name of the form `l1_link:if:<key1>/Ethernet1|if:<key2>/Ethernet1`.

Follow it back to the bytes, both sides:

```sql
SELECT g.name, ev.side, o.target, o.collected_at, r.command, r.hash
FROM edge g
JOIN edge_evidence ev ON ev.edge_id = g.id
JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id
WHERE g.snapshot_id = :SNAP AND g.type = 'l1_link' ORDER BY g.name, ev.side;
```

Expected: two sides, two different targets, two `show lldp neighbors detail` commands.

The addresses (US3-3, FR-013):

```sql
SELECT g.from_ref, g.to_ref, g.confidence FROM edge g
WHERE g.snapshot_id = :SNAP AND g.type = 'has_address' ORDER BY g.from_ref;
```

Expected: one row per address each switch answered on, `confidence = 'direct'`.

## 4. The same link across two runs (SC-006)

Run the perimeter again with nothing changed, then compare the two snapshots:

```sql
SELECT name, count(*) FROM edge WHERE snapshot_id IN (:SNAP1, :SNAP2) AND type = 'l1_link'
GROUP BY name HAVING count(*) <> 2;
```

Expected: no rows. Every link carries the same name in both snapshots, because both device keys and
both canonical names did, and nothing was minted to make that true.

## 5. A one-sided link (US3-1)

Add an unmanaged neighbour to the lab, or point a seed at a device no credential set can reach, and
run again:

```sql
SELECT g.to_ref, g.confidence, g.attributes
FROM edge g WHERE g.snapshot_id = :SNAP AND g.type = 'l1_link' AND g.to_entity_id IS NULL;
```

Expected: one row per cable to the unreachable box, `confidence = 'one_end'`, a `to_ref` starting
`unknown:`, and `attributes` carrying whatever the reporting switch said about it: system name,
chassis identifier, management address, port spelling.

## 6. A disagreement (FR-014, SC-007)

Contrived, so it runs against the fake transport rather than the lab: make sw1 report sw2 on
`Ethernet1` while sw2 reports sw1 on a different port. Project, then:

```sql
SELECT f.subject_ref, f.detail FROM finding f
WHERE f.snapshot_id = :SNAP AND f.category = 'link_disagreement';
SELECT count(*) FROM edge WHERE snapshot_id = :SNAP AND type = 'l1_link' AND confidence = 'both_ends';
```

Expected: one finding naming both device keys and what each side said, and zero `both_ends` links
between that pair. Each side's own report survives as a `one_end` edge, which is what each side
actually described.

## 7. Recovery (FR-021)

Stop the engine, run a crawl to completion with a collector alone, restart the engine:

```sql
SELECT count(*) FROM snapshot s
JOIN resolution r ON r.snapshot_id = s.id
LEFT JOIN projection p ON p.snapshot_id = s.id
WHERE s.state = 'closed' AND p.snapshot_id IS NULL;
```

Expected: 0 within a few ticks, with no command run to make it so. Then re-resolve one snapshot and
watch the sweep project it again, because its entity set, and with it its interfaces, were replaced:

```sh
./netmapper resolve :SNAP
```

```sql
SELECT p.resolution_at = r.computed_at FROM projection p JOIN resolution r USING (snapshot_id)
WHERE p.snapshot_id = :SNAP;
```

Expected: true again once the sweep has run.

## Divergences recorded during implementation

Recorded here as they are found, the way 003's quickstart did, so the reference and the behaviour stay
corrected in the same change.

1. **"Projects to nothing" does not include the addresses.** The spec's edge case says a snapshot with
   an entity set but no interface and no neighbour observation "projects to nothing". That holds for
   everything those two families feed, so no interface and no link, and it does not hold for the
   `has_address` edges: FR-013 builds those from the entity set rather than from a fact family, so a
   device that answered still gets one. The test asserts `0 interfaces, 1 edge` rather than nothing.
2. **`interface.source` and `interface_alias.source` answer two different questions**, and sharing a
   pair of values made that easy to miss. On the interface, `neighbour` means the interfaces recipe
   never listed this port. On the alias, `neighbour` means another device wrote this spelling while
   reporting a cable; a device naming its own port as the local end of its own neighbours row is
   `device`, even though that same row is what created the port. The first implementation labelled the
   owner's own spelling `neighbour`, which the alias test caught.
3. **The empty far end had to be tested before the references were sorted.** A report saying nothing
   about the far end yields an empty reference, which sorts before every other, so after sorting it is
   indistinguishable from a near end that happens to sort low. The first implementation checked the
   sorted pair and produced an edge with no resolved endpoint, which `edge_from_is_resolved` rejected.
   The constraint did its job; the check moved before the sort.
4. **`testutil.Settle` had to learn about projections.** A job reaches `succeeded` inside the tick that
   closes its snapshot, before that same tick judges, resolves and now projects it, so stopping the
   engine could cancel the sweep mid-way. 003 added `Settle` for the entity set; it now waits for a
   `projection` row whose `resolution_at` matches the resolution too.
5. **The fakeos neighbours template gained a column, and an older test was feeding it the old one.**
   `internal/parse` TestOutcomes passed a five-column line to a six-column template, got
   `parse_failed` and no rows, and panicked indexing them. Adding a far-end port to a pack's data is a
   pack change that reaches every test built on that pack, which is the cost of vendor specifics being
   data rather than code, and a cheap one.
6. **`pack.LoadRoot` does not follow symlinks.** It lists a directory with `os.ReadDir` and keeps the
   entries reporting themselves as directories, which a symlink does not. A test that assembles a pack
   root has to copy. Worth knowing before anyone deploys a pack directory built out of links.
7. **An engine pointed at the wrong pack directory degrades quietly.** With no pack for a platform,
   `Normalise` returns the spelling unchanged, which is correct for an unknown platform (FR-005) and,
   for a known one loaded from the wrong directory, silently produces a second interface per port and
   links that never pair. It is not an error and nothing reports it. Worth a thought when the API
   feature gives these rows a surface.
