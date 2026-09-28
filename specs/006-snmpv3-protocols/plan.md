# Implementation Plan: Configurable SNMPv3 authentication and privacy

**Branch**: `006-snmpv3-protocols` | **Date**: 2026-09-28 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/006-snmpv3-protocols/spec.md`

## Summary

`snmp_v3` credential sets gain two optional keys, `auth_protocol` and `priv_protocol`, validated when
the document is loaded, stored with the set, and passed by the collector to the SNMP transport, which
maps them to gosnmp constants with a plain `switch`. `priv_protocol: none` selects authNoPriv. The
collector checks a v3 secret's fields before using it and treats a missing one like an unresolved
reference. A net-snmp agent joins the development compose file so the v3 path is tested against a
real agent in CI, and the test lab gets one v3 switch. Documentation is corrected in the same change.

## Technical Context

**Language/Version**: Go 1.27 (as `go.mod`)

**Primary Dependencies**: gosnmp v1.44.0 (existing, supports every listed protocol, research R1). No
new dependency.

**Storage**: PostgreSQL 17, one migration (`0009`) adding two columns to `credential_set`.

**Testing**: `go test`; integration tests gated by `NETMAPPER_TEST_*` variables, a new one
`NETMAPPER_TEST_SNMP` for the net-snmp agent (research R7, R8).

**Target Platform**: Linux containers and binaries, as today.

**Project Type**: single Go binary with subcommands (`collector`, `engine`, `api`, operator commands).

**Performance Goals**: none new. One SNMP session per device as today; protocol choice does not
change the number of requests.

**Constraints**: GET and GETBULK only (principle IV); protocol names in the document, passphrases only
behind references (principle III).

**Scale/Scope**: about 150 lines of Go outside tests, one migration, one test agent, docs.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How it holds |
| --- | --- | --- |
| I. Evidence travels with the answer | No | A refused v3 credential keeps producing `denied` with the device's reason as evidence (R6). |
| II. Observations immutable, rest rebuildable | No | No change to observations. The migration adds configuration columns and backfills rows with the value they already meant (R3). |
| III. Credentials and reach stay in the collector | Yes | Protocol names are not credentials: they open nothing. Passphrases stay behind `env:`/`vault:` references, resolved only by the collector. `netmapper_api` gets no grant on `credential_set`. The missing-field check logs field names, never values (R4). |
| IV. Read only, outward | Yes | The transport still issues only GET and GETBULK; only the USM parameters change (FR-011). |
| V. Vendor specifics are data | No | Protocols are standard SNMP, not vendor specific, and live in the configuration, not in code paths per vendor. No pack change. |

Development workflow rules that apply:

- Documentation of a changed configuration key is corrected in the same change: `docs/how-to/deploy.md`,
  `specs/001-crawl-loop/contracts/config.md`, `test/lab/netmapper.yaml` (FR-012).
- A path assigned to a role is tested under that role: the `jobrunner` write runs as
  `netmapper_operator`, the collector read as `netmapper_collector`, as the existing lab helper does.

Gate: pass, no violation to justify.

Post-design re-check (after Phase 1): unchanged, pass. The design adds no role, no grant and no new
kind of request.

## Project Structure

### Documentation (this feature)

```text
specs/006-snmpv3-protocols/
├── spec.md
├── plan.md              # this file
├── research.md          # Phase 0
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   └── config.md        # Phase 1, delta on 001's config contract
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/config/config.go            # two fields, defaults, validation (R2, R5)
internal/config/config_test.go
migrations/0009_snmpv3_protocols.sql # columns, backfill, checks (R3)
internal/jobrunner/start.go          # write the protocols with the set
internal/collector/pool.go           # read them with the set
internal/collector/creds.go          # secret field check before use (R4), pass protocols on
internal/collector/creds_test.go
internal/transport/transport.go      # Credential gains AuthProtocol, PrivProtocol
internal/transport/snmp/snmp.go      # switch to gosnmp constants, AuthNoPriv
internal/transport/snmp/snmp_test.go # new, against the net-snmp agent (R8)
deploy/compose.yaml                  # snmpd service
deploy/snmpd/Dockerfile              # alpine + net-snmp
deploy/snmpd/snmpd.conf              # one user per test case
.github/workflows/quality.yml        # start snmpd, set NETMAPPER_TEST_SNMP
.gitleaks.toml                       # allowlist the test passphrases (R10)
test/lab/sw2.cfg                     # v3 user, no community (R9)
test/lab/netmapper.yaml              # ro-snmp-v3 set
docs/how-to/deploy.md                # v3 secret shapes, example
specs/001-crawl-loop/contracts/config.md
README.md                            # development section: snmpd and NETMAPPER_TEST_SNMP
```

**Structure Decision**: existing layout, no new package. The only new directory is `deploy/snmpd/`
for the test agent, next to the other development-stack files.

## Open points

- The Reeder variants (`aes192c`, `aes256c`) are tested against net-snmp only; no Cisco device is
  available to confirm interoperability.
- A device that stays silent on a protocol mismatch, instead of sending a report, is recorded as
  silence. Not made more precise here (spec, Assumptions).

## Complexity Tracking

No constitution violation; nothing to justify.
