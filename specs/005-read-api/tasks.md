---
description: "Task list for the read API (serving the graph with its evidence)"
---

# Tasks: The read API (serving the graph with its evidence)

**Input**: Design documents from `/specs/005-read-api/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/rest.md, quickstart.md

**Tests**: Included. [quickstart.md](quickstart.md) section 1 lists the cases this feature must pass,
and SC-002, SC-004, SC-006 and SC-008 each say "verified over the whole surface", which only a test can
do.

**Organization**: Tasks are grouped by user story so each story can be built and tested on its own.
US1 and US2 ship together in delivery (spec.md, US2 priority note), but authentication itself is in
Phase 2 because no endpoint may exist without it (FR-010).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1, US2, US3 from spec.md
- Paths are relative to the repository root. Same module and layout as 001 to 004: `cmd/` plus
  `internal/`, tests next to the code (plan.md, Project Structure).

Integration tests use `internal/testutil`. Every test that builds a snapshot through `NewLab` needs
both `NETMAPPER_TEST_DSN` and `NETMAPPER_TEST_S3_ENDPOINT` and skips without them. The server under
test connects through `testutil.As(t, db, "netmapper_api")`, never through the owner pool: a suite that
serves as the owner proves nothing about the grants (Constitution, Development Workflow).

Two things found while writing these tasks, carried below rather than resolved silently:

- **The grant list in data-model.md is one link short.** It omits `entity_claim` and
  `identifier_claim`, and the device's own evidence chain in the same document runs
  `entity → entity_claim → identifier_claim → observation`. Without them a device cannot carry its
  evidence and FR-002 fails. T004 grants them and T052 corrects data-model.md.
- **Nothing in the schema evicts a snapshot.** `snapshot.state` is `open` or `closed`, and no table or
  function records a tombstone. FR-009's "has been evicted" case has nothing to read. T053 records it
  as a divergence rather than inventing a state.

---

## Phase 1: Setup

**Purpose**: The package this feature lives in

- [X] T001 Create the package skeleton in internal/api/api.go: a `Server` struct holding the
  `*pgxpool.Pool` (connected as `netmapper_api`) and a `*store.RawStore`, a `New(db, raw) *Server`
  constructor, a `Handler() http.Handler` method returning a `http.ServeMux` with no routes yet, and a
  `writeJSON(w, status, v)` helper that sets `Content-Type: application/json`. Package comment states
  that this package opens no device session, resolves no secret reference and computes nothing
  (FR-015), and that it is the only package in the project a network caller reaches
- [X] T002 [P] Correct the comment on `rawStore()` in cmd/netmapper/collector.go: "Only the collector
  does" stops being true in this feature. Say the collector writes and `netmapper api` reads, and that
  nothing else touches the object store. The function itself is reused unchanged by T013

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schema, role, authentication, the read-only transaction, the snapshot envelope and the
evidence helper. Every endpoint in every story goes through all of them

**⚠️ CRITICAL**: No user story work can begin until this phase is complete. T005 and T006 are what
bound the only component anyone can reach; read them twice

### Schema and role

- [X] T003 Create migrations/0008_api.sql with the `api_token` table exactly as in data-model.md:
  `id bigserial PRIMARY KEY`, `name text NOT NULL UNIQUE`, `hash bytea NOT NULL UNIQUE`,
  `scopes text[] NOT NULL CHECK (cardinality(scopes) > 0)`,
  `created_at timestamptz NOT NULL DEFAULT now()`, `last_used_at timestamptz NULL`,
  `revoked_at timestamptz NULL`. Do **not** add a `CHECK` on the scope values: the interface refuses
  an undefined value (R13) and pinning the set here makes the second scope a migration before it is a
  decision (data-model.md). `ALTER TABLE api_token OWNER TO netmapper_owner`. Use goose `-- +goose Up`
  / `-- +goose Down` markers as 0007 does
- [X] T004 Add the `netmapper_api` role to migrations/0008_api.sql inside a
  `-- +goose StatementBegin` / `DO $$ ... $$` block copied from 0004_roles.sql (create `NOLOGIN`,
  swallow `duplicate_object OR unique_violation`, `GRANT USAGE ON SCHEMA current_schema()`). Then the
  grants, and nothing more:
  `GRANT SELECT ON snapshot, snapshot_judgement, projection, resolution, entity, entity_claim,
  identifier_claim, interface, interface_alias, interface_evidence, edge, edge_evidence, observation,
  observation_raw, raw_object, finding, finding_evidence, api_token TO netmapper_api` and
  `GRANT UPDATE (last_used_at) ON api_token TO netmapper_api`. `entity_claim` and `identifier_claim`
  are not in data-model.md's list and are required by its own evidence chain (see the note at the top;
  T052 corrects the document). Column-level `UPDATE` is narrower than the table grant data-model.md
  describes and costs nothing; say so in a comment. No grant on `credential_set`, `config_version`,
  `perimeter`, `seed_set`, `task`, `job`, `entity_decision` or `audit_log`
- [X] T005 Grant the operator its token rights in migrations/0008_api.sql:
  `GRANT SELECT, INSERT, UPDATE ON api_token TO netmapper_operator` and
  `GRANT USAGE ON SEQUENCE api_token_id_seq TO netmapper_operator`. No role gets `DELETE` on
  `api_token`, so a revoked token stays visible and a name is never reused (data-model.md)
- [X] T006 Add role tests to internal/store/roles_test.go beside the existing ones, connecting through
  `testutil.As(t, db, "netmapper_api")`:
  `TestAPICannotReadCredentials` (`SELECT` on `credential_set`, `config_version`, `perimeter`,
  `seed_set`, `task`, `job`, `entity_decision`, `audit_log` each fails);
  `TestAPICannotWriteAnyZone` (`INSERT`, `UPDATE` and `DELETE` on every table in T004's `SELECT` list
  fail, except `UPDATE api_token SET last_used_at = now()`, which succeeds);
  `TestAPICanOnlyTouchLastUsed` (`UPDATE api_token SET revoked_at = NULL`, `SET scopes = ...`,
  `SET hash = ...` and `DELETE FROM api_token` all fail);
  `TestOperatorCannotDeleteTokens` (as `netmapper_operator`, `DELETE FROM api_token` fails and
  `INSERT` plus `UPDATE ... SET revoked_at` succeed)

### Authentication (R4, R5, R12, R13)

- [X] T007 Create internal/api/auth.go with the token primitives: `const tokenPrefix = "nm_"`,
  `NewToken() (value string, hash []byte, err error)` (32 bytes from `crypto/rand`, value is
  `nm_` + `base64.RawURLEncoding`, hash is `sha256.Sum256` of the whole value string including the
  prefix), `HashToken(value string) []byte`, and `var Scopes = map[string]bool{"read": true}` as the
  closed set, with a comment that it is the only place a scope is defined. Exported because
  cmd/netmapper/token.go uses them (T035)
- [X] T008 Add the authentication middleware to internal/api/auth.go: `func (s *Server) authenticate(
  next http.Handler) http.Handler`. Read `Authorization`, require the exact form `Bearer nm_...`,
  hash it, and `SELECT id, scopes, revoked_at FROM api_token WHERE hash = $1`. No header, a malformed
  header, no row, or a non-null `revoked_at` all return **the same** response:
  `401` with body `{"error":"unauthenticated"}` and nothing else, written by one function so the four
  cannot drift apart (R12). Then check scopes: every value must be in `Scopes` and `read` must be
  present, otherwise `403` with `{"error":"forbidden","need":"read"}` (FR-011, R13: an undefined value
  refuses the token rather than being ignored). On success run
  `UPDATE api_token SET last_used_at = now() WHERE id = $1` outside any read transaction (R6, R7). No
  cache (R5). Wrap the **whole mux**, not individual routes, so an unknown path without a token is a
  401 and not a 404 (contracts/rest.md: no unauthenticated endpoint at all)
- [X] T009 Add test helpers in internal/api/helpers_test.go: `mintToken(t, db, name string,
  scopes ...string) string` inserting a row as the owner pool with `api.NewToken()` and returning the
  value; `revoke(t, db, name)`; `serve(t, l *testutil.Lab) *httptest.Server` building
  `api.New(testutil.As(t, l.DB, "netmapper_api"), testutil.S3(t)).Handler()`; and
  `get(t, srv, token, path) (status int, body []byte)`. Follow internal/graph/graph_test.go for how a
  lab is built, crawled and settled, including `t.Setenv("NM_SSH", "lab-ssh-secret")`

### The read-only transaction (R7)

- [X] T010 Add the transaction wrapper to internal/api/api.go:
  `type handler func(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error` and
  `func (s *Server) read(h handler) http.HandlerFunc`, which opens
  `s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})`, calls `h`, and always rolls back
  (nothing to commit). An error `h` returns that is not already written becomes `500`
  `{"error":"internal"}` with the detail logged through `slog`, never returned. Every route in every
  story is registered through `read`
- [X] T011 Write `TestHandlerWriteFailsInReadOnlyTransaction` in internal/api/api_test.go: register a
  deliberately bad handler through `read` that runs `UPDATE api_token SET last_used_at = now()` (the
  one write the role's grant permits) and assert it fails with SQLSTATE `25006`
  (`read_only_sql_transaction`). This is the only way to prove R7 is structural rather than a habit
  (quickstart: "needs a deliberately bad handler to prove")

### Snapshot envelope and choice (R9, FR-017 to FR-020)

- [X] T012 Create internal/api/snapshot.go with the envelope every answer carries:
  `type SnapshotRef struct { ID int64; ClosedAt *time.Time; State string; Verdict *string;
  Coverage *float64; HasGraph bool }` with JSON names `id`, `closed_at`, `state`, `verdict`,
  `coverage`, `has_graph`. `verdict` and `coverage` come from the `snapshot_judgement` row with
  `active = true`; a snapshot never judged serialises `"verdict": null` explicitly rather than omitting
  the key (FR-020: the key is always there). `has_graph` is true when a `projection` row exists and its
  `resolution_at` equals `resolution.computed_at` for that snapshot (R9). Add
  `loadSnapshot(ctx, tx, id) (SnapshotRef, error)` returning `errNoSnapshot` when the id does not exist,
  and `defaultSnapshot(ctx, tx) (SnapshotRef, error)` selecting the most recently closed snapshot with
  a current projection, ordered `closed_at DESC, id DESC`, whatever its verdict (FR-019)
- [X] T013 Add `pickSnapshot(ctx, tx, r *http.Request) (SnapshotRef, error)` to internal/api/snapshot.go:
  if `?snapshot=<id>` is present, parse it (not an integer is `400` `{"error":"bad_snapshot"}`),
  load exactly that one and never fall back to another (FR-018); otherwise `defaultSnapshot`. When no
  snapshot carries a graph at all, return `409` `{"error":"not_projected","snapshot":null}`. Add
  `requireGraph(w, ref) bool` writing `409` `{"error":"not_projected","snapshot":{...}}` for an open
  snapshot or one without a current projection (FR-009), and `404` `{"error":"no_such_snapshot"}` for
  an unknown id. `no_such_snapshot` is not in contracts/rest.md; T054 adds it there

### Evidence (R11, FR-002)

- [X] T014 Create internal/api/evidence.go: `type Evidence struct { ObservationID int64
  "observation_id"; CollectedAt time.Time "collected_at"; Family string "family"; Target string
  "target"; Side string "side,omitempty" }` and one loader per element kind, each taking the ids of the
  elements in one answer and returning `map[int64][]Evidence` in a single query (no query per row):
  `entityEvidence` (`entity_claim → identifier_claim → observation`, deduplicated by observation),
  `interfaceEvidence` (`interface_evidence → observation`), `edgeEvidence` (`edge_evidence →
  observation`, carrying `side`) and `findingEvidence` (`finding_evidence → observation`). Join
  `observation` on `(snapshot_id, id)`, since that is its key. Order each list by `collected_at, id`
  so answers are stable. Add `mustEvidence(kind string, id int64, ev []Evidence) error` returning an
  error when the list is empty: the handler turns that into a 500 rather than serving an element
  without its evidence (FR-002: "MUST NOT be served")
- [X] T015 Write the whole-surface evidence checker in internal/api/evidence_test.go:
  `checkEvidence(t, body []byte)` decodes the JSON into `any`, walks it, and fails for any object under
  `devices`, `interfaces`, `edges` or `findings` (and the top-level device object of
  `/v1/devices/{name}`) that lacks a non-empty `evidence` array whose every entry has
  `observation_id` and a non-zero `collected_at`, or that lacks a `confidence` whose value is in the
  set for its kind (FR-002a): devices `strong`|`weak`, interfaces `described`|`revealed`, edges
  `both_ends`|`one_end`|`direct`, findings `direct`|`derived`. Also assert every answer has a
  top-level `snapshot` object carrying the `verdict` key. Add `TestCheckEvidenceCatchesMissing`,
  feeding it three hand-written bodies, one bare interface, one interface with evidence and no
  `confidence`, one edge with `confidence: "maybe"`, and asserting each fails (SC-002: "an answer
  containing one element without evidence fails the suite"), using a `testing.TB` fake so the failure
  is observed, not fatal

### Process wiring

- [X] T016 Create cmd/netmapper/api.go with `cmdAPI`: flag `--listen` defaulting to `:8080`, then
  `rawStore()` (missing `NETMAPPER_S3_*` is exit 2 like the collector), `connect(ctx)` (exit 1 on
  failure), `raw.Client.BucketExists(ctx, raw.Bucket)` so an unreachable object store is exit 1 at
  startup (contracts/rest.md), then `http.Server{Addr, Handler: api.New(db, raw).Handler(),
  ReadHeaderTimeout: 10 * time.Second}`, shut down with `srv.Shutdown` when `ctx` is cancelled. Log
  `api started` with the listen address and never the S3 keys
- [X] T017 Register `"api": cmdAPI` and `"token": cmdToken` in the `commands` map of
  cmd/netmapper/main.go, and add both to the usage line and the package comment. `cmdToken` is written
  in T035; until then leave a stub returning `exitInvalid` so the binary builds

**Checkpoint**: migrations apply, role tests pass, a request with a valid token to an unregistered path
is a 404 and without one is a 401. User stories can start

---

## Phase 3: User Story 1 - A device, its ports and its cables, each with what it rests on (Priority: P1) 🎯 MVP

**Goal**: `GET /v1/devices`, `GET /v1/devices/{name}`, `GET /v1/observations/{id}`,
`GET /v1/observations/{id}/raw/{step}` and `GET /v1/interfaces/{device}/{name}`, each element carrying
its evidence and collection time

**Independent Test**: crawl and project a lab snapshot, ask for one device by hostname, confirm its
ports, cables and addresses each carry evidence; follow one `observation_id` to its raw bytes

### Tests for User Story 1

> Write these first and see them fail. Every test runs `checkEvidence` (T015) on every 200 body.

- [X] T018 [P] [US1] Write naming tests in internal/api/devices_test.go: the same device is reachable
  by its device key, by its hostname and by an address in its `targets`, and all three answers carry
  the same `device_key` (FR-001a, FR-001b); a tie built with inputs that actually tie, two
  `testutil.FakeOS` devices with the **same name and different serials**, so resolution produces two
  entities sharing a hostname, returns `409` with `candidates` naming both keys, sorted, and the test
  runs the request several times and asserts the same body each time (FR-001a, SC-001a, Constitution
  tie-break rule); an address tie, the shape a VRRP or HSRP virtual IP produces when the pair fails
  over between the `find` and `scrape` tasks, so two boxes with contradicting serials answered on one
  address in one snapshot: after the crawl, use the owner pool to append one address to the `targets`
  of a second entity, then `/v1/devices/<that address>` returns `409` with both keys sorted, the same
  body on every repeat. Comment the test with that real-world origin. The tie is inserted rather than
  crawled because the fake network shows one device per address whatever the transport, and the rule
  under test is the API's, not resolution's; editing the computed zone in a test is legitimate since
  it is rebuildable (Principle II). A device key from snapshot A asked of snapshot B where it does not exist is `404`
  `no_such_device`, not an answer from A (FR-018, edge case)
- [X] T019 [P] [US1] Write device-content tests in internal/api/devices_test.go: `/v1/devices` carries
  `device_key`, `hostname`, `platform`, `targets`, `weak` and `evidence` for each device and **no**
  `interfaces` or `edges` key (FR-001c, SC-001b); a device on a flat segment, one port with neighbour
  entries to at least four other devices, returns every edge on that port (FR-001d); an agreed link has
  `confidence: "both_ends"` and evidence on both sides, a one-sided one has `"one_end"` and one side
  (FR-003, SC-003); an interface revealed only by a neighbour has `source: "neighbour"`, one the device
  described has `"device"` (FR-005); an interface a neighbour spelled differently (`p1` vs `port1`,
  the case 004's T002 built) carries both spellings in `aliases` (FR-005); an edge to a far end no
  entity accounts for is returned with its `unknown:` reference (edge case); a weakly identified device
  has `weak: true` (FR-006, US1-4)
- [X] T020 [P] [US1] Write snapshot-choice tests in internal/api/devices_test.go: with no `?snapshot`
  the answer comes from the most recently closed projected snapshot and names it (FR-017, FR-019); a
  newer closed snapshot whose `projection` row was deleted by the owner pool (simulating the sweep not
  having reached it) is not the default, and naming it explicitly returns `409 not_projected` (FR-009,
  FR-019 edge case); a snapshot whose `resolution.computed_at` was moved forward so the projection is
  stale is treated the same way (R9); an open snapshot named explicitly is `409 not_projected`; an
  unknown id is `404 no_such_snapshot`; `?snapshot=abc` is `400`
- [X] T021 [P] [US1] Write evidence-chain tests in internal/api/raw_test.go: take an `observation_id`
  from an interface's evidence, `GET /v1/observations/{id}` lists its commands with `step`, `command`
  and hex `hash`, and `GET /v1/observations/{id}/raw/{step}` returns `200`,
  `Content-Type: application/octet-stream` and bytes equal to `RawStore.Get` of that hash (FR-004,
  SC-006a, US1-3); an unknown observation is `404 no_such_observation`; an unknown step is
  `404 no_such_step`; the response body of the observation endpoint contains no object-store key
  string (`raw/sha256/`) and no bucket name (FR-004a)
- [X] T022 [P] [US1] Write `TestInterfaceEndpoint` in internal/api/devices_test.go: `/v1/interfaces/
  {device}/{name}` returns the same interface object, aliases, evidence and edges as the matching
  element of `/v1/devices/{device}`, and an unknown port is `404 no_such_interface`

### Implementation for User Story 1

- [X] T023 [US1] Implement device naming in internal/api/devices.go: `resolveDevice(ctx, tx,
  snapshotID, name) (entityIDs []int64, keys []string, err error)` as one query trying
  `device_key = $2`, then `attributes->>'hostname' = $2`, then `attributes->'targets' ? $2`, in that
  order, stopping at the first tier that matches anything (R10). Exact, case-sensitive match (R10
  rejects guessing). Return keys sorted so the 409 body is deterministic. "More than one match" is
  decided in one place shared by the hostname and address tiers, not once per tier, so T018's two tie
  tests cover one refusal path rather than two copies that can drift
- [X] T024 [US1] Implement `GET /v1/devices` in internal/api/devices.go: `pickSnapshot`,
  `requireGraph`, then every `entity` of the snapshot ordered by `device_key` with `device_key`,
  `hostname`, `platform` and `targets` read from `attributes` (the JSON names internal/entity/entity.go
  uses), `weak`, `confidence` (`weak` when `weak` is true, else `strong`, FR-002a), `first_seen`,
  `last_seen`, and `evidence` from `entityEvidence`. Response
  `{"snapshot": {...}, "devices": [...]}`. No interfaces and no edges (FR-001c). No filtering and no
  query parameters beyond `snapshot`
- [X] T025 [US1] Implement `GET /v1/devices/{name}` in internal/api/devices.go: `pickSnapshot`,
  `requireGraph`, `resolveDevice` (zero is `404 {"error":"no_such_device"}`, more than one is
  `409 {"error":"ambiguous","candidates":[...]}`), then the device as in T024 plus `interfaces` and
  `edges`. Each interface: `canonical_name`, `source`, `description`, `admin_state`, `oper_state`,
  `speed_bps`, `mtu`, `mac`, `confidence` (`described` for `source = 'device'`, `revealed` for
  `'neighbour'`, FR-002a), `first_seen`, `last_seen`, `aliases` (`[{spelling, source}]` from
  `interface_alias`) and `evidence`. Each edge touching the device, meaning `from_entity_id` or
  `to_entity_id` equals it (that covers `l1_link` on its ports and `has_address`): `name`, `type`,
  `from_ref`, `to_ref`, `confidence` (the column as stored, FR-002a), `attributes`, `first_seen`, `last_seen` and `evidence` with
  `side`. No `LIMIT` anywhere (FR-001d). Order interfaces by `canonical_name` and edges by `name`
- [X] T026 [US1] Implement `GET /v1/interfaces/{device}/{name}` in internal/api/devices.go by reusing
  the T025 loaders filtered to one `canonical_name`, not by writing a second query set. Edges are those
  whose `from_interface_id` or `to_interface_id` is that interface
- [X] T027 [US1] Create internal/api/raw.go with `GET /v1/observations/{id}`: the `observation` row
  (`id`, `snapshot_id`, `collected_at`, `target`, `transport`, `platform`, `recipe_id`,
  `fact_family`, `status`, `detail`) and `commands` from `observation_raw` (`step` from `step_id`,
  `command`, `hash` as lowercase hex, `size` from `raw_object`), wrapped in the envelope of the
  observation's own snapshot. Do not return `parsed`: it is not evidence the contract names and can be
  large. `404 {"error":"no_such_observation"}` when absent
- [X] T028 [US1] Add `GET /v1/observations/{id}/raw/{step}` to internal/api/raw.go: look up the hash in
  `observation_raw` inside the read transaction, then call `s.raw.Get(ctx, hash)` and write the bytes
  with `Content-Type: application/octet-stream`. A missing row is `404 {"error":"no_such_step"}`; an
  object-store error is a 500 whose logged detail may include the key and whose body never does
  (FR-004a, FR-014). Never call `Put` or any other write method
- [X] T029 [US1] Register the five US1 routes in `Server.Handler` in internal/api/api.go, each through
  `s.read(...)`, using method-and-path patterns (`GET /v1/devices`, `GET /v1/devices/{name}`,
  `GET /v1/interfaces/{device}/{name}`, `GET /v1/observations/{id}`,
  `GET /v1/observations/{id}/raw/{step}`). An `{id}` that is not an integer is `400`

**Checkpoint**: US1 tests pass under a valid token; quickstart sections 2 and 3 work against the lab

---

## Phase 4: User Story 2 - Only a holder of the right token gets an answer (Priority: P2)

**Goal**: the operator can issue, list and revoke tokens, and every endpoint refuses every token state
except a valid `read` one, without leaking anything and without writing anything

**Independent Test**: call every endpoint with no token, a malformed header, an unknown token, a revoked
token and a token with an undefined scope; confirm the four 401s are byte-identical and the 403 differs

### Tests for User Story 2

- [X] T030 [P] [US2] Write token CLI tests in cmd/netmapper/token_test.go, following
  cmd/netmapper/project_test.go for running a subcommand against the test database as
  `netmapper_operator`: `create` prints one line starting `nm_` and the stored `hash` equals
  `sha256` of it while no column contains the value (FR-013, R4); `list` output contains no `nm_`
  string (FR-013); a duplicate name, `--scope admin` and `revoke` of an unknown name each exit 2;
  `revoke` sets `revoked_at` and a second `revoke` of the same name exits 2 rather than moving the
  timestamp
- [X] T031 [P] [US2] Write `TestEveryEndpointUnderEveryTokenState` in internal/api/auth_test.go as one
  table: the eight endpoint paths (with a real device, observation, step and interface from the lab)
  crossed with the states no header, `Authorization: Basic x`, `Bearer nm_unknown`, a revoked token, a
  token with `scopes = {admin}`, a token with `scopes = {read, admin}`, and a valid `read` token.
  Assert: the first four give `401` with byte-identical bodies equal to `{"error":"unauthenticated"}`
  (FR-010, SC-004, R12); both undefined-scope tokens give `403` `{"error":"forbidden","need":"read"}`,
  so a known-plus-unknown set is refused and not treated as permissive (FR-011, SC-005, R13); the valid
  token gives `200` or the expected status
- [X] T032 [P] [US2] Write the probing tests in internal/api/auth_test.go: without a token,
  `/v1/devices/<existing>` and `/v1/devices/nonesuch`, `/v1/observations/<existing>` and
  `/v1/observations/999999999`, and `/healthz`, `/version` and `/` all return the identical 401
  (SC-004, contracts/rest.md: no unauthenticated endpoint); revoking a token between two requests on
  the same server makes the second one 401 with no wait (FR-021, R5, US2-4); `last_used_at` is set
  after a successful call and not after a refused one
- [X] T033 [P] [US2] Write `TestNoSecretInAnyResponse` in internal/api/boundary_test.go: set `NM_SSH`
  and `NM_SNMP` to distinctive values, crawl, then hit every endpoint (including every raw step of every
  observation) and assert no body contains either secret value, either `secret_ref` string
  (`env:NM_SSH`, `env:NM_SNMP`), the S3 access key or the S3 secret key, or any `nm_` token value
  (FR-013, FR-014, SC-006). Whole surface, not only where a secret might be expected
- [X] T034 [P] [US2] Write `TestServingWritesNothing` in internal/api/boundary_test.go: compute a
  checksum per table over every row of `observation`, `observation_raw`, `raw_object`,
  `identifier_claim`, `snapshot`, `snapshot_judgement`, `resolution`, `entity`, `entity_claim`,
  `projection`, `interface`, `interface_alias`, `interface_evidence`, `edge`, `edge_evidence`,
  `finding`, `finding_evidence` (as owner, `md5(string_agg(t::text, '|' ORDER BY t::text))`), serve
  every endpoint under every token state, recompute, assert equal (FR-016, SC-008, US2-5)

### Implementation for User Story 2

- [X] T035 [US2] Create cmd/netmapper/token.go with `cmdToken` dispatching `create`, `list` and
  `revoke`, replacing the T017 stub. `create --name N --scope S` (repeatable `--scope`, at least one)
  refuses any scope not in `api.Scopes` with exit 2, calls `api.NewToken`, inserts `name`, `hash`,
  `scopes`, and prints the value alone on stdout, once. A unique violation on `name` is exit 2
- [X] T036 [US2] Add `list` to cmd/netmapper/token.go: tab-separated `name`, `scopes` joined by `,`,
  `created_at`, `last_used_at` or `-`, `revoked_at` or `-`, ordered by `name`. Never select `hash`
- [X] T037 [US2] Add `revoke --name N` to cmd/netmapper/token.go:
  `UPDATE api_token SET revoked_at = now() WHERE name = $1 AND revoked_at IS NULL`; zero rows affected
  is exit 2 with `no active token named N` on stderr. No `DELETE` (data-model.md)

**Checkpoint**: US1 and US2 together are the deliverable; quickstart sections 5 and 6 pass

---

## Phase 5: User Story 3 - Whether to trust what was just read (Priority: P3)

**Goal**: `GET /v1/snapshots`, `GET /v1/snapshots/{id}` and `GET /v1/findings`

**Independent Test**: judge snapshots into published, degraded and quarantined; read each and confirm
the verdict, coverage and baseline are there; read the findings of one with their evidence

### Tests for User Story 3

- [X] T038 [P] [US3] Write snapshot tests in internal/api/snapshot_test.go: `/v1/snapshots` lists every
  snapshot newest first with `has_graph` correct for a projected, an unprojected and an open one;
  `/v1/snapshots/{id}` carries `classification`, `coverage`, `baseline_snapshot_id`,
  `baseline_devices`, `carried_over`, `reached`, `thresholds` and `breakdown` (FR-007, US3-1)
- [X] T039 [P] [US3] Write `TestQuarantinedSnapshotIsServed` in internal/api/snapshot_test.go,
  building the quarantine the way `TestQuarantinedSnapshotIsProjected` in internal/graph/graph_test.go
  does (two crawls, the second reaching fewer devices): with no `?snapshot`, `/v1/devices` answers from
  the quarantined snapshot and its envelope carries `"verdict":"quarantined"` and the coverage figure
  (FR-019, FR-020, SC-009, US3-3)
- [X] T040 [P] [US3] Write findings tests in internal/api/findings_test.go: a lab device refusing
  every credential set raises a `credential_denied` finding, and `/v1/findings` returns it with
  `category`, `domain`, `severity`, `subject_ref`, `state`, `detail` and non-empty `evidence`
  (FR-008, US3-2); findings of another snapshot are not included (FR-018)

### Implementation for User Story 3

- [X] T041 [US3] Add `GET /v1/snapshots` to internal/api/snapshot.go: every snapshot as a
  `SnapshotRef`, ordered `id DESC`, in `{"snapshots": [...]}`. This endpoint lists snapshots rather
  than answering from one, so it carries no top-level `snapshot` envelope; T015's checker must allow
  that for this path only, and T054 states it in contracts/rest.md
- [X] T042 [US3] Add `GET /v1/snapshots/{id}` to internal/api/snapshot.go: the envelope plus a
  `judgement` object from the active `snapshot_judgement` row with the columns T038 names, plus
  `computed_at` and `gate_version`; `"judgement": null` when never judged; `404 no_such_snapshot` when
  absent. Served for open and unprojected snapshots too, since a verdict does not need a graph
- [X] T043 [US3] Create internal/api/findings.go with `GET /v1/findings`: `pickSnapshot`, then every
  `finding` of that snapshot ordered by `id`, with the columns T040 names and `evidence` from
  `findingEvidence`, and `confidence` from a map in findings.go: `unknown_platform`, `parse_failed`,
  `credential_denied` are `direct`; `identity_conflict`, `link_disagreement` are `derived` (FR-002a).
  A category missing from the map is an error that returns 500, not a guessed value, so a sixth
  category cannot ship without a decision. Findings exist before projection, so this does **not**
  call `requireGraph`; an open snapshot named explicitly is still answered, with its `state` in the
  envelope
- [X] T044 [US3] Register the three US3 routes in `Server.Handler` in internal/api/api.go through
  `s.read(...)`, and add all three to the endpoint table of T031 so they are covered by every token
  state and by T033 and T034

**Checkpoint**: all eight endpoints exist and every token-state, evidence and boundary test covers them

---

## Phase 6: Polish and cross-cutting concerns

**Purpose**: the reference documents corrected in the same change as the behaviour (Constitution,
Development Workflow), and the lab run

- [X] T045 [P] Correct docs/c4-model/03-components.md: replace "bearer tokens, the three scopes" with
  one `read` scope and a sentence saying the set is defined when there is something to mutate (plan.md
  documentation deltas)
- [X] T046 [P] In docs/c4-model/03-components.md, mark the Config service and Job endpoints under `api`
  as not built: this feature serves reads only, configuration and jobs stay on the operator CLI
- [X] T047 [P] Correct the `api` row of docs/c4-model/02-containers.md: it holds no device credential
  and it does hold read-only object-store access for raw output
- [X] T048 [P] Add `api_token` to the control plane in docs/c4-model/04-data-model.md (the value is
  never stored, only its SHA-256), and add `netmapper_api` to the grant matrix with exactly the grants
  of T004 and T005
- [X] T049 [P] Add one sentence to docs/c4-model/01-system-context.md confirming the Grafana exception
  stands alongside the new API contract rather than being replaced by it
- [X] T050 Run `go vet ./...` and `go test ./...` with the compose stack up and both test variables
  set; fix anything that fails. Record the result, including any test that skipped
- [X] T051 Run quickstart.md sections 2 to 6 against the containerlab topology and record anything that
  differs from the expected output in quickstart.md's "Divergences recorded during implementation"

### Corrections found while generating tasks

- [X] T052 Correct the `netmapper_api` grant block in data-model.md to include `entity_claim` and
  `identifier_claim`, and to show `UPDATE (last_used_at)` on `api_token` if T004 kept the column-level
  grant. Say why in one sentence: the device's evidence chain in the same document needs them
- [X] T053 Record in quickstart.md's divergences, and as a known open point in plan.md, that FR-009's
  "has been evicted" case has nothing to read: no eviction or tombstone exists in the schema. The API
  handles `open` and `closed`; the evicted answer arrives with the feature that evicts
- [X] T054 Add to contracts/rest.md the error bodies the tasks introduced and the contract did not
  list: `400 bad_snapshot`, `404 no_such_snapshot`, `no_such_observation`, `no_such_step`,
  `no_such_interface`, `500 internal`; the `has_graph` field of the envelope; that `/v1/snapshots`
  carries no envelope; that `?snapshot` also applies to `/v1/devices/{name}` and
  `/v1/interfaces/{device}/{name}`; that a never-judged snapshot has `"verdict": null`; and the `confidence` field on every device,
  interface, edge and finding with its value set per kind (spec FR-002a)

---

## Dependencies and execution order

### Phase dependencies

- **Setup (Phase 1)**: none
- **Foundational (Phase 2)**: after Setup; blocks every story
- **US1 (Phase 3)**: after Phase 2
- **US2 (Phase 4)**: after Phase 2. Its CLI tasks (T030, T035 to T037) are independent of US1; its
  whole-surface tests (T031 to T034) need the US1 routes to have something to call, and pick up US3's
  routes through T044
- **US3 (Phase 5)**: after Phase 2; independent of US1 except that T039 reads `/v1/devices`
- **Polish (Phase 6)**: T045 to T049 and T052 to T054 can start any time; T050 and T051 last

### Within Phase 2

T003 → T004 → T005 → T006. T007 → T008. T001 → T010 → T011. T012 → T013. T014 → T015. T009 after
T007 and T010. T016 and T017 after T001 and T008.

### Within each story

Tests first and failing, then implementation. In US1: T023 → T024 → T025 → T026, T027 → T028, all
before T029. In US3: T041 and T042 before T043, then T044.

### Parallel opportunities

- T002 alongside T001
- In Phase 2, the migration chain (T003 to T006), the auth chain (T007, T008), the envelope chain
  (T012, T013) and the evidence chain (T014, T015) touch different files
- All US1 test tasks (T018 to T022); T027 and T028 (raw.go) alongside T023 to T026 (devices.go)
- US2's CLI (T035 to T037) alongside all of US1
- All US3 test tasks (T038 to T040)
- All documentation tasks T045 to T049 and T052 to T054

---

## Parallel example: User Story 1

```bash
# Tests, all at once:
Task: "Naming tests in internal/api/devices_test.go"            # T018
Task: "Evidence-chain tests in internal/api/raw_test.go"         # T021

# Implementation, two files at once:
Task: "Device naming and endpoints in internal/api/devices.go"   # T023 to T026
Task: "Observation and raw endpoints in internal/api/raw.go"     # T027, T028
```

---

## Implementation strategy

### MVP (US1 plus US2, delivered together)

1. Phase 1 and Phase 2
2. Phase 3 (US1): a device by hostname, with its ports, cables and evidence, down to the bytes
3. Phase 4 (US2): the token CLI and the five-state matrix. The spec ships these two together, and the
   authentication is already in place from Phase 2, so this phase is mostly proof
4. Stop and run quickstart sections 2, 3, 5 and 6 against the lab

### Then

5. Phase 5 (US3): snapshots and findings, quickstart section 4
6. Phase 6: the documents, and the full quickstart

---

## Notes

- [P] tasks touch different files and depend on nothing incomplete
- Every handler goes through `s.read`, and every 200 body in every test goes through `checkEvidence`
- The one write in the process is `last_used_at`, in T008. If a later task seems to need another,
  stop: that is a spec change, not an implementation detail
- Commit after each task or logical group
