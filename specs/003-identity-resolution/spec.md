# Feature Specification: Identity resolution (claims into device entities)

**Feature Branch**: `003-identity-resolution`

**Created**: 2026-09-24

**Status**: Draft

**Input**: User description: "The next feature after the coverage gate (002). The crawl loop (001) writes
identifier claims while it walks the network, and the gate judges whether the snapshot as a whole is
worth reading. Neither of them ever says which claims belong to the same device. This feature turns a
closed snapshot's identifier claims into device entities, by replay, with operator decisions (merge,
split, never-merge) stored as replayable records rather than applied in place (docs/c4-model/03-components.md:
'the identity resolver turns claims into entities, merges on strong ones, quarantines conflicts, applies splits')."

## Clarifications

### Session 2026-09-24

- Q: Does a device entity live inside one snapshot, or does it carry an identity from one run to the
  next? → A: A row per snapshot, plus a device key computed from the entity's strong identifiers that
  is stable across snapshots. The diff, intent matching and the operator decisions all name that key,
  so no later consumer re-derives the grouping this feature already did.
- Q: Which closed snapshots get resolved: every one of them, or only those the coverage gate found
  usable? → A: Every closed snapshot, whatever the gate classified it as. The two components stay
  independent, and a quarantined snapshot stays as readable as any other; deciding whether to trust it
  is the consumer's job, as 002 already settled.
- Q: Does this feature produce interfaces as well as devices? → A: Device entities only. Interfaces
  come from the interfaces fact family and the packs' naming rules rather than from identifier claims,
  and they belong with the graph projector that attaches edges to them.
- Q: Is a device key unique within one perimeter, or shared across every perimeter? → A: Unique within a
  perimeter, scoped to the perimeter name. Two perimeters never combine a device, even on an identical
  strong identifier, so a lab clone carrying a factory serial cannot reach into another perimeter's
  history. A chassis reached by two perimeters carries one key in each.
- Q: When a snapshot is resolved a second time, what becomes of the identity collision findings the
  previous resolution raised? → A: They are replaced with the entity set. A re-resolution removes the
  collision findings its predecessor raised for that snapshot and raises those that still hold, so what
  an engineer reads always describes the current set and a conflict settled by a decision stops being
  reported.
- Q: For a device with no strong identifier, is the key built from its hostname or from the address it
  answered on? → A: From the address. A hostname is not unique (two chassis can carry the same one, which
  the spec keeps as two entities), so a hostname key would collide and need an address tie-break anyway.
  A renumbered weak device therefore reads as a new device, which the weak marker is there to explain.
- Q: When two observations grouped into one entity disagree on a weak attribute, such as a short hostname
  against an FQDN, which value does the entity carry? → A: The winning observation's, the one not marked
  duplicate, which is already how 001 and 002 name the device itself. Deterministic, and every other value
  stays readable through the entity's claims.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - One device, one entity, however many ways it was reached (Priority: P1)

A crawl reaches a switch at its management address, and reaches it again through a neighbour that
advertises its loopback. The run stores two sets of identifier claims that happen to describe the same
chassis. Nothing in the snapshot says so: `identifier_claim` has no entity, and the crawl's live
deduplication only catches the case where one task's strong identifier was already held by another.
This feature reads a closed snapshot's claims and groups them, so that from here on a device is a
single subject with a single set of evidence, whatever route reached it.

**Why this priority**: Every later piece of the engine (the graph, the diff, reconciliation with
intent) attaches to a device, not to a claim. Without a resolved device there is nothing for an edge
to connect, nothing for a diff to compare and nothing for an intent record to match. This is the first
computed zone row the product needs, and no consumer can be built before it exists.

**Independent Test**: Close a fake-transport snapshot in which one device is reached through two
addresses and a chain of claims links them only transitively (one claim set carries a serial, one
carries a chassis MAC, one carries both). Resolve the snapshot and confirm the three claim sets land on
one entity carrying all of them, with no crawl, no graph and no diff involved.

**Acceptance Scenarios**:

1. **Given** a closed snapshot where two claim sets share one strong identifier of the same kind and
   value, **When** the snapshot is resolved, **Then** both belong to one entity, and that entity names
   both sets of claims as its evidence.
2. **Given** a closed snapshot where claim set A shares a serial with C, and C shares a chassis MAC
   with B, while A and B share nothing, **When** the snapshot is resolved, **Then** A, B and C are one
   entity, because the grouping follows the chain and not only the direct pair.
3. **Given** a device the crawl already ended as a duplicate of another task, **When** the snapshot is
   resolved, **Then** its observation hangs off the same entity as the task it duplicated, and the
   address it answered on is readable from that entity.
4. **Given** two claim sets that share only a weak identifier (the same hostname on two different
   chassis), **When** the snapshot is resolved, **Then** they remain two entities.
5. **Given** a device whose platform pack declares no strong identifier and which returned only a
   hostname, **When** the snapshot is resolved, **Then** it still becomes an entity, marked as weakly
   identified, rather than being dropped from the computed zone.
6. **Given** a closed snapshot with no identifier claims at all, **When** it is resolved, **Then** the
   resolution completes with zero entities and is recorded as done, not as a failure.
7. **Given** two consecutive snapshots of one perimeter that both reached the same device, **When** both
   are resolved, **Then** the two entities carry the same device key, so a later consumer can follow the
   device from one run to the next without regrouping its claims.

---

### User Story 2 - A contradicted identity is reported, never guessed (Priority: P2)

Two devices come back with the same serial, because a chassis was replaced without clearing the
inventory, or because two lab VMs were cloned from the same image. Merging them would produce one
entity with two hostnames, two management addresses and a graph that draws links through a device that
does not exist. Refusing to say anything would hide the problem. The system keeps them apart and raises
a finding that names both subjects and the identifier they collide on.

**Why this priority**: A wrong merge is worse than no merge, because it corrupts every edge and every
diff that follows it, and it is invisible in the result. The engineer needs the collision surfaced
where the other data quality problems already appear (001's findings), while still getting a usable
snapshot. It is P2 rather than P1 only because a snapshot with no collisions is already useful.

**Independent Test**: Close a snapshot with two devices deliberately sharing one strong identifier and
disagreeing on another of the same kind, resolve it, and confirm two entities plus one finding naming
both, without any operator input.

**Acceptance Scenarios**:

1. **Given** two claim sets sharing a strong identifier of one kind while carrying different strong
   identifiers of another kind, **When** the snapshot is resolved, **Then** they are not merged, both
   entities exist, and a finding names both and the identifier they contradict each other on.
2. **Given** that same collision, **When** the engineer opens the finding, **Then** it leads back to
   the observations and the raw output each conflicting claim came from.
3. **Given** a collision that the operator resolves with a decision, **When** the snapshot is resolved
   again, **Then** the decision applies and the finding is not raised again for that pair.

---

### User Story 3 - An operator decision survives every recomputation (Priority: P3)

An engineer knows two entities are the same box (a stack member re-cabled, a device whose serial
changed under a warranty swap), or knows one entity is really two. They record the decision once. Every
later run of that perimeter, and every recomputation of an old snapshot after a parser fix, applies it
without being asked again.

**Why this priority**: Without this, an operator correction is a manual repair of computed data, which
the constitution forbids (Principle II: everything outside the collected zone is derivable by replay,
with no manual repair step). It is P3 because a snapshot resolves usefully before any decision exists.

**Independent Test**: Record a merge decision on two entities of one snapshot, recompute that snapshot
from its stored claims alone, and confirm the merge is present in the result without the operator
touching anything.

**Acceptance Scenarios**:

1. **Given** a merge decision on two devices, **When** any snapshot containing both is resolved,
   **Then** they form one entity.
2. **Given** a never-merge decision on two devices that share a strong identifier, **When** the
   snapshot is resolved, **Then** they stay two entities and no collision finding is raised for that
   pair, because the answer is already recorded.
3. **Given** a split decision on an entity the system had merged, **When** the same perimeter runs
   again and the same shared identifier reappears, **Then** the two devices are still separate: the
   split is not undone by the next resolution.
4. **Given** a decision recorded after a snapshot was already resolved, **When** that snapshot is
   resolved again, **Then** the new entity set reflects the decision and the previous entity set is no
   longer the current one.
5. **Given** a decision whose subjects appear in no snapshot of a perimeter, **When** that perimeter's
   snapshots are resolved, **Then** the decision is ignored for them and stays available for any later
   snapshot where its subjects do appear.

---

### Edge Cases

- A device answers on two addresses within one run and the crawl caught it live, ending the second task
  as a duplicate. The entity must carry both addresses and both observations; the duplicate must not
  disappear from the evidence just because it was not the task that collected.
- The chain is transitive across three or more claim sets. Grouping follows the chain to its end, or a
  device reached through several protocols splits into as many entities as it has identifier kinds.
- A device produced no strong identifier at all. It is still an entity, marked as weakly identified, and
  it never merges with another on a weak identifier alone, however unique that hostname looks.
- Two different devices genuinely share a strong identifier (cloned VM, re-used serial). No merge, a
  finding, both entities readable.
- A merge decision and a never-merge decision name the same pair of subjects. The most recently recorded
  decision governs, and the earlier one stays readable so the history of the disagreement survives.
- A decision names a subject that no longer resolves to anything in the snapshot being computed. It is
  skipped for that snapshot without failing the resolution, and applies again the day the subject
  reappears.
- A replay writes a new parse generation over the same raw output, changing which claims exist.
  Resolution reads the snapshot's active parse generation only, so a superseded generation's claims
  never mix into the result.
- The same snapshot is resolved twice concurrently (a sweep and an explicit request landing together).
  The snapshot must end with one current entity set, never two half-written ones, and never a mix.
- Resolution is interrupted halfway. No consumer must ever see a partial entity set: either the previous
  one or the complete new one.
- The resolving side is down while several snapshots close. Each is resolved once it comes back, the same
  way the coverage gate sweeps for unjudged snapshots (002, FR-014).
- A snapshot is quarantined by the gate. It is still a closed snapshot with claims in it, and this
  feature does not decide whether a consumer should trust the result.
- A conflict reported on one resolution is settled by a decision and the snapshot is resolved again. The
  finding must be gone, not left open beside a set that no longer contains the conflict. A conflict that
  still holds must be reported once, not once per resolution.
- Two devices are kept apart by a split or a never-merge decision although they share a strong
  identifier. Their device keys must stay distinct, or the two entities collide under one key the moment
  anything looks a device up by it.
- A device's serial changes between two runs, because the chassis was swapped or because a parser fix
  reads it differently. Its device key changes with it, so it reads as a device gone and a device
  appeared until an operator records a merge. The system must not bridge the two on its own.
- A weakly identified device answers on a different address in the next run. Its key moves with it and it
  reads as a new device. That is the cost of having no strong identifier, and the entity is marked
  weakly identified precisely so a consumer can tell that story apart from a real replacement.
- An entity's claims come from observations spread over a long run, so the times attached to an entity
  are a range, not a point. The entity must carry when it was first and last observed within the
  snapshot, not just the resolution time, or freshness is lost for every consumer downstream
  (Principle I).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST resolve a closed snapshot's identifier claims into device entities without
  an operator asking, and MUST refuse to resolve a snapshot that is still open.
- **FR-002**: Two claim sets MUST belong to the same entity when they share an identifier of the same
  kind and value at strength strong, and grouping MUST follow such links transitively, so that claim
  sets linked only through a third one still form a single entity.
- **FR-003**: An identifier at strength weak MUST NOT cause two claim sets to be grouped. It MAY be
  recorded on the entity it belongs to as an attribute and MUST remain readable as evidence. When the claim
  sets grouped into one entity disagree on such an attribute, the entity MUST carry the value of the
  observation that stands for the device itself, the one not marked duplicate, so the same inputs always
  produce the same attribute (FR-013); the other values MUST stay readable through the entity's claims.
- **FR-004**: A device that produced no strong identifier MUST still become an entity, built from the
  claims it did produce and the address it answered on, and MUST be marked as weakly identified so that
  a later consumer can tell a confidently identified device from a presumed one.
- **FR-005**: Every entity MUST record which identifier claims it was built from, and through them the
  observations and the raw output behind each, so that no entity is served without the evidence and the
  collection time it rests on.
- **FR-006**: Every entity MUST record when its evidence was first and last collected within the
  snapshot it belongs to.
- **FR-007**: When two claim sets share a strong identifier of one kind but carry contradicting strong
  identifiers of another kind, the system MUST NOT group them, MUST keep both entities, and MUST raise a
  finding naming both subjects and the contradicting identifier, in the same findings surface the crawl
  loop already writes to (001).
- **FR-008**: The observation of a task the crawl ended as a duplicate MUST be attached to the entity of
  the task it duplicated, so the addresses and evidence of every route that reached a device stay with
  that device.
- **FR-009**: Operator decisions MUST be recorded as merge, split or never-merge records and MUST be
  applied on every resolution and every recomputation. A decision MUST NOT be applied by editing a
  computed row in place, and recording one MUST NOT modify or delete any earlier decision.
- **FR-010**: A decision MUST name its subjects by their device key (FR-021), never by a row identifier
  that a recomputation could re-issue, so a decision recorded today still applies to the same devices
  after the snapshot is resolved again.
- **FR-011**: When decisions conflict on the same subjects, the most recently recorded decision MUST
  govern, and every earlier decision MUST stay readable.
- **FR-012**: A split decision MUST keep its subjects apart on every later resolution, including one
  where the identifier that originally merged them is observed again.
- **FR-013**: Resolution MUST be reproducible: resolving the same closed snapshot from the same active
  parse generation with the same set of decisions MUST produce the same grouping of claims into
  entities.
- **FR-014**: Resolution MUST NOT modify or delete any observation, identifier claim, raw output or
  judgement, MUST contact no device, and MUST resolve no secret.
- **FR-015**: Resolving a snapshot again MUST produce a complete new entity set for that snapshot rather
  than a patch of the previous one, and at no point MUST a consumer read a partially written set.
  A snapshot MUST have exactly one current entity set. The identity collision findings of that snapshot
  MUST be replaced with it: a re-resolution MUST remove the ones the previous resolution raised and raise
  those that still hold, so a conflict settled by a decision stops being reported and a snapshot resolved
  ten times carries one finding per live conflict, not ten. Findings raised by anything but resolution
  MUST NOT be touched.
- **FR-016**: The system MUST find closed snapshots carrying no current entity set and resolve them, so
  that an interruption, or a period with the resolving side down, repairs itself with no dedicated
  recovery path.
- **FR-017**: An operator MUST be able to ask for a named snapshot to be resolved again, and the system
  MUST NOT decide on its own to re-resolve a snapshot it has already resolved, whatever changed since.
- **FR-018**: The system MUST let an engineer retrieve, for any resolved snapshot, its entities with the
  claims and observations behind each, and the collision findings raised while resolving it.
- **FR-019**: Resolution MUST read only the claims of the snapshot's active parse generation.
- **FR-020**: An entity MUST NOT be derived from anything but the collected zone and the recorded
  decisions, so that losing every computed row costs a recomputation and never a re-crawl.
- **FR-021**: An entity MUST belong to exactly one snapshot and MUST carry a device key computed from its
  strong identifiers, so that the same device resolved in two snapshots of the same perimeter carries the
  same key and a later consumer can follow it across runs without regrouping claims itself. The key MUST
  be recomputable from the claims and the decisions alone. A device key MUST be scoped to a perimeter,
  identified by its name the way 002 identifies one across config versions: two perimeters MUST NOT be
  combined into one device, whatever identifier they share.
- **FR-022**: An entity with no strong identifier MUST still carry a device key, derived from the address
  it answered on, and MUST be marked as weakly identified (FR-004) so that a consumer can tell a key that
  rests on a serial from one that rests on an address. A weak key MUST NOT be derived from a hostname,
  which two devices can share (FR-003), and a weakly identified device that answers on a different
  address in a later run is therefore a new device until an operator decides otherwise (FR-009).
- **FR-023**: When a device's strong identifiers change between two runs so that none of them links it to
  a device already known, a new key MUST be minted rather than guessed onto the nearest match. Attaching
  the old key and the new one to one device MUST be an operator merge decision (FR-009), never a
  similarity judgement made by the system. A device that still carries one of its known identifiers keeps
  its key, whatever else changed around it.
- **FR-024**: Two entities of the same snapshot MUST NOT carry the same device key. Where a split or a
  never-merge decision keeps apart two devices that share a strong identifier, their keys MUST stay
  distinct.
- **FR-025**: Resolution MUST run on every closed snapshot, whatever the coverage gate classified it as.
  A quarantined snapshot MUST be resolved like any other, and this feature MUST NOT decide whether a
  consumer should trust the result.
- **FR-026**: This feature MUST produce device entities only. Interfaces, their canonical names and their
  aliases are out of scope and belong with the graph projector, which reads the entities produced here.

### Key Entities

- **Device entity**: a resolved device. Carries the identifier claims it was built from, the addresses it
  answered on, the attributes its weak identifiers give it (hostname, platform, taken from the observation
  that stands for the device itself), whether it is strongly or weakly identified, and when its evidence was
  first and last collected. Computed, never collected:
  rebuildable at any time from the claims and the decisions.
- **Device key**: what makes a device the same device in two runs of one perimeter, computed from its
  strong identifiers (or, for a weakly identified device, from what it does carry). It is the name a
  decision, a diff or an intent record uses to point at a device without pointing at one snapshot's row.
  Scoped to a perimeter name, so the same key under two perimeters is two devices.
- **Entity claim link**: which identifier claims were grouped into which entity. The path from an answer
  back to the observation and the raw output that justify it.
- **Entity decision**: an operator's merge, split or never-merge, naming its subjects, its actor and when
  it was recorded. Append only, replayed on every resolution, never applied in place.
- **Identity collision finding**: a report that two devices contradict each other on a strong identifier,
  naming both subjects and the identifier, with the observations behind each side.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every closed snapshot ends with exactly one current set of device entities, and none is
  left unresolved.
- **SC-002**: A device reached through several addresses or several protocols in one run appears exactly
  once in that run's entity set.
- **SC-003**: For any entity, an engineer can name the identifier claims, the observations and the raw
  output it was built from, and when that evidence was collected, without reading a log.
- **SC-004**: Resolving the same snapshot again, from the same parse generation and the same decisions,
  yields the same grouping every time.
- **SC-005**: Two devices contradicting each other on a strong identifier are never silently merged: the
  result is two entities and one finding an operator can act on.
- **SC-006**: An operator decision recorded once keeps applying to every later run of that perimeter and
  to every recomputation of an older snapshot, with no repeated action.
- **SC-007**: A device reached in two consecutive snapshots of the same perimeter carries the same device
  key in both, without any consumer regrouping its claims to find out.
- **SC-008**: Deleting every computed row and recomputing restores the same entity set from the stored
  claims and decisions alone, with no device contacted.

## Assumptions

- This feature groups claims into devices and stops there. It builds no interfaces, no edges, no L2
  domains, no diff and no intent match; those read entities, they are not produced by one
  (docs/c4-model/00-overview.md: the engine's later work). Interfaces are the graph projector's, since
  they come from the interfaces fact family and the packs' naming rules rather than from identifier
  claims.
- Which identifiers a platform yields, and whether each is strong or weak, is pack data (001, Principle V).
  This feature reads the strength recorded on the claim and never decides per vendor what counts as strong.
- The crawl's live deduplication (001) stays as it is. It keeps a run from collecting the same device twice
  and it is not a substitute for resolution: it compares one strong identifier at a time, at the moment a
  task claims, and never closes a transitive chain.
- The coverage gate (002) and this feature are independent: the gate counts identity observations, not
  entities, so resolution does not change any verdict and does not need one to run. A quarantined
  snapshot is resolved like any other, and reading its entities as if they were current stays the
  consumer's responsibility, exactly as 002 left it.
- A resolution result is computed data, rebuildable, and therefore replaced wholesale on a recomputation.
  Only the decisions behind it are append only, the same way observations are.
- Presenting entities in any interface (API, MCP, UI) is out of scope; this feature computes and stores
  them in a form a later interface can read, which is where the evidence requirement will be enforced as a
  response contract.
