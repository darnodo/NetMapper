# Quickstart: validating the coverage gate

Run guide for proving the feature end to end. Schema is in [data-model.md](data-model.md), commands in
[contracts/cli.md](contracts/cli.md), configuration in [contracts/config.md](contracts/config.md).
Decisions are in [research.md](research.md).

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

Expected: all pass. The gate tests build snapshots with the fake transport and must include at least
these cases:

| Test                                                           | Proves            |
| -------------------------------------------------------------- | ----------------- |
| first snapshot of a perimeter: published, null baseline        | FR-004, R10       |
| identical second snapshot: published, coverage 1.0             | US1-1, SC-001     |
| one baseline device now unreachable                            | US1-2/3, FR-006   |
| one baseline device never attempted at all                     | SC-006, US2-2     |
| baseline device renumbered, same strong claim                  | R2, no false loss |
| perimeter narrowed between runs, dropped device not a loss     | R8, edge case     |
| one device reached on two addresses counts once                | R2, R3, FR-003    |
| every reason named, with the addresses behind it               | FR-006, US2-1     |
| perimeter matched by name across two config versions           | R1                |
| baseline is the preceding run, not the last one judged         | FR-015, R7        |
| declared threshold classifies differently from the default    | US3, FR-005       |
| a threshold change leaves earlier verdicts alone               | SC-005, FR-005    |
| re-judge writes a new active row, old row still readable       | FR-009, FR-012    |
| re-judge with same inputs yields identical figures             | FR-012, SC-003    |
| engine cannot UPDATE or DELETE a judgement                     | FR-009            |
| snapshot closed while the engine was down is judged on restart | FR-014, US1-6     |
| open snapshot is refused                                       | FR-001            |
| two engines judging at once produce one active row             | R12               |
| a quarantined snapshot stays readable and unmodified            | FR-011, FR-008    |

## 2. Lab run: a clean baseline

With the two-switch lab from 001 deployed and the engine and a collector running:

```sh
JOB=$(./netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds)
```

Wait for the job to succeed, then:

```sql
SELECT classification, coverage, baseline_devices, carried_over, baseline_snapshot_id
FROM snapshot_judgement j JOIN snapshot s ON s.id = j.snapshot_id JOIN job b ON b.snapshot_id = s.id
WHERE b.id = :JOB AND j.active;
-- first run of the perimeter: published, coverage null, baseline_snapshot_id null
```

Run it a second time with nothing changed. Expected: `published`, `coverage = 1.0000`,
`baseline_devices = 2`, `carried_over = 2` (sw1 and sw2; sw3 is `denied`, never reached, so it is not
in the baseline device set, and 172.20.20.9 was never reachable either).

## 3. A device that stops being discovered (SC-006)

This is the shape of the LLDP parsing bug found while validating 001: a device that disappears from
discovery without ever being reported unreachable or denied. Turn LLDP off on sw1 so its neighbour
table comes back empty and sw2 is never queued:

```sh
docker exec -it clab-netmapper-sw1 Cli -p 15 -c 'configure
no lldp run'
```

Run again, then:

```sql
SELECT classification, coverage, carried_over, baseline_devices, breakdown->'missing'->'not_attempted'
FROM snapshot_judgement WHERE active AND snapshot_id = :SNAP;
-- quarantined, coverage 0.5000, carried_over 1, baseline_devices 2,
-- not_attempted: {"count": 1, "targets": ["172.20.20.3"]}
```

The point of this scenario: every outcome-based count in the snapshot looks perfect. sw2 is not
unreachable, not denied, not a parse failure. It is simply absent, and only the comparison against
the baseline catches it.

Restore LLDP before continuing:

```sh
docker exec -it clab-netmapper-sw1 Cli -p 15 -c 'configure
lldp run'
```

## 4. Per-perimeter thresholds (US3)

With two switches, losing one is 50% coverage, so the defaults quarantine it. Declare a looser
perimeter to see the same figure classified `degraded` instead:

```yaml
perimeters:
  - name: lab
    include: [172.20.20.0/24]
    exclude: [172.20.20.1/32]
    degraded_at: 0.5
```

Restore LLDP first and take a clean two-device baseline with that document, then turn LLDP off again
and run once more: a baseline is the run before, so section 3 left the perimeter at one device and
comparing one against one is published (see the divergences below). Expected on that second run: same
coverage `0.5000`, classification `degraded` instead of `quarantined`, and
`thresholds->>'source' = 'perimeter'`.

## 5. Re-judging (FR-009, FR-013)

```sh
./netmapper judge <snapshot-id>
```

```sql
SELECT id, classification, active, computed_at, gate_version
FROM snapshot_judgement WHERE snapshot_id = :SNAP ORDER BY id;
-- two rows: the first with active = false, the second active, both readable
```

Expected: the superseded row is still there with its original figures, and re-judging with nothing
changed reproduces the same classification and coverage (FR-012).

Also check the refusals:

```sh
./netmapper judge 999999; echo $?   # snapshot 999999 not found, exit 2
```

## 6. Recovery (FR-014)

Covered by an integration test rather than the lab: closing a snapshot requires the engine, so there
is no way to close one while the engine is down by hand. The test closes a snapshot directly against
the database with no engine running, then starts the runner and asserts the snapshot ends with
exactly one active judgement.

## Divergences recorded during implementation (2026-09-24)

Sections 1 to 5 run against the real lab. Section 6 stays an integration test as written above.

- Section 3: the neighbour table does not come back empty when LLDP is off, it comes back
  `parse_failed`. cEOS answers `% LLDP is not enabled`, which matches neither the template nor the
  pack's `empty_lines`, so the run raises a `data_quality` finding as well. This does not weaken the
  scenario: the finding says a command stopped parsing, nothing in the snapshot says a device is
  missing. Only the judgement does, as `not_attempted`.
- Sections 3 and 4 in sequence: a regression left in place becomes the new normal on the very next
  run, because a snapshot's baseline is the run before it. After section 3, the following run
  compares one device against one device and is published again. Section 4 therefore has to restore
  LLDP, take a clean two-device baseline, and only then turn LLDP off again with the tolerant
  document. This is the intended consequence of measuring change rather than absolute state, but it
  means an operator who ignores a quarantined verdict stops being told about that loss.
- Section 4 found a real defect: a perimeter declaring only `degraded_at: 0.5` kept the default
  `quarantined_below: 0.9`, so a coverage of 0.5 satisfied "degraded from 0.5" and "quarantined below
  0.9" at once and came out quarantined. The document's validation only compared the two when both
  were written out, so nothing caught it. The fix went further than the bug: since published is fixed
  at full coverage, the two keys could only ever describe one boundary, so `quarantined_below` was
  removed rather than repaired. Re-judging the affected snapshot with `netmapper judge` turned
  quarantined into degraded while keeping the superseded verdict readable, which is the first real
  use of the re-judge path. `gateVersion` went to 2 afterwards, so those two rows both carry version
  1 and cannot be told apart by it: the first time the column would have been useful, the bump came
  too late. That is the hand-bumping weakness R9 names, seen once in practice.
- Section 5: `netmapper judge <id>` prints `degraded 1/2 (baseline snapshot 19)` and
  `published no baseline` as the contract says, and exits 2 with `snapshot 999999 not found`.
