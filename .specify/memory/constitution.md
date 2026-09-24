<!--
Sync Impact Report (scratch, remove before committing the amendment)
Version change: 1.0.0 -> 1.1.0
Bump rationale: MINOR. Development Workflow materially expanded. No principle added, removed or
  redefined; Core Principles, Architectural Constraints and Governance are untouched.
Modified principles: none
Added sections: none
Removed sections: none
Development Workflow gains three rules. Each comes from a defect 003-identity-resolution shipped and a
review or a convergence pass then found:
  - test each path under the role the contracts assign it. 003 granted netmapper_operator the write
    rights `netmapper resolve` needs but not the reads, and every test connected as netmapper_engine,
    so the operator's own command could not run at all and nothing noticed until a security review.
  - correct the reference document in the same change as the behaviour. Three separate convergence
    passes each found an artefact still describing what had been superseded, twice in the grant matrix
    and once in a description of the computed zone.
  - test a tie-break with inputs that tie. The reduction of operator decisions was tested with one pair
    and looked deterministic, while two decisions naming the same key reduced in map iteration order and
    gave a different answer per run, against the feature's own reproducibility requirement.
Templates checked: `.specify/templates/plan-template.md` derives its Constitution Check from this file
  ("[Gates determined based on constitution file]"), so the new rules become gates with no edit to it.
Deferred items: none
-->

# NetMapper Constitution

## Core Principles

### I. Evidence Travels With The Answer

Every device, interface, edge, endpoint and finding returned by any interface MUST carry the
observation it came from, when that observation was collected, and a confidence. A link both
endpoints agree on and a link deduced from a forwarding table MUST be distinguishable in the
response. An answer stripped of its evidence and its age MUST NOT be served, at any level of
privilege, through REST, MCP or the Web UI.

Rationale: a human tolerates an unqualified fact and works around it; an agent acts on it. Without
this the tool is one more scraper.

### II. Observations Are Immutable, Everything Else Is Rebuildable

Observations, raw output and identifier claims MUST be append only: never updated, never deleted
outside eviction. Entities, edges, L2 domains, findings and intent matches MUST be derivable from
that zone alone, by replay, with no manual repair step. Operator decisions that shape resolution
(merge, split, never-merge) MUST be stored as replayable records rather than applied in place.
Snapshots MUST be immutable once closed.

Rationale: losing the computed zone costs time, never data. A parser fix must be a replay, not a
re-crawl of the building.

### III. Credentials And Reach Stay In The Collector (NON-NEGOTIABLE)

Only `collector` opens a session to a device. Only `collector` resolves a secret, in memory, for
the time of the task. `engine` and `api` MUST hold no credential and reach no device. `api` is the
only role exposed to users, agents or browsers. The database and the configuration document MUST
store secret references, never values, and no interface MUST return a secret value. Every target
MUST be checked against the active perimeter before a packet leaves.

Rationale: the part anyone can reach must never be the part that can log into every switch in the
building.

### IV. Read Only, Outward

NetMapper MUST NOT change device configuration, and MUST NOT write back to NetBox or any other
intent source. Commands sent to devices MUST come from loaded platform packs, and packs MUST
contain read-only commands. Reconciliation reports the gap between observed and declared; it never
closes it by writing.

Rationale: pushing discovered devices into the documentation destroys the difference between what
exists and what was declared, and that difference is the product.

### V. Vendor Specifics Are Data, Not Code

Fingerprint rules, recipes, templates and interface naming rules MUST live in platform packs, loaded
as data. `PackRegistry` and the parser MUST be the only components aware of a vendor. Adding a
platform MUST require no change to transports, worker pool, resolver, projector or API. A pull
request that adds a vendor name outside a pack MUST be rejected unless it also states why the pack
boundary cannot hold it.

Rationale: the day something vendor-specific leaks outside the registry, adding a platform stops
being a directory of data and becomes a patch.

## Architectural Constraints

- One Go binary, one image, three roles selected by subcommand: `collector`, `engine`, `api`.
  Splitting into separate binaries requires an amendment.
- The task frontier lives in PostgreSQL and is claimed with `FOR UPDATE SKIP LOCKED`. Collector
  state that would be lost on a crash MUST NOT accumulate in process memory.
- Raw output is stored in the object store addressed by content hash; PostgreSQL holds the
  accounting, not the bytes.
- Observation status is one of `collected`, `empty`, `unsupported`, `parse_failed`, `unreachable`,
  `denied`, and is never nullable. A disappearance in a diff MUST require a successful collection.
- Grafana reading PostgreSQL directly is a named exception, outside the API contract, read only. No
  second such exception without an amendment.
- Configuration is a YAML document posted whole, versioned, and recorded on every run.

## Development Workflow

- Every change states which principle it touches, or states that it touches none.
- Schema changes MUST preserve the four zones and the rebuildability boundary between what was
  collected and what was computed.
- A change to a parser, template or recipe MUST be verifiable by replay over stored raw output
  before it is trusted on live devices.
- New interfaces returning graph data MUST ship with evidence and freshness in the same response,
  reviewed as a gate rather than as a follow-up.
- Open design questions are recorded in `docs/`, not resolved silently in code.
- A path the contracts assign to a role MUST be tested under that role, not only under the role
  that happens to be convenient. A suite that connects as one role proves one role.
- A document that describes behaviour MUST be corrected in the same change as the behaviour, not
  only in the record of divergences. A divergence note explains why; the reference says what ships.
- A rule that settles a tie MUST be tested with inputs that actually tie. Determinism assumed on
  one input is determinism untested.

## Governance

This constitution supersedes other practices in this repository. Amendments are made by editing
this file, stating the rationale, and bumping the version: MAJOR for removing or redefining a
principle, MINOR for adding one or materially expanding guidance, PATCH for clarifications.

Reviews verify compliance with the five principles. Complexity that violates one is either
justified in writing at the point it is introduced or removed. Principle III admits no exception.

**Version**: 1.1.0 | **Ratified**: 2026-09-17 | **Last Amended**: 2026-09-24
