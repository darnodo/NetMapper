# Fact families added by feature 011

To merge into `specs/001-crawl-loop/contracts/fact-families.md`, under "Routing families", after
`bgp_peers`, in the same change as the code (FR-011). The section heading becomes "Routing
families (features 010 and 011)".

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
| auth_type     | string | yes: `none`, `simple`, `md5`, `sha`, `other`, `unknown` |
| dr_router_id  | string | no |
| bdr_router_id | string | no |

- Identity within an observation: `vrf` and `interface`.
- `area` is always in dotted form; an area the device prints as an integer is converted (1
  becomes `0.0.0.1`).
- `area_type` of a stub or NSSA area with summaries suppressed is its base type.
- `auth_type` is `none` only when the device reports that no authentication is configured.
  `unknown` means the platform or transport did not expose the type; the parser sets it when no
  step gives a value, and a recipe cannot default it. No field holds a key or key ID.
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

### Rules common to both

- `empty` means the device said it has nothing, each step having completed normally. On
  `arista_eos`, "no OSPF here" is the guard step's answer to `show ip ospf vrf all | json`: a `vrfs`
  key holding an empty object, whatever its whitespace and line breaks; a blank answer there is `parse_failed`. Once the guard shows OSPF running, a blank
  interface or neighbour output means no interface or no adjacency. A blank output after a timeout,
  a dropped session or a missing prompt is never `empty`: the observation is `unreachable`, or the
  task fails with no observation for the family. Any error output is `parse_failed`.
- `router_id` and `area_type` come from a joined step (`join_on`, `unmatched: ignore` on
  `arista_eos`): an instance or area with no row to join onto is not recorded.
- No timer, counter, age or LSA/SPF statistic is recorded. Two collections of an unchanged
  network give the same rows.
- A row that breaks any rule makes the observation `parse_failed`; rows are never dropped one by
  one.
- OSPFv3 is not collected.
