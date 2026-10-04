---
description: "Task list for device management configuration facts on Arista EOS"
---

# Tasks: Device management configuration facts on Arista EOS

**Input**: Design documents from `/specs/007-device-management-facts/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/fact-families.md,
contracts/rest-facts.md, quickstart.md

**Tests**: Included. FR-013a asks for a template test per row type runnable with no lab, FR-014 for
a secret test, and the constitution for API paths tested under `netmapper_api`.

**Organization**: Tasks are grouped by user story. US2 adds the `empty` cases to the US1 recipes, so
it follows US1. US3 (the endpoint) only needs Phase 2 and can run beside US1.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 006.
- **[VM]** in a description: needs the `NetLab` VM (remote, not always up), where `test/lab` and
  arista-evpn-vxlan-clab run. Every other task runs on the Mac with no lab.

Integration tests use `internal/testutil` and skip without `NETMAPPER_TEST_DSN` and
`NETMAPPER_TEST_S3_ENDPOINT`; the CI fails on any skip.

Command, template and fixture names used throughout (research R1, corrected by R13 after recording). Every command is sent with
` | no-more` appended. A fixture is `packs/arista_eos/testdata/lab/<switch>_<template base>.raw`.

| Family | Step command | Template (`packs/arista_eos/templates/`) |
| ------ | ------------ | ---------------------------------------- |
| `snmp` | `show running-config sanitized \| include ^snmp-server community` | `show_running_config_sanitized_snmp_community.textfsm` |
| `snmp` | `show snmp user` | `show_snmp_user.textfsm` |
| `aaa_servers` | `show tacacs` | `show_tacacs.textfsm` |
| `aaa_servers` | `show radius` | `show_radius.textfsm` |
| `local_users` | `show users accounts` | `show_users_accounts.textfsm` |
| `management_apis` | `show management api gnmi` | `show_management_api_gnmi.textfsm` |
| `management_apis` | `show management api http-commands` | `show_management_api_http_commands.textfsm` |
| `management_apis` | `show management api netconf` | `show_management_api_netconf.textfsm` |
| `management_apis` | `show management ssh` | `show_management_ssh.textfsm` |
| `management_apis` | `show management telnet` | `show_management_telnet.textfsm` |
| `management_apis` | `show snmp` | `show_snmp.textfsm` |
| `aaa_methods` | `show aaa methods all` | `show_aaa_methods_all.textfsm` |
| `management_servers` | `show running-config sanitized \| include ^ntp server` | `show_running_config_sanitized_ntp_server.textfsm` |
| `management_servers` | `show logging \| include Logging to` | `show_logging.textfsm` |
| `management_servers` | `show ip name-server` | `show_ip_name_server.textfsm` |

---

## Phase 1: Setup

**Purpose**: Give `test/lab` the configuration, record every command once on the VM, and prove no
recorded output holds a secret before any template exists (research R9, R10).

- [X] T001 Edit test/lab/sw2.cfg (research R10). Keep every existing line. Add, every secret prefixed `nm-lab-secret-`: `vrf instance MGMT`; standard ACLs `NM-SNMP-RO` and `NM-SNMP-RW`; `snmp-server community nm-lab-secret-ro ro NM-SNMP-RO` and `snmp-server community nm-lab-secret-rw rw NM-SNMP-RW`; group `nm-auth v3 auth read all` and user `nm-authonly nm-auth v3 auth sha256 nm-lab-secret-authonly`; `snmp-server vrf MGMT`; `tacacs-server timeout 1`, `tacacs-server host 192.0.2.10 key 0 nm-lab-secret-tacacs`, `tacacs-server host 192.0.2.11 key 0 nm-lab-secret-tacacs`, `aaa group server tacacs+ NM-TACACS` with both; `radius-server timeout 1`, `radius-server host 192.0.2.20 key 0 nm-lab-secret-radius`, `aaa group server radius NM-RADIUS` with it; `aaa authentication login default local group NM-TACACS`, `aaa authentication login console local`, `aaa authentication enable default local group NM-TACACS`, `aaa authorization exec default local group NM-TACACS`, `aaa authorization commands all default local group NM-TACACS`, `aaa accounting exec default start-stop group NM-TACACS`, `aaa accounting commands all default start-stop group NM-TACACS`; users `nm-auto privilege 15 role network-admin secret nm-lab-secret-auto` with an `ssh-key` line holding a throwaway ed25519 public key (generate with `ssh-keygen -t ed25519 -N '' -C nm-auto@test-lab`, commit only the `.pub` line, discard the private key) and `nm-ops privilege 1 role network-operator secret nm-lab-secret-ops`; `management api gnmi` / `transport grpc default`; `management api http-commands` / `no shutdown` / `vrf MGMT` / `no shutdown`; `management api netconf` / `transport ssh default`; `management telnet` / `shutdown`; `ntp server 192.0.2.30`, `ntp server vrf MGMT 192.0.2.31`; `logging host 192.0.2.40`, `logging host 192.0.2.41 1514`, `logging vrf MGMT host 192.0.2.42`; `ip name-server vrf default 192.0.2.50`, `ip name-server vrf MGMT 192.0.2.51`. `Management0` stays in `default`. Header comment: management configuration for feature 007, dead documentation addresses, `local` first in every method list so no login waits on them
- [X] T002 [P] Update the header comment of test/lab/two-switch.clab.yaml: sw2 also carries the management configuration of feature 007 (SNMP v2c communities not in `netmapper.yaml`, so the crawl still reads sw2 over SNMPv3 only; dead AAA servers behind `local`); sw1 has none and is the `empty` case
- [X] T003 [P] Run `gitleaks dir --no-banner test/lab`. If an `nm-lab-secret-` value is reported, add it to .gitleaks.toml in a new `[[allowlists]]` block "test/lab dummy secrets" by exact value (`regexTarget = "secret"`, one `'''^value$'''` per line), same style as the existing block
- [X] T004 [P] Add `TestNoSecretInRecordedOutput` to packs/packs_test.go (research R9): read every `packs/*/testdata/lab/*.raw` and fail, naming file and match, on any of: the literal strings `nm-lab-secret-`, `lab-auth-sw2`, `lab-priv-sw2`, `evpnlab-`; the regexes `\$[156]\$`, `sha512 \$`, `key 7 [0-9A-Fa-f]{6,}`. A comment says why `admin` and `public` are not listed (ordinary words in legitimate output)
- [X] T005 [VM] Deploy the changed lab (`sudo containerlab deploy -t test/lab/two-switch.clab.yaml --reconfigure`) and, for each command of the table above, record sw2's and sw1's output with `docker exec clab-netmapper-<sw> Cli -p 15 -c '<command> | no-more'` into packs/arista_eos/testdata/lab/<sw>_<template base>.raw. A sw1 output with nothing to report gets `_empty` before `.raw` (`sw1_show_tacacs_empty.raw`). Also record `sw2_show_running_config_sanitized_snmp_community.raw` and check it shows `<removed>` in place of both strings
- [X] T006 Run `go test -count=1 -run TestNoSecretInRecordedOutput ./packs/`. It must pass on the T005 recordings. If a command prints a secret, remove that command from research.md R1 and pick another; never edit a recording to hide one
- [X] T007 Compare every T005 recording with the formats research R1 to R3 expect (VRF of the SNMP agent in `show snmp`, group membership in `show tacacs`/`show radius`, level and record mode in `show aaa methods all`, ports in `show logging`). Write any difference into research.md under the decision it affects, before Phase 3

**Checkpoint**: recordings committed, secret test green, research.md matches the real outputs. The VM
is not needed again until Phase 6.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Schemas and the three recipe additions. No pack uses them yet; every existing test still
passes.

- [X] T008 In internal/fact/fact.go, add six entries to `Families`, with the fields of data-model.md exactly. `snmp`: `version` (String, Required, Enum `v2c`,`v3`), `access` (Enum `ro`,`rw`), `acl`, `user`, `group`, `auth_protocol` (Enum `md5`,`sha`,`sha224`,`sha256`,`sha384`,`sha512`), `priv_protocol` (Enum `des`,`3des`,`aes`,`aes192`,`aes256`). `aaa_servers`: `protocol` (Required, Enum `radius`,`tacacs`), `address` (Required), `port` (Int), `vrf` (Required), `group`. `local_users`: `name` (Required), `role`, `privilege` (Int), `ssh_key` (Required, Enum `yes`,`no`). `management_apis`: `api` (Required, Enum `gnmi`,`eapi`,`netconf`,`ssh`,`telnet`,`snmp`), `enabled` (Required, Enum `yes`,`no`), `transport`, `port` (Int), `vrfs` (Strings). `aaa_methods`: `type` (Required, Enum `authentication`,`authorization`,`accounting`), `service` (Required), `list` (Required), `level`, `record` (Enum `start-stop`,`stop-only`), `methods` (Strings, Required). `management_servers`: `service` (Required, Enum `ntp`,`syslog`,`dns`), `address` (Required), `port` (Int), `vrf` (Required). Update the package comment if it names the families
- [X] T009 [P] In internal/fact/fact_test.go, add cases: a valid row per new family passes; `snmp` with `version: v1`, `management_apis` with `enabled: true`, `aaa_methods` without `methods`, and `management_servers` without `vrf` are each refused
- [X] T010 In internal/pack/registry.go, add `Map map[string]string \`yaml:"map"\`` to `Step`, and `Defaults map[string]string \`yaml:"defaults"\`` and `Split map[string]string \`yaml:"split"\`` to `Impl`, each with a one-line comment from data-model.md "Recipe format additions". In the load checks next to the existing `map` checks: a step `map` field must be in the family schema; a `defaults` field must be in the schema and its value must pass `fact.Validate` for that field alone; a `split` field must be in the schema with type `fact.Strings` and a non-empty separator
- [X] T011 In internal/pack/registry.go, add one `missingCost` entry per new family, all with the same text: "no management inventory for these devices" (research R12)
- [X] T012 [P] In internal/pack/registry_test.go, add load cases: a step `map` on an unknown field, `defaults: {enabled: maybe}`, `split` on a `string` field, and `split` with an empty separator are each refused with a message naming the recipe; a recipe using all three correctly loads
- [X] T013 In internal/parse/parse.go, apply the additions in the order of data-model.md: for each step, build the effective map (the step's `Map` over `im.Map`) and pass it to `mapRow` instead of `im.Map`; in `mapRow`, when the schema field is `fact.Strings`, keep a `[]string` value as a list (trim each item, drop empty ones) and split a string value on `im.Split[field]` when set, instead of joining; after mapping, set each `im.Defaults` field the row does not have. A step with no `Map` behaves exactly as today
- [X] T014 [P] In internal/parse/parse_test.go, add hand-written cases: two steps with a step-level literal each give rows with different literals; `defaults` fills an absent `vrf` and does not overwrite a present one; `split: {methods: ', '}` turns `group NM-TACACS, local` into `[group NM-TACACS, local]` in that order; a TextFSM `List` value stays a list for a `strings` field and is still joined for a `string` field
- [X] T015 In packs/packs_test.go `TestPacks`/`parseRecorded`: a recording whose name contains `_empty` must yield zero rows from its template (or be declared empty by the step's `empty_lines`), instead of failing with "yielded no row"
- [X] T016 In packs/packs_test.go, add `TestFamiliesFromLab` (research R11): for each file `packs/arista_eos/testdata/lab/<sw>_<family>.facts.yml` (YAML: `status:` and `rows:`), load the pack, take the implementation for `<family>` at version `4.36.0F`, read each step's recording `<sw>_<template base>[_empty].raw` in step order, run `parse.Parse`, and compare status and rows (rows compared as sets: order is not part of the contract, except inside a `strings` field). Skip nothing: a missing recording fails

**Checkpoint**: `go test ./internal/... ./packs/...` green with the new keys and no recipe using them.

---

## Phase 3: User Story 1 - See how a device is managed, with no secret stored (Priority: P1), MVP

**Goal**: the six families collected on Arista EOS from secret-free commands.

**Independent Test**: `go test ./packs/` with no lab: every sw2 recording parses to its `.yml`, every
`sw2_<family>.facts.yml` matches, the secret test passes.

Done differently (2026-10-04): no per-template `.yml`. The `<switch>_<family>.facts.yml` files of T016 check
the same templates through the whole parse, for sw1, sw2, dc-leaf1 and campus-access1, so a second set of
expected rows per template would only duplicate them. `TestPacks` still parses every recording.

For each template below: write the `.textfsm`, then the expected `.yml` next to the sw2 recording
(`parsed_sample:` in ntc-templates format, values exactly as `test/lab/sw2.cfg` implies). Value names
are upper case like the existing templates. Each recipe is `packs/arista_eos/recipes/<family>.yaml`
with one implementation, `versions: '>=4.20'`, `transport: ssh`, steps in the order of the table.

- [X] T017 [P] [US1] snmp templates: show_running_config_sanitized_snmp_community.textfsm (ACCESS `ro|rw`, ACL optional; the masked string is matched and not captured) and show_snmp_user.textfsm (USER, GROUP, AUTH, PRIV), each with its sw2 `.yml`
- [X] T018 [US1] packs/arista_eos/recipes/snmp.yaml: step 1 map `version: '=v2c'`, `access: ACCESS`, `acl: ACL`; step 2 map `version: '=v3'`, `user: USER`, `group: GROUP`, `auth_protocol: AUTH`, `priv_protocol: PRIV`; `values` translating the EOS spellings of T007 to `md5`/`sha`/`sha224`/`sha256`/`sha384`/`sha512` and `des`/`3des`/`aes`/`aes192`/`aes256`, and the "none" spelling of privacy to `''` (absent). Then packs/arista_eos/testdata/lab/sw2_snmp.facts.yml: status `collected`, 2 v2c rows (`ro` + `NM-SNMP-RO`, `rw` + `NM-SNMP-RW`) and 2 v3 rows (`netmapper`/`nm-ro`/`sha256`/`aes256`, `nm-authonly`/`nm-auth`/`sha256` without `priv_protocol`)
- [X] T019 [P] [US1] aaa_servers templates: show_tacacs.textfsm and show_radius.textfsm (ADDRESS, PORT, VRF, GROUP as T007 found them), each with its sw2 `.yml`
- [X] T020 [US1] packs/arista_eos/recipes/aaa_servers.yaml: step maps `protocol: '=tacacs'` and `protocol: '=radius'`; implementation map `address`, `port`, `vrf`, `group`; `defaults: {vrf: default}`. Then sw2_aaa_servers.facts.yml: `tacacs` 192.0.2.10 and 192.0.2.11 in group `NM-TACACS`, `radius` 192.0.2.20 in group `NM-RADIUS`, all `vrf: default`, `port` only where the recording prints one (FR-007a)
- [X] T021 [P] [US1] show_users_accounts.textfsm (NAME, ROLE, PRIVILEGE, SSHKEY: the key text, captured only to test presence) with its sw2 `.yml`
- [X] T022 [US1] packs/arista_eos/recipes/local_users.yaml: map `name`, `role`, `privilege`, `ssh_key: SSHKEY`; `values: {ssh_key: {'*': 'yes'}}`; `defaults: {ssh_key: 'no'}`. Then sw2_local_users.facts.yml: `admin`, `netmapper`, `nm-auto` (`ssh_key: 'yes'`), `nm-ops` (privilege 1), the others `ssh_key: 'no'`; no key text in any row
- [X] T023 [P] [US1] management_apis templates: show_management_api_gnmi.textfsm, show_management_api_http_commands.textfsm, show_management_api_netconf.textfsm, show_management_ssh.textfsm, show_management_telnet.textfsm, show_snmp.textfsm, each giving one row with ENABLED, TRANSPORT and PORT where printed, and VRFS as a TextFSM `List` or one line for `split`, with sw2 `.yml` files
- [X] T024 [US1] packs/arista_eos/recipes/management_apis.yaml: six steps with map `api: '=gnmi'`, `'=eapi'`, `'=netconf'`, `'=ssh'`, `'=telnet'`, `'=snmp'`; implementation map `enabled`, `transport`, `port`, `vrfs`; `values` mapping the EOS wording of each enabled state to `yes`/`no` (shut down and not configured both `no`); `split` for `vrfs` if T007 found them on one line. Then sw2_management_apis.facts.yml: six rows, `eapi` with `vrfs: [default, MGMT]`, `snmp` with `vrfs: [default, MGMT]`, `telnet` `enabled: 'no'`
- [X] T025 [P] [US1] show_aaa_methods_all.textfsm: TYPE and SERVICE filled down from each section header, then LIST, LEVEL (commands only), RECORD (accounting only), METHODS, with its sw2 `.yml`
- [X] T026 [US1] packs/arista_eos/recipes/aaa_methods.yaml: map `type`, `service`, `list`, `level`, `record`, `methods`; `values` lowercasing the header spellings to the data-model values; `split: {methods: <separator T007 found>}`. Then sw2_aaa_methods.facts.yml: authentication `login`/`default` `[local, group NM-TACACS]`, authentication `login`/`console` `[local]`, authentication `enable`/`default`, authorization `exec`/`default`, authorization `commands`/`default` `level: all`, accounting `exec`/`default` `record: start-stop` `[group NM-TACACS]`, accounting `commands`/`default` `level: all` `record: start-stop`; plus any default list EOS prints that sw2.cfg does not set, as the recording shows it
- [X] T027 [P] [US1] management_servers templates: show_running_config_sanitized_ntp_server.textfsm (VRF optional, ADDRESS), show_logging.textfsm (ADDRESS, PORT, VRF of each logging host), show_ip_name_server.textfsm (ADDRESS, VRF), with sw2 `.yml` files
- [X] T028 [US1] packs/arista_eos/recipes/management_servers.yaml: step maps `service: '=ntp'`, `'=syslog'`, `'=dns'`; implementation map `address`, `port`, `vrf`; `defaults: {vrf: default}`. Then sw2_management_servers.facts.yml: ntp 192.0.2.30 (`default`) and 192.0.2.31 (`MGMT`); syslog 192.0.2.40, 192.0.2.41 `port: 1514`, 192.0.2.42 `MGMT`; dns 192.0.2.50 `default`, 192.0.2.51 `MGMT`; `port` elsewhere only as printed
- [X] T029 [US1] Run `go test -count=1 ./packs/ ./internal/pack/ ./internal/parse/`: the pack loads with no `arista_eos` "no recipe" warning, `TestPacks`, `TestFamiliesFromLab` and the secret test pass

**Checkpoint**: US1 complete. A crawl would collect the six families; verified on the VM in T042.

---

## Phase 4: User Story 2 - Tell "not configured" from "not collected" (Priority: P2)

**Goal**: nothing configured is `empty`, a bad output is `parse_failed`, another platform is
`unsupported`.

**Independent Test**: `go test ./packs/` with the sw1 `_empty` recordings: the families sw1 has
nothing for come out `empty`.

- [X] T030 [US2] For each step whose sw1 recording (T005) is not blank but says "nothing configured" (a header or a fixed message), add an `empty_lines` entry to that step in its recipe matching only those lines (research R7). Commands that print nothing need none
- [X] T031 [US2] Add packs/arista_eos/testdata/lab/sw1_aaa_servers.facts.yml and sw1_management_servers.facts.yml with `status: empty`, `rows: []`, and sw1_management_apis.facts.yml, sw1_aaa_methods.facts.yml and sw1_local_users.facts.yml with `status: collected` and the rows sw1 shows (EOS always prints SSH, the default method lists and its accounts, research R13), sw1_snmp.facts.yml with its one v2c `ro` row; `TestFamiliesFromLab` picks them up
- [X] T032 [P] [US2] In packs/packs_test.go, add a case to a table test like `TestNoLLDPNeighbourIsEmpty`: for `aaa_servers`, sw2's `show tacacs` recording with one line reworded gives `parse_failed`, never `empty`
- [X] T033 [P] [US2] In internal/collector (the existing test that checks `no_recipe`), add a case: a device of a pack with no recipe for the new families gets an `unsupported` observation with detail `no_recipe` for each of the six, and the snapshot verdict is the same as without them (FR-015, SC-004)

**Checkpoint**: US1 and US2 complete.

---

## Phase 5: User Story 3 - Read the management facts through the read interface (Priority: P3)

**Goal**: `GET /v1/devices/{name}/facts/{family}` as in contracts/rest-facts.md.

**Independent Test**: `go test ./internal/api/` under the `netmapper_api` role.

- [X] T034 [US3] Create internal/api/facts.go with the handler `deviceFacts`: check `r.PathValue("family")` against `fact.Families` first and answer 404 `{"error": "no_such_family", "snapshot": …}` (after `pickSnapshot`, so the snapshot is known); then `pickDevice`; then read, in one query, the observations of the snapshot with `fact_family = $family`, `host(target)` in the device's `Targets`, joined to `parse_generation` with `active`, ordered by `collected_at, id`, selecting status, detail, parsed, and the `Evidence` fields. None: 404 `{"error": "not_collected", "snapshot": …}`. Else 200 `{snapshot, device_key, family, observations: [{status, detail, rows, evidence}]}` with `rows` `[]` when `parsed` is null and `detail` null when empty. Comment pointing at contracts/rest-facts.md
- [X] T035 [US3] In internal/api/api.go, register `mux.Handle("GET /v1/devices/{name}/facts/{family}", s.read(s.deviceFacts))` after the device route, and update the `Server` comment ("eight read endpoints" becomes nine)
- [X] T036 [P] [US3] Create internal/api/facts_test.go using the existing helpers (helpers_test.go), connected as `netmapper_api`: a collected family returns its rows and evidence; an `empty` observation is 200 with `status: empty` and `rows: []`; an unknown family is 404 `no_such_family`; a device with no observation of the family is 404 `not_collected`; an observation of an inactive parse generation is not returned; `identity` on a device that answered on two addresses returns two observations in collection order; no token is 401 and a token without `read` is 403
- [X] T037 [US3] Merge contracts/rest-facts.md into specs/005-read-api/contracts/rest.md under "Endpoints" after `GET /v1/devices/{name}`, and add `no_such_family` and `not_collected` to its error table

**Checkpoint**: all three stories complete.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T038 [P] Merge contracts/fact-families.md into specs/001-crawl-loop/contracts/fact-families.md after `interfaces` (field tables from data-model.md), and change its last line ("This feature ships `identity`, `neighbours` and `interfaces`") to say feature 007 adds the six management families
- [X] T039 [P] Update docs/how-to/write-a-pack.md: the six families in the list of families a pack can fill; the three recipe keys (`map` on a step, `defaults`, `split`) with one example each taken from the Arista recipes; the rule that a recipe command must print no secret, and the secret test that enforces it on lab recordings
- [X] T040 [P] Update README.md where it lists what is collected and the API endpoints: the six families and `GET /v1/devices/{name}/facts/{family}`
- [X] T041 Run `gofmt -l .`, `go vet ./...` and `go test -count=1 ./...` with the test stack of quickstart.md section 1: no output from gofmt, nothing skipped
- [ ] T042 [VM] Not run (2026-10-04): test/lab is deployed on the NetLab VM (sw1, sw2) but 172.20.20.0/24
  is not routed over the Tailnet, so the Mac cannot crawl it. Needs a subnet route, or the stack on the VM.
  Its recordings and the offline tests cover the parsing; T043 covered a live crawl. Original task: Run quickstart.md section 4 against `test/lab`: sw2 rows match sw2.cfg, sw1 `empty` cases, sw3/sw4 and sw2-over-SNMPv3 outcomes unchanged, crawl duration within a few seconds of `main`, endpoint 200/404 cases, no `nm-lab-secret-` in parsed facts or stored raw objects. Record any difference in research.md
- [X] T043 [VM] Done 2026-10-04 from the Mac over the Tailnet (SSH admin, SNMPv3 snmp-ro): 28 switches, the six
  families `collected` on all 28, none `parse_failed`; 0 lab secret, hash or type 7 key in `observation.parsed`
  and in the 420 raw outputs read back through `/v1/observations/{id}/raw/{step}`; the facts endpoint
  answers for campus-access1. Fixtures for dc-leaf1 and campus-access1 were recorded in T005. Original task:
  When arista-evpn-vxlan-clab runs: quickstart.md section 5 (SC-003). Record dc-leaf1 and campus-access1 outputs as extra fixtures with `.yml` files, rerun `go test ./packs/`. If the lab is down, note it in the PR as not run; it does not block the merge (spec assumption: second source)

---

## Dependencies & Execution Order

- **Phase 1**: T001 first; T002 to T004 in parallel after it; T005 [VM] after T001; T006 and T007 after T005 and T004.
- **Phase 2**: T008 before T009; T010 before T011, T012 and T013; T013 before T014; T015 and T016 after T013. Phase 2 can start before the VM step (T005), but T016 needs recordings to run.
- **US1 (Phase 3)**: needs Phases 1 and 2. Each family is a template task [P] then its recipe task; the six families are independent of one another.
- **US2 (Phase 4)**: needs the US1 recipes (it adds `empty_lines` to them).
- **US3 (Phase 5)**: needs only T008 (family names). Can run beside US1.
- **Phase 6**: T038 to T041 after US1 to US3; T042 and T043 last, on the VM.

## Parallel Example: User Story 1

```text
T017 snmp templates            T019 aaa_servers templates     T021 local_users template
T023 management_apis templates T025 aaa_methods template      T027 management_servers templates
```

then each family's recipe task (T018, T020, T022, T024, T026, T028), which touch different files.

## Implementation Strategy

1. **MVP**: Phases 1 to 3. The six families are collected, tested offline, and no secret is stored.
   Mergeable alone if needed: data is in the database and in Grafana's view.
2. **US2**: the `empty` and `parse_failed` cases, small once US1 exists.
3. **US3**: the endpoint, independent of the pack work.
4. **Polish**: contracts, docs, then the VM runs (T042, T043).

The one hard external dependency is the `NetLab` VM, needed at T005 and again at T042/T043. Plan the
session around it: record everything in one sitting, then everything up to T041 runs offline.

---

## Phase 7: Convergence

- [ ] T044 [VM] Crawl test/lab (sw1, sw2, already deployed on NetLab) with quickstart.md section 4, once 172.20.20.0/24 is routed over the Tailnet or the stack runs on the VM: sw2 rows match test/lab/sw2.cfg, the sw1 empty cases hold, sw3, sw4 and the SNMPv3 read of sw2 keep their 001/006 outcomes, crawl duration within a few seconds of `main`, no `nm-lab-secret-` in parsed facts or stored raw output, per SC-002 (partial)
- [X] T045 Record in specs/007-device-management-facts/plan.md (Technical Context "Storage") and research.md R8 that migration 0010 grants `netmapper_api` SELECT on `parse_generation`, and why the facts endpoint needs it, per plan: storage decision "No migration" (contradicts)
- [X] T046 Narrow `versions` to '>=4.36' in the six recipes packs/arista_eos/recipes/{snmp,aaa_servers,local_users,management_apis,aaa_methods,management_servers}.yaml until an older EOS release is recorded and passes TestNoSecretInRecordedOutput, and note the reason in research.md R13, per spec edge case "version without the command" and FR-009 (partial)
- [X] T047 Fix specs/007-device-management-facts/quickstart.md: section 3 gets the `| include` commands of the recipes, the stripping of the `> <command>` echo line that `Cli -c` adds, and `show logging | include Logging to`; section 4 expects `empty` on sw1 for `aaa_servers` and `management_servers` only (EOS always prints its method lists, research R13), per FR-014a (partial)
- [X] T048 [VM] Done 2026-10-04. EOS prints both "SNMP agent enabled in VRFs: default" and "SNMP agent disabled", so the template now clears on the disabled line. On test/lab sw1, remove the SNMP community, record `show snmp | no-more` into packs/arista_eos/testdata/lab/sw1nosnmp_show_snmp.raw, put the community back, then check the `SNMP agent disabled` rule of packs/arista_eos/templates/show_snmp.textfsm against the recording and add a test that this output gives `{api: snmp, enabled: no}`, per FR-013a (partial)
- [X] T049 In specs/007-device-management-facts/data-model.md and specs/001-crawl-loop/contracts/fact-families.md, change the Required column of `snmp.access` and `snmp.user` from `v2c`/`v3` to `no`, with the note "set for v2c" / "set for v3", per FR-010 (partial)
