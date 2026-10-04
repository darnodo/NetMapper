# Data model: device management configuration facts

No table, column or migration. Each family is a set of rows in `observation.parsed`, validated by
`internal/fact` like `neighbours` and `interfaces`. The contract text that ships is
[contracts/fact-families.md](contracts/fact-families.md), merged into
`specs/001-crawl-loop/contracts/fact-families.md` in the implementation (FR-010).

Types are the existing ones: `string`, `int`, `strings` (ordered list). `yes`/`no` stands for a
boolean (research R6). No field holds a secret (FR-002 to FR-004, FR-009).

## snmp

One row per v2c community or v3 user.

| Field         | Type   | Required | Values / notes |
| ------------- | ------ | -------- | -------------- |
| version       | string | yes      | `v2c`, `v3` |
| access        | string | v2c      | `ro`, `rw` |
| acl           | string | no       | ACL name bound to the community |
| user          | string | v3       | v3 user name |
| group         | string | no       | v3 group |
| auth_protocol | string | no       | `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512` (names of feature 006) |
| priv_protocol | string | no       | `des`, `3des`, `aes`, `aes192`, `aes256`; absent means authNoPriv (research R4) |

No community string, no passphrase, no VRF: the agent's VRFs are on the `management_apis` row
`api: snmp` (research R2). Two communities with the same access and ACL give two identical rows.

## aaa_servers

One row per RADIUS or TACACS+ server.

| Field    | Type   | Required | Values / notes |
| -------- | ------ | -------- | -------------- |
| protocol | string | yes      | `radius`, `tacacs` |
| address  | string | yes      | as the device prints it, never resolved |
| port     | int    | no       | only when the output shows it (FR-007a) |
| vrf      | string | yes      | `default` when none is stated (FR-008) |
| group    | string | no       | server group; absent for a server in no named group |

## local_users

One row per local account.

| Field     | Type   | Required | Values / notes |
| --------- | ------ | -------- | -------------- |
| name      | string | yes      | |
| role      | string | no       | |
| privilege | int    | no       | |
| ssh_key   | string | yes      | `yes`, `no`; the key itself is not stored |

## management_apis

One row per management API.

| Field     | Type    | Required | Values / notes |
| --------- | ------- | -------- | -------------- |
| api       | string  | yes      | `gnmi`, `eapi`, `netconf`, `ssh`, `telnet`, `snmp` |
| enabled   | string  | yes      | `yes`, `no`; configured but shut down, or never configured, is `no` |
| transport | string  | no       | as printed: `grpc`, `https`, `http`, `ssh`, ... |
| port      | int     | no       | only when the output shows it |
| vrfs      | strings | no       | VRFs the API serves, in the device's order (research R3) |

## aaa_methods

One row per method list. Key: `type`, `service`, `list`, `level`.

| Field   | Type    | Required | Values / notes |
| ------- | ------- | -------- | -------------- |
| type    | string  | yes      | `authentication`, `authorization`, `accounting` |
| service | string  | yes      | `login`, `enable`, `exec`, `commands`, `system`, `dot1x`, or another the device names, lowercase |
| list    | string  | yes      | `default` or a named list |
| level   | string  | no       | `commands` only: `all` or `0` to `15`, as printed |
| record  | string  | no       | accounting only: `start-stop`, `stop-only` |
| methods | strings | yes      | in order, as printed: `group NM-TACACS`, `local`, `logging` |

`level` is a string because `all` is a valid value and is not converted to a number.

## management_servers

One row per NTP, syslog or DNS server.

| Field   | Type   | Required | Values / notes |
| ------- | ------ | -------- | -------------- |
| service | string | yes      | `ntp`, `syslog`, `dns` |
| address | string | yes      | as printed, never resolved |
| port    | int    | no       | only when the output shows it |
| vrf     | string | yes      | `default` when none is stated |

## Observation status per family

Unchanged rules (`internal/parse`):

| Situation | Status |
| --------- | ------ |
| Rows parsed | `collected` |
| Every step printed nothing, or only its `empty_lines` | `empty` |
| A step's output gave no row and is not empty | `parse_failed` |
| A row breaks the schema (unknown field, missing required field, value outside the enum) | `parse_failed` |
| Pack has no recipe (any platform but Arista EOS) | `unsupported`, detail `no_recipe` |

## Recipe format additions (internal/pack)

| Key | Level | Type | Validation at load |
| --- | ----- | ---- | ------------------ |
| `map` | step | field → column or `=literal` | every field in the family schema, like the implementation `map` |
| `defaults` | implementation | field → literal | field in the schema; value passes the field's enum and type |
| `split` | implementation | field → separator | field in the schema and of type `strings`; separator not empty |

Order applied to each row: step `map` over implementation `map`, then `values`, then `split`, then
`defaults` for fields still absent, then canonical and MAC normalisation as today, then
`fact.Validate` on the whole set.

## Read model

No new storage. `GET /v1/devices/{name}/facts/{family}` reads
`observation` (active parse generation) joined to the device's `entity.attributes.targets`; see
[contracts/rest-facts.md](contracts/rest-facts.md).
