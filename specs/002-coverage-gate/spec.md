# Feature Specification: Coverage gate (judge a closed snapshot)

**Feature Branch**: `002-coverage-gate`

**Created**: 2026-09-24

**Status**: Draft

**Input**: User description: "The next feature after the crawl loop (001), as spec.md's own Assumptions section names it: 'Measuring coverage and deciding whether a closed snapshot is published, degraded or quarantined is the next feature's work.' The crawl loop hands over a snapshot that is closed and nothing more; this feature judges it."

## Clarifications

### Session 2026-09-24

- Q: When a snapshot is judged a second time (for example after a fix to the coverage calculation), does the new verdict replace the old one or sit beside it? → A: Judgements stack, one active at a time. Earlier judgements stay readable but inactive, the same way `parse_generation` already works in 001.
- Q: Who triggers a judgement, and who triggers a re-judgement? → A: The engine judges every snapshot automatically as it closes; a re-judge is an explicit operator action on a named snapshot, never something the engine decides on its own.
- Q: If the engine stops or fails between a snapshot closing and its judgement being written, how does that snapshot still get judged? → A: The engine sweeps for closed snapshots with no active judgement and judges them, so any interruption is picked up on the next pass with no dedicated recovery path.
- Q: Which predecessor is a snapshot compared against: the one closed immediately before it, or the last one judged at the moment the calculation runs? → A: The one closed immediately before it, among those carrying an active judgement. A purely time-ordered rule, so a re-judge of an old snapshot finds the same baseline it originally had.
- Q: What default thresholds separate published, degraded and quarantined for a perimeter that declares none? → A: published when every baseline device is reached again, degraded at 90% or more, quarantined below that. Strict by default, loosened per perimeter where the noise justifies it. (Implementation note, 2026-09-24: since published is fixed at full coverage, this needs one declared figure, not two. A draft with a second key was removed after the lab showed the pair could be declared crossed.)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Trust a snapshot before acting on it (Priority: P1)

The moment a run's snapshot closes, nobody yet knows whether it is trustworthy enough to read. A run
where a broken credential rollout left two-thirds of a perimeter unreachable must not be treated the
same as a clean run, or a device that simply vanished from discovery reads as "gone" instead of "we
failed to reach it this time." The system compares the closed snapshot against the perimeter's
immediately preceding snapshot and classifies it as published, degraded or quarantined, so every later
consumer of that snapshot starts from a verdict instead of guessing.

**Why this priority**: The crawl loop (001) already produces closed snapshots; without this, every
consumer (graph, diff, an engineer reading the run) must independently guess whether a snapshot is
reliable, and a coverage collapse silently reads as network change instead of collection failure. This
is the one piece missing before anything downstream can trust a snapshot at all.

**Independent Test**: Close two snapshots of the same fake-transport perimeter, the second missing a
device the first reached and never reporting it as unreachable or denied (it was never attempted).
Judge the second snapshot and confirm it is not published, without touching the crawl loop or any
downstream comparison code.

**Acceptance Scenarios**:

1. **Given** a closed snapshot that reaches every device its baseline reached, **When** the gate
   judges it, **Then** it is classified published.
2. **Given** a perimeter on default thresholds whose baseline reached ten devices, and a closed
   snapshot that reaches nine of them, **When** the gate judges it, **Then** it is classified
   degraded.
3. **Given** the same perimeter and a closed snapshot that reaches eight of those ten devices,
   **When** the gate judges it, **Then** it is classified quarantined.
4. **Given** a snapshot already judged, **When** the gate is asked to judge it again with the same
   baseline and thresholds, **Then** the new active judgement carries the same classification and
   figures as the one it supersedes, and the superseded one is still readable.
5. **Given** a perimeter with no prior judged snapshot, **When** its first snapshot is judged, **Then**
   the judgement is computed from that snapshot's own counts alone and states that no baseline existed.
6. **Given** a snapshot that closed while the judging side was stopped, **When** the judging side runs
   again, **Then** that snapshot is judged without anyone asking for it, and it ends with exactly one
   active judgement.

---

### User Story 2 - Understand the gap, not just the verdict (Priority: P2)

An engineer opens a degraded or quarantined snapshot and, without reading logs or raw observations,
sees the figures behind the verdict: how many devices the baseline expected, how many this snapshot
reached, and which outcome category (unreachable, denied, unsupported, parse failed, or simply never
attempted) explains the rest.

**Why this priority**: A verdict without a reason is as unusable as the successes-only report the
crawl loop already refuses to produce (001, FR-015). An operator must be able to act: fix a credential,
investigate connectivity, or extend a perimeter, not just be told "degraded."

**Independent Test**: Judge two snapshots that both land in the degraded range for different reasons
(one from a rise in unreachable devices, one from a rise in denied devices) and confirm each
judgement's recorded breakdown names its own dominant cause, not a generic label.

**Acceptance Scenarios**:

1. **Given** a degraded or quarantined snapshot, **When** the engineer inspects its judgement,
   **Then** it states the coverage figure, the baseline it was measured against, and a per-outcome
   breakdown of the devices that did not carry over from that baseline.
2. **Given** a device present in the baseline but absent from the newer snapshot with no matching
   task at all (never attempted, not merely failed), **When** the engineer inspects the judgement,
   **Then** that device is named under a distinct "not attempted" reason, separate from unreachable
   or denied.

---

### User Story 3 - Configure how strict a perimeter's gate is (Priority: P3)

An operator declares, per perimeter, the coverage at or above which a snapshot counts as degraded
rather than quarantined, because a home-lab perimeter and a compliance-sensitive perimeter do not
deserve the same tolerance for missing devices.

**Why this priority**: Without a per-perimeter threshold, one tolerance must fit every perimeter the
tool ever watches, which either quarantines a noisy lab constantly or lets a critical perimeter's real
regressions through as merely degraded.

**Independent Test**: Judge the same coverage figure under two perimeters with different configured
thresholds and confirm it lands in a different classification for each.

**Acceptance Scenarios**:

1. **Given** a perimeter with no declared threshold, **When** its first snapshot is judged,
   **Then** the documented default applies and the judgement states that the default was used.
2. **Given** a perimeter whose threshold is changed, **When** its next snapshot closes,
   **Then** it is judged under the new value, and prior judgements are unchanged.

---

### Edge Cases

- A perimeter has no prior judged snapshot (first run ever, or every prior snapshot was itself judged
  from no baseline in a chain). The judgement must still be produced, from that snapshot's own counts,
  stating plainly that there was nothing to compare against, rather than defaulting to published or
  refusing to judge.
- A perimeter's `include`/`exclude` ranges are deliberately narrowed between two runs. The baseline
  device set must be filtered to the current perimeter before comparison, or a legitimate scope
  reduction reads as a coverage collapse.
- The baseline snapshot's device set is empty (nothing was ever reached, or the perimeter is new and
  narrow). A judgement must not report vacuous full coverage from an empty baseline; an empty baseline
  is itself the "no baseline" case.
- The current snapshot reaches zero devices in a perimeter that previously reached many. Coverage is
  zero and the snapshot is quarantined; the judgement must not divide by zero or crash on an empty
  numerator.
- A single device dominates the perimeter's expected set (for example, one seed with no reachable
  neighbours). A judgement must still be produced; a one-device perimeter losing its one device is a
  real coverage collapse, not a rounding error to be smoothed over.
- A snapshot is quarantined, and a later, better snapshot of the same perimeter closes. The earlier
  quarantined snapshot is not re-judged on its own; a better successor is not evidence about the
  earlier run. Only an explicit re-judge changes which verdict is active, and it never erases the one
  it supersedes.
- The gate is asked to judge a snapshot that has not closed yet. It must refuse: judging before the
  run finishes would compare an incomplete result against a complete baseline.
- Two snapshots of the same perimeter close close enough together that the second's baseline lookup
  could race with the first's own judgement being written. Closing order decides the baseline
  (FR-015), so the second must either wait for its predecessor's judgement or be picked up by the
  sweep afterwards; it must never silently skip a predecessor and compare against an older run.
- The judging side stops between a snapshot closing and its judgement being written, or is down
  entirely while several snapshots close. Each of them must end up judged once it comes back, and a
  snapshot interrupted mid-judgement must not end up with two active judgements or a half-written one.
- A loss is left unfixed and the perimeter runs again. Because a snapshot is measured against the run
  before it, the second run compares the reduced device set against itself and is published: the
  gate reports a change, not a standing state, so it says a device went missing once and then stops
  saying it. An operator who ignores a quarantined verdict is not told again by the next run.
- The gate is switched on for the first time on a database that already holds closed snapshots. Every
  one of them is judged, oldest first, since none carries a verdict yet: history is judged in one
  pass rather than left in a state no consumer can read.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST judge every snapshot on its own, without an operator asking, once that
  snapshot reaches the `closed` state, and MUST refuse to judge a snapshot that is still `open`. A
  snapshot MUST carry exactly one active judgement at any moment.
- **FR-002**: Every judgement MUST classify its snapshot as exactly one of: published, degraded,
  quarantined.
- **FR-003**: Coverage MUST be computed as the fraction of the devices reached by the snapshot's
  baseline that are reached again by the snapshot being judged. A device counts as reached when it has
  a `collected` `identity` observation in the respective snapshot.
- **FR-004**: When a perimeter has no prior judged snapshot, or that snapshot's own reached-device set
  is empty, the system MUST still produce a judgement from the current snapshot's own counts and MUST
  record that no usable baseline existed, rather than computing a ratio against nothing.
- **FR-005**: Each perimeter MUST be able to declare the coverage at or above which a snapshot is
  degraded rather than quarantined. Published MUST require every device of the baseline to be reached
  again, whatever that figure is, so one threshold per perimeter is the whole of the tuning. A
  perimeter that declares none MUST be judged degraded at 90% or above and quarantined below. The
  judgement MUST record the threshold applied and whether it was declared or defaulted.
- **FR-006**: Every judgement MUST record the figures behind its classification: the baseline
  snapshot it was compared against (or that none existed), the coverage fraction, and a breakdown of
  every baseline device not carried over, by reason: unreachable, denied, unsupported, parse failed,
  or not attempted at all in the newer snapshot.
- **FR-007**: A judgement MUST be reproducible from the snapshot's own observations and its baseline's
  observations alone, with no device contacted again, consistent with the computed zone being
  rebuildable rather than collected.
- **FR-008**: Judging a snapshot MUST NOT modify that snapshot, its observations, or the baseline
  snapshot it is compared against; a judgement is a separate, append-only record referencing both.
- **FR-009**: A judgement, once written, MUST NOT be modified or deleted. Judging a snapshot again
  MUST write a new judgement that becomes the active one and leave every earlier judgement of that
  snapshot readable and marked inactive, so a corrected calculation can be applied to an old snapshot
  without a re-crawl and without erasing what was previously reported.
- **FR-010**: The system MUST let an engineer retrieve, for any judged snapshot, its classification,
  the figures behind it (FR-006), and when it was computed.
- **FR-011**: A quarantined snapshot MUST remain fully readable like any other closed snapshot; this
  feature MUST NOT delete, hide, or lock it. It carries its classification for any later feature to
  read before deciding whether to treat it as current.
- **FR-012**: A re-judge over the same snapshot, the same baseline, the same threshold and the same
  version of the calculation MUST produce the same classification and figures as the judgement it
  supersedes (FR-007). A re-judge after the calculation itself was corrected is expected to differ,
  and is the reason FR-009 keeps the superseded verdict readable: the two rows together are what says
  what changed and why.
- **FR-013**: A re-judge MUST happen only when an operator asks for it on a named snapshot. The
  system MUST NOT re-judge a snapshot on its own, whatever changed since: not a threshold change, not
  a newer judgement of another snapshot, not a corrected calculation.
- **FR-014**: The system MUST find closed snapshots that carry no active judgement and judge them,
  so that an interruption between a snapshot closing and its judgement being written repairs itself,
  and a snapshot that closed while the judging side was down is judged once it comes back.
- **FR-015**: A snapshot's baseline MUST be the snapshot of the same perimeter closed immediately
  before it that carries an active judgement. The baseline MUST be selected by closing order alone,
  never by which snapshot was judged most recently, so that judging a snapshot today and re-judging it
  after other runs have closed both resolve to the same baseline.

### Key Entities

- **Coverage judgement**: one verdict about one closed snapshot, carrying its classification
  (published, degraded, quarantined), the baseline snapshot it was measured against (if any), the
  coverage fraction, the thresholds applied, the per-reason breakdown of what did not carry over, when
  it was computed, and whether it is the snapshot's active verdict. Append only, like the snapshot it
  judges: a snapshot may accumulate several judgements over time, exactly one of them active.
- **Coverage threshold**: the coverage at or above which a perimeter's snapshot is degraded rather
  than quarantined, as that perimeter declares it, or the system default when it declares none.
  Scoped to a perimeter, the same way a credential set's attempt budget already is (001).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every snapshot that closes carries exactly one active coverage judgement; none is left
  unjudged.
- **SC-002**: An engineer can state why a snapshot was not published using only its judgement, without
  reading raw observations or logs.
- **SC-003**: Re-computing a judgement from the same closed snapshot and the same baseline always
  yields the same classification and the same figures.
- **SC-004**: A quarantined snapshot is distinguishable from a published or degraded one by its
  classification alone, before any later feature reads its observations.
- **SC-005**: An operator can change a perimeter's threshold and see the next snapshot of that
  perimeter judged under the new value, with no earlier judgement changed.
- **SC-006**: A coverage collapse caused by devices that were never attempted (not merely marked
  unreachable or denied) is caught by the judgement, since that is the gap this feature exists to
  close.

## Assumptions

- This feature judges one snapshot at a time against its perimeter's own history. It does not build a
  graph, compute a structural diff, or reconcile with intent; those read a judgement, they are not
  produced by one (docs/c4-model/00-overview.md: that is the engine's later work).
- "Reached" for coverage purposes means a `collected` `identity` observation exists for that device in
  the snapshot. Whether every fact family the pack defines also came back cleanly is a per-family data
  quality question (001's `data_quality` findings), not a whole-snapshot coverage question.
- A quarantined snapshot is a label, not a lock: this feature records the classification and stops
  there. Any future feature that reads snapshots for a graph, a diff, or an API response is
  responsible for checking the judgement before treating a snapshot as current; enforcing that check
  is not part of this slice.
- A snapshot's baseline is its perimeter's immediately preceding closed snapshot (FR-015), whatever
  that snapshot's own classification was; a degraded or quarantined snapshot still serves as the next
  run's baseline, since the alternative (skipping it) would let a slow, multi-run regression hide
  between judged pairs that each look like a small step down.
- Presenting a judgement in any interface (API, UI, alert, notification) is out of scope; this feature
  only computes and stores it in a form a later interface can read.
