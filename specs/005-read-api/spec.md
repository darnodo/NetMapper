# Feature Specification: The read API (serving the graph with its evidence)

**Feature Branch**: `005-read-api`

**Created**: 2026-09-25

**Status**: Draft

**Input**: User description: "The read API: serving the graph with its evidence, REST only. The feature
after the graph projector (004). Four features have filled the computed zone and none of them serves
anything: a device, its interfaces and the edges between them are readable only by SQL, which both 003
and 004 recorded as a known open point against their own FR-024. This feature builds the `api` role,
the third of the three the architecture names and the only one that does not exist, and it is the first
place the constitution's first principle becomes a response contract rather than a data layout. It is
also the first component exposed to users, so it brings the project's first access control. MCP, the Web
UI, the diff engine, flooding domains and reconciliation with intent are all out of scope."

## Decisions taken in this spec

Two things are settled here rather than left to the planner, because both shape what the feature is.

- **One shape of the query layer, not two.** The architecture lists REST and MCP side by side, and this
  feature builds only the first. An agent consuming MCP and an engineer consuming REST ask the same
  questions of the same rows, so the second shape is cheap once the first has settled and expensive
  while it is still moving. Building both at once would mean changing two contracts every time an
  answer's shape is corrected.
- **The evidence requirement is a response contract, not a column.** Every earlier feature stored the
  provenance an answer needs and none of them had to return it. This is where the first principle
  either holds or is quietly dropped, so a response missing its evidence is a defect of this feature in
  the same way a wrong device key was a defect of 003.

## Clarifications

### Session 2026-09-25

- Q: Does this feature serve reads only, or also accept the configuration document and trigger runs? → A: Reads only. Configuration and job triggering stay on the operator CLI and reach the interface in a later feature.
- Q: What are the three scopes the architecture names? → A: Deferred. One `read` scope ships now; the set is defined when there is something to mutate, and the architecture's "three scopes" is corrected rather than implemented.
- Q: Which snapshot answers a question that names none? → A: The most recently closed one, whatever the coverage gate said about it. The verdict travels in the answer so the caller can judge it.
- Q: Does the interface serve the raw command output itself, or only a reference to it? → A: It serves the bytes, holding read-only object-store credentials, which are configuration rather than a resolved secret and are never returned.
- Q: What does a caller name a device by? → A: Its device key, its hostname or an address it answered on, tried in that order; a name matching two devices is refused with both named rather than resolved to one.
- Q: Can a caller list or search the devices of a snapshot? → A: A flat list, each device with its key, hostname, addresses and weak marker, and no ports or edges. No filtering or search.
- Q: Should the default require a snapshot that already has a graph, or the most recently closed one even if projection has not reached it? → A: The most recently closed one that carries a graph. Projection lags closing by a sweep, and a caller cannot tell that race from a crawl that found nothing.
- Q: How much of a flat segment should one device's answer contain? → A: Everything. Every edge touching its ports, uncapped, unfiltered and unsummarised; counting is the caller's business.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A device, its ports and its cables, each with what it rests on (Priority: P1)

An engineer wants to know what a switch looks like right now: which ports it has, what is cabled to
them, and which addresses answer for it. Today the only way is to write a join across six tables and
know which of them carries the evidence. This story serves that question over the network, and every
device, interface and edge in the answer arrives carrying the observation it came from, when that
observation was collected, and how well it is known.

**Why this priority**: it is the reason the previous four features exist. Until this works, the computed
zone is a private data structure. It is also the smallest slice that is useful alone: an engineer who
can read one device with its evidence has something no other tool in the stack gives them.

**Independent Test**: close and project a snapshot, then ask the interface for one device and confirm
the answer names its ports, the cables on them, the addresses it answered on, and for each of those the
observation, the collection time and how well it is known. No other endpoint involved.

**Acceptance Scenarios**:

1. **Given** a projected snapshot, **When** a caller asks for a device, **Then** the answer carries the
   device, its interfaces and the edges touching them, and every one of those carries its evidence and
   the time that evidence was collected.
2. **Given** an edge both ends reported and an edge one end reported, **When** both are returned,
   **Then** a reader can tell them apart without opening the evidence.
3. **Given** any interface or edge in an answer, **When** a caller follows the evidence it names,
   **Then** the raw output behind it is reachable, and the answer says when it was collected.
4. **Given** a device that resolution marked weakly identified, **When** it is returned, **Then** the
   answer says so, because a consumer deciding whether to act on it needs to know.
5. **Given** a snapshot that was never projected, **When** a caller asks for its graph, **Then** the
   answer says the snapshot carries no graph rather than returning an empty one, because empty and
   absent are different answers.

---

### User Story 2 - Only a holder of the right token gets an answer (Priority: P2)

The interface is the first component anyone can reach, and the database behind it holds every address,
credential reference and command the collector ever ran. A caller with no token gets nothing. A caller
with a token gets exactly what its scope allows, and every call that changes something is recorded
against the token that made it.

**Why this priority**: the constitution makes this the only exposed role and forbids it from holding a
credential or reaching a device, so the boundary this story draws is the one the whole security model
rests on. It is P2 rather than P1 only because a story that returns nothing to anyone is not
demonstrable on its own; in delivery the two ship together.

**Independent Test**: call every endpoint with no token, with a revoked token, and with a token whose
scopes do not cover it, and confirm each is refused; then confirm the refusals say which of the three
happened without revealing whether the thing asked for exists.

**Acceptance Scenarios**:

1. **Given** no token or an unknown one, **When** any endpoint is called, **Then** the call is refused
   and nothing about the data is revealed.
2. **Given** a token whose scopes do not cover the call, **When** it is made, **Then** it is refused,
   and the refusal is distinguishable from an unknown token.
3. **Given** any answer at any scope, **When** it is returned, **Then** it contains no credential value,
   because no interface returns one to anyone at any level of privilege.
4. **Given** a token that has been revoked, **When** it is used, **Then** it is refused from that moment,
   without waiting for anything to expire.
5. **Given** any endpoint of this interface, **When** it is called by any token, **Then** nothing in the
   collected, computed or reported zones changes and no configuration is accepted, because this feature
   exposes no call that alters anything.

---

### User Story 3 - Whether to trust what was just read (Priority: P3)

A graph is only as good as the crawl behind it. This story serves what the crawl and the computations
reported about themselves: the coverage verdict on a snapshot, and the findings raised against it, so a
consumer can decide whether the answer they just got is worth acting on.

**Why this priority**: it is what makes the first two honest. A quarantined snapshot that reads exactly
like a published one invites an agent to act on half a network. It is P3 because the first two stories
are useful without it, and because a lab where everything answers rarely exercises it.

**Independent Test**: judge a snapshot into each of the three verdicts, ask the interface for each, and
confirm the verdict, the coverage figure and the reason are readable; then confirm the findings raised
against a snapshot are readable with the observations behind them.

**Acceptance Scenarios**:

1. **Given** a snapshot with a coverage verdict, **When** it is read, **Then** the verdict, the figure
   it rests on and what it was compared against are in the answer.
2. **Given** findings raised against a snapshot, **When** they are read, **Then** each names its
   category, what it is about, and the observations it cites.
3. **Given** a quarantined snapshot, **When** its graph is read, **Then** the answer is served and
   carries its verdict, because the gate labels a snapshot rather than hiding it.

---

### Edge Cases

- A device sits on a flat segment where every other device is its neighbour, so one of its ports carries
  an edge per device on that segment. The answer carries all of them.
- A caller asks for a device that does not exist in the snapshot they named. Absent and empty must not
  read the same.
- A caller names a device by a hostname two devices share in that snapshot, which is what identity
  resolution produces when it refuses to merge a contradicting component. The answer names both and
  resolves to neither.
- A caller names a device by an address that one device answered on in this snapshot and a different
  device answered on in an earlier one. The answer is about the snapshot asked for and says which device
  key it resolved to, so the caller can see which box they got.
- A caller asks for a device by a key that existed in an earlier snapshot and not in this one. The
  answer must not silently fall back to another snapshot.
- An interface exists but was never described by its own device, only revealed by a neighbour. The
  answer must carry that distinction, since it changes how much the port can be trusted.
- An edge names a far end no entity accounts for. The answer must return what the report said about it
  rather than omitting the edge or inventing a device.
- A snapshot has been evicted and only its tombstone remains. Asking for its graph must say so rather
  than returning an empty graph.
- The same token is used concurrently from several callers. Nothing about the answers changes; the
  record of mutating calls still names every one of them.
- A token is revoked while a call it authorised is in flight. The call in flight is allowed to finish;
  the next one is refused.
- A caller asks for a snapshot that is still open. It carries no entity set and no graph, and the answer
  must say that rather than returning a partial one.
- A crawl has just closed and the projector has not reached it yet. A caller naming no snapshot is
  answered from the previous one, which is one crawl old and says so; a caller naming the new one
  explicitly is told it carries no graph. The default never reports a race as an outcome.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST serve, over a network interface, the devices of a projected snapshot with
  their interfaces, the edges touching those interfaces, and the addresses each device answered on.
- **FR-001a**: A caller MUST be able to name a device by its device key, by its hostname, or by an
  address it answered on, tried in that order, so that the precise handle exists for a machine and the
  memorable one for a person. When a hostname or an address names more than one device in the snapshot,
  the system MUST refuse and name every candidate rather than resolving to one: a hostname is a weak
  identifier, and identity resolution refuses to merge on one for the same reason.
- **FR-001b**: An answer MUST state which device key it resolved to, whatever the caller named, so that
  a second request can be made precise and two answers can be compared.
- **FR-001d**: A device's answer MUST carry every edge touching its interfaces, with no cap, no filter
  and no summary, however many there are. A flat management segment puts one edge per other device on a
  single port, which the containerlab lab already shows at four nodes, so the count can be large. Any
  cap would be the interface deciding which of a caller's cables matter, and this project has
  consistently refused that shape of decision: the coverage gate labels a snapshot rather than hiding
  it, and the projector records a one-sided link rather than dropping it.
- **FR-001c**: The system MUST serve the list of devices in a snapshot, each with its device key, its
  hostname, the addresses it answered on and whether it is weakly identified, and without their
  interfaces or edges. Without it the key FR-001a accepts could only be found by querying the database,
  which is the friction this feature exists to remove. Filtering and searching are out of scope: the
  list is small enough to read, and a query language is a second contract to keep stable for the agent
  consumer that arrives later.
- **FR-002**: Every device, interface, edge and finding in any answer MUST carry the observations it was
  derived from and the time those observations were collected. An answer that omits either MUST NOT be
  served, at any level of privilege.
- **FR-003**: Every edge in an answer MUST say how well it is known, so that a link both ends reported
  and a link one end reported are distinguishable without opening the evidence.
- **FR-004**: An answer MUST let a caller reach the raw output behind any observation it names, and the
  interface MUST serve those bytes itself rather than pointing at somewhere else to fetch them. An
  evidence chain that stops one link short of the thing it points at is the footnote this feature exists
  to remove.
- **FR-004a**: Reading raw output MUST use read-only access to the object store. That access is
  configuration, not a credential resolved for the duration of a task, and the interface MUST NOT
  return it, MUST NOT write to the object store, and MUST NOT gain any access that opens a session to a
  device. The constitution's boundary is about reach into the network, and this does not cross it.
- **FR-005**: An interface in an answer MUST say whether its device described it or a neighbour revealed
  it, and MUST carry the other spellings it is known by.
- **FR-006**: A device in an answer MUST say whether resolution identified it weakly.
- **FR-007**: The system MUST serve the coverage verdict of a snapshot, the figure it rests on, and what
  it was compared against.
- **FR-008**: The system MUST serve the findings raised against a snapshot, each with its category, its
  subject and the observations it cites.
- **FR-009**: A snapshot that was never projected, is still open, or has been evicted MUST produce an
  answer that says so, distinct from an answer describing a graph with nothing in it.
- **FR-010**: Every call MUST present a token. A call with no token, an unknown token or a revoked token
  MUST be refused, and MUST reveal nothing about whether the thing asked for exists.
- **FR-011**: A token MUST carry a set of scopes drawn from a closed set of values, and a call the
  token's scopes do not cover MUST be refused in a way a caller can tell apart from an unknown token.
  `read` is the only value this feature defines; a token carrying none of the defined values, or one
  that is not defined, MUST be refused rather than treated as permissive.
- **FR-012**: This interface MUST expose no call that changes anything: no configuration accepted, no
  job triggered, no row of the collected, computed or reported zones written. Those paths stay on the
  operator command line until a later feature brings them here, and the audit obligation the
  constitution places on a mutating call arrives with them.
- **FR-013**: The system MUST store a token in a form from which the token itself cannot be recovered,
  and MUST NOT return a token value after the moment it is issued.
- **FR-014**: No answer MUST contain a credential value, a secret reference's resolved value, or any
  device password, at any level of privilege.
- **FR-015**: The component serving this interface MUST NOT open a session to any device, MUST NOT
  resolve a secret reference into a credential value, and MUST NOT compute any entity, interface, edge
  or verdict. It reads what other components wrote, from the database and from the object store.
- **FR-016**: The system MUST NOT modify any observation, raw output, identifier claim, judgement,
  entity, interface, edge or operator decision in the course of serving a read.
- **FR-017**: An answer MUST name the snapshot it was built from, so that two answers read minutes apart
  can be told apart.
- **FR-018**: The system MUST let a caller ask for a named snapshot explicitly, and MUST NOT silently
  answer from a different one.
- **FR-019**: When a caller names no snapshot, the system MUST answer from the most recently closed
  snapshot that carries a graph, whatever the coverage gate said about it, and the answer MUST carry
  both that snapshot's identity and its verdict.
  Two axes are settled here, separately. The coverage verdict MUST NOT be used to choose, because a
  consumer that can see the verdict can judge it while one that never sees a quarantined snapshot
  cannot tell a quarantine from an outage. Whether a graph exists MUST be used to choose, because
  projection lags closing by one sweep and answering "no graph" for those seconds is a race a caller
  cannot tell apart from a crawl that found nothing. A default that is briefly one crawl old is the
  price, which is why every answer names its snapshot and carries its collection times.
- **FR-020**: Every answer MUST carry the coverage verdict of the snapshot it was built from, not only
  the endpoint that serves verdicts, because FR-019 makes a quarantined snapshot a possible default and
  a caller must not have to ask a second question to learn that.
- **FR-021**: A token MUST be issuable and revocable without this interface, since it exposes no call
  that changes anything, and revoking one MUST take effect on the next call rather than at an expiry.
- **FR-022**: The system MUST serve answers of this shape for any projected snapshot without an operator
  preparing anything, so that reading requires no step beyond the crawl that produced the snapshot.

### Key Entities

- **Token**: what a caller presents. Carries a name, its scopes, when it was created, when it was last
  used, and whether it has been revoked. The value itself is never stored and never returned after
  issue.
- **Answer**: what a caller receives. Whatever it describes, it carries the snapshot it came from, the
  observations behind each element, and when those were collected.
- **Evidence reference**: the link from an element of an answer to an observation, its collection time
  and the raw output behind it. The same chain the computed zone already records, made reachable from
  outside.
- **Snapshot reference**: the identity and the coverage verdict of the snapshot an answer was built
  from, carried by every answer so a caller never has to ask a second question to know what they are
  looking at.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An engineer can obtain a device's ports, its cables and its addresses in one request,
  naming the device by something they already know rather than by a key they had to look up, where the
  same question previously required writing a join across six tables.
- **SC-001a**: A name that fits two devices never returns one of them; the caller learns both exist and
  can ask again precisely.
- **SC-001b**: An engineer who knows nothing but a snapshot can discover every device in it, and from
  there reach any one of them, without writing a query.
- **SC-002**: Every element of every answer names its evidence and its collection time; a response
  containing an element without them is a failure, and this is checked over the whole surface rather
  than sampled.
- **SC-003**: A cable both ends reported and a cable one end reported are distinguishable in an answer
  by a reader who never opens the evidence.
- **SC-004**: A caller with no token, an unknown token or a revoked token obtains nothing, and cannot
  determine from the refusal whether what they asked for exists.
- **SC-005**: A token carrying no defined scope, or an undefined one, obtains nothing, so adding a
  second scope later cannot turn an existing token permissive by accident.
- **SC-006**: No response on any endpoint, at any scope, contains a credential value or an object-store
  access key; this is verified across the whole surface and not only where credentials are expected.
- **SC-006a**: An engineer reading any element of any answer can obtain the raw command output behind
  it without leaving the interface and without holding any credential of their own beyond their token.
- **SC-007**: Two answers about the same device from two different snapshots can be told apart by the
  caller without external context.
- **SC-008**: Serving any answer leaves every row of the collected, computed and reported zones
  unchanged, verified over the whole surface rather than on the endpoints where a write would be
  expected.
- **SC-009**: A caller reading a quarantined snapshot by default learns it is quarantined from the same
  answer, without asking anything else.

## Assumptions

- The interface serves what the first four features already store. It adds no fact family, no
  computation and no new column to the collected or computed zones beyond what tokens need.
- Serving raw output makes this the second component to read the object store, after the collector. It
  reads and never writes, so eviction and the orphan sweep stay the engine's and the maintenance
  path's business.
- Tokens are issued and revoked from the operator command line, alongside the subcommands that already
  record decisions and trigger work. The interface exposes no call that changes anything, so it cannot
  manage its own credentials, and inventing a bootstrap endpoint for that would contradict FR-012.
- The audit surface the collector uses today records device commands: its action set and its target
  column are shaped for an address and an SSH or SNMP call. It is untouched here. The constitution's
  audit obligation attaches to mutating calls, and this feature has none; the question of whether that
  surface widens or gains a sibling arrives with the feature that brings mutation.
- `03-components.md` says the interface has "the three scopes" and names them nowhere. One scope ships,
  and that sentence is corrected rather than implemented; the planner carries that as a documentation
  delta the way 004 carried the engine's packs.
- Pagination and the exact shape of a response body are left to the planner, as the contract form for
  this project type. Filtering and search are out of scope by decision rather than by omission.
- The device list is expected to be a few hundred rows for a homelab perimeter, which is why it is
  served whole rather than behind a query language. The same reasoning bounds a device's edges: a few
  hundred is a small answer, and the flat-segment case that makes it large is the one a caller most
  needs to see whole.
- No performance or latency target is set, following 002, 003 and 004, which measured none. Nothing
  here is served to a user-facing page with a budget to meet.
- The Grafana exception stands: it reads the database directly, outside this interface, and this feature
  neither serves it nor replaces it.
- An AI agent is the second consumer of these answers and the reason evidence travels with them, but the
  shape it consumes arrives in a later feature. Nothing here should make that shape harder to add.
- The Web UI, the diff engine, flooding domains and reconciliation with intent are all out of scope, and
  none of them is a prerequisite for this one.
