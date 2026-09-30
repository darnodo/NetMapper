---
description: "Task list for configurable SNMPv3 authentication and privacy"
---

# Tasks: Configurable SNMPv3 authentication and privacy

**Input**: Design documents from `/specs/006-snmpv3-protocols/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/config.md, quickstart.md

**Tests**: Included. FR-014 asks for the SNMPv3 path to be tested end to end against a real agent,
and SC-003 asks for one test per mistake of User Story 3.

**Organization**: Tasks are grouped by user story. US2 and US3 both touch `snmp.go` and `creds.go`
after US1; they do not depend on each other.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US4 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 005.

Integration tests use `internal/testutil` and skip without `NETMAPPER_TEST_DSN` and
`NETMAPPER_TEST_S3_ENDPOINT`. The new SNMP transport test skips without `NETMAPPER_TEST_SNMP`
(`127.0.0.1:1161` with the compose agent). The CI fails on any skip, so all three are set there.

Test agent users (fixed by T001, used by T010, T015, T021). Passphrases follow one pattern so the
test can build them: `auth-<user>` and `priv-<user>`, all at least 8 characters.

| User             | Auth    | Priv       | Level      |
| ---------------- | ------- | ---------- | ---------- |
| `nm-sha-aes`     | SHA     | AES        | authPriv   |
| `nm-sha256-aes`  | SHA-256 | AES        | authPriv   |
| `nm-sha512-aes256` | SHA-512 | AES-256  | authPriv   |
| `nm-sha-aes256c` | SHA     | AES-256-C  | authPriv   |
| `nm-sha256-nopriv` | SHA-256 | none     | authNoPriv |

---

## Phase 1: Setup

**Purpose**: The SNMP test agent, available to developers and CI (research R7, R10)

- [X] T001 Create deploy/snmpd/Dockerfile (`FROM alpine:3.22`, `apk add --no-cache net-snmp net-snmp-tools`, `mkdir -p /var/lib/net-snmp`, `COPY snmpd.conf /etc/snmp/snmpd.conf`, `CMD ["snmpd", "-f", "-Lo", "-C", "-c", "/etc/snmp/snmpd.conf"]`) and deploy/snmpd/snmpd.conf with `agentAddress udp:161`, `sysName nm-snmpd`, one `createUser <user> <auth> auth-<user> <priv> priv-<user>` line per user of the table above (net-snmp spellings `SHA`, `SHA-256`, `SHA-512`, `AES`, `AES-256`, `AES-256-C`; the authNoPriv user has no priv part), and one `rouser <user>` line per user (`rouser nm-sha256-nopriv auth` for the authNoPriv one). Header comment: test agent only, dummy passphrases
- [X] T002 Add service `snmpd` to deploy/compose.yaml: `build: ./snmpd`, `ports: ["1161:161/udp"]`, healthcheck `snmpget -v3 -l authPriv -u nm-sha-aes -a SHA -A auth-nm-sha-aes -x AES -X priv-nm-sha-aes 127.0.0.1 1.3.6.1.2.1.1.5.0`, interval 2s, retries 30. Update the file's header comment to mention it
- [X] T003 [P] In .github/workflows/quality.yml, start `snmpd` with postgres and garage (`up -d --wait postgres garage snmpd`), and add `NETMAPPER_TEST_SNMP: 127.0.0.1:1161` to the env of both the `go test` and `no skipped test` steps
- [X] T004 [P] Run `gitleaks dir --no-banner deploy/snmpd` locally. If any passphrase from T001 is reported, add it to the development-stack allowlist in .gitleaks.toml by exact value, with a trailing comment `# snmpd test agent`

**Checkpoint**: `docker compose -f deploy/compose.yaml up -d --wait snmpd` is healthy.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Carry the two protocols from the document to the transport, with defaults, and change
no behaviour yet. After this phase every existing test still passes.

- [X] T005 In internal/transport/transport.go, add `AuthProtocol, PrivProtocol string` to `Credential`, with a comment: protocol names as in the configuration document (`sha256`, `aes`, `none`...), set for `snmp_v3` only, never key material
- [X] T006 [P] In internal/config/config.go, add `AuthProtocol string \`yaml:"auth_protocol"\`` and `PrivProtocol string \`yaml:"priv_protocol"\`` to `CredentialSet`. In `Parse`, after `d.Discovery.fill()`, set defaults for `snmp_v3` sets only: empty `AuthProtocol` becomes `sha`, empty `PrivProtocol` becomes `aes`. Leave both empty for other kinds (validation of values is T020)
- [X] T007 [P] Create migrations/0009_snmpv3_protocols.sql (goose, same header style as 0008): Up adds `auth_protocol text NULL` and `priv_protocol text NULL` to `credential_set`, backfills `UPDATE credential_set SET auth_protocol = 'sha', priv_protocol = 'aes' WHERE kind = 'snmp_v3'`, then adds `CHECK ((kind = 'snmp_v3') = (auth_protocol IS NOT NULL))`, the same for `priv_protocol`, `CHECK (auth_protocol IN ('md5','sha','sha224','sha256','sha384','sha512'))` and `CHECK (priv_protocol IN ('none','des','aes','aes192','aes256','aes192c','aes256c'))`. Comment: the backfill is what those rows already meant (research R3); no grant change, table-level grants cover the columns and `netmapper_api` has none. Down drops both columns
- [X] T008 In internal/jobrunner/start.go, write `auth_protocol` and `priv_protocol` in the `INSERT INTO credential_set`, as `nullable(c.AuthProtocol)` and `nullable(c.PrivProtocol)` (depends on T006, T007)
- [X] T009 In internal/collector/pool.go and internal/collector/creds.go, add `AuthProtocol, PrivProtocol string` to `credSet` (after `Max`, matching the column order of the query since rows are collected by position), select `coalesce(auth_protocol, ''), coalesce(priv_protocol, '')` in the `credential_set` query, and pass both into the `transport.Credential` built in `open` (depends on T005, T007)

**Checkpoint**: `go test ./...` passes unchanged; a v3 set in a started job has `sha`/`aes` stored.

---

## Phase 3: User Story 1 - Crawl devices whose SNMPv3 user is not SHA-1/AES-128 (Priority: P1) MVP

**Goal**: the protocols named on a set are the ones used on the wire.

**Independent Test**: the transport test reads sysName from the agent with every authPriv user of
the table, each with the matching protocols.

### Tests for User Story 1

- [X] T010 [P] [US1] Create internal/transport/snmp/snmp_test.go: skip unless `NETMAPPER_TEST_SNMP` is set; parse it with `netip.ParseAddrPort`; build `snmp.New()` with `Port` from it; a helper `get(t, user, auth, priv string) (transport.RawOutput, error)` that opens a session with `transport.Credential{Kind: "snmp_v3", Username: user, AuthProtocol: auth, PrivProtocol: priv, Secret: <auth-/priv- fields>}` and runs one step with `OIDs: ["1.3.6.1.2.1.1.5.0"]`. Table test, one case per authPriv user: `nm-sha-aes` with empty protocols (the collector always passes them, but an empty value must mean the default so the transport alone is safe), `nm-sha-aes` with `sha`/`aes`, `nm-sha256-aes` with `sha256`/`aes`, `nm-sha512-aes256` with `sha512`/`aes256`, `nm-sha-aes256c` with `sha`/`aes256c`. Each expects one varbind whose value is `nm-snmpd`. Secret values are built with a test helper; check internal/secret for an existing constructor usable from another package and add a small exported one (for example `secret.FromFields(map[string]string) Secret`) only if none exists
- [X] T011 [P] [US1] In internal/config/config_test.go, add: a `snmp_v3` set with no protocol keys parses with `sha`/`aes`; each accepted `auth_protocol` value and each accepted `priv_protocol` value parses and is kept; a `ssh` and a `snmp_v2c` set keep empty protocols; the document in `TestValid` still parses to the same values (FR-009)
- [X] T012 [P] [US1] Add a test in internal/jobrunner (next to the existing start tests) that starts a job with one `snmp_v3` set naming `sha256`/`aes256`, one naming nothing, and one `ssh` set, then reads `credential_set` as the superuser: `sha256|aes256`, `sha|aes`, and NULLs for `ssh`

### Implementation for User Story 1

- [X] T013 [US1] In internal/transport/snmp/snmp.go, replace the fixed `gosnmp.SHA` and `gosnmp.AES` with two small functions using a plain `switch`: `authProtocol(name string) (gosnmp.SnmpV3AuthProtocol, error)` mapping `""` and `sha` to `SHA`, `md5`, `sha224`, `sha256`, `sha384`, `sha512` to their constants; `privProtocol(name string) (gosnmp.SnmpV3PrivProtocol, error)` mapping `""` and `aes` to `AES`, `des`, `aes192`, `aes256`, `aes192c`, `aes256c` to theirs. An unknown name returns an error from `Open` (config validation makes it unreachable, but the transport does not guess). Keep `MsgFlags = gosnmp.AuthPriv` for now (T018 adds `none`). Comment that nothing here sends anything but GET and GETBULK (FR-011)
- [X] T014 [US1] Run T010, T011 and T012 against the compose stack and make them pass

**Checkpoint**: US1 works on its own: a set naming SHA-256 reads a SHA-256 agent.

---

## Phase 4: User Story 2 - SNMPv3 user with authentication but no privacy (Priority: P2)

**Goal**: `priv_protocol: none` selects authNoPriv, and its secret needs only `auth`.

**Independent Test**: the transport test reads sysName with `nm-sha256-nopriv`, `sha256`/`none`, and
a secret holding only `auth`.

### Tests for User Story 2

- [X] T015 [P] [US2] In internal/transport/snmp/snmp_test.go, add the case `nm-sha256-nopriv` with `sha256`/`none` and a secret holding only `auth`, expecting `nm-snmpd`
- [X] T016 [P] [US2] In internal/config/config_test.go, add a `snmp_v3` set with `priv_protocol: none` that parses and keeps `none`

### Implementation for User Story 2

- [X] T017 [US2] In internal/transport/snmp/snmp.go, map `none` to `gosnmp.NoPriv` in `privProtocol`; when the privacy protocol is `none`, set `MsgFlags = gosnmp.AuthNoPriv` and leave `PrivacyPassphrase` empty, otherwise keep `AuthPriv`
- [X] T018 [US2] Run T015 and T016 and make them pass

**Checkpoint**: US1 and US2 both pass.

---

## Phase 5: User Story 3 - Find out why an SNMPv3 set does not work (Priority: P2)

**Goal**: every way of getting a v3 set wrong names its cause: at load time, before sending, or in
the denial evidence (spec SC-003).

**Independent Test**: one test per mistake: bad protocol name, protocol key on another kind,
`vault:...#field`, missing secret field, protocol mismatch, unknown user.

### Tests for User Story 3

- [X] T019 [P] [US3] In internal/config/config_test.go, add cases to `TestInvalid` with the exact messages from contracts/config.md: `auth_protocol: SHA256` → `credential set "<name>": auth_protocol must be one of md5, sha, sha224, sha256, sha384, sha512`; `priv_protocol: aes-256` → `credential set "<name>": priv_protocol must be one of none, des, aes, aes192, aes256, aes192c, aes256c`; `auth_protocol` on a `snmp_v2c` set and `priv_protocol` on a `ssh` set → `credential set "<name>": auth_protocol applies to snmp_v3 only` (resp. `priv_protocol`); a `snmp_v3` set with `secret_ref: vault:kv/data/x#auth` → `credential set "<name>": a snmp_v3 secret_ref must reference the whole secret, without #field`. A `snmp_v3` set with `vault:kv/data/x` (no `#`) stays valid
- [X] T020 [P] [US3] In internal/collector/creds_test.go, add a v3 variant of `credLab` (device answering `snmp` only, one `snmp_v3` set `v3-a` with `secret_ref: env:NM_V3`) and three cases: `NM_V3='{"auth":"a"}'` with default protocols → task `last_error` is `credential_unresolved: v3-a (missing priv)`, `l.Net.Opens()` has no `snmp` open, and the set's `cred_attempts` count is 0; `NM_V3='{"priv":"p"}'` → `(missing auth)`, same checks; `NM_V3='{"auth":"a"}'` with `priv_protocol: none` → the set is opened (one `snmp` open for `v3-a`). Check how `fake.Device` accepts SNMP credentials and extend it minimally if it cannot tell v3 fields apart
- [X] T021 [P] [US3] In internal/transport/snmp/snmp_test.go, add: `nm-sha256-aes` with `sha`/`aes` (mismatch) and user `nm-nobody` with `sha`/`aes`. Both return an error for which `errors.Is(err, transport.ErrAuth)` holds, and whose `*transport.AuthError` evidence is non-empty (wrong digest for the first, unknown user name for the second; assert on the substring gosnmp produces, checked when writing the test)

### Implementation for User Story 3

- [X] T022 [US3] In internal/config/config.go `validate`, add the four checks of T019 with those exact messages: accepted values as two package-level slices (`authProtocols`, `privProtocols`) used for both the check and the message via `strings.Join(..., ", ")`; protocol keys on a non-`snmp_v3` set; `#` in a `vault:` reference of a `snmp_v3` set. Validation runs on the values after defaults are applied, so an absent key never fails
- [X] T023 [US3] In internal/collector/creds.go `open`, right after `d.c.Secrets.Resolve` succeeds: for `snmp_v3` sets, compute the missing field (`auth` if `sec.Field("auth") == ""`, else `priv` if `cs.PrivProtocol != "none"` and `sec.Field("priv") == ""`). If one is missing: log a warning with the set, target and field name (never a value), append `cs.Name + " (missing <field>)"` to `o.unresolved`, and `continue` without counting an attempt or opening a session (research R4). `verdict` needs no change: it joins the `unresolved` entries as they are
- [X] T024 [US3] If T021 shows that the mismatch or unknown-user error is not classified as `ErrAuth` by `classify` in internal/transport/snmp/snmp.go, add the missing gosnmp error to its `case` list; otherwise leave `classify` unchanged (research R6)
- [X] T025 [US3] Run T019, T020 and T021 and make them pass

**Checkpoint**: each mistake produces its own message or record.

---

## Phase 6: User Story 4 - Configure an SNMPv3 set correctly the first time (Priority: P3)

**Goal**: the guide and the reference are enough to set up a working v3 set (SC-004).

**Independent Test**: read docs/how-to/deploy.md alone and write a valid `snmp_v3` set with an
`env:` and a `vault:` secret.

- [X] T026 [P] [US4] In docs/how-to/deploy.md section 3 "Device secrets", replace the one-line `snmp_v3` note with: `env:NAME` holds a JSON object `{"auth": "...", "priv": "..."}`; `vault:` points at the KV v2 secret without `#field`, with fields `auth` and `priv`; with `priv_protocol: none` only `auth` is needed; a missing field shows up as `credential_unresolved`. In section 4, add a `snmp_v3` set to the example document with `auth_protocol: sha256`, `priv_protocol: aes` and a `vault:` reference without `#field`
- [X] T027 [P] [US4] In specs/001-crawl-loop/contracts/config.md, add `auth_protocol` and `priv_protocol` to the example, the accepted values and defaults table, the four validation rules and the secret shape table, copied from specs/006-snmpv3-protocols/contracts/config.md, with a line saying they were added by 006
- [X] T028 [P] [US4] In README.md "Development", add `snmpd` to the compose command and `export NETMAPPER_TEST_SNMP=127.0.0.1:1161` to the test variables, and say that without it the SNMP transport test skips

**Checkpoint**: an operator can follow deploy.md to a working v3 set.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T029 [P] In test/lab/sw2.cfg, remove `snmp-server community public ro` and add `snmp-server view all iso included`, `snmp-server group nm-ro v3 priv read all`, `snmp-server user netmapper nm-ro v3 auth sha256 lab-auth-sw2 priv aes256 lab-priv-sw2` (research R9). Add the two dummy passphrases to .gitleaks.toml if gitleaks reports them
- [X] T030 [P] In test/lab/netmapper.yaml, add after `ro-snmp` a set `{name: ro-snmp-v3, kind: snmp_v3, username: netmapper, auth_protocol: sha256, priv_protocol: aes256, secret_ref: env:LAB_SNMP_V3, max_attempts_per_device: 1, perimeters: [lab]}` and add `LAB_SNMP_V3='{"auth":"lab-auth-sw2","priv":"lab-priv-sw2"}'` to the header comment's `export` line
- [X] T031 Run `gofmt -l .`, `go vet ./...` and `go test -count=1 ./...` with the three `NETMAPPER_TEST_*` variables set, and check that `go test -v ./... | grep -- '--- SKIP'` finds nothing
- [X] T032 Manual: quickstart.md section 3 on the containerlab test lab (sw2 identified over `ro-snmp-v3`, then the missing-`priv` run). Record the result in quickstart.md under a "Results" heading
- [X] T033 Manual: quickstart.md section 4 on the external lab through `~/Projets/netmapper-trial` (rebuild the image from this branch, add the `lab-snmp` set and `LAB_SNMP_V3` to its compose and config). SC-001: every device in `/v1/devices` has a hostname. Then `auth_protocol: sha`: SNMP attempts are `denied` (research R6: the evidence does not name the reason). Record the result in quickstart.md
- [X] T034 Set `**Status**: Implemented` in specs/006-snmpv3-protocols/spec.md, and note in plan.md "Open points" anything T032 or T033 contradicted

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (Phase 1)**: none. T002 depends on T001.
- **Foundational (Phase 2)**: none on Setup. T008 depends on T006 and T007; T009 on T005 and T007.
- **US1 (Phase 3)**: depends on Phase 2; T010 and T014 need the agent from Phase 1.
- **US2 (Phase 4)**: depends on T013 (same function in snmp.go).
- **US3 (Phase 5)**: depends on Phase 2 and T013. Independent of US2, except T020's `none` case,
  which needs T017.
- **US4 (Phase 6)**: documentation only; can be written any time after Phase 2, and must describe
  what US1 to US3 actually shipped.
- **Polish (Phase 7)**: after all stories.

### Parallel opportunities

- Phase 1: T003 and T004 once T001 exists.
- Phase 2: T006 and T007 together, then T008 and T009 in parallel (different files), T005 any time.
- US1: T010, T011, T012 in parallel (three packages).
- US3: T019, T020, T021 in parallel; T022, T023, T024 touch three different files.
- US4: T026, T027, T028 in parallel.
- Polish: T029 and T030 in parallel.

### Parallel example: User Story 1

```text
T010 internal/transport/snmp/snmp_test.go   agent-backed table test
T011 internal/config/config_test.go         defaults and accepted values
T012 internal/jobrunner/..._test.go         stored protocols
```

## Implementation Strategy

### MVP (User Story 1)

Phases 1, 2 and 3. That alone makes the external lab usable with `auth_protocol: sha256`, which is the
case that started the issue: T033's first half can be run right after T014.

### Incremental delivery

1. Setup + Foundational: nothing changes for users, all existing tests pass.
2. US1: non-default protocols work.
3. US3: mistakes are named. Worth doing before US2 in practice, since it is what an operator hits
   first when a set does not work.
4. US2: authNoPriv.
5. US4 and Polish: documentation, lab, manual validation.

## Phase 8: Convergence

- [X] T035 Extend `TestRepositoryDocuments` in internal/config/config_test.go to also load the complete configuration examples of the documentation: extract each fenced `yaml` block that contains `credential_sets:` from README.md and docs/how-to/deploy.md, and check that each parses with `Parse` (the README quick start example predates 006; deploy.md now carries a `snmp_v3` set) per SC-002 (partial)
