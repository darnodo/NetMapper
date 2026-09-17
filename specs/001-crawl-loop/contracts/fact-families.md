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
| remote_mgmt_address | string | no             |

A row without `remote_mgmt_address` (or whose name does not resolve) is kept in the observation and
queues nothing: known but not collected (spec edge case). A row whose address is outside the
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

Other families (`mac_table`, `arp_table`, ...) follow the same rule: added here first, then in packs.
This feature ships `identity`, `neighbours` and `interfaces`.
