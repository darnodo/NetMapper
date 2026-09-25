# Implementation Plan: The read API (serving the graph with its evidence)

**Branch**: `005-read-api` | **Date**: 2026-09-25 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/005-read-api/spec.md`

## Summary

`netmapper api` serves the graph the first four features computed, over HTTP, behind a bearer token.
Eight read endpoints: the snapshots, the devices of one, a device with its interfaces and every edge
touching them, the findings, and the raw command output behind any observation. Every element of every
answer carries the observations it rests on and when they were collected, which is where the
constitution's first principle stops being a data layout and becomes a response contract. The
component holds `SELECT` on what it serves, `UPDATE` on one table for a last-used timestamp, and
nothing else; it opens no device session and resolves no secret. Tokens are issued and revoked from
the operator command line, because the interface exposes no call that changes anything. Details and
trade-offs are in [research.md](research.md).

## Technical Context

**Language/Version**: Go 1.27.1, same toolchain and module as 001 to 004

**Primary Dependencies**: none added. `net/http` and its `ServeMux`, which matches a method and path
wildcards since Go 1.22, is the whole of the routing. pgx/v5, minio-go and goose are already in
`go.mod`; `crypto/rand` and `crypto/sha256` are standard library

**Storage**: PostgreSQL, read only except one table. One new table (`api_token`), one new role
(`netmapper_api`), and the grants. The object store is read for the first time by something other than
the collector, through the `RawStore.Get` that 001 already wrote

**Testing**: `go test`; `httptest` against PostgreSQL and Garage from `deploy/compose.yaml`, with
snapshots built by the fake transport from 001; the containerlab topology for
[quickstart.md](quickstart.md). Every endpoint is exercised under five token states: valid, absent,
unknown, revoked, and carrying a scope the interface does not define

**Target Platform**: Linux containers (amd64, arm64), unchanged. One port is exposed for the first
time

**Project Type**: single Go binary with role subcommands; this feature adds the third long-running
role (`api`) and one operator subcommand (`token`)

**Performance Goals**: none measured (R14), consistent with 002, 003 and 004. No cache, no pagination,
no rate limit

**Constraints**: no device contacted and no secret reference resolved (FR-015); nothing written in the
collected, computed or reported zones, enforced by a role with no `INSERT` or `DELETE` anywhere and by
a `READ ONLY` transaction around every handler's queries (FR-016, R7); no credential value or
object-store key in any response at any scope (FR-014); no unauthenticated endpoint at all, including
health (FR-010, contracts/rest.md); the interface manages no credential of its own, since FR-012
leaves it no mutating call

**Scale/Scope**: one new table, one new role, one migration, two new subcommands, one new package
(`internal/api`). Eight endpoints, one scope value, five token states to test

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle / constraint                               | Status | How this plan holds it |
| ---------------------------------------------------- | ------ | ---------------------- |
| I. Evidence travels with the answer                  | Pass   | This is the feature where the principle is finally exercised. FR-002 puts the observations and their collection time on every element, FR-003 puts confidence on every edge, FR-020 puts the coverage verdict on every answer, and FR-004 makes the bytes reachable. SC-002 checks it over the whole surface rather than by sampling, which is the only way a response contract can be said to hold |
| II. Observations immutable, rest rebuildable         | Pass   | Nothing is written outside `api_token`. The role holds no `INSERT` and no `DELETE` on any zone, and every handler's reads sit in a `READ ONLY` transaction, so the guarantee is the database's rather than the handlers' (R7) |
| III. Credentials and reach stay in the collector     | Action | The interface opens no session and resolves no secret reference, and it cannot read `credential_set` at all. It does gain read-only object-store access, which is the one place this feature widens what the exposed component can touch. The reasoning is in FR-004a and R11: the bytes are evidence, the chain has to reach them, and that access opens nothing on the network. This is the single item a reviewer should weigh rather than skim |
| IV. Read only, outward                               | Pass   | No device contacted, nothing written back to any intent source |
| V. Vendor specifics are data                         | Pass   | No pack is loaded. The interface serves canonical names the parser and the projector already produced, and contains no vendor name |
| One binary, three roles by subcommand                | Pass   | `api` is the third role the constraint already names, and the first to exist. `token` joins the operator subcommands alongside `resolve`, `decide` and `project` |
| Frontier in PostgreSQL, no crash state in memory     | Pass   | The interface holds no state between requests. A killed process loses a connection pool |
| Raw output in object store by hash                   | Pass   | Read through `RawStore.Get`, addressed by the hash `observation_raw` records. Nothing is written there and no eviction path is touched |
| Status enum closed and non-null                      | Pass   | No new enum. `api_token.scopes` is deliberately not constrained in the schema (data-model.md): the interface refuses a value it does not define, so adding the second scope is a decision before it is a migration |
| Snapshots immutable once closed                      | Pass   | Nothing about a snapshot is written |
| `api` is the only role exposed to users              | Action | First time this is true of anything. The grant matrix is what bounds it, and `internal/store/roles_test.go` gains the fourth role the way it gained the projected tables in 004 |
| Config YAML posted whole, versioned, recorded on run | Pass   | No configuration change. The listen address is a process flag, like `--packs` |
| Workflow: a path is tested under its own role        | Action | Here the roles are tokens. Every endpoint is tested under all five token states, not only a valid one, for the same reason 004 tested `netmapper project` under `netmapper_operator` |
| Workflow: a tie-break is tested with inputs that tie | Action | One tie exists: a hostname or an address naming two devices (FR-001a). It is tested with inputs that actually tie, and the expected result is a refusal naming both, not an order |
| Workflow: reference documents corrected with behaviour | Action | `03-components.md` says the interface has "the three scopes" and names them nowhere. One ships. That sentence is corrected in this branch, not implemented |

Post-design re-check: still passes, with one item that should not be waved through.

**The object-store access is the only widening in this feature, and it deserves the scrutiny.** The
constitution's third principle is about reach: credentials that open a session to a device, and a path
from the exposed component to the network. Read-only access to a bucket of stored command output is
neither. But it is the first time the component anyone can reach holds a key to anything outside
PostgreSQL, and the bucket contains every command the collector ever ran, including the output of the
ones that failed authentication. Three things bound it, and all three are requirements rather than
intentions: the access is read-only (FR-004a), it is never returned (FR-014, SC-006), and it reaches
no device. If a reviewer disagrees with the trade, the alternative is in R11 and costs the feature its
point: an evidence chain that stops one link short of the thing it names.

The second thing worth re-reading is the single write. `api_token.last_used_at` is set on every
successful authentication, which makes the exposed component's role not quite read-only. R6 argues it,
data-model.md scopes the grant to one table, and R7 puts every other query behind a `READ ONLY`
transaction so this remains the only one. It is small, and it is the kind of small that is worth naming
rather than discovering.

Principles touched by this feature: I, II, III.

## Documentation deltas to carry into `docs/c4-model/`

- `03-components.md` says the interface has "bearer tokens, the three scopes". One scope ships,
  `read`, and the set is defined when there is something to mutate. Correct the sentence rather than
  invent two scopes to match it.
- `03-components.md` lists Config service and Job endpoints under `api`. Neither ships here: the
  clarification settled this feature as reads only. Say so, so the next reader does not take the table
  for the state of the code.
- `02-containers.md`'s `api` row says it holds no credentials. It holds read-only object-store access,
  which the row should say, in the same way 004 corrected the engine's packs.
- `04-data-model.md` gains `api_token` in the control plane, with the note that the value is never
  stored, and the grant matrix gains `netmapper_api`.
- `01-system-context.md` describes Grafana reading PostgreSQL directly as a named exception outside the
  API contract. That is still true and now has an API contract to be outside of; worth a sentence
  confirming the exception stands rather than leaving a reader to wonder whether this feature replaced
  it.

## Project Structure

### Documentation (this feature)

```text
specs/005-read-api/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── rest.md
├── checklists/
│   └── requirements.md
└── tasks.md              # /speckit-tasks
```

### Source Code (repository root)

```text
cmd/netmapper/
├── main.go                 # + api and token subcommand dispatch
├── api.go                  # netmapper api --listen
└── token.go                # netmapper token create|list|revoke

internal/
├── api/                    # the feature
│   ├── api.go              # the server, the mux, the read-only transaction wrapper (R1, R2, R7)
│   ├── auth.go             # token hashing, lookup, revocation, scopes, the two refusals (R4, R5, R12, R13)
│   ├── snapshot.go         # choosing the snapshot, and the envelope every answer carries (R9)
│   ├── devices.go          # the device list, naming a device, one device with its ports and edges (R10)
│   ├── evidence.go         # attaching observations and collection times to every element (R11)
│   └── raw.go              # the bytes, read from the object store (R11, FR-004a)
├── store/                  # unchanged; RawStore.Get already does what raw.go needs
└── ...                     # nothing else in internal/ changes

migrations/0008_api.sql     # api_token, the netmapper_api role, the grants
```

**Structure Decision**: one new package, `internal/api`, and `cmd/netmapper` gains two files. Nothing
in `internal/store`, `internal/entity` or `internal/graph` changes: this feature reads what they wrote
and its correctness cannot depend on editing them. That is the boundary worth keeping, because the
moment a handler needs a column that does not exist, the honest fix is a change to the feature that
owns it rather than a join invented here.

Six files rather than one for the reason 003 and 004 used: they are six topics that each fit on a
screen, and `auth.go` in particular is the one a reviewer will want to read alone.

## Known open points

| Point                                                  | Why it is open                                                                                                                                                       | Closed when                                                                                       |
| ------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| No transport security                                  | The plan serves plain HTTP. Everything here goes through the Tailnet, which carries the encryption, so terminating TLS in the process would be a second key to manage | Something outside the Tailnet needs to reach it, at which point the terminator is the question, not this process |
| The interface writes `last_used_at`                    | The one write the exposed role makes (R6). Narrow, justified, and still an exception to a component that otherwise only reads                                         | Someone decides the timestamp is not worth it, or a later feature gives the interface writes anyway |
| No pagination anywhere                                 | The clarification settled that an answer is served whole, and a homelab perimeter is a few hundred rows                                                              | A perimeter grows enough that one answer is unwieldy, which is a measurement nobody has            |
| No rate limit and no request log                       | The constitution attaches audit to mutating calls and this feature has none. A read flood is a denial of service, which nothing in this project defends against yet   | The interface leaves the Tailnet, or an agent starts polling it hard enough to notice              |
| One scope that governs everything                      | `read` covers all eight endpoints, so a token that can see the topology can also see every command the collector ran                                                  | A consumer needs the graph without the raw output, which is the first real case for a second scope |
| `GET /v1/interfaces/{device}/{name}`                   | A convenience over the device endpoint that no requirement asks for                                                                                                  | It earns its place in use, or it is dropped                                                       |

## Complexity Tracking

| Addition                                          | Why needed                                                                                                                                                  | Simpler alternative rejected because                                                                                                                  |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A fourth database role                            | The exposed component's blast radius is bounded by its grants, not by its handlers. A role with no `INSERT` and no `DELETE` makes FR-016 the database's rule | Reusing `netmapper_engine` hands the whole computed zone's write rights to the process on the port (R3)                                               |
| A `READ ONLY` transaction around every handler    | The grant alone still permits the one write R6 opens, so a stray write in a later handler would pass review                                                 | Trusting the grant is the version of this that fails silently once (R7)                                                                               |
| Tokens managed from the command line              | FR-012 leaves the interface no mutating call, so it cannot manage its own credentials without contradicting the requirement it exists under                 | A bootstrap endpoint is a second authentication mechanism for one call, guarded by a file-based secret nobody rotates (R8)                             |
| SHA-256 rather than a password hash               | A 256-bit random token has nothing to guess, so a slow KDF costs milliseconds per request and removes no attack                                             | argon2id is already available and would be the right answer for a user-chosen secret, which this is not (R4)                                          |
| Refusing an undefined scope rather than ignoring it | It is what keeps the second scope from silently widening every token that already exists                                                                   | Treating unknown values as harmless is the permissive default that makes scope checks useless the first time the set grows (R13)                      |
