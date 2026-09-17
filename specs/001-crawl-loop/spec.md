# Feature Specification: Crawl loop (find + scrape)

**Feature Branch**: `001-crawl-loop`

**Created**: 2026-09-17

**Status**: Draft

**Input**: User description: "Crawl loop (find + scrape) - the collector's core slice: perimeter check, fingerprint a seed, queue neighbours through the frontier, run recipes, write observations and raw output."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Discover a network from a seed (Priority: P1)

A network engineer declares a perimeter, a credential set and one seed device, then triggers a run.
The run logs into the seed, identifies it, reads its neighbour table, and repeats on every neighbour
that falls inside the perimeter until there is nothing left to visit. When the run ends, the engineer
has a snapshot listing every device that was reached, what was collected from each, and for each one
that failed, why.

**Why this priority**: Nothing else in the product exists without it. Identity resolution, the graph,
diffs and reconciliation all read what this loop writes.

**Independent Test**: Point a run at a lab with two connected switches and a seed. It delivers value
on its own: the snapshot answers "what is on this network and what did each device tell us", which no
other part of the system is needed to read.

**Acceptance Scenarios**:

1. **Given** a perimeter, a credential set and one reachable seed inside it, **When** the engineer
   triggers a run, **Then** the run reaches the seed, reaches every neighbour inside the perimeter
   that the seed and its neighbours reported, and leaves behind a closed snapshot, awaiting the
   coverage judgement, containing one observation per fact family attempted per device.
2. **Given** a device reachable through two management addresses, **When** both are queued, **Then**
   it is identified once and collected once.
3. **Given** a neighbour reported outside the perimeter, **When** the crawl reaches that point,
   **Then** no packet is sent to it and the run records that the target was skipped as out of
   perimeter.
4. **Given** a run that completes, **When** the engineer inspects any collected fact, **Then** it
   carries the device it came from, the command or OID that produced it, when it was collected, and
   the raw output it was parsed from.

---

### User Story 2 - Account for what could not be collected (Priority: P2)

The engineer opens a finished run and sees, per device, which fact families came back, which came
back empty, which are unsupported on that platform, which failed to parse, and which devices refused
every credential or never answered. No silence is left unexplained.

**Why this priority**: A crawl that reports only its successes is unusable for the product's main
question. A device missing from the graph must be distinguishable from a device that was there and
refused to talk.

**Independent Test**: Run against a lab containing one unreachable address, one device with wrong
credentials, and one device on an unknown platform. Every one of the three appears in the result
with a distinct, correct status.

**Acceptance Scenarios**:

1. **Given** a target inside the perimeter that does not answer, **When** the run ends, **Then** the
   snapshot records it as unreachable rather than omitting it.
2. **Given** a device that answers but rejects every credential set covering its perimeter, **When**
   the run ends, **Then** it is recorded as denied, distinct from unreachable, and a compliance
   finding cites the evidence that it answered.
3. **Given** a device whose platform matches no loaded pack, **When** the run ends, **Then** it is
   recorded as an unidentified platform and raised as a finding, and the crawl continues elsewhere.
4. **Given** a command whose output no longer matches its template, **When** the run ends, **Then**
   the fact family is recorded as a parse failure, the raw output is still stored, and a finding is
   raised.

---

### User Story 3 - Survive an interruption (Priority: P2)

A run is under way when the collector is restarted. The engineer restarts it and the run continues
from where it stopped, without revisiting devices already done and without losing what was already
collected.

**Why this priority**: A discovery over a real network takes long enough that a restart during a run
is normal rather than exceptional. Without this, every interruption costs a full re-crawl of the
building.

**Independent Test**: Start a crawl over a lab, kill the collector mid-run, restart it, and confirm
the run finishes with the same result as an uninterrupted run, with no device visited twice.

**Acceptance Scenarios**:

1. **Given** a run in progress, **When** the collector process stops and restarts, **Then** the run
   resumes and completes, and every device is collected exactly once.
2. **Given** a device whose collection was interrupted, **When** the run resumes, **Then** that
   device is retried, up to a bounded number of attempts, after which it is recorded as failed with
   its last error.
3. **Given** two collectors running against the same run, **When** both pull work, **Then** no
   device is handled by both.

---

### Edge Cases

- A device reports itself as its own neighbour, or two devices report each other. The crawl must not
  loop.
- A neighbour is reported by name only, with no reachable address. It must be recorded as known but
  not collected, rather than dropped or invented.
- A device answers the identification step but times out during collection. The identification is
  kept; the collection is recorded as failed.
- A device answers only one of the two supported transports. The other transport must not be
  reported as a device failure.
- A device returns a very large table. The run must not be prevented from finishing by one device.
- A target appears in the queue both as a seed and as a neighbour of something else.
- The perimeter is empty or the seed falls outside it. The run must refuse to start and say why.
- A credential set resolves to no value at collection time. The next covering set is tried, and the
  run does not abort. A device left with no set that resolves is recorded as denied.
- A credential set is nearly out of attempts on a device that is still refusing it. The remaining
  sets are tried, and no set exceeds its own configured attempt budget on that device.
- Two devices are reported under the same name in different parts of the network. Both are collected
  and both sets of identifiers are recorded, and the disambiguation is left to resolution.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A run MUST refuse to start without a perimeter, at least one seed inside it, and at
  least one credential set covering that perimeter, and MUST state which of these was missing.
- **FR-002**: Every target MUST be checked against the active perimeter before any packet is sent to
  it, including targets learned from a neighbour table.
- **FR-003**: The system MUST identify a device before collecting from it, recording the platform and
  the strong identifiers the device reported.
- **FR-004**: The system MUST record identifiers at identification time and use them to ensure a
  device reachable through several addresses is identified and collected once per run.
- **FR-005**: The system MUST queue a device's neighbours as soon as it is identified, and MUST
  queue the collection work for that same device at the same moment, as separate work that runs
  independently. Identification of the topology MUST NOT wait on how long any one device takes to
  collect.
- **FR-006**: The system MUST select what to collect from a device according to its platform and OS
  version, drawn only from loaded platform packs.
- **FR-007**: The system MUST store the output of every command exactly as the device returned it,
  and MUST store identical output once regardless of how many devices produced it.
- **FR-008**: Every stored fact MUST reference the device, the command or OID, the collection time,
  and the stored raw output it was parsed from.
- **FR-009**: Every fact family the collector attempted against a device MUST carry exactly one
  outcome from: collected, empty, unsupported, parse failed, unreachable, denied. The outcome MUST
  never be absent. A task that ended before it could record an outcome, because the collector itself
  failed, records its failure on the task under FR-014 and writes no observation: there is
  nothing it learned about the device to state.
- **FR-010**: Collected facts MUST be written and never modified afterwards.
- **FR-011**: A re-parse of stored output MUST be possible later without contacting any device. This
  feature must store everything such a re-parse would need.
- **FR-012**: Work remaining in a run MUST survive a collector restart, and a device MUST NOT be
  collected twice because of one.
- **FR-013**: Several collectors MUST be able to work on one run without handling the same target
  twice.
- **FR-014**: A target that fails MUST be retried a bounded number of times before being recorded as
  failed with its last error.
- **FR-015**: A device that cannot be identified and a command whose output no longer parses MUST
  each raise a data quality finding, and neither MUST stop the run.
- **FR-016**: A run MUST end in bounded time: the queue empties, a final retry pass runs over the
  targets that failed, and the snapshot leaves its open state, even if some targets never answered.
- **FR-017**: Credentials MUST be resolved from a reference at collection time and MUST NOT be stored
  or returned anywhere.
- **FR-018**: Several credential sets MAY cover one perimeter. They MUST be tried against a device
  in a defined, repeatable order.
- **FR-019**: Each credential set MUST have its own bounded number of attempts per device,
  configurable per set rather than shared by all of them, because consecutive failures against a
  centralised authentication service can lock the account being used.
- **FR-020**: A device that answered its transport and rejected every covering credential set MUST be
  recorded as denied.
- **FR-021**: A denied device MUST raise a finding in a compliance domain, distinct from the data
  quality findings raised by parse failures and unidentified platforms. A device reachable inside the
  perimeter but outside the declared authentication regime is a governance observation, not only a
  collection failure.
- **FR-022**: That finding MUST cite the evidence that the device answered, so that denied stays
  distinguishable from unreachable.
- **FR-023**: Every command sent to a device MUST be recorded in the audit trail with its target and
  its result.
- **FR-024**: Only read-only commands MUST be sent. No device configuration is changed.
- **FR-025**: A run MUST be cancellable, and a cancelled run MUST keep what it had already collected.

### Key Entities

- **Perimeter**: the address ranges a run is allowed to touch, as include and exclude ranges.
  Nothing is contacted outside it.
- **Seed**: a device a crawl starts from.
- **Run**: one execution of a discovery, with a state, a start, an end, and counters.
- **Task**: one unit of work in a run, either identifying a target or collecting from it. Carries its
  target, its state, its owner while claimed, and its attempt count.
- **Snapshot**: everything one run collected. It leaves its open state once the run's task queue is
  exhausted, and is not modified afterwards.
- **Observation**: one statement from one source about one device at one moment, with its outcome and
  its link to the raw output it came from.
- **Raw output**: the bytes a device returned, stored once per distinct content.
- **Identifier claim**: an identifier a device reported about itself, recorded at identification time,
  before any device entity exists.
- **Fact family**: a named unit of meaning such as the neighbour table or the MAC table, with a
  stable shape independent of platform.
- **Platform pack**: the data describing how to recognise a platform and what to run on it.
- **Finding**: something worth reporting from the run, carrying a domain and its evidence. Data
  quality covers an unidentified platform or a template that stopped matching; compliance covers a
  device that answered inside the perimeter and refused every credential set.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Starting from one seed, a run reaches every device that is inside the perimeter,
  reachable, and reported by something the crawl already reached, and collects none of them more
  than once.
- **SC-002**: No device outside the declared perimeter receives a single packet during any run.
- **SC-003**: Every device the run attempted appears in the result with an outcome, so an engineer
  can account for 100% of attempted targets without consulting logs.
- **SC-004**: An interruption and restart during a run costs no collected data and no repeated device
  visit, and the run still finishes.
- **SC-005**: Every collected fact can be traced to the exact device output it came from, and that
  output is retrievable after the run.
- **SC-006**: An engineer can run a discovery on a new platform by adding pack data alone, with no
  change to the crawl itself.
- **SC-007**: No credential value is retrievable from any stored record or any interface after a run.

## Assumptions

- Neighbour discovery relies on what devices report about their directly connected neighbours. Which
  neighbour protocols are read is a property of platform packs, not of this feature.
- Identification and collection are two separate kinds of work on one queue, so that a device can be
  identified and its neighbours queued before it is fully collected.
- Unauthenticated probing of endpoints found in forwarding or address tables is out of scope here.
  This feature discovers devices that can be logged into; qualifying leaf endpoints is separate work.
- A run collects a fixed set of fact families per platform, taken from the packs. Choosing a subset
  per run is out of scope for this feature.
- Credential sets are declared as covering a perimeter, not as belonging to a device. Which set
  opens a given device is discovered by trying them in order, and the order is part of the
  configuration rather than something this feature invents.
- Scheduling a run, the rule that only one run is active per perimeter at a time, and reading a run's
  progress while it is in flight are separate work. They belong with the scheduler and the job
  runner, not with the crawl.
- Devices that never answer are expected to be a normal fraction of a real perimeter, so a run's
  success is not defined as reaching everything.
- Turning collected facts into entities, a graph, or a diff is out of scope. This feature ends when
  the snapshot closes.
- Judging the result is out of scope with it. Measuring coverage and deciding whether a closed
  snapshot is published, degraded or quarantined is the next feature's work. This one hands over a
  snapshot that is closed and nothing more.
- Raw output is retained at least as long as its snapshot. Retention and eviction policy is separate
  work.
- A seed is given as an address or hostname that resolves to one inside the perimeter.
