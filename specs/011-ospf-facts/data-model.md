# Data model: OSPF interfaces and adjacencies

No table changes. Both families are rows in `observation.parsed`, like every fact family; the raw
outputs go to the object store. What changes is the schema in `internal/fact`, one field
attribute, and one recipe key the parser understands.

## New field attribute: `Missing`

A field marked `Missing: "unknown"` is never absent from a parsed row. When no step gives it a
value, after the merge, the join and the recipe defaults, the parser sets it to `unknown`. The
field stays `Required`, so `fact.Validate` still rejects a row without it that did not go through
the parser. Only `ospf_interfaces.auth_type` uses it. The registry refuses a recipe `defaults`
entry on a `Missing` field, so a pack cannot turn a missing value into `none` (spec FR-004,
research R6).

## New recipe key: `join_on` (per step)

```yaml
- command: show ip ospf vrf all | no-more
  template: show_ip_ospf_vrf_all.textfsm
  join_on: [vrf, instance, area]
  map: { vrf: VRF, instance: INSTANCE, area: AREA, router_id: ROUTER_ID, area_type: AREA_TYPE }
```

A step with `join_on` adds its fields to every row so far that holds the same values on those
fields, without overwriting a value already set. It never creates a row: a join row that matches
nothing is ignored. Its rows do not make the observation non-empty, so a device whose only output
is the instance view is `empty`. The registry refuses `join_on` on the first step. Steps without
`join_on` behave as before (research R2).

## `ospf_interfaces`

One row per interface in OSPF, identified by `vrf` and `interface`.

| Field         | Type   | Rule |
| ------------- | ------ | ---- |
| vrf           | string | required, `default` for the global table |
| instance      | string | required, as the device names it |
| router_id     | string | required, the instance's router ID (from the join) |
| interface     | string | required, canonical |
| area          | string | required, dotted form |
| area_type     | string | required: `normal`, `stub`, `nssa` (from the join) |
| network_type  | string | required: `broadcast`, `point_to_point`, `nbma`, `point_to_multipoint`, `loopback`, `other` |
| cost          | int    | required |
| passive       | string | required: `yes`, `no` |
| state         | string | required: `dr`, `bdr`, `drother`, `point_to_point`, `loopback`, `waiting`, `down`, `other` |
| auth_type     | string | required, `Missing: unknown`: `none`, `simple`, `md5`, `sha`, `other`, `unknown` |
| dr_router_id  | string | optional, when the device reports a DR on the segment |
| bdr_router_id | string | optional, when the device reports a BDR on the segment |

`dr_router_id` and `bdr_router_id` are router IDs, not segment addresses (research R5, spec
amendment). If recordings show EOS printing them on a point-to-point interface, `OnlyWith` is
extended to a list of values and both fields become `OnlyWith: network_type in (broadcast,
nbma)`.

## `ospf_neighbours`

One row per adjacency, identified by `vrf`, `interface` and `neighbour_router_id`.

| Field               | Type   | Rule |
| ------------------- | ------ | ---- |
| vrf                 | string | required, `default` for the global table |
| instance            | string | required |
| router_id           | string | required, the local instance's router ID (from the join) |
| interface           | string | required, canonical |
| area                | string | required, dotted form |
| neighbour_router_id | string | required |
| neighbour_address   | string | required |
| state               | string | required: `down`, `attempt`, `init`, `2way`, `exstart`, `exchange`, `loading`, `full`, `other` |

## State changes

The interface state, the DR and BDR router IDs and the adjacency state are statuses: a later
observation records whatever the device reports at collection time. There is no transition table
to enforce; any value of the enum can follow any other. A neighbour the device stops listing has no
row in the next observation.

## Relationships

- An `ospf_interfaces` row joins the same device's `interfaces` row on `interface` (spec SC-005).
- An `ospf_neighbours` row joins the same device's `ospf_interfaces` row on `vrf` and `interface`.
- A neighbour's DR or BDR role is read by comparing `neighbour_router_id` with the local
  interface row's `dr_router_id` and `bdr_router_id`.
- The same adjacency seen from both ends is two rows on two devices. Nothing joins them in this
  feature.
