# Quickstart: validating the crawl loop

Run guide for proving the feature end to end. Schema is in [data-model.md](data-model.md), commands
in [contracts/cli.md](contracts/cli.md), configuration in [contracts/config.md](contracts/config.md).

## Prerequisites

- Go (version in `go.mod`), Docker with compose.
- For the lab scenarios: containerlab and an Arista cEOS image imported as `ceos:latest`.
  On macOS, open the repo in the devcontainer (`.devcontainer/devcontainer.json`, OrbStack or
  Docker Desktop) which ships containerlab and Go; on Apple Silicon use the cEOS ARM64 image
  (`docker import cEOSarm-lab-<version>.tar.xz ceos:latest`).

## 1. Automated checks (no lab)

```sh
docker compose -f deploy/compose.yaml up -d          # PostgreSQL + Garage (+ one-shot bucket setup)
export NETMAPPER_TEST_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900     # without it the crawl tests skip
go test ./...
```

Expected: all pass. The crawl tests use the fake transport and must include at least these cases:

| Test                                                  | Proves                     |
| ----------------------------------------------------- | -------------------------- |
| perimeter excludes before dial                        | FR-002, SC-002             |
| seed plus two neighbours, one reporting the other     | US1-1, no loop             |
| one device on two addresses                           | US1-2, FR-004              |
| neighbour outside perimeter                           | US1-3                      |
| unreachable, denied, unknown platform, template drift | US2-1..4, FR-009           |
| credential budgets per set, unresolvable reference    | FR-018, FR-019, edge cases |
| kill worker mid-scrape, second worker resumes         | US3-1, US3-2               |
| two workers on one job                                | US3-3, FR-013              |
| collector role cannot UPDATE observation              | FR-010                     |
| no lab secret found in any table or audit row         | FR-017, SC-007             |
| every shipped pack passes the read-only lint          | FR-024                     |

## 2. Lab run

```sh
sudo containerlab deploy -t test/lab/two-switch.clab.yaml     # sw1, sw2, LLDP between them, plus
                                                             # an unused address and a wrong-password host
export NETMAPPER_DSN=postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable
export NETMAPPER_S3_ENDPOINT=localhost:3900 NETMAPPER_S3_BUCKET=netmapper NETMAPPER_S3_INSECURE=1 \
       NETMAPPER_S3_ACCESS_KEY=GK0123456789abcdef01234567 \
       NETMAPPER_S3_SECRET_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
export LAB_SNMP_COMMUNITY=public LAB_SSH_PASSWORD=admin
export LAB_SNMP_V3='{"auth":"lab-auth-sw2","priv":"lab-priv-sw2"}'
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

## Divergences recorded during implementation (2026-09-18)

- Section 1: the tests also need `NETMAPPER_TEST_S3_ENDPOINT`; without it every test that
  touches the object store is skipped, not failed. The dev S3 key and bucket are created by the
  `garage-init` service of `deploy/compose.yaml`.
- Section 2: the collector needs the `NETMAPPER_S3_*` variables above. The unused address
  (172.20.20.9) and the wrong-password host (sw3, 172.20.20.4) are seeds in `lab-seeds`, since
  LLDP never reports an unused address. **Not yet run**: the implementation machine had neither
  containerlab nor a cEOS image, so the lab scenarios and the recording of real cEOS output into
  `packs/arista_eos/testdata/lab/` (T045) are still to do. Without the lab, the same binary was
  run against 172.20.20.0/24 with nothing answering: every seed ended `identity unreachable`,
  the job `succeeded`, and SIGTERM stopped the collector cleanly.
- Section 3: covered without the lab by `internal/collector/resume_test.go` and `multi_test.go`.
- Section 4: checked with the binary: `seed "10.99.0.1" is outside perimeter "lab"` on stderr,
  exit 2, no job row.
- Section 5: covered without the lab by `internal/jobrunner/cancel_test.go`; `netmapper cancel`
  on a job that is not running exits 2.

## Divergences recorded during implementation (2026-09-24)

Sections 2 and 3 run against the real lab (containerlab, `arista_ceos`/cEOS, two-switch topology)
for the first time, closing the items left open above.

- Section 2: found a real bug, not caught by `go test ./...` since no test exercised the
  `testdata/lab/` fixtures. Arista cEOS renders `Management Address Subtype` as a Python tuple
  repr, e.g. `('IPv4 ',)`, instead of the plain `IPv4` the template expected. `MGMT_ADDRESS_SUBTYPE`
  stayed empty, so `remote_mgmt_address_type` was dropped from the row while `remote_mgmt_address`
  stayed set; `fact.Validate`'s `TypedBy` check then rejected the row, `neighbours` came back
  `parse_failed`, and sw2 (reachable only through LLDP from sw1, per `lab-seeds`) was never
  discovered. Fixed in `packs/arista_eos/templates/show_lldp_neighbors_detail.textfsm`: the
  subtype value is now read tolerant of surrounding `(` `'` `,` `)` and of the numeric suffix
  form (`IPv4 (1)`) already present in `testdata/ntc/`. Re-run after the fix: sw1 and sw2 both
  `identity`/`interfaces`/`neighbours` `collected`, sw3 `identity denied`, 172.20.20.9
  `identity unreachable`, snapshot `closed` — matches US1-1/SC-001 as specified.
- Section 3: both sub-cases pass on the real lab. Killing the collector with `-9` right after the
  first `scrape` task on sw1 reached `claimed` (caught by polling `task` for `kind='scrape' AND
  state='claimed'`), then restarting it with the same `--id c1`: the job still reached `succeeded`
  with zero rows in the `GROUP BY ... HAVING count(*) > 1` duplicate check. Running `c1` and `c2`
  together against a fresh job: same zero-duplicate result, and `SELECT DISTINCT claimed_by FROM
  task` showed both ids.
