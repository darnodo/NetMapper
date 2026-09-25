# Feature Specification: Graph projector (interfaces and edges)

**Feature Branch**: `004-graph-projector`

**Created**: 2026-09-24

**Status**: Draft

**Input**: User description: "The graph projector: interfaces and edges. The feature after identity
resolution (003). 003 turns a closed snapshot's identifier claims into device entities with a stable
device key, and FR-026 explicitly hands interfaces, their canonical names and their aliases to this
feature. Nothing yet connects one device to another: a device has no ports, and the neighbours the crawl
collected sit in the `neighbours` fact family with nobody reading them. This feature projects, by replay,
the interfaces of each resolved device and the edges between them."

## Decisions taken in this spec

Two questions were left open for this feature rather than inherited. Both are settled here, and both are
the ones to challenge first in `/speckit-clarify`.

- **An edge carries a row per snapshot, not a validity interval across snapshots.** This is what
  `docs/c4-model/04-data-model.md` recorded as open, and what 003 settled for entities while saying
  explicitly that edges inherit nothing from it. Per snapshot is chosen for the same reason it was chosen
  there, and for one more: an interval has to decide when a link *ended*, and a disappearance is only
  meaningful after a successful collection on both sides, which makes the end of an interval a judgement
  rather than an observation. A row per snapshot keeps a recomputation a replacement.
- **An edge is named by its endpoints, not by a minted key.** A device gets its key from a registry
  because a serial is the only thing that survives a renumbering. An edge has no such problem: both of
  its endpoints already carry stable names, so an edge's name is derivable from them and needs no
  registry, no mint and no operator decision. This keeps the feature far smaller than 003.

## Clarifications

### Session 2026-09-25

- Q: When a neighbour report names a remote device only by its system name, should the link attach to a device entity whose hostname matches? → A: No. Chassis identifier, then management address, in that order; the system name is recorded but never resolves an endpoint.
- Q: If one device reports the same cable under two discovery protocols, one link or two? → A: One link. The protocol is evidence, not identity: the link records every protocol that reported it and cites each report.
- Q: When a remote identifier in a neighbour report matches more than one device entity, which one does the link attach to? → A: None. The link stays one-sided, carrying the identifier; the identity conflict that split the entities already reports the cause.
- Q: What makes two reports about one pair of devices a contradiction rather than two separate cables? → A: They name one port in common and a different port opposite it. Reports sharing no port are two cables, each known from one side.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A device has ports, and they are called one thing (Priority: P1)

A crawl collects a switch's interfaces, and a neighbour of that switch reports the far end of a cable by
a shorter spelling of the same port. The device says `Ethernet1`, its neighbour says `Et1`, and an intent
source will one day say `eth1`. Nothing today turns those into one port: the `interfaces` fact family sits
unread, and a device entity has no ports at all. This feature gives each resolved device its interfaces
under one canonical name, with every spelling ever seen still readable against it.

**Why this priority**: An edge attaches to a port. Until a device has ports that can be named the same way
from both ends of a cable, there is nothing for a link to connect, and the spellings are exactly what makes
two reports of one cable look like two different things. This is the smallest piece that is useful on its
own: an engineer can already ask what a device's ports are, what state they are in, and what else calls
them something else.

**Independent Test**: Close a fake-transport snapshot with one device reporting several interfaces and a
neighbour reporting one of them under another spelling. Project it and confirm each port appears once,
under its canonical name, with the neighbour's spelling recorded as an alias and traceable to the
observation it came from. No edges involved.

**Acceptance Scenarios**:

1. **Given** a resolved snapshot whose device reported interfaces, **When** it is projected, **Then** each
   interface appears once for that device, under the canonical name the pack's naming rules produce, and
   carries the description, states, speed, MTU and MAC that were collected.
2. **Given** two spellings of one port, one from the device itself and one from a neighbour's report,
   **When** the snapshot is projected, **Then** there is one interface, and both spellings are readable
   against it with the source each came from.
3. **Given** an interface, **When** an engineer reads it, **Then** the observation it was collected from,
   and through it the raw output and the time of collection, are reachable.
4. **Given** a device whose interfaces were never collected, because the scrape failed or the platform
   supports no such recipe, **When** the snapshot is projected, **Then** the device keeps its entity and
   simply has no interfaces, rather than the projection failing.
5. **Given** two devices that each have a port spelled the same way, **When** the snapshot is projected,
   **Then** they are two interfaces, because an interface belongs to exactly one device.

---

### User Story 2 - A cable both ends agree on is a link (Priority: P2)

Two switches are cabled together and both report the other over LLDP. Each names its own port and the
port it sees at the far end. This feature turns the pair of reports into one link between two interfaces,
and records that both ends agreed, which is what separates a fact from a deduction.

**Why this priority**: This is the topology, and it is the first thing about the graph that anyone wants to
look at. It is P2 rather than P1 only because it needs US1's interfaces to attach to, and because a
snapshot with interfaces and no links is already readable.

**Independent Test**: Close a snapshot with two devices reporting each other over LLDP, project it, and
confirm one link between the two interfaces, marked as agreed by both ends, citing both observations.

**Acceptance Scenarios**:

1. **Given** two devices that each reported the other, naming the same pair of ports, **When** the snapshot
   is projected, **Then** there is one link between those two interfaces, not two, and it is marked as
   agreed by both ends.
2. **Given** that link, **When** an engineer reads it, **Then** it cites the observation from each side, and
   through them the raw output behind each.
3. **Given** a neighbour report naming a remote device by a chassis identifier or a management address that
   resolution already attributed to a device entity, **When** the snapshot is projected, **Then** the link
   lands on that entity rather than on a name.
4. **Given** two devices cabled on more than one port, **When** the snapshot is projected, **Then** there is
   one link per cable, because a link is identified by the pair of interfaces and not by the pair of devices.

---

### User Story 3 - A link only one side saw is still worth having, and says so (Priority: P3)

A switch reports a neighbour that cannot report back, because it is unmanaged, was never reached, or
refused the collector's credentials. The cable exists and one end knows about it. Dropping it loses real
topology; recording it as if both ends had agreed would be a lie. This feature keeps it, marked as known
from one side only, and does the same for the address a device answered on.

**Why this priority**: It is the constitution's first principle made concrete: a link both endpoints agree
on and a link deduced from one side must be distinguishable in the result. It is P3 because a lab where
everything answers never exercises it, and because the agreed case is the one that carries most of the
value.

**Independent Test**: Close a snapshot where one device reports a neighbour that was never reached, project
it, and confirm the link exists, is marked as one-sided, and names what little is known about the far end.

**Acceptance Scenarios**:

1. **Given** a neighbour report whose remote device no entity matches, **When** the snapshot is projected,
   **Then** the link is recorded from the reporting interface, marked as known from one side, and carries
   the identifiers the report gave for the far end.
2. **Given** a neighbour report whose remote device resolution did match, but which that device never
   reported back, **When** the snapshot is projected, **Then** the link connects the two and is still marked
   as one-sided rather than agreed.
3. **Given** a device entity that answered on one or more addresses, **When** the snapshot is projected,
   **Then** each address is recorded against that device as its own kind of edge, so that a later consumer
   can ask what answers at an address without reading identity claims.
4. **Given** a one-sided link that both ends report in a later snapshot, **When** that snapshot is projected,
   **Then** it is marked as agreed there, and the earlier snapshot's record is unchanged.

---

### Edge Cases

- A device reports a neighbour on a port it never listed among its own interfaces. The port is created from
  the neighbour report rather than the link being dropped, and it must be distinguishable from a port the
  device described itself.
- A neighbour reports a remote port spelled in a way no naming rule matches. The spelling is kept as the
  alias it is, and the canonical name falls back to the spelling itself rather than to nothing.
- Three or more devices report the same link, which a shared medium or a mistaken report can produce. The
  result must not silently pick two of them and discard the rest. Each pair of them agrees with itself, so
  a shared segment is several links and not a contradiction.
- Two devices report each other but disagree about which ports are cabled. That is a contradiction of the
  same shape identity resolution already reports, and the result must not invent a link neither side
  described.
- A device is reached on two addresses, so resolution collapsed two observations into one entity. Its
  interfaces must not be duplicated per address.
- A neighbour reports a chassis identifier that two entities of the snapshot both carry, which identity
  resolution produces when it refuses to merge a component that contradicts itself. The link attaches to
  neither and stays known from one side.
- A snapshot has an entity set but no interface and no neighbour observation at all. It projects to nothing
  and is recorded as projected, not as pending.
- A snapshot is projected again after a parser fix changed which interfaces or neighbours exist. The result
  is the new set, complete, with no remnant of the old one.
- The same snapshot is projected twice at once. It ends with one set, never two half-written ones.
- A snapshot is projected before it has been resolved, or has no entity set at all. There is nothing to
  attach a port to, so it is not projected and is not treated as an error either.
- An interface's MAC is also a device's chassis MAC, which resolution used as a strong identifier. The
  interface must not be mistaken for a device.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST project a closed snapshot that carries a current entity set into interfaces
  and edges without an operator asking, and MUST NOT project one that has no entity set.
- **FR-002**: Every interface MUST belong to exactly one device entity, and MUST be identified within that
  entity by its canonical name, so that two devices may each have a port of the same name.
- **FR-003**: An interface's canonical name MUST come from the naming rules its platform pack declares, and
  the system MUST NOT decide per vendor what a port is called.
- **FR-004**: The system MUST record every spelling of an interface it has seen, with the source each came
  from, and MUST keep them readable against the interface they name.
- **FR-005**: A spelling that matches no naming rule MUST still produce an interface, named by that spelling.
- **FR-006**: An interface MUST carry every field the interfaces fact family defines for it that was
  collected, and MUST distinguish a port the device described itself from one known only because a
  neighbour reported it.
- **FR-007**: Every interface and every edge MUST record the observations it was derived from, and through
  them the raw output and the collection time behind each, so that none is served without its evidence.
- **FR-008**: Every edge MUST record when its evidence was first and last collected within the snapshot it
  belongs to.
- **FR-009**: Two neighbour reports describing the same cable from opposite ends MUST produce one link, not
  two, and that link MUST be marked as agreed by both ends.
- **FR-010**: A link reported by one end only MUST still be recorded, MUST be marked as known from one side,
  and MUST carry whatever the report said about the far end.
- **FR-011**: A link MUST be identified by the pair of interfaces it connects, so that two devices cabled on
  several ports produce one link per cable. The discovery protocol a report arrived on MUST NOT be part of
  that identity: two reports of one cable under two protocols are one link, which MUST record every
  protocol that reported it and MUST cite the observation behind each report.
- **FR-012**: When a neighbour report names a remote device by a chassis identifier or a management
  address that the snapshot's entity set already accounts for, the link MUST attach to that entity,
  matching on the chassis identifier first and the management address second. A remote system name MUST
  be recorded against the link but MUST NOT on its own attach it to an entity, because a name is a weak
  identifier and two devices may share one. When a report names a remote device that nothing accounts
  for, the link MUST still be recorded against what is known. When an identifier matches more than one
  entity, the link MUST NOT attach to any of them and MUST be recorded as known from one side, because
  attributing it to one of several candidates is a guess the result could not be told apart from a fact.
- **FR-013**: The system MUST record, as its own kind of edge, each address a device entity answered on.
- **FR-014**: Two reports about the same pair of devices contradict each other when they name one port in
  common and a different port opposite it, because one port cannot face two different far ends. Two reports
  about one pair of devices that share no port are two separate cables, each known from one side, and are
  not a contradiction. When reports contradict each other, the system MUST NOT invent a link neither side
  described, MUST keep each side's own report as a link known from that side, and MUST report the
  disagreement where the crawl and resolution already report data quality problems.
- **FR-015**: An edge MUST carry a name derived from its endpoints, stable across runs for as long as the
  endpoints are, so that a later consumer can follow a link from one run to the next without rebuilding the
  graph. That name MUST NOT require a registry, a mint or an operator decision.
- **FR-016**: An edge MUST belong to exactly one snapshot.
- **FR-017**: Projection MUST be reproducible: the same snapshot, the same parse generation and the same
  entity set MUST produce the same interfaces and the same edges.
- **FR-018**: Projecting a snapshot again MUST produce a complete new set rather than a patch of the
  previous one, and at no point MUST a consumer read a partially written set. A snapshot MUST have exactly
  one current set.
- **FR-019**: Projection MUST read only the observations of the snapshot's active parse generation.
- **FR-020**: Projection MUST NOT modify or delete any observation, raw output, identifier claim, judgement,
  entity or operator decision, MUST contact no device, and MUST resolve no secret.
- **FR-021**: The system MUST find closed snapshots that carry an entity set and no current projection, and
  project them, so that an interruption repairs itself with no dedicated recovery path.
- **FR-022**: An operator MUST be able to ask for a named snapshot to be projected again, and the system
  MUST NOT decide on its own to redo one it has already projected.
- **FR-023**: Interfaces and edges MUST be derivable from the collected zone and the entity set alone, so
  that losing every projected row costs a recomputation and never a re-crawl.
- **FR-024**: The system MUST let an engineer retrieve, for any projected snapshot, its interfaces and edges
  with the observations behind each, and the disagreements reported while projecting it.
- **FR-025**: This feature MUST produce interfaces and edges only. Flooding domains, and serving any of this
  through an interface, are out of scope.

### Key Entities

- **Interface**: a port of one device entity. Carries its canonical name, the operational facts collected
  about it, whether the device described it or a neighbour revealed it, and the observations behind it.
  Computed, never collected.
- **Interface alias**: one spelling of an interface, with the source that used it. The record of why two
  reports of one cable are one cable.
- **Edge**: a relationship between two endpoints in one snapshot. Carries its type, what is known and how
  well it is known, the observations behind each side, and when that evidence was collected. Named by its
  endpoints.
- **Edge evidence**: which observations an edge was deduced from, and from which side.
- **Link disagreement**: a report that two devices contradict each other about which ports are cabled,
  naming both sides and what each said.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every closed snapshot that has an entity set ends with exactly one current set of interfaces
  and edges, and none is left unprojected.
- **SC-002**: A port that two sources spell differently appears exactly once per device, and both spellings
  lead to it.
- **SC-003**: For any interface or edge, an engineer can name the observations, the raw output and the
  collection time it rests on, without reading a log.
- **SC-004**: A cable both ends reported appears exactly once, and carries a marker that tells it apart
  from one a single end reported, without a reader having to open the evidence.
- **SC-005**: Projecting the same snapshot again, from the same parse generation and entity set, yields the
  same interfaces and edges every time.
- **SC-006**: A link whose two endpoints are still the same devices carries the same name in two consecutive
  snapshots of one perimeter.
- **SC-007**: Two devices that disagree about which ports are cabled produce a reported disagreement and no
  invented link.
- **SC-008**: Deleting every projected row and recomputing restores the same interfaces and edges from the
  stored observations and the entity set alone, with no device contacted.

## Assumptions

- Only two of the four edge types the domain model lists are buildable from what the crawl collects today.
  `l1_link` comes from the `neighbours` family and `has_address` from the addresses a device answered on.
  `attached`, which puts an endpoint behind a port, needs forwarding or address tables, and
  `protocol_adjacency` needs routing protocol state; neither family is collected, so neither type is in
  scope here. Inventing a data source for them is not an option, and widening the crawl is another feature.
- The canonical name of an interface is already applied by the parser, which the `interfaces` and
  `neighbours` families mark on the fields that carry it. What a neighbour says about the *far end* port is
  not canonicalised, which is where aliases actually come from today.
- Flooding domains are a separate concern about which ports share a broadcast domain, and they read edges
  rather than produce them.
- The entity set this feature reads is the one identity resolution produced, including its device keys, its
  weak marker and its addresses. This feature produces no device entities and changes none.
- Any entity kind beyond `device` that this feature needs is a schema change made deliberately, since
  resolution left that set deliberately narrow.
- Presenting interfaces or edges in any interface is out of scope, which is where the evidence requirement
  will be enforced as a response contract.
- A snapshot the coverage gate quarantined is projected like any other, the way resolution treats it:
  whether a consumer should trust the result stays the consumer's call.
