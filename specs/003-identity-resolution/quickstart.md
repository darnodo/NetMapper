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

| Test                                                                      | Proves                |
| ------------------------------------------------------------------------- | --------------------- |
| two claim groups sharing one strong identifier become one entity          | US1-1, FR-002         |
| A shares a serial with C, C a chassis MAC with B: one entity              | US1-2, FR-002         |
| a duplicate-marked observation lands on the entity it duplicated          | US1-3, FR-008, SC-002 |
| two chassis sharing only a hostname stay two entities                     | US1-4, FR-003         |
| a device with no strong identifier resolves, marked weak                  | US1-5, FR-004, FR-022 |
| a snapshot with no claims resolves to zero entities, marked resolved      | US1-6, R8             |
| the same device in two snapshots carries the same device key              | US1-7, SC-007, FR-021 |
| two devices contradicting on a serial: two entities, one finding          | US2-1, FR-007, SC-005 |
| that finding cites the observations behind each side                      | US2-2, FR-005, SC-003 |
| a claim group set matching two known devices: lowest key, one finding     | R6, FR-023            |
| a merge decision makes two keys one device                                | US3-1, FR-009         |
| a never-merge decision suppresses the conflict finding for that pair      | US3-2, FR-009         |
| a split survives the next resolution where the identifier reappears       | US3-3, FR-012         |
| a decision recorded after a resolution applies on the next one            | US3-4, FR-017, SC-006 |
| a decision whose subjects are absent is skipped, not an error             | US3-5, edge case      |
| merge then never-merge on the same pair: the later one governs            | FR-011, edge case     |
| resolution reads only the active parse generation                         | FR-019, R10           |
| re-resolving with the same inputs yields the same grouping                | FR-013, SC-004        |
| re-resolving replaces the set, never leaves two sets or a partial one     | FR-015, R8            |
| two resolutions of one snapshot at once produce one set                   | edge case, R8         |
| a snapshot closed while the engine was down is resolved on restart        | FR-016, SC-001        |
| a split survives, and the merge keeps applying, on a third run            | SC-006, FR-012        |
| an open snapshot is refused                                               | FR-001                |
| a quarantined snapshot is resolved like any other                         | FR-025                |
| re-resolving replaces the conflict findings, none duplicated, none stale  | FR-015, R11           |
| re-resolving leaves the collector's own findings untouched                | FR-015, R11           |
| grouped observations disagreeing on a hostname resolve the same way twice | FR-003, FR-013, R15   |
| a weakly identified device keys on its address, not its hostname          | FR-022, R5            |
| two perimeters sharing a strong identifier stay two devices               | FR-021, clarification |
| resolution writes nothing in the collected zone                           | FR-014, Principle II  |
| the engine cannot UPDATE or DELETE an entity_decision                     | FR-009, R12           |
| wiping every computed row and replaying restores the same keys and set    | SC-008, FR-020        |
| an entity carries the first and last collected_at of its own evidence     | FR-006, Principle I   |
| a device that changed every strong identifier mints a new key             | FR-023                |
| a never-merge and a split leave two entities under two distinct keys      | FR-024                |
| a weakly identified device renumbered mints a new key                     | FR-022, edge case     |

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
-- two rows, sw1 and sw2, neither weak, each keyed on its chassis MAC (see divergence 10)
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

Then renumber a switch's management address and run again: same keys, a new address in
`attributes->'targets'`, no new device in `device`. Change it in place rather than redeploying, or the
node comes back with a new serial and a new MAC and reads as a replacement instead of a renumbering:

```sh
docker exec -i clab-netmapper-sw2 Cli -p 15 <<'EOF'
configure
interface Management0
ip address 172.20.20.13/24
end
EOF
```

## 4. A contradiction (US2)

sw4 in the topology is that device: sw1's serial, its own chassis MAC, on 172.20.20.5 and seeded. The
MACs must differ, because two nodes identical in every strong identifier are indistinguishable from one
node answering on two addresses, and 001's live deduplication ends the second as a duplicate before
resolution ever sees it.

The serial is pinned through `/mnt/flash/ceos-config`, bound in from `test/lab/ceos-config-sw1` and
`ceos-config-sw4`. cEOS ignores a `SERIALNUMBER` environment variable, so containerlab's `env:` does not
work for this (divergence 12), and without pinning the serial is generated per container and changes on
every redeploy.

```sql
SELECT subject_ref, detail FROM finding WHERE category = 'identity_conflict' AND snapshot_id = :SNAP;
-- {"conflict": "within_snapshot", "kind": "chassis_mac", "values": [the two MACs], "keys": [both]}
-- The kind named is the one they contradict each other on, not the one they share (divergence 13).
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

Run these as the operator rather than as the database owner, or the grants the contract relies on are
never exercised. The roles are `NOLOGIN`, so a lab needs a login role that is a member of one:

```sql
CREATE ROLE lab_operator LOGIN PASSWORD 'lab';
GRANT netmapper_operator TO lab_operator;
```

```sh
export NETMAPPER_DSN='postgres://lab_operator:lab@localhost:5432/netmapper?sslmode=disable'
```

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

Recorded on 2026-09-24. Section 1 is green, three parallel runs in a row. Sections 2, 3 and 5 have been
run against the containerlab lab (three cEOS nodes on 172.20.20.0/24); divergences 10 and 11 come from
that run. What is still unrun, and why, is at the end.

1. **The operator needs the finding grants too, `SELECT` included.** T010 gave `INSERT, DELETE` on
   `finding` and `finding_evidence` to `netmapper_engine` only, but `netmapper resolve` runs the resolver
   in the operator's process, so a re-resolution of a snapshot holding a collision would have failed for
   the operator. Both roles hold them. `SELECT` was missing from the first correction and a security
   review caught it: PostgreSQL reads the columns of a `DELETE`'s `WHERE` clause and of a `RETURNING`
   clause, so all three statements the resolver runs on that surface were refused under the operator role
   while every test connected as the engine, which holds `SELECT` from 001. The grant test now exercises
   both roles.

2. **Raising a finding needs its sequence.** 001 granted `USAGE ON ALL SEQUENCES` to the operator and
   the collector, not to the engine, so `GRANT USAGE ON SEQUENCE finding_id_seq TO netmapper_engine`
   is part of this migration. Research R12 predicted this was the shape of the gap, and it was.

3. **FR-024 needed a rule the plan did not have.** Two claim groups refused a merge can both match the
   identifier they collided on, which would give them one key and violate
   `UNIQUE (snapshot_id, device_key)`. The rule added: a key already used by an earlier component of
   the same snapshot is not offered again, and a component that cannot use its anchor walks to the next
   free identifier it carries before falling back to `addr:<address>`. Components are ordered by their
   anchor, so this is reproducible.

4. **A merge has to link claim groups that share nothing.** Decisions name device keys, not
   identifiers, so a merge of two devices with no identifier in common could not be applied by the
   token-based grouping alone. A device key the registry already resolves a claim group to is now a
   link like an identifier is.

5. **An operator merge is exempt from the contradiction rule.** Two devices an operator declares to be
   one box do disagree on their serials, so the automatic "a component that disagrees with itself is
   not merged" rule tore the merged entity apart again on every resolution. A component whose claim
   groups all resolve to the same single device key is one the registry already settled, by a previous
   resolution or by a decision, and the contradiction rule leaves it alone. R4's refusal is about
   automatic merges, which this is not.

6. **001's schema cannot hold two parse generations of one observation.** `observation` is unique on
   `(snapshot_id, target, fact_family, task_id)`, so the FR-019 test builds the superseded generation
   on its own address rather than as a second reading of the same one. Worth knowing before a replay
   feature is written; it is 001's constraint, not this feature's.

7. **`Lab.Crawl` had to wait for the engine's sweeps.** A job reaches `succeeded` inside the tick that
   closes its snapshot, before that same tick judges and resolves it, so stopping the engine right
   after `Wait` could cancel the sweep mid-way. The race predated this feature and was rare enough to
   pass; a second sweep per tick made it fire on roughly one parallel run in one. `Crawl` now waits for
   the verdict and the entity set before stopping the engine, which also removes a pre-existing source
   of flakiness in 002's tests.

8. **A split has to silence the identifier as a link, not only in the registry lookup.** Found by
   `/speckit-converge` after the first implement pass. Detaching an identifier from a device made a
   later claim group carrying it mint its own device, but two claim groups of one snapshot sharing that
   identifier still merged, because the grouping linked them on the token regardless. FR-012 and FR-024
   both ask for them to stay apart. The rule settled with the operator: a strong identifier a split
   detached from a device no longer links claim groups that resolve to that device, the way a
   never-merge silences a bridging identifier, and it is not attributed back to that device on the next
   resolution. A device key can be spelled like an identifier, so only identifier links are silenced,
   never key links.

9. **`resolverVersion` stays at 1 (T069).** The grouping changed several times while this was being
   built, but no snapshot outside the test schemas was ever resolved by an earlier version, so there is
   nothing to find and replay. The first bump belongs to the first change made after the lab has run.

10. **An Arista device keys on its chassis MAC, not its serial.** Found by the lab run. Section 2 used
    to say "each with its serial as the key", which is wrong for any pack declaring both: the anchor is
    the lexicographically smallest `<kind>:<value>` (R5), and `chassis_mac` sorts before `serial`. The
    real keys are `chassis_mac:00:1c:73:6d:29:e1` for sw1 and `chassis_mac:00:1c:73:4c:e4:1f` for sw2.
    The behaviour follows R5 exactly; the expectation was written before the rule met a real pack, and
    section 2 is corrected. Worth knowing for anyone reading a key and expecting a serial.

11. **A merge of two devices that are both real leaves the hostname to the lowest observation id.**
    Found by the lab run. R15 settles the weak attributes by taking the observation the crawl did not
    mark a duplicate, which answers the one-device-two-addresses case it was written for. When an
    operator merges two genuinely distinct devices, neither observation is a duplicate, so the tie-break
    falls through to the lowest observation id. Observed on the lab: merging sw1 and sw2 gives one entity
    carrying sw2's key and sw1's hostname, with both addresses in `targets`. Deterministic and
    reproducible, so FR-013 holds, but a reader will find it odd, and it is the operator's own decision
    that produced it. Left as is: naming the entity after the first subject of the decision would be the
    alternative, and that is a spec question rather than a bug.

12. **cEOS ignores a `SERIALNUMBER` environment variable.** Found while building section 4's clone.
    containerlab passes `env:` into the container, and the variable is there, but cEOS reads its platform
    overrides from `/mnt/flash/ceos-config` and generates a serial per container otherwise. The topology
    therefore binds a one-line file into sw1 and sw4. Verified from a cold `containerlab destroy` followed
    by `deploy`: the relative bind path resolves, both nodes come up with the pinned serial and their own
    chassis MACs, section 4 still raises exactly one collision finding, and cEOS does not rewrite the
    bound file in the working tree. Two consequences worth knowing: an unpinned serial is generated per
    container, so a lab expectation naming one is only good until something recreates the node, and the
    chassis MAC is not guaranteed either, since sw1's changed once during this work, although it survived
    the destroy and deploy above unchanged; and section 3's renumbering has to be a configuration change
    on a running node, because recreating it gives a new identity and reads as a replacement.

13. **The collision finding names the kind the two devices differ on, not the one they share.** Section 4
    expected `"kind": "serial"`. On the lab, sw1 and sw4 share the serial and differ on their chassis MAC,
    and the finding says `"kind": "chassis_mac"` with the two MACs as its values. FR-007 is explicit, the
    finding names "the contradicting identifier", so the behaviour is right and the expectation was
    written the wrong way round. Section 4 is corrected.

### What the lab confirmed

Sections 2, 3, 4 and 5 all pass against four cEOS nodes. Section 6 stays an integration test, for the
reason 002 gave: closing a snapshot needs the engine, so there is no way to close one by hand while the
engine is down.

Beyond the assertions those sections list, the run confirmed several things no test in section 1 reaches:
the evidence chain resolves on real device output, from entity through `entity_claim` and
`identifier_claim` to `observation_raw` and the commands behind each side, on both the SNMP and the SSH
path; `attributes->'identifiers'` carries the real serials and MACs; `netmapper decide` and
`netmapper resolve` print exactly what contracts/cli.md specifies, `resolve 1, 2 to apply` included, with
the real stale snapshots named; the collector's own `credential_denied` finding from sw3 survives a
re-resolution that removes the collision findings; and SC-006 holds end to end, since the never-merge
recorded against the first snapshot applied on its own to the next run of the perimeter, which is the one
claim about decisions that no single-snapshot test can make.
