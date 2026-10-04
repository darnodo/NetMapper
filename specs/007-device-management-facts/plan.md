# Implementation Plan: Device management configuration facts on Arista EOS

**Branch**: `007-device-management-facts` | **Date**: 2026-10-04 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/007-device-management-facts/spec.md`

## Summary

Six new platform-neutral fact families (`snmp`, `aaa_servers`, `local_users`, `management_apis`,
`aaa_methods`, `management_servers`) in `internal/fact`, filled on Arista EOS by one recipe each.
Every command is a `show` command that prints no secret (checked on cEOS 4.36 by the EVPN lab
survey), except SNMP v2c communities and NTP servers, read from `show running-config sanitized`
sections (research R1). Three small, vendor-neutral additions to the recipe format: a `map` per
step, `defaults` and `split` (R5). The repository's `test/lab` gets the management configuration on
sw2, so every fixture and test runs from this repository with no lab up (R10, R11). A secret test
scans every recorded lab output (R9). One read endpoint, `GET /v1/devices/{name}/facts/{family}`,
serves any family's observations with their evidence (R8).

Spec amendments from research, made in this change: `snmp` rows carry no VRF and no security level
(R2, R4); `management_apis` carries a list of VRFs and has an `snmp` row for the agent (R2, R3).

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `gotextfsm` (templates), `pgx/v5` (API reads),
`go.yaml.in/yaml/v3` (packs). No new dependency.

**Storage**: none new. Rows go to `observation.parsed`, outputs to the object store, as for every
family. One migration, 0010: `netmapper_api` already reads `observation`, and gains SELECT on
`parse_generation` so the facts endpoint serves the active parse generation only (research R8).

**Testing**: `go test ./...`; template fixtures in `packs/arista_eos/testdata/lab/` (`TestPacks`), a
family-level test over recorded sw2 outputs, a secret scan, API tests under the `netmapper_api` role
(existing harness).

**Target Platform**: Linux container (one binary, three roles); collector against Arista EOS 4.20+,
fixtures from cEOS 4.36.0F.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. The scrape session gains about twelve short `show` commands per
EOS device; the one concern is a login slowed by dead AAA servers in `test/lab`, avoided by putting
`local` first (R10).

**Constraints**: no secret in raw output or facts (FR-009); tests must pass with no lab up; `NetLab`
VM needed once to record fixtures.

**Scale/Scope**: a few to a few dozen rows per family per device; 28-switch EVPN lab as the widest
check.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | The new endpoint returns each observation with its evidence; `not_collected` answers 404 rather than a 200 with nothing to cite. |
| II. Observations immutable, rest rebuildable | yes | Rows live in `observation.parsed`; a template fix is a replay over stored outputs. The endpoint reads the active parse generation only. |
| III. Credentials stay in the collector | yes, the main risk | No command printing a secret is allowed (R1); communities are recorded by access level; secret scan over fixtures (R9). `api` gains no grant and no secret path. |
| IV. Read only, outward | yes | Only `show` commands; packs stay read only. |
| V. Vendor specifics are data | yes | Commands, templates, literals and defaults are in `packs/arista_eos`; the recipe additions (R5) carry no vendor name; schemas are neutral. |

Workflow rules: the README and `docs/how-to/write-a-pack.md` are updated for the new families, the
endpoint and the recipe keys (FR-017); the fact families and REST contracts of 001 and 005 are
updated in the same change (FR-010); the API path is tested under `netmapper_api`.

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/007-device-management-facts/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── fact-families.md   # merged into specs/001-crawl-loop/contracts/fact-families.md
│   └── rest-facts.md      # merged into specs/005-read-api/contracts/rest.md
└── tasks.md               # /speckit-tasks
```

### Source Code (repository root)

```text
internal/fact/fact.go              # six families
internal/pack/registry.go          # Step.Map, Impl.Defaults, Impl.Split, load checks, missingCost
internal/parse/parse.go            # apply step map, split, defaults; keep lists as lists
internal/api/api.go                # route
internal/api/facts.go              # new handler
packs/arista_eos/recipes/          # snmp, aaa_servers, local_users, management_apis,
                                   # aaa_methods, management_servers (.yaml)
packs/arista_eos/templates/        # one .textfsm per command of research R1
packs/arista_eos/testdata/lab/     # sw2_*, sw1_*_empty recordings, .yml, sw2_<family>.facts.yml
packs/packs_test.go                # _empty handling, family test, secret test
test/lab/sw2.cfg                   # management configuration
test/lab/two-switch.clab.yaml      # topology comment
specs/001-crawl-loop/contracts/fact-families.md
specs/005-read-api/contracts/rest.md
README.md, docs/how-to/write-a-pack.md
```

**Structure Decision**: the existing layout; one new Go file (`internal/api/facts.go`), everything
else extends files in place.

## Implementation order

1. `test/lab/sw2.cfg` and topology comment (R10). On the `NetLab` VM: deploy, record every command
   of R1 on sw2 and sw1, add the secret test, run it on the raw recordings before anything else.
2. `internal/fact`: the six schemas.
3. `internal/pack` and `internal/parse`: step `map`, `defaults`, `split`, lists, with unit tests.
4. Templates and recipes, family by family, each with its `.yml` and `facts.yml`.
5. API endpoint and tests.
6. Contracts, README, write-a-pack guide, `missingCost`.
7. Quickstart sections 4 and 5 on the VM.

Steps 2 to 6 need no lab.

## Complexity Tracking

No constitution violation to justify.
