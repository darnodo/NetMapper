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
| access        | string | no       | `ro`, `rw`; set for v2c |
| acl           | string | no       | ACL name bound to the community |
| user          | string | no       | set for v3 |
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

One row per management API and transport: gNMI gives a row per transport, and eAPI a second row,
`transport: http`, when its cleartext HTTP server runs.

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

## Layer 2 families (feature 008)

Written for every identified device, like `interfaces`. The graph engine reads neither in this
feature; the `l2domain` projection (#37) will.

### VLAN lists

A field marked "VLAN list" is a list of strings holding a set of VLAN IDs: each item is `N` or
`lo-hi` (1 to 4094), sorted ascending, with overlapping and adjacent ranges merged, so a set has one
form only. Example: `["1-9", "11-4094"]`. The parser produces this form for every pack; a row in any
other form is rejected. An empty set is an absent field.

### vlans

One row per declared VLAN. Internal VLANs a device allocates for routed ports are not rows. On EOS
this family is never `empty`, since VLAN 1 always exists.

| Field   | Type   | Required                                    |
| ------- | ------ | ------------------------------------------- |
| vlan_id | int    | yes, 1 to 4094                              |
| name    | string | no                                          |
| status  | string | yes: `active`, `suspended`, `shutdown`, `other` |

`shutdown` is a VLAN shut down locally on that device, on a platform that has it (EOS does not).

### interface_vlans

One row per switched port (port-channels included), and one per port-channel member.

| Field         | Type                | Required                                 |
| ------------- | ------------------- | ---------------------------------------- |
| interface     | string              | yes, canonical                           |
| mode          | string              | no: `access`, `trunk`, or as the device names it |
| access_vlan   | int                 | no, access mode only, 1 to 4094          |
| native_vlan   | int                 | no, trunk mode only, 1 to 4094           |
| allowed_vlans | strings, VLAN list  | no, trunk mode only                      |
| active_vlans  | strings, VLAN list  | no, trunk mode only                      |
| channel       | string              | no, canonical, member rows only          |

- A member row has `interface` and `channel` only; VLAN settings are on the channel's row.
- On a trunk row, absent `allowed_vlans` means the trunk allows no VLAN; a trunk allowing every
  VLAN has `["1-4094"]`. The VLAN lists and the native VLAN are those of an active trunk: a trunk the
  device does not report as active has `mode` only (research R2).
- `active_vlans` is what the device reports as allowed and active on the port, not computed.
- Routed ports and management interfaces have no row. A device with none of the above is `empty`.

## Routing families (features 010 and 011)

Written for every identified device, like `interfaces`. The graph engine does not read them, and
no routing peer queues a discovery task.

### Empty and absent lists

The two address family lists of `bgp_peers` are an exception to the rule of the VLAN lists: an empty
list is stored as `[]` and means "none", and an absent list means "not known". A field marked
"only when established" is present exactly when `state` is `established`; a row in any other form
is rejected.

### bgp_peers

One row per BGP peer per VRF. A device that runs no BGP, or runs it with no peer, is `empty`.

| Field                       | Type    | Required                                                       |
| --------------------------- | ------- | -------------------------------------------------------------- |
| vrf                         | string  | yes, `default` for the global table                            |
| peer_address                | string  | yes                                                            |
| peer_as                     | int     | yes                                                            |
| type                        | string  | yes: `ebgp`, `ibgp`                                            |
| state                       | string  | yes: `idle`, `connect`, `active`, `opensent`, `openconfirm`, `established`, `other` |
| shutdown                    | string  | yes: `yes`, `no`                                               |
| dynamic                     | string  | yes: `yes`, `no`                                               |
| local_as                    | int     | yes                                                            |
| router_id                   | string  | yes                                                            |
| configured_address_families | strings | yes, sorted, `[]` when none of the covered families       |
| negotiated_address_families | strings | only when established, sorted, `[]` when none                  |
| peer_router_id              | string  | no, only when established                                      |
| peer_group                  | string  | no                                                             |
| description                 | string  | no                                                             |

- Identity within an observation: `vrf` and `peer_address`.
- Address family names are `ipv4 unicast`, `ipv6 unicast`, `l2vpn evpn`, or as the device names
  any other family. Both lists are sorted, with no duplicate.
- The configured list covers only the families the implementation declares in its recipe
  (`covers`), found through the observation's recipe ID. On `arista_eos`: `ipv4 unicast`,
  `ipv6 unicast`, `l2vpn evpn`, in every VRF for the first two, VRF `default` for EVPN. An empty
  list means none of those; a family outside them can appear only in the negotiated list.
- `empty` means the device answered that BGP is not running, or has no peer. Any error output is
  `parse_failed`, never `empty`.
- A family configured and not negotiated on an established session is a capability mismatch. It is
  read by comparing the two lists; no field stores it.
- `shutdown: yes` is a peer shut by configuration; its state is `idle`.
- `dynamic: yes` is a peer accepted from a listen range. It has a row while the device lists it;
  its disappearance is a state change, not a configuration change.
- On a row not established, a negotiated list or peer router ID the device still prints from the
  last session is not recorded. A row that breaks any rule above makes the observation
  `parse_failed`; rows are never dropped one by one.
- No uptime, counters, timers or last-reset details are recorded. Two collections of an unchanged
  network give the same rows.
- No field holds a password, or says whether one is set.

### ospf_interfaces

One row per interface in OSPFv2. A device that runs no OSPF, or runs it with no interface, is
`empty`.

| Field         | Type   | Required |
| ------------- | ------ | -------- |
| vrf           | string | yes, `default` for the global table |
| instance      | string | yes, as the device names it |
| router_id     | string | yes |
| interface     | string | yes, canonical |
| area          | string | yes, dotted form |
| area_type     | string | yes: `normal`, `stub`, `nssa` |
| network_type  | string | yes: `broadcast`, `point_to_point`, `nbma`, `point_to_multipoint`, `loopback`, `other` |
| cost          | int    | yes |
| passive       | string | yes: `yes`, `no` |
| state         | string | yes: `dr`, `bdr`, `drother`, `point_to_point`, `loopback`, `waiting`, `down`, `other` |
| auth_type     | string | yes: `none`, `simple`, `md5`, `sha`, `message_digest`, `other`, `unknown` |
| dr_router_id  | string | no |
| bdr_router_id | string | no |

- Identity within an observation: `vrf` and `interface`.
- `area` is always in dotted form; an area the device prints as an integer is converted (1
  becomes `0.0.0.1`).
- `area_type` of a stub or NSSA area with summaries suppressed is its base type.
- `auth_type` is `none` only when the device reports that no authentication is configured.
  `unknown` means the platform or transport did not expose the type; the parser sets it when no
  step gives a value, and a recipe cannot default it. `message_digest` is a keyed-hash
  authentication whose algorithm the platform does not expose (`arista_eos`: md5 and sha256 print
  the same); `md5` and `sha` are only recorded where the device names the algorithm. No field holds
  a key or key ID.
- `dr_router_id` and `bdr_router_id` are the router IDs of the segment's DR and BDR, on every
  transport, never their interface addresses. Each is absent when the device reports no router in
  that role. An implementation whose source gives an address (SNMP `ospfIfDesignatedRouter`,
  `ospfIfBackupDesignatedRouter`) resolves it through `ospfNbrTable` (`ospfNbrIpAddr` to
  `ospfNbrRtrId`), or to `ospfRouterId` when it is the device's own; when neither resolves, the
  field is `unknown`, never the raw address.
- `state` and the DR and BDR router IDs are statuses: they change with an election or a link
  failure, not with traffic.

### ospf_neighbours

One row per OSPFv2 adjacency. A device with no adjacency is `empty`, whatever its
`ospf_interfaces` observation says.

| Field               | Type   | Required |
| ------------------- | ------ | -------- |
| vrf                 | string | yes, `default` for the global table |
| instance            | string | yes |
| router_id           | string | yes, the local router ID |
| interface           | string | yes, canonical |
| area                | string | yes, dotted form |
| neighbour_router_id | string | yes |
| neighbour_address   | string | yes |
| state               | string | yes: `down`, `attempt`, `init`, `2way`, `exstart`, `exchange`, `loading`, `full`, `other` |

- Identity within an observation: `vrf`, `interface` and `neighbour_router_id`.
- A neighbour's DR or BDR role is not a field: compare `neighbour_router_id` with the interface
  row's `dr_router_id` and `bdr_router_id`.

### Rules common to both OSPF families

- `empty` means the device said it has nothing, each step having completed normally. On
  `arista_eos`, "no OSPF here" is the guard step's answer to `show ip ospf vrf all | json |
  no-more`: a `vrfs` key holding an empty object, whatever its whitespace and line breaks; a blank
  answer there is `parse_failed`. Once the guard shows OSPF running, a blank neighbour output means
  no adjacency. A blank interface output means no interface only if the guard counts none
  (`numIntf`); otherwise it is `parse_failed`. A blank output after a timeout, a dropped session
  or a missing prompt is never `empty`: the observation is `unreachable`, or the task fails with no
  observation for the family. Any error output is `parse_failed`.
- `router_id` and `area_type` come from a joined step (`join_on`, `unmatched: ignore` on
  `arista_eos`): an instance or area with no row to join onto is not recorded.
- No timer, counter, age or LSA/SPF statistic is recorded. Two collections of an unchanged
  network give the same rows.
- A row that breaks any rule makes the observation `parse_failed`; rows are never dropped one by
  one.
- OSPFv3 is not collected.

## A family with nothing to run

Every family is written for every identified device, whether or not its pack has a recipe for it.
When nothing can run, the observation is `unsupported` with one of two details:

- `no_recipe`: the pack defines no recipe for the family;
- `no_matching_version`: the pack has a recipe, but no implementation's `versions` fits the device's
  version.

Neither changes the snapshot verdict, which counts `identity` observations only.

Other families (`mac_table`, `arp_table`, ...) follow the same rule: added here first, then in packs.
This feature ships `identity`, `neighbours` and `interfaces`; feature 007 adds the six management
families above, feature 008 adds `vlans` and `interface_vlans`, feature 010 adds `bgp_peers`, and
feature 011 adds `ospf_interfaces` and `ospf_neighbours`.
