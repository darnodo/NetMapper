# Quickstart: validating the crawl loop

Run guide for proving the feature end to end. Schema is in [data-model.md](data-model.md), commands
in [contracts/cli.md](contracts/cli.md), configuration in [contracts/config.md](contracts/config.md).

## Prerequisites

- Go (version in `go.mod`), Docker with compose.
- For the lab scenarios: containerlab and an Arista cEOS image imported as `ceos:latest`.

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d          # PostgreSQL + Garage
export NETMAPPER_TEST_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
go test ./...
```

Expected: all pass. The crawl tests use the fake transport and must include at least these cases:

| Test | Proves |
|---|---|
| perimeter excludes before dial | FR-002, SC-002 |
| seed plus two neighbours, one reporting the other | US1-1, no loop |
| one device on two addresses | US1-2, FR-004 |
| neighbour outside perimeter | US1-3 |
| unreachable, denied, unknown platform, template drift | US2-1..4, FR-009 |
| credential budgets per set, unresolvable reference | FR-018, FR-019, edge cases |
| kill worker mid-scrape, second worker resumes | US3-1, US3-2 |
| two workers on one job | US3-3, FR-013 |
| collector role cannot UPDATE observation | FR-010 |
| no lab secret found in any table or audit row | FR-017, SC-007 |
| every shipped pack passes the read-only lint | FR-024 |

## 2. Lab run

```sh
sudo containerlab deploy -t test/lab/two-switch.clab.yaml     # sw1, sw2, LLDP between them, plus
                                                             # an unused address and a wrong-password host
export NETMAPPER_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export LAB_SNMP_COMMUNITY=public LAB_SSH_PASSWORD=admin
netmapper migrate
netmapper engine &
netmapper collector --id c1 --packs ./packs &
JOB=$(netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set lab-seeds)
```

Wait until the job ends:

```sql
SELECT state FROM job WHERE id = :JOB;                       -- succeeded
SELECT state, closed_at FROM snapshot WHERE job_id = :JOB;   -- closed, not null
```

Expected results:

```sql
-- US1-1 / SC-001: both switches identified and scraped once each
SELECT target, fact_family, status FROM observation o JOIN snapshot s ON s.id = o.snapshot_id
WHERE s.job_id = :JOB ORDER BY target, fact_family;
-- sw1 and sw2: identity, neighbours, interfaces all collected, one row each

-- US2: every attempted target has an outcome
-- unused address -> identity unreachable; wrong-password host -> identity denied

SELECT domain, category, subject_ref FROM finding f JOIN snapshot s ON s.id = f.snapshot_id
WHERE s.job_id = :JOB;
-- compliance / credential_denied for the wrong-password host, with a finding_evidence row

-- US1-4 / SC-005: provenance
SELECT o.target, r.command, o.collected_at, encode(r.hash, 'hex')
FROM observation o JOIN observation_raw r ON r.observation_id = o.id
WHERE o.fact_family = 'neighbours';
-- then fetch raw/sha256/<hex> from the bucket: bytes equal the device output
```

## 3. Interruption (US3)

Start a new run, then `kill -9` the collector as soon as the first `scrape` task is `claimed`. Start
it again with the same command. Expected: the job succeeds, and

```sql
SELECT target, fact_family, count(*) FROM observation o JOIN snapshot s ON s.id = o.snapshot_id
WHERE s.job_id = :JOB GROUP BY 1, 2 HAVING count(*) > 1;
-- zero rows
```

Run two collectors (`--id c1`, `--id c2`) against a third job. Same query, zero rows, and
`SELECT DISTINCT claimed_by FROM task WHERE job_id = :JOB` shows both ids.

## 4. Refusal to start (FR-001)

```sh
netmapper run --config test/lab/netmapper.yaml --perimeter lab --seed-set outside-seeds; echo $?
```

Expected: `seed "10.99.0.1" is outside perimeter "lab"` on stderr, exit code 2, no new row in `job`.

## 5. Cancellation (FR-025)

Start a run, then `netmapper cancel $JOB`. Expected: job ends `cancelled`, snapshot `closed`, the
observations written before the cancel are still there, and no task is left `pending`.
