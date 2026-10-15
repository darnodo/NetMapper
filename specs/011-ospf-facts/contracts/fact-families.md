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
- `area_type` of a stub or NSSA area with summaries suppressed is its base type.
- `auth_type` is `none` only when the device reports that no authentication is configured.
  `unknown` means the platform or transport did not expose the type; the parser sets it when no
  step gives a value, and a recipe cannot default it. No field holds a key or key ID.
- `dr_router_id` and `bdr_router_id` are the router IDs of the segment's DR and BDR, not their
  addresses. Each is absent when the device reports no router in that role.
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

- `empty` means the device gave its "nothing here" answer to every step that creates rows (on
  `arista_eos`, a blank output). Any error output is `parse_failed`, never `empty`.
- No timer, counter, age or LSA/SPF statistic is recorded. Two collections of an unchanged
  network give the same rows.
- A row that breaks any rule makes the observation `parse_failed`; rows are never dropped one by
  one.
- OSPFv3 is not collected.
