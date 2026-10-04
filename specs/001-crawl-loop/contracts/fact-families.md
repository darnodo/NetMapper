# Contract: fact families written by this feature

`observation.parsed` holds an array of rows in the schema below. Schemas are platform neutral and
versioned in code (`internal/fact`), since the engine reads them. Fields not listed are rejected by the
mapper.

## identity

Written by every `find` that attempted the target, including the ones that fail. Never needs a pack to
exist, so an unreachable, denied or unidentified target always has one observation. A target skipped
as out of perimeter has none: it is a `skipped` task, not an observation.

| Field               | Type     | Notes                                                 |
| ------------------- | -------- | ----------------------------------------------------- |
| platform            | string   | pack name, absent when unidentified                   |
| os_version          | string   |                                                       |
| sys_object_id       | string   | when SNMP answered                                    |
| sys_descr           | string   |                                                       |
| transports_answered | string[] | `ssh`, `snmp`                                         |
| duplicate_of_task   | int      | set when another task already holds a strong claim with the same identifier |

Status mapping: `collected` when a pack matched; `unsupported` with detail `unknown_platform` when a
transport answered and no pack matched; `denied`; `unreachable` when every transport was silent.

## neighbours

| Field               | Type   | Required       |
| ------------------- | ------ | -------------- |
| protocol            | string | yes            |
| local_interface     | string | yes, canonical |
| remote_chassis_id   | string | no             |
| remote_system_name  | string | no             |
| remote_interface    | string | no             |
| remote_mgmt_address | string | no; if present, `remote_mgmt_address_type` is required beside it |
| remote_mgmt_address_type | string | `ipv4`, `ipv6`, `mac`, `other`: the LLDP management address subtype, translated by the pack |

The address is stored as the device reported it, with its type. A `mac` value is normalised to the
lowercase colon form; an IP is left as given. A consumer reads the type, never guesses it from the
value.

Only a row whose address is typed `ipv4` or `ipv6` queues a `find`. A row with no address, or with
a MAC or another kind of address, is kept in the observation and queues nothing: known but not
collected (spec edge case). Names are never resolved. A system name is what the neighbour declares,
not an address it is reachable at; a lookup could point at the wrong machine, would send a query
about a device that may be outside the perimeter, and would make two identical runs differ with the
state of a DNS zone. Matching the name against an entity discovered elsewhere is identity
resolution's job. A row whose address is outside the
perimeter is inserted as a `find` task in state `skipped` with `skip_reason = 'out_of_perimeter'`, in
the same transaction that writes this observation. No observation is written for it, since no packet
was sent. The dial check still refuses the address if anything else ever tries to reach it.

## interfaces

| Field       | Type   | Required                            |
| ----------- | ------ | ----------------------------------- |
| name        | string | yes, canonical                      |
| description | string | no                                  |
| admin_state | string | `up`, `down`                        |
| oper_state  | string | `up`, `down`, `other`               |
| speed_bps   | int    | no                                  |
| mtu         | int    | no                                  |
| mac         | string | no, normalised lowercase colon form |

## Management families (feature 007)

Written for every identified device, like `interfaces`. They describe the device, not its links:
the engine does not read them, and they do not count toward the snapshot verdict (FR-015).

Rules common to the six:

- **No secret, ever.** No field holds a community string, a server or NTP key in any encoding, a
  password or a hash. The stored raw output of the commands behind them holds none either: a pack
  uses only commands that print none (FR-009). A community is recorded by its access level and ACL.
- **A port is what the device printed.** `port` is absent when the output shows none. A pack never
  fills in a protocol's default.
- **A VRF is always set where the schema has one.** A server with no VRF stated is `vrf: default`.
  `snmp` has no VRF: where a platform binds SNMP to VRFs per agent, they are on the
  `management_apis` row `api: snmp`.
- **Addresses are stored as given** and never resolved, like a neighbour's system name.
- **Nothing configured is `empty`.** An output the template does not understand is `parse_failed`.
  A consumer may read `empty` as "the device was asked and has none"; it may not read a missing
  observation that way.
- `yes`/`no` fields are strings, by the same convention as `admin_state`.

Status mapping: `collected`; `empty`; `parse_failed`; `unsupported` with `no_recipe` for a pack with
no recipe; `unreachable` and `denied` as for every scraped family.

### snmp

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

### aaa_servers

One row per RADIUS or TACACS+ server.

| Field    | Type   | Required | Values / notes |
| -------- | ------ | -------- | -------------- |
| protocol | string | yes      | `radius`, `tacacs` |
| address  | string | yes      | as the device prints it, never resolved |
| port     | int    | no       | only when the output shows it (FR-007a) |
| vrf      | string | yes      | `default` when none is stated (FR-008) |
| group    | string | no       | server group; absent for a server in no named group |

### local_users

One row per local account.

| Field     | Type   | Required | Values / notes |
| --------- | ------ | -------- | -------------- |
| name      | string | yes      | |
| role      | string | no       | |
| privilege | int    | no       | |
| ssh_key   | string | yes      | `yes`, `no`; the key itself is not stored |

### management_apis

One row per management API.

| Field     | Type    | Required | Values / notes |
| --------- | ------- | -------- | -------------- |
| api       | string  | yes      | `gnmi`, `eapi`, `netconf`, `ssh`, `telnet`, `snmp` |
| enabled   | string  | yes      | `yes`, `no`; configured but shut down, or never configured, is `no` |
| transport | string  | no       | as printed: `grpc`, `https`, `http`, `ssh`, ... |
| port      | int     | no       | only when the output shows it |
| vrfs      | strings | no       | VRFs the API serves, in the device's order (research R3) |

### aaa_methods

One row per method list. Key: `type`, `service`, `list`, `level`.

| Field   | Type    | Required | Values / notes |
| ------- | ------- | -------- | -------------- |
| type    | string  | yes      | `authentication`, `authorization`, `accounting` |
| service | string  | yes      | `login`, `enable`, `exec`, `commands`, `system`, `dot1x`, or another the device names, lowercase |
| list    | string  | no       | the list name, where the device prints one (EOS: authentication lists only, research R13) |
| level   | string  | no       | `commands` only: the privilege level or range, as printed (EOS: `0-15`) |
| record  | string  | no       | accounting only: `start-stop`, `stop-only` |
| methods | strings | no       | in order, as printed: `local`, `group NM-TACACS`, `none`; absent when the device lists none |

`level` is a string because a range such as `0-15` is a valid value. EOS prints every method list,
configured or not, so this family is never `empty` on EOS.

### management_servers

One row per NTP, syslog or DNS server.

| Field   | Type   | Required | Values / notes |
| ------- | ------ | -------- | -------------- |
| service | string | yes      | `ntp`, `syslog`, `dns` |
| address | string | yes      | as printed, never resolved |
| port    | int    | no       | only when the output shows it |
| vrf     | string | yes      | `default` when none is stated |

## A family with nothing to run

Every family is written for every identified device, whether or not its pack has a recipe for it.
When nothing can run, the observation is `unsupported` with one of two details:

- `no_recipe`: the pack defines no recipe for the family;
- `no_matching_version`: the pack has a recipe, but no implementation's `versions` fits the device's
  version.

Neither changes the snapshot verdict, which counts `identity` observations only.

Other families (`mac_table`, `arp_table`, ...) follow the same rule: added here first, then in packs.
This feature ships `identity`, `neighbours` and `interfaces`; feature 007 adds the six management
families above.
