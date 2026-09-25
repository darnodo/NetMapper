# Research: The read API (serving the graph with its evidence)

Phase 0 of [plan.md](plan.md). Each entry is a decision, why it was taken, and what was rejected. The
five clarifications in [spec.md](spec.md) settled scope; these settle how it is built.

## R1. Where the interface runs

**Decision**: a fourth subcommand, `netmapper api`, in the same binary, listening on an address given by
a flag. The work lives in a new package, `internal/api`.

**Rationale**: the constitution's architectural constraints already name three roles selected by
subcommand, `collector`, `engine` and `api`, so this is the third of a set that was declared from the
start rather than a new shape. Splitting it into a second binary would need an amendment and buy
nothing: the code it shares with the others is the store package and the schema.

**Alternatives considered**: a separate binary, which the one-binary constraint rules out without an
amendment; folding the interface into the engine, which would put a listener on the component that
holds the computed zone's write rights and is precisely what Principle III separates.

## R2. HTTP with the standard library and nothing else

**Decision**: `net/http` alone. Routing with `http.ServeMux`, which since Go 1.22 matches a method and
path patterns with wildcards (`GET /v1/devices/{name}`). No router, no framework, no middleware
library.

**Rationale**: the surface is a handful of read endpoints. The standard library's mux does method
matching and path variables, which is the whole of what a router was needed for. Adding a dependency
here would be the first one this project takes for convenience rather than for a protocol it cannot
implement itself, and the decision ladder says a short thing that does one job beats a framework.

**Alternatives considered**: chi or gorilla/mux, which are small and still a dependency for path
parsing the standard library now does; a generated server from an OpenAPI document, which is a build
step, a code generator and a schema language for eight endpoints.

## R3. A fourth database role

**Decision**: `netmapper_api`, created in the same way 0004 creates the other three, holding `SELECT`
on what it serves and nothing else, plus `UPDATE` on one table for R6.

**Rationale**: this is the component anyone can reach, so the grant matrix is where the damage of a
compromise is bounded, not the code. A role with no `INSERT` and no `DELETE` on any zone makes FR-016
and SC-008 statements the database enforces rather than statements the handlers promise. It also makes
the constitution's "engine and api hold no credential" testable the way 003's and 004's role tests are.

**Alternatives considered**: reusing `netmapper_engine`, which holds write rights on the whole computed
zone and would hand them to the exposed process; reusing `netmapper_operator`, which can insert
decisions and trigger work.

## R4. Tokens: random, hashed with SHA-256, looked up by hash

**Decision**: a token is 32 random bytes from `crypto/rand`, shown once as `nm_` plus its base64url
form. The database stores the SHA-256 of the presented string and nothing else. A request is
authenticated by hashing what it presented and looking that up.

**Rationale**: a slow password hash exists to make a low-entropy secret expensive to guess. A 256-bit
random token has no guessable structure, so argon2 or bcrypt would add tens of milliseconds to every
request and remove no attack. SHA-256 keeps the stored form irreversible, which is what FR-013 asks,
and makes the lookup a single indexed equality rather than a scan that hashes every row.

**Alternatives considered**: argon2id, already available through `golang.org/x/crypto` and rejected
above; storing a prefix in clear text to index on, which is unnecessary when the hash itself is the
index; JWTs, which move the decision to a signing key, cannot be revoked without a list anyway, and
would be the project's first token format nobody can read.

## R5. Revocation is checked on every request

**Decision**: `api_token` carries `revoked_at`. Every request reads the row, and a revoked one is
refused. No cache.

**Rationale**: FR-021 asks that revoking take effect on the next call rather than at an expiry. Any
cache is a window in which a revoked token still works, and the only thing a cache would buy is one
indexed lookup per request on a homelab.

**Alternatives considered**: a short-lived in-process cache, which trades the one guarantee this
requirement asks for against a cost nothing has measured.

## R6. The one write, and why it is not a violation

**Decision**: a successful authentication sets `api_token.last_used_at`. The `netmapper_api` role holds
`UPDATE` on `api_token` and on no other table.

**Rationale**: FR-012 forbids this interface from writing the collected, computed or reported zones,
and this touches none of them: `api_token` is control plane, it is the interface's own bookkeeping, and
knowing which tokens are dead is how an operator prunes them. The grant is one table wide, which is the
narrowest form the database can express.

This is the single place the exposed component writes anything, and it is worth a reviewer's attention
for that reason alone.

**Alternatives considered**: dropping `last_used_at`, which keeps the role strictly read-only and
leaves an operator no way to tell a live token from a forgotten one; writing it from a different
process, which needs a queue for a timestamp.

## R7. A read-only transaction per request

**Decision**: every query runs inside a transaction opened `READ ONLY`. Authentication, with its one
write, happens before it.

**Rationale**: it makes FR-016 structural. A handler that grows a stray write later fails at runtime
against the database rather than passing review, which is a stronger guarantee than the grant alone,
since the grant would still allow the `api_token` update from inside a handler.

**Alternatives considered**: relying on the grants alone, which permits exactly one unintended write,
the one R6 opens.

## R8. Issuing and revoking tokens from the command line

**Decision**: `netmapper token create|list|revoke`, run as `netmapper_operator`, alongside `resolve`,
`decide` and `project`. `create` prints the value once and never again.

**Rationale**: FR-012 leaves the interface no call that changes anything, so it cannot manage its own
credentials, and a bootstrap endpoint that could would contradict the requirement it is meant to
respect. The operator command line is where this project already puts things an operator does.

**Alternatives considered**: a bootstrap endpoint guarded by a file-based secret, which is a second
authentication mechanism for one call; seeding a token from the configuration document, which would put
a credential in a document CI posts.

## R9. Choosing the snapshot

**Decision**: one query picks the most recently closed snapshot carrying a `projection` row whose
`resolution_at` matches its `resolution`, ordered by `closed_at` then `id`. A caller naming a snapshot
gets that one, whatever state it is in, with an answer that says what state that is.

**Rationale**: FR-019 as clarified. Matching `resolution_at` matters and is not pedantry: a snapshot
whose entity set was replaced has a `projection` row pointing at a set that no longer exists until the
sweep catches up, and serving from it would answer from a graph mid-rebuild.

**Alternatives considered**: the most recently closed snapshot regardless, which returns "no graph" for
the seconds between closing and projection; the most recently published one, which the clarification
rejected because a consumer that never sees a quarantined snapshot cannot tell a quarantine from an
outage.

## R10. Naming a device

**Decision**: one query tries the device key, then the hostname in `attributes->>'hostname'`, then the
addresses in `attributes->'targets'`, in that order, and returns every match. One match answers; more
than one is refused with all of them named; none is a plain absence.

**Rationale**: FR-001a. The order matters because the key is the only form guaranteed unique, so a
caller who used it never gets an ambiguity refusal. Refusing rather than choosing is the same rule
identity resolution applies to a contradicting component and the projector applies to an ambiguous
chassis identifier: a wrong answer nobody can see is worse than a refusal.

**Alternatives considered**: matching all three at once and preferring the key on a tie, which gives
the same answer with a rule that has to be read twice; case-insensitive hostname matching, which is a
guess about what devices report and can be added without changing the contract.

## R11. The shape of an answer, and where the bytes are

**Decision**: every element of an answer carries an `evidence` array of `{observation_id, collected_at,
family, target}`, and the bytes live behind a separate endpoint keyed by observation and step. The
device endpoint embeds interfaces and edges; nothing else is embedded.

**Rationale**: FR-002 wants the evidence and its age with the element, which the array gives without
inlining kilobytes of command output into every device. FR-004 wants the bytes reachable from the
interface, which the endpoint gives. Embedding raw output in the device answer would make a four-port
switch a megabyte.

**Alternatives considered**: inlining the bytes, rejected on size; returning only a hash, which the
clarification rejected because it stops the chain one link short.

## R12. Telling the three refusals apart

**Decision**: no token, an unknown token and a revoked token all return the same refusal, carrying no
detail. A token that authenticates but whose scopes do not cover the call returns a different one.
Neither reveals whether what was asked for exists.

**Rationale**: FR-010 and FR-011 together. The first three are indistinguishable on purpose: telling a
caller that their token is known but revoked tells an attacker that a guessed token was once real. The
fourth is distinguishable on purpose, because a caller holding a valid token needs to know the problem
is their scope rather than their credentials.

**Alternatives considered**: distinguishing unknown from revoked, which leaks token validity for no
operational gain, since the operator who revoked it already knows.

## R13. Scopes with one value

**Decision**: `api_token.scopes` is a text array. `read` is the only value this feature defines. A token
whose array is empty, or carries a value the interface does not define, is refused.

**Rationale**: FR-011 as clarified. Refusing an unknown value rather than ignoring it is what keeps a
second scope from silently widening tokens that already exist, which is the failure mode of every
permissive scope check.

**Alternatives considered**: a boolean column, which cannot grow; accepting unknown values as
harmless, rejected above.

## R14. Scale and cost

**Decision**: nothing measured, no cache, no pagination, no rate limit.

**Rationale**: the same reasoning 002, 003 and 004 used. A homelab perimeter is a few hundred devices
read by one engineer and, later, one agent. The clarification on flat segments already decided that an
answer is served whole.

## R15. Testing

**Decision**: `httptest` against a real database and object store from `deploy/compose.yaml`, driven by
the lab helpers from 001, plus the containerlab topology for the quickstart. Every endpoint is
exercised under a token of each kind: valid, absent, unknown, revoked, and wrong scope.

**Rationale**: the constitution's workflow rule about testing a path under its own role is why 004
caught its grant gaps, and here the roles are tokens. A suite that only ever presents a valid token
proves one of five cases.
