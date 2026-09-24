# Implementation Plan: Coverage gate (judge a closed snapshot)

**Branch**: `002-coverage-gate` | **Date**: 2026-09-24 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/002-coverage-gate/spec.md`

## Summary

After a run's snapshot closes, the engine compares it against the same perimeter's previous snapshot
and records a verdict: published, degraded or quarantined. The comparison matches devices by their
strong identifier claims, so a renumbered device is not a loss, and attributes a reason to every
baseline device that did not carry over, including `not_attempted` for devices that silently left
discovery. The verdict is a row in a new `snapshot_judgement` table, never a change to the snapshot
itself; judgements stack with exactly one active per snapshot, enforced by a partial unique index.
A new step in `jobrunner.Tick` sweeps closed snapshots with no active judgement, so an interruption
repairs itself; `netmapper judge <id>` is the only way to judge one twice. Details and trade-offs are
in [research.md](research.md).

## Technical Context

**Language/Version**: Go, same toolchain and module as 001 (1.26 or later)

**Primary Dependencies**: none added. pgx/v5 and goose/v3 already in `go.mod`; this feature is SQL and
control flow, no new transport, no new parser, no object store access

**Storage**: PostgreSQL only. One new table (`snapshot_judgement`), one new `SECURITY DEFINER`
function, one optional column on `perimeter`, three grants. Garage is untouched: a judgement cites
observations, it stores no bytes

**Testing**: `go test`; integration tests against PostgreSQL from `deploy/compose.yaml`, building
snapshots with the fake transport from 001; the containerlab two-switch topology for the end-to-end
scenarios in [quickstart.md](quickstart.md)

**Target Platform**: Linux containers (amd64, arm64), unchanged

**Project Type**: single Go binary with role subcommands; this feature adds one operator subcommand
(`judge`) and one step to the engine's existing loop

**Performance Goals**: none measured. One SQL statement per judgement over two snapshots' `identity`
observations (hundreds of rows at this tool's scale), run once per closed snapshot on the engine's
existing tick

**Constraints**: no device contacted (FR-007); nothing in the collected zone modified (FR-008); no
verdict rewritten in place, enforced by withheld grants rather than by convention (FR-009); the
judgement stays outside the snapshot-closing transaction, so a failing calculation can never keep a
job from finishing

**Scale/Scope**: one new table, one migration, one new subcommand, one new package
(`internal/gate`), plus the judging step in `internal/jobrunner`. Three classifications, five missing
reasons

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle / constraint                               | Status | How this plan holds it                                                                                                                                                                                                                                                                                                                                                                              |
| ---------------------------------------------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| I. Evidence travels with the answer                  | Pass   | A judgement is never a bare verdict: `breakdown`, `coverage`, `baseline_snapshot_id` and `thresholds` ship with it (FR-006), and every figure traces to `identity` observations in two named snapshots. No interface serves it here, so the response-level part of the principle is not yet in scope                                                                                                |
| II. Observations immutable, rest rebuildable         | Pass   | The collected zone is read only in this feature. A judgement is computed and rebuildable: FR-007/FR-012 require the same inputs to reproduce the same verdict, and `gate_version` records which calculation produced a row so a corrected one can be replayed (R9) rather than re-crawled. Judgements themselves are append only, with the supersede path confined to a `SECURITY DEFINER` function |
| III. Credentials and reach stay in the collector     | Pass   | Judging runs in `engine` and in the operator CLI. Neither loads packs, resolves a `secret_ref` nor dials. No perimeter check is needed because no packet is sent                                                                                                                                                                                                                                    |
| IV. Read only, outward                               | Pass   | No device contacted at all                                                                                                                                                                                                                                                                                                                                                                          |
| V. Vendor specifics are data                         | Pass   | The gate reads `identity` observations and identifier claims, both platform neutral. No vendor name appears anywhere in this feature                                                                                                                                                                                                                                                                |
| One binary, three roles                              | Pass   | No new long-running role: a step inside the existing `engine` loop, plus the operator subcommand `judge` alongside `run`, `cancel`, `migrate`                                                                                                                                                                                                                                                       |
| Frontier in PostgreSQL, no crash state in memory     | Pass   | The sweep is a query over `snapshot`, so a killed engine leaves nothing behind; the next tick finds the same work (FR-014)                                                                                                                                                                                                                                                                          |
| Raw output in object store by hash                   | Pass   | Not touched                                                                                                                                                                                                                                                                                                                                                                                         |
| Status enum closed and non-null                      | Pass   | The six observation statuses are unchanged and read only. The judgement adds its own closed enum of three classifications, `NOT NULL` with a `CHECK`                                                                                                                                                                                                                                                |
| Snapshots immutable once closed                      | Pass   | The `snapshot_closed_is_final` trigger from 001 stays; the verdict lives in its own table precisely so the snapshot row is never touched (R4)                                                                                                                                                                                                                                                       |
| Config YAML posted whole, versioned, recorded on run | Pass   | Thresholds are two optional keys inside the same document, stored on the `perimeter` row of that config version, so a verdict is read against the thresholds its own run used (R13)                                                                                                                                                                                                                 |
| Workflow: open questions go to `docs/`               | Action | The deltas below must be carried into `docs/c4-model/04-data-model.md` in this branch                                                                                                                                                                                                                                                                                                               |

Post-design re-check: still passes. Phase 1 added the `judge_snapshot` function, the partial unique
index, `gate_version`, and the two `perimeter` columns. None of them crosses the collected/computed
boundary, moves a credential, or gives any role a way to modify an observation.

Principles touched by this feature: I, II.

## Documentation deltas to carry into `docs/c4-model/04-data-model.md`

- New reported-zone table `snapshot_judgement`, with the partial unique index as the "one active
  verdict" invariant.
- `judge_snapshot(...)` as the only write path to it, `SECURITY DEFINER`, owned by `netmapper_owner`.
- `perimeter` gains `degraded_at`.
- The engine's grant set gains `INSERT` on one reported-zone table and sequence usage, which 001 had
  given to no role but the operator and collector.
- Invariant: a perimeter's identity across config versions is its **name**, not its id.

## Project Structure

### Documentation (this feature)

```text
specs/002-coverage-gate/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── cli.md
│   └── config.md
├── checklists/
│   └── requirements.md
└── tasks.md              # /speckit-tasks
```

### Source Code (repository root)

```text
cmd/netmapper/
├── main.go                   # + judge subcommand dispatch
└── judge.go                  # netmapper judge <snapshot-id>

internal/
├── gate/                     # the feature: baseline selection, comparison, classification
│   ├── gate.go               # Judge(ctx, db, snapshotID) : the whole verdict, one transaction
│   ├── baseline.go           # perimeter-by-name lookup, closing-order selection (R1, R7)
│   ├── compare.go            # the comparison query and its row types (R2, R3, R11)
│   └── thresholds.go         # defaults, per-perimeter overrides, classification (R13)
├── config/                   # + degraded_at parse and validation
├── jobrunner/                # + judging step in Tick, sweep query (R6)
└── store/                    # unchanged

migrations/0005_judgement.sql # table, function, perimeter columns, grants
```

**Structure Decision**: one new package, `internal/gate`, holding everything that decides a verdict;
`internal/jobrunner` only calls it. That boundary is what lets the sweep, the operator subcommand and
the tests all go through the same function, which is what makes FR-012 (same inputs, same verdict)
testable rather than aspirational. Everything else extends a file that already exists.

## Known open points

| Point                                                 | Why it is open                                                                                                                   | Closed when                                                                                                      |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| A judgement's figures are readable only through SQL   | No interface serves them yet; the spec puts presentation out of scope, and `api` does not exist as a role                        | The API feature ships and reads `snapshot_judgement` alongside the snapshot                                      |
| `gate_version` is bumped by hand                      | Nothing detects that the calculation changed; the failure mode is re-judging more snapshots than strictly needed (R9)            | A second calculation version actually exists and the bump is exercised once                                      |
| Weak-claim-only devices fall back to address matching | 001's shipped pack gives every reachable device a strong claim (serial, chassis MAC), so the fallback is unexercised in practice | A pack ships a platform where no strong identifier can be read, and a test covers a renumbering of such a device |

## Complexity Tracking

| Addition                                                | Why needed                                                                                                                       | Simpler alternative rejected because                                                                                                                            |
| ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `judge_snapshot` as a `SECURITY DEFINER` function       | The supersede path needs one `UPDATE` (clearing `active`) that must not become a general right to rewrite verdicts               | Granting the engine `UPDATE` on the table would make FR-009 a convention rather than a constraint; 001 already uses this device for partition creation          |
| Matching devices on strong claims rather than addresses | A renumbered device would otherwise read as a loss plus an arrival, producing false quarantines on any DHCP or renumbering event | Address-only matching is simpler and wrong in exactly the environments this tool is for; the claims already exist in the schema, unused by anything else so far |
| Perimeter identity by name                              | `perimeter.id` changes on every run because the config document is posted whole and versioned                                    | Comparing by id gives every snapshot an empty history and silently disables the entire feature (R1)                                                             |
