# Research: Configurable SNMPv3 authentication and privacy

Phase 0 of [plan.md](plan.md). Each entry: decision, rationale, alternatives considered.

## R1. Library support

**Decision**: keep gosnmp v1.44.0, already a dependency. It exposes every protocol the spec lists:
`MD5`, `SHA`, `SHA224`, `SHA256`, `SHA384`, `SHA512` (`SnmpV3AuthProtocol`) and `NoPriv`, `DES`,
`AES`, `AES192`, `AES256`, `AES192C`, `AES256C` (`SnmpV3PrivProtocol`), plus the `AuthNoPriv` and
`AuthPriv` message flags.

**Rationale**: no new dependency, and the transport already builds `UsmSecurityParameters`; the change
is which constants it passes.

**Alternatives considered**: none needed.

## R2. Protocol value spelling

**Decision**: lowercase names without separators, matching gosnmp's constant names: `md5`, `sha`,
`sha224`, `sha256`, `sha384`, `sha512`; `none`, `des`, `aes`, `aes192`, `aes256`, `aes192c`,
`aes256c`. Exact match only; any other spelling (including `SHA256` or `sha-256`) is refused with
the list of accepted values.

**Rationale**: the document's other enumerations (`kind: snmp_v3`) are lowercase and exact. Case
folding would mean two spellings of one document, and the error already tells the operator what to
write. Arista writes `sha256` and `aes`, so the lab's values carry over as typed.

**Alternatives considered**: case-insensitive matching, and accepting net-snmp spellings
(`SHA-256`, `AES-256-C`). Both widen what a document can say for no gain once the error names the
accepted values.

## R3. Where the protocols live after loading

**Decision**: two nullable columns on `credential_set`, `auth_protocol` and `priv_protocol`, written
by `jobrunner` from the parsed document with defaults already applied, read by the collector with
the rest of the set. A check constraint makes them present for `snmp_v3` rows and absent for the
others. The migration backfills existing `snmp_v3` rows with `sha` and `aes`, which is what those
rows meant when they were written.

**Rationale**: the collector learns everything about a set from this table today (`pool.go`), and the
table is the recorded form of the configuration a run used. Storing the resolved value, not the
absence of one, means a later change of default cannot change the meaning of an old run. The grants
are table level: `netmapper_collector` and `netmapper_engine` read the new columns, `netmapper_api`
still has no grant on the table.

**Alternatives considered**: the collector reading `config_version.document` and re-parsing it (a
second path to the same data, and the parse would have to be kept in step with `jobrunner`); a single
`jsonb` options column (hides two fixed fields behind a free-form blob, and the check constraint gets
harder to read).

## R4. Checking the secret before use

**Decision**: in `collector/creds.go`, right after a set's reference resolves: for `snmp_v3`, a
missing or empty `auth` field, or (for authPriv) a missing or empty `priv` field, is handled like a
reference that resolved to nothing. The set goes to `unresolved`, no attempt is counted, nothing is
sent, and the existing verdict turns it into `credential_unresolved` (or `credential_partial`). The
log line names the set and the missing field, never a value. The failure message keeps naming sets,
now with the field: `lab-snmp (missing priv)`.

**Rationale**: the attempt loop already has exactly this outcome for an unresolvable reference, with
the right accounting (no budget spent, the task fails rather than calling the device denied). A
secret with a missing field is the same situation seen one step later.

**Alternatives considered**: checking inside the SNMP transport (it would have to invent an error
kind that the loop then maps back to "unresolved"); checking in the `secret` package (it does not
know the set's kind or security level).

## R5. Load-time validation of `vault:...#field` on v3 sets

**Decision**: `config.validate` refuses a `snmp_v3` set whose `secret_ref` starts with `vault:` and
contains `#`, with a message saying that a v3 set must reference the whole secret.

**Rationale**: `internal/secret/vault.go` collapses a `#field` reference to one unnamed value, so
`auth` and `priv` would always be empty. R4 would catch it at collection time, device by device; the
load-time check catches it once, before any run.

**Alternatives considered**: making the vault resolver keep named fields when `#field` is given
(changes the meaning of a reference form other kinds rely on).

## R6. Protocol mismatch and denial evidence

**Decision**: no change. `snmp.classify` already maps `ErrUnknownUsername`, `ErrWrongDigest`,
`ErrDecryption` and `ErrUnknownSecurityLevel` to `transport.AuthError` with the error text as
evidence, and the attempt loop records `denied` with that evidence. The tests of R8 pin it.

**Rationale**: checked against net-snmp 5.9.4: a SHA-1 request to a SHA-256 user gets an
authentication failure report, and an unknown user gets an unknown-user report. Both are answers,
so both can be told apart and recorded.

**Alternatives considered**: a new observation status or finding category for "protocol mismatch".
The device's report cannot tell a wrong protocol from a wrong passphrase (both are a wrong digest),
so a dedicated category would claim more than is known.

## R7. SNMP agent for automated tests

**Decision**: a net-snmp agent built from `alpine:3.22` with `net-snmp`, added to
`deploy/compose.yaml` as service `snmpd`, published on `localhost:1161/udp`, with a fixed
`snmpd.conf` holding one user per case (see [quickstart.md](quickstart.md)). The CI already brings
this compose file up; it gains `snmpd` in the same step. Tests read `NETMAPPER_TEST_SNMP`
(`ip:port`, an address since the transport targets a `netip.Addr`) and skip when unset, like the
other integration variables, so the CI's no-skip rule makes it mandatory there.

**Rationale**: checked by hand: net-snmp 5.9.4 on Alpine accepts SHA-256, SHA-512, AES-256,
AES-256-C (Reeder) and authNoPriv users, and answers mismatches with reports. It runs anywhere Docker
runs, unlike cEOS (licensed image, not available in CI). One compose file for developers and CI
keeps the two from drifting, as the workflow comment already says.

**Alternatives considered**: a cEOS node in CI (image not redistributable); an in-process Go agent
(new dependency, and a test agent written against the same library as the client can share its
bugs); a third-party snmpd image (unpinned contents).

## R8. Test levels

**Decision**:

- `internal/config`: table tests for defaults, every accepted value, unknown values, fields on
  non-v3 kinds, `vault:...#field` on v3, and a pre-006 document loading unchanged.
- `internal/transport/snmp`: a new integration test against the R7 agent, one case per user:
  default (sha/aes), sha256/aes, sha512/aes256, sha/aes256c, authNoPriv; plus protocol mismatch and
  unknown user, both returning `transport.ErrAuth` with the device's reason in the evidence.
- `internal/collector`: with the fake transport, a v3 set whose secret lacks `priv` (and one lacking
  `auth`) ends as `credential_unresolved` naming the field, counts no attempt and opens no session;
  an authNoPriv set without `priv` is used.
- `internal/jobrunner`: the resolved protocols are written to `credential_set`.
- End to end on cEOS: manual, in `test/lab` (R9) and on the external lab (SC-001), following
  [quickstart.md](quickstart.md).

**Rationale**: the SNMP wire behaviour is tested against a real agent, the accounting against the
fake network that the collector tests already use. A full crawl against net-snmp would stop at
fingerprinting (no pack matches net-snmp's sysObjectID) and would prove less than the transport test.

**Alternatives considered**: a new fake SNMP pack for net-snmp to run a whole crawl in CI. It would
test a pack that exists only for the test.

## R9. Test lab

**Decision**: in `test/lab`, sw2 drops `snmp-server community public ro` and gets an SNMPv3 user
with `auth sha256` and `priv aes256`; `test/lab/netmapper.yaml` gains a `ro-snmp-v3` set after
`ro-snmp`. sw1 and sw4 keep v2c, sw3 keeps no SNMP.

**Rationale**: sw2 then answers v2c with silence, so the collector reaches the v3 set on it, and the
other switches keep exercising the paths they already cover. sw2 is found through LLDP, so the v3
set is also exercised on a device that was not a seed.

**Alternatives considered**: adding the v3 user next to the community on sw2 (v2c would win and v3
would never be tried); a fifth switch (more lab for one user).

## R10. Test agent passphrases and gitleaks

**Decision**: the passphrases in `deploy/snmpd/snmpd.conf` and in the test are fixed dummy values,
added to the `.gitleaks.toml` allowlist by exact value, like the other development-stack values.

**Rationale**: same rule as the S3 key and Garage token: listed exactly, so a real secret in the same
files is still reported.
