# Feature Specification: Snapshot eviction and pinning

**Feature Branch**: `012-snapshot-eviction`

**Created**: 2026-10-19

**Status**: Draft

**Input**: User description: "darnodo/NetMapper#21. NetLab is up and running; this is snapshot
management, so there should be no action on the lab." Issue #21: there is no way to remove a
snapshot. Every crawl adds one, with its observations, claims, graph and raw output, and nothing
goes away. A crawl run with a broken configuration stays in the history for good, and the only way
out is wiping the whole deployment, tokens included. The issue proposes operator commands to evict,
pin and unpin a snapshot, an evicted snapshot kept as a tombstone, and a read API that reports
evicted snapshots clearly instead of answering empty.

## Decisions taken in this spec

- **Three operator commands: evict, pin, unpin**, run with the operator's access like `token` and
  `decide`. The read API stays read-only and gains no way to evict.
- **An evicted snapshot keeps a tombstone.** Its id, its crawl, when it opened and closed, its
  verdict and the fact that it was evicted stay readable. Everything else that was collected or
  computed for it goes: observations, raw output links, identifier claims, its crawl's task list,
  findings, entities, resolution, graph, L2 domains and parse generations. The verdict is kept
  because it is small, says why the run was probably evicted, and is what later verdicts refer to.
- **Raw output shared with another snapshot is kept.** Raw output is stored once per distinct
  content, so two crawls of an unchanged device share most of it. Eviction deletes only the stored
  outputs the evicted snapshot referenced that no remaining snapshot references. Outputs already
  orphaned before the eviction are left alone: cleaning them up is a separate sweep.
- **No eviction while a crawl is running, in any perimeter.** A crawl that finds an output already
  stored skips storing it again and records it later, so it can rely on an output of the evicted
  snapshot that no row references yet. Refusing while a crawl runs closes that gap without
  coordinating with the collector.
- **Evicting a snapshot that served as a baseline is allowed.** The verdicts already given to later
  snapshots stay as they are, still naming the evicted snapshot as their baseline, and the API shows
  that baseline as evicted. Refusing would block the main use case: a broken crawl that was judged
  becomes the baseline of the next crawl, so it is exactly the snapshot an operator wants gone. A
  snapshot judged again after its baseline was evicted is measured against the closest earlier
  snapshot that is still there.
- **An open snapshot and a pinned snapshot are refused.** Unpin first to evict a pinned one.
  Eviction is also refused while another snapshot of the same perimeter is open or not yet
  resolved, because the registry cleanup cannot know which entries that snapshot will use.
- **An eviction in progress is visible as `evicting`.** The snapshot moves from `closed` to
  `evicting` when its data is removed from the database, and to `evicted` once its stored outputs
  are deleted. An eviction interrupted between the two stays `evicting` until the same command is
  run again, and no crawl starts while any snapshot is `evicting`.
- **Eviction is idempotent and resumable.** Evicting an evicted snapshot succeeds and changes
  nothing. An eviction interrupted halfway is completed by running the same command again.
- **Pinning only protects from eviction.** It does not change what a snapshot contains or how any
  answer is built from it. A closed snapshot stays immutable; pinning is a flag on it, not a change
  to its content.
- **The device registry keeps only what a remaining snapshot uses.** A registry entry is deleted
  when no remaining (non-evicted) snapshot references it, whichever snapshot created it. An entry
  still referenced stays with its key unchanged, its first-seen and last-seen times as they were, and
  its link to the snapshot that first saw it, even if that snapshot is now a tombstone. The
  identifiers attributed to a deleted entry go with it. Operator decisions (merge, split,
  never-merge) are kept: they are replayable records, not registry rows.
- **Evict shows before it deletes.** Without `--yes`, the command only reports what it would
  remove, changes nothing and exits 3; with `--yes`, it evicts. The preview doubles as a dry run
  and a script stays one command.
- **Every eviction, pin and unpin is recorded in the audit trail** with who ran it, when, and on
  which snapshot.
- **A retention policy (keep the last N, or anything younger than X days) is out of scope.** It can
  come later on top of the same eviction.

## Clarifications

### Session 2026-10-19

- Q: When the evicted snapshot was the only one that saw a device, does its registry entry stay?
  -> A: A registry entry is deleted when no remaining (non-evicted) snapshot references it,
  regardless of which snapshot created it.
- Q: Does `evict` run as soon as it gets an id, or ask for a confirmation? -> A: Without `--yes` it
  shows what it would delete and changes nothing; with `--yes` it evicts.
- Q: What does eviction do when another snapshot of the same perimeter is open or closed but not
  yet resolved? -> A: It refuses (non-zero exit, nothing changed) and names that snapshot; retry
  once it is resolved.
- Q: Which stored outputs can eviction delete? -> A: Only those the evicted snapshot referenced and
  no other snapshot uses; older orphans stay for a separate sweep. Eviction is refused while any
  crawl is running, since a crawl can rely on an existing output before recording it.
- Q: What exit code does `evict` return when it only shows the preview? -> A: A distinct non-zero
  code (3, "nothing done, `--yes` needed"), separate from errors and refusals.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Remove a broken crawl from the history (Priority: P1)

An operator ran a crawl with a broken configuration (SNMP refused, so no device has a hostname). The
snapshot sits in the history, shows up in the snapshot list and may be the latest one with a graph.
The operator evicts it by id. Its data is gone, its raw output no longer used by anything is gone
from the object store, the snapshot list shows it as evicted, and the default answer of the read API
comes from the most recent snapshot that is still there.

**Why this priority**: this is the problem the issue describes, and today the only workaround wipes
everything, tokens included.

**Independent Test**: crawl the lab twice, the second time with credentials that fail, evict the
second snapshot, and check the database, the object store and the read API.

**Acceptance Scenarios**:

1. **Given** a closed, unpinned snapshot, **When** the operator evicts it, **Then** the command
   exits 0, the snapshot is listed as `evicted`, and none of its observations, claims, tasks,
   findings, entities, graph or L2 domains remain.
2. **Given** an evicted snapshot whose raw output was partly shared with another snapshot, **When**
   the eviction completes, **Then** every stored output the other snapshot references is still
   readable through the API, and every stored output only the evicted snapshot referenced is gone.
3. **Given** the evicted snapshot was the latest one with a graph, **When** a client asks the read
   API for the graph without naming a snapshot, **Then** the answer comes from the most recent
   remaining snapshot with a graph.
4. **Given** an evicted snapshot, **When** a client names it in a graph, device or fact request,
   **Then** the API answers with an error that says the snapshot was evicted, distinct from "no such
   snapshot" and from "not projected".
5. **Given** an evicted snapshot, **When** the operator evicts it again, **Then** the command exits
   0 and reports that there was nothing left to do.
6. **Given** an eviction interrupted after the database part and before the object store part,
   **When** the operator runs the same command again, **Then** the remaining unreferenced output is
   deleted and the command exits 0.

---

### User Story 2 - Protect a snapshot from eviction (Priority: P2)

An operator keeps a reference snapshot (the one taken before a change window) and does not want it
evicted by mistake, by a later script, or by a future retention policy. The operator pins it. An
eviction of a pinned snapshot is refused until it is unpinned.

**Why this priority**: eviction is irreversible; pinning is the guard for the snapshots that matter.
It is useful only once eviction exists.

**Independent Test**: pin a snapshot, try to evict it, unpin it, evict it.

**Acceptance Scenarios**:

1. **Given** a pinned snapshot, **When** the operator tries to evict it, **Then** the command
   refuses with a message that names the pin, exits non-zero, and nothing is removed.
2. **Given** a pinned snapshot, **When** the operator unpins it and evicts it, **Then** the eviction
   succeeds.
3. **Given** a snapshot already pinned, **When** the operator pins it again, **Then** the command
   exits 0 and nothing changes; the same holds for unpinning a snapshot that is not pinned.
4. **Given** any snapshot, **When** a client lists snapshots or reads one, **Then** the answer says
   whether it is pinned.

---

### User Story 3 - Know what happened to a snapshot (Priority: P3)

Weeks later, someone reading the history sees a gap or a verdict whose baseline no longer has data.
The tombstone and the audit trail say which snapshot was evicted, when, and by whom.

**Why this priority**: an unexplained gap in the history erodes trust in every answer around it.

**Independent Test**: evict a snapshot used as a baseline, then read the later snapshot's verdict,
the evicted snapshot and the audit trail.

**Acceptance Scenarios**:

1. **Given** an evicted snapshot, **When** a client reads it by id, **Then** the answer shows its
   state as `evicted`, when it was evicted, its original open and close times, and its verdict.
2. **Given** a later snapshot whose verdict used the evicted snapshot as baseline, **When** a client
   reads that verdict, **Then** the coverage figure and baseline id are unchanged and the baseline
   is shown as evicted.
3. **Given** evictions, pins and unpins were run, **When** the audit trail is read, **Then** each
   one on an existing snapshot appears with actor, time, action and snapshot id, including refused
   attempts.

### Edge Cases

- **Open snapshot**: a snapshot still collecting is refused, with a message saying so. Cancel the
  crawl first.
- **Unknown id**: refused as invalid input, nothing is recorded as evicted.
- **Engine working on the snapshot**: a closed snapshot may be in the middle of being judged,
  resolved or projected. Eviction and that work must not interleave so that computed rows are
  written for a snapshot already evicted, and the engine skips evicted snapshots from then on.
- **Crawl running when eviction is asked**: refused, whatever the perimeter, naming the running
  crawl. A running crawl may rely on an output of the evicted snapshot it found already stored and
  has not recorded yet.
- **Crawl started while an eviction is unfinished**: refused while any snapshot is `evicting`,
  with a message naming it and the command that finishes the eviction.
- **Outputs orphaned before the eviction** (by an earlier failure): left in the object store,
  neither counted nor deleted.
- **Device seen only by the evicted snapshot**: its registry entry and identifiers are deleted. A
  later crawl that sees the device again gives it a fresh entry.
- **Device first seen by the evicted snapshot, seen again later**: its entry stays, key unchanged.
- **Another snapshot of the same perimeter open or not yet resolved**: it has no entities yet, so
  it cannot say which registry entries it will use. Eviction is refused, names that snapshot, and
  changes nothing; the operator retries once it is resolved. A resolution that starts while an
  eviction runs must not reuse an entry the eviction deletes.
- **Operator decision naming a deleted device key**: the decision stays; replaying it against keys
  that no longer exist behaves as it already does for any unknown key.
- **Evict without `--yes`**: prints the preview, changes nothing, records nothing in the audit
  trail, and exits 3, so a script that forgot `--yes` does not read it as done. A preview of a
  snapshot that would be refused says why and exits as the refusal would. A preview of an already
  evicted snapshot says there is nothing to do and exits 0. A preview of an `evicting` snapshot
  exits 3 and lists the stored outputs still to delete.
- **Evicting the only snapshot with a graph**: allowed; the read API then answers "not projected"
  without a snapshot name, as it does on a fresh install.
- **Observation looked up by id without naming a snapshot**: an observation of an evicted snapshot
  is answered as not found, the same as any id that does not exist.
- **Pinning an evicted snapshot**: refused, there is nothing left to protect.
- **Pinning an open snapshot**: allowed, so a crawl can be protected before it ends.
- **Rerunning `judge` on a snapshot whose baseline was evicted**: it is measured against the closest
  earlier snapshot of the same perimeter that is not evicted, or reports no baseline.
- **Object store unreachable during eviction**: the database part may complete; the snapshot stays
  `evicting`, the command exits non-zero, says the eviction is incomplete, and a rerun finishes it.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The operator MUST be able to evict a snapshot by id with an operator command.
- **FR-002**: Eviction MUST remove everything collected or computed for that snapshot: observations,
  raw output links, identifier claims, its crawl's task list, findings and their evidence, entities
  and their claims, resolution, projection, graph, L2 domains and parse generations.
- **FR-003**: Eviction MUST keep a tombstone: the snapshot's id, its crawl, its open and close
  times, its verdict, its state (`evicting` while stored outputs remain to delete, then `evicted`)
  and the time of eviction.
- **FR-004**: Eviction MUST delete from the object store every stored output the evicted snapshot
  referenced that no remaining snapshot references, and MUST NOT delete any other output, including
  outputs orphaned before the eviction.
- **FR-005**: Eviction MUST refuse an open snapshot, a pinned snapshot, any snapshot whose
  perimeter has another snapshot open or closed but not yet resolved, and any eviction while a
  crawl is running in any perimeter. A refusal MUST exit non-zero, say why (naming the blocking
  snapshot when there is one), and change nothing. A crawl MUST NOT start while any snapshot is
  `evicting`. A crawl and an eviction MUST never run at the same time.
- **FR-006**: Evicting an already evicted snapshot MUST exit 0 and change nothing. Rerunning an
  interrupted eviction MUST complete it.
- **FR-007**: The operator MUST be able to pin and unpin a snapshot by id. Both MUST be idempotent.
  Pinning an evicted snapshot MUST be refused.
- **FR-008**: Every eviction, pin and unpin of an existing snapshot, accepted or refused, MUST be
  recorded in the audit trail with actor, time, action, snapshot id and outcome. Invalid input (an
  unknown id, missing object store settings) and previews are not recorded.
- **FR-009**: The read API MUST list a snapshot being evicted with state `evicting` and an evicted
  one with state `evicted`, and MUST show for every snapshot whether it is pinned and when it was
  evicted.
- **FR-010**: A graph, device, fact or observation request naming an `evicting` or `evicted`
  snapshot MUST get an error that says the snapshot was evicted, distinct from "no such snapshot"
  and "not projected". Reading the evicted snapshot itself MUST return its tombstone.
- **FR-011**: The default snapshot of the read API, the baseline chosen by the coverage gate, and
  the snapshots the engine picks up for judging, resolving and projecting MUST skip evicted
  snapshots.
- **FR-012**: Eviction MUST delete every device registry entry, with its attributed identifiers,
  that no remaining (non-evicted) snapshot references, whichever snapshot created it. Entries still
  referenced MUST keep their key and history unchanged.
- **FR-013**: Verdicts of other snapshots that name the evicted snapshot as baseline MUST stay
  unchanged.
- **FR-014**: The evict command MUST change nothing unless given `--yes`. Without it, it MUST show
  the snapshot (id, close time, verdict, pinned or not) and what eviction would remove, as listed in
  FR-015, and exit with code 3, distinct from success (0), runtime error (1) and invalid input or
  refusal (2, as `netmapper cancel` uses for a job that is not running). On a snapshot already
  `evicting`, the preview also exits 3 and lists the stored outputs still to delete.
- **FR-015**: The evict command MUST report what it removed, or would remove in a preview: the
  number of observations, the number and total size of stored outputs deleted, the number of stored
  outputs kept because another snapshot shares them, and the number of registry entries deleted.
- **FR-016**: The API role MUST gain no ability to delete or modify anything; eviction is available
  only to the operator.
- **FR-017**: The README, the deploy guide and the data model document MUST describe the new
  commands, the `evicting` and `evicted` states, the pin flag, and the object store key the operator
  needs, in the same change. The deploy guide MUST make a dedicated operator key the default; the
  collector's key MUST NOT gain any right for this feature.

### Key Entities

- **Snapshot**: one crawl's collected data. Gains the `evicting` and `evicted` states, a pinned
  flag and the time it was evicted. An evicted snapshot is a tombstone: it keeps its identity and
  verdict, nothing else. States: `open`, `closed`, `evicting`, `evicted`; pinning is possible on
  `open` and `closed` only.
- **Stored output**: raw command output kept once per distinct content and shared by every
  observation that produced the same bytes. Deleted only when no remaining snapshot references it.
- **Verdict**: the coverage gate's judgement of a snapshot, naming its baseline. Kept on eviction,
  both for the evicted snapshot and for the snapshots that used it as baseline.
- **Device registry entry**: a device's stable key within a perimeter, with when it was first and
  last seen and the snapshot that first saw it. Spans snapshots. Deleted on eviction once no
  remaining snapshot references it.
- **Audit entry**: who evicted, pinned or unpinned which snapshot, when, and with what outcome.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After evicting a snapshot, a count of rows tied to its id across every per-snapshot
  table returns 0, and its tombstone remains readable.
- **SC-002**: After evicting one of two snapshots of the same lab, 100% of the stored outputs the
  remaining snapshot references are readable, and 0 of the evicted snapshot's outputs remain that no
  snapshot references.
- **SC-003**: An operator removes a broken crawl with one command (after an optional preview),
  without touching the database by hand and without losing tokens, configuration or other
  snapshots.
- **SC-004**: Running the same eviction twice, or rerunning an interrupted one, leaves the same end
  state as a single uninterrupted run.
- **SC-005**: 100% of refused evictions (open, pinned, unknown id, perimeter with an unresolved
  snapshot, crawl running) leave the database and the object store unchanged.
- **SC-006**: Every request naming an evicted snapshot gets the evicted error, never an empty result.
- **SC-007**: After any eviction, 0 registry entries remain that no remaining snapshot references,
  and every key a remaining snapshot uses is unchanged.

## Assumptions

- Testing needs no change to the lab and no device action beyond ordinary read-only crawls: the
  snapshots to evict are produced by normal crawls of `test/lab` on NetLab, and the broken crawl by
  running one with credentials that fail. Most checks can run on recorded fixtures without the lab.
- Operator commands today reach only the database. Deleting stored output needs the operator to
  have its own object store key that can delete; the collector's key and the API's read-only key
  are unchanged (Principle III).
- The current schema has neither the `evicting` and `evicted` states nor the pin flag. The data
  model document lists `evicted` and the pin flag but not `evicting`, and puts the verdict in the
  state although it lives beside the snapshot. Bringing the schema and the document in line is part
  of this feature.
- Grafana dashboards reading the database directly may show evicted snapshots; adapting them is out
  of scope.
- Retention policies and automatic eviction by the engine are out of scope, and so is a sweep of
  stored outputs orphaned outside eviction.
- The constitution already allows deleting observations through eviction (Principle II); this
  feature is that eviction. A closed snapshot's content stays immutable until it is evicted.
