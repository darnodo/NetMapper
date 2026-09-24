# Research: Coverage gate (judge a closed snapshot)

The stack is already fixed by 001 and the constitution: same Go module, same binary, same database.
This file settles what the spec leaves open, and records what reading the 001 schema turned up. Each
entry gives the decision, why, and what else was considered.

## R1. What identifies a perimeter across runs

- Decision: a perimeter's identity across runs is its **name**, scoped to nothing else. Two snapshots
  belong to the same perimeter when their jobs' `parameters.perimeter_id` rows resolve to the same
  `perimeter.name`.
- Rationale: `perimeter` is `UNIQUE (config_version, name)` and every `netmapper run` posts the
  configuration document whole, creating a fresh `config_version` and therefore a fresh `perimeter.id`
  even when the document is byte-identical. Comparing by `perimeter_id` would give every snapshot an
  empty history and make FR-015 unimplementable: each run would look like a first run.
- Alternatives: comparing by `config_version` (worse, same problem one level up); hashing the
  include/exclude ranges as the identity (a legitimate scope change would then silently start a new
  history, which is exactly the case the spec says must be visible as coverage, not hidden);
  introducing a stable `perimeter_key` column now (more schema for a property the name already
  carries, and renaming a perimeter is a deliberate act that can reasonably start a new history).

## R2. Matching one device across two snapshots

- Decision: match on **strong identifier claims** first (`identifier_claim` rows with
  `strength = 'strong'`, joined on `(kind, value)` between the two snapshots), and fall back to the
  address (`observation.target`) only for baseline devices that have no strong claim to match on.
- Rationale: 001 already writes strong claims (serial, chassis MAC) at identification time precisely
  so one device reached on several addresses is one device. Matching on address alone would report a
  renumbered or DHCP-addressed device as one loss plus one arrival, which is a false coverage
  collapse. The fallback matters because a device that went unreachable or denied in the newer
  snapshot has no claim in it at all: the only handle left is the address it was tried on.
- Alternatives: address-only matching (simplest, but produces false regressions on any renumbering);
  hostname matching (`hostname` is a weak claim in the 001 pack and two devices can share a name, an
  edge case 001's own spec calls out); waiting for the entity resolution feature to provide stable
  device ids (that feature reads judgements, so depending on it inverts the order).

## R3. Attributing a reason to a device that did not carry over (FR-006)

- Decision: for each baseline device missing from the newer snapshot, look in the newer snapshot for
  an `identity` observation on **any address that device was known by in the baseline** (its own
  identity observation's `target`, plus the targets of the baseline's other `identity` observations
  carrying `duplicate_of_task` pointing at it). The reason is that observation's `status`
  (`unreachable`, `denied`, `unsupported`, `parse_failed`); when no such observation exists at any of
  those addresses, the reason is `not_attempted`.
- Rationale: `not_attempted` is the reason the whole feature exists (SC-006). It is the shape of the
  LLDP parsing bug found while validating 001: sw2 never appeared as unreachable or denied, it simply
  never entered the frontier, so every outcome-based count looked perfect. Reading the newer
  snapshot at the baseline's addresses is the only way to tell "we tried and failed" from "we never
  tried".
- Alternatives: reading `task.state`/`skip_reason` instead of observations (task rows are control
  plane and partitioned per job, and `skipped` out-of-perimeter targets would need special handling
  anyway, while observations are the zone the constitution calls authoritative); reporting a single
  aggregate count with no per-reason breakdown (fails FR-006 and US2).

## R4. Where a judgement lives, and who may write it

- Decision: a new `snapshot_judgement` table in the reported zone (migration `0005_judgement.sql`),
  beside `finding`. `netmapper_engine` gains `INSERT` on it, plus `USAGE` on its sequence; no role
  gains `UPDATE` or `DELETE` on it.
- Rationale: a judgement is computed from observations, not collected from a device, so it belongs
  with `finding` rather than in the collected zone. The 001 grant matrix gives the engine only
  `SELECT` on the collected zone and `SELECT, UPDATE` on `job`, `task`, `snapshot`, and gives sequence
  `USAGE` to the operator and collector only: both gaps have to be closed explicitly for the engine to
  insert anything at all. Withholding `UPDATE`/`DELETE` is what makes FR-009 (never modified, never
  deleted) true in the database rather than by convention.
- Alternatives: adding columns to `snapshot` (blocked outright by the `snapshot_closed_is_final`
  trigger from 001, and it would put a computed verdict in a row the collected zone owns); reusing
  `finding` with a new domain (a finding is about a subject inside a snapshot, a judgement is about
  the snapshot itself, and overloading it would force `subject_ref` to mean two things).

## R5. One active judgement per snapshot

- Decision: a `active boolean NOT NULL` column with a partial unique index
  `UNIQUE (snapshot_id) WHERE active`, and a re-judge that flips the old row and inserts the new one
  in a single transaction.
- Rationale: this is the pattern 001 already uses for `parse_generation` ("partial unique index,
  exactly one true per snapshot"), so there is one way to express "many rows, one current" in this
  schema rather than two. The database enforces FR-001's "exactly one active judgement at any moment"
  instead of the application promising it.
- Alternatives: a `superseded_by` pointer (readable, but nothing stops two heads); deriving "active"
  as the highest `id` per snapshot (no constraint to violate, so a bug produces a silently wrong
  answer rather than an error); deleting the old row (breaks FR-009).
- Note: flipping `active` on the previous row is an UPDATE, which contradicts "no role gains UPDATE"
  in R4. Resolved by making the flip the responsibility of a `SECURITY DEFINER` function owned by
  `netmapper_owner` (`judge_snapshot`), the same device 001 uses for partition creation. The engine
  may call it; it may not update the table directly, so the only reachable transition is
  "deactivate current, insert new", never "rewrite a verdict in place".

## R6. Where the judging loop runs

- Decision: a new step inside `jobrunner.Tick`, independent of the loop over active jobs: one query
  for closed snapshots with no active judgement, then judge each. No change to the close path.
- Rationale: FR-014 needs a sweep, and `Tick` already is the engine's only component and already runs
  on a ticker. Hanging the judgement off the close path instead would leave a permanent gap whenever
  the engine dies between the two, and `Tick`'s existing loop only selects jobs in `running` or
  `cancelling`, so a snapshot closed a moment ago is already out of its reach. A separate query is
  also what keeps the judgement out of the closing transaction, which is what the spec's Q3 answer
  asked for.
- Alternatives: judging inside the closing transaction (a failing calculation would then block the
  job from ever finishing); a separate `netmapper judge --watch` process (a fourth long-running role,
  which the constitution forbids without an amendment).

## R7. Choosing the baseline

- Decision: the baseline is the snapshot of the same perimeter (R1) with the greatest `closed_at`
  strictly earlier than the snapshot being judged, among those carrying an active judgement. Ties on
  `closed_at` break by `snapshot.id`. A snapshot with no such predecessor has no baseline.
- Rationale: FR-015 requires selection by closing order alone so a re-judge months later resolves the
  same baseline. Restricting to snapshots that carry an active judgement keeps the chain well
  defined: every baseline has itself been judged, so "coverage against the last known good reading"
  is meaningful rather than accidental.
- Alternatives: ordering by `opened_at` (two overlapping runs would compare against the wrong one);
  ignoring the "has an active judgement" restriction (an unjudged predecessor would be skipped
  silently on one pass and used on the next, making the result depend on sweep timing).
- Consequence for the race in the spec's edge cases: when a snapshot's immediate predecessor is
  closed but not yet judged, the newer one is left alone this pass rather than compared against an
  older run. The sweep picks it up once its predecessor has a verdict, which is why the sweep is
  ordered by `closed_at` ascending.

## R8. Filtering the baseline to the current perimeter

- Decision: before comparing, drop from the baseline device set every device whose addresses all fall
  outside the include/exclude ranges of the perimeter **as declared for the snapshot being judged**.
  The test is written in SQL, `target <<= ANY (include) AND NOT (target <<= ANY (exclude))`, against
  the `perimeter` row of the judged snapshot's own `config_version`.
- Rationale: the spec's edge case is explicit: a deliberately narrowed perimeter must not read as a
  coverage collapse. Expressing include-then-exclude in SQL keeps the whole comparison in the single
  statement R11 asks for, rather than loading the ranges to filter rows the database already holds.
  It is the same test `perimeter.Allowed` applies in 001, one `ANY` per side.
- Alternatives: calling `perimeter.Allowed` from Go after loading the ranges (this was the original
  decision here, changed during implementation: it would have meant reading the baseline devices out
  of the database only to drop some of them in the caller); comparing unfiltered and explaining the
  drop in the breakdown (honest, but it makes every intentional scope reduction produce a quarantine
  that an operator must learn to ignore, which trains people to ignore the gate).

## R9. Recording which calculation produced a judgement

- Decision: a `gate_version integer NOT NULL` column, written from a single constant in the judging
  code, bumped by hand when the coverage calculation changes in a way that alters results.
- Rationale: the re-judge path (FR-009, FR-013) exists so a corrected calculation can be applied to
  old snapshots. Without a version recorded, there is no way to ask "which judgements came from the
  version with the bug", so a fix means re-judging everything blind. One integer column and one
  constant is the smallest thing that answers that question, and the failure mode of forgetting to
  bump it is exactly the behaviour we would have had without the column.
- Alternatives: hashing the compiled calculation (nothing in Go makes this cheap or stable, and it
  would change on unrelated refactors); a full `parser_versions`-style jsonb (001 left that column
  null for want of a use; one integer is enough until a second calculation exists); recording nothing
  (defensible at this scale, since re-judging every snapshot in a homelab costs seconds, but it
  throws away the ability to tell two verdicts apart in the history the spec just decided to keep).

## R10. Classification when there is no baseline

- Decision: `published`, with a null baseline and an explicit `no_baseline` reason recorded in the
  figures.
- Rationale: FR-004 requires a judgement but does not say which. A first run has nothing to have
  regressed from, and quarantining it would mean every new perimeter starts blocked and every
  operator's first experience of the gate is a false alarm. Recording `no_baseline` keeps the claim
  honest: this is "nothing says it is bad", not "verified good", and a reader can tell the two apart.
- Alternatives: a fourth classification such as `unrated` (changes the closed enum the spec fixes at
  three values, and every downstream consumer would need to learn it); quarantining by default (safe
  in the abstract, unusable in practice).

## R11. Computing the comparison

- Decision: the comparison is one SQL statement, joining the two snapshots' `identity` observations
  and their strong claims and returning one row per baseline device, already carrying its reason. No
  snapshot is loaded into memory as a whole. A judgement runs a few statements around it (describe the
  snapshot, pick the baseline, count what this one reached, write the verdict), each a single query.
- Rationale: both sides are a few hundred rows of `identity` observations at the scale this tool
  targets, both are indexed by `snapshot_id` through partitioning, and the reasoning (match, then
  attribute a reason) is a join and a `GROUP BY`. Keeping it in one statement also makes the
  reproducibility requirement (FR-012) a property of a query rather than of an evaluation order.
- Alternatives: streaming both sides into Go and diffing there (more code for the same answer, and a
  second place where the definition of "reached" lives).

## R12. Concurrency between two engines

- Decision: rely on the partial unique index from R5 plus a short transaction per judgement. A second
  engine that judges the same snapshot at the same time loses on the index and skips the snapshot.
- Rationale: nothing in the constitution forbids running two engines, and the frontier already
  assumes several collectors, so the gate should not be the component that breaks when someone
  scales the engine out. The index makes the safe outcome the default one.
- Alternatives: an advisory lock per snapshot (more machinery for a case the unique index already
  covers); assuming a single engine (an assumption nothing enforces).

## R13. Declaring thresholds in the configuration document

- Decision: one optional key under each entry of `perimeters:` in the configuration document,
  `degraded_at`, a fraction between 0 and 1. Absent means the documented default of 0.9 (published
  only at full coverage, degraded from 0.9, quarantined below). Stored on the `perimeter` row, so a
  judgement reads the threshold of the config version its own snapshot ran under.
- Rationale: the configuration document is already the one place an operator declares a perimeter
  (001, contracts/config.md), and thresholds are a property of a perimeter, not of a run. Storing
  them on the row rather than re-reading the document keeps a judgement reproducible after the
  document changes.
- Alternatives: a separate thresholds table keyed by perimeter name (a second place to declare a
  perimeter); command-line flags on the engine (global, so US3 fails); percentages as integers
  (fractions match how the coverage figure itself is expressed, so one less conversion to get wrong).
- Revised on 2026-09-24, during the lab run: this started as two keys, `degraded_at` and
  `quarantined_below`. Since FR-005 fixes published at full coverage, the pair can only ever describe
  one boundary, and declaring one key without the other left the second on a default that crossed it
  (`degraded_at: 0.5` against a default quarantine at 0.9 quarantined the very coverages the declared
  key called degraded). The second key is gone rather than repaired: it added a way to be wrong and
  no way to say anything new.
