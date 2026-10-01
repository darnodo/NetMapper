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

## A family with nothing to run

Every family is written for every identified device, whether or not its pack has a recipe for it.
When nothing can run, the observation is `unsupported` with one of two details:

- `no_recipe`: the pack defines no recipe for the family;
- `no_matching_version`: the pack has a recipe, but no implementation's `versions` fits the device's
  version.

Neither changes the snapshot verdict, which counts `identity` observations only.

Other families (`mac_table`, `arp_table`, ...) follow the same rule: added here first, then in packs.
This feature ships `identity`, `neighbours` and `interfaces`.
