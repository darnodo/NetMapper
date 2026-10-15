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

## New field attribute: `DottedID`

A string field marked `DottedID` holds a 32-bit identifier in dotted form (`0.0.0.1`). The parser
converts a plain integer from 0 to 4294967295 to that form (1 becomes `0.0.0.1`, 256 becomes
`0.0.1.0`) and keeps a dotted value as is. `fact.Validate` rejects any other form, so a value
that is neither is `parse_failed`. Both families' `area` fields use it (spec FR-003). It is
vendor-neutral: OSPF areas are 32-bit on every platform, and some print them as integers.

## New recipe key: `join_on` (per step)

```yaml
- command: show ip ospf vrf all | no-more
  template: show_ip_ospf_vrf_all.textfsm
  join_on: [vrf, instance, area]
  map: { vrf: VRF, instance: INSTANCE, area: AREA, router_id: ROUTER_ID, area_type: AREA_TYPE }
```

A step with `join_on` adds its fields to every row so far that holds the same values on those
fields, without overwriting a value already set. It never creates a row. Its rows do not make the
observation non-empty, so a device whose only output is the instance view is `empty`. Steps
without `join_on` behave as before (research R2).

A step with `join_on` must also declare `unmatched`, with no default:

- `fail`: a join row that matches no row makes the observation `parse_failed`, naming the step and
  the key;
- `ignore`: such rows stay out of the observation; the parser returns their keys and the collector
  logs them, one `info` line per observation. The raw output is stored as for any step.

The registry refuses `join_on` with no row-creating step before it, `join_on` without
`unmatched`, and either key in a pack whose `pack_schema` is below 2.

## New recipe key: `guard` (per step)

```yaml
- command: show ip ospf vrf all | json
  template: show_ip_ospf_vrf_all_json.textfsm
  guard: true
  nothing_pattern: '^\{"vrfs":\{\}\}$'   # matched against the output with all whitespace removed
```

A guard step decides whether the family has anything on this device, with a "none" answer that is
not blank (research R14):

- its output, with all whitespace removed, matches its `nothing_pattern`: the observation is
  `empty`; every other step must be nothing too, or the observation is `parse_failed`. Whitespace
  and line breaks in the device's answer never change the outcome;
- its output is blank: `parse_failed`, never nothing;
- otherwise its template must yield a row, as for any step; the rows are not mapped into the
  family and create no row.

A guard step has no `map` and no `empty_lines`, and is the first step. The registry refuses a
guard that is not first, has a `map` or `empty_lines`, has no `nothing_pattern`, or sits in a pack
below `pack_schema` 2. `nothing_pattern` is refused on a step that is not a guard. (The key was
first drafted as `none`; renamed so it cannot be confused with the `auth_type` value `none`.)

## New pack key: `pack_schema`

`pack.yaml` gains `pack_schema`, the pack format version. Absent means 1: every existing pack loads
unchanged. Version 2 adds `guard`, `nothing_pattern`, `join_on` and `unmatched`. The `covers` key of feature 010
stays valid in both versions. A pack declaring a version the registry does
not know is refused. `arista_eos` declares 2.

## `ospf_interfaces`

One row per interface in OSPF, identified by `vrf` and `interface`.

| Field         | Type   | Rule |
| ------------- | ------ | ---- |
| vrf           | string | required, `default` for the global table |
| instance      | string | required, as the device names it |
| router_id     | string | required, the instance's router ID (from the join) |
| interface     | string | required, canonical |
| area          | string | required, `DottedID`: dotted form, a plain integer converted (1 becomes `0.0.0.1`) |
| area_type     | string | required: `normal`, `stub`, `nssa` (from the join) |
| network_type  | string | required: `broadcast`, `point_to_point`, `nbma`, `point_to_multipoint`, `loopback`, `other` |
| cost          | int    | required |
| passive       | string | required: `yes`, `no` |
| state         | string | required: `dr`, `bdr`, `drother`, `point_to_point`, `loopback`, `waiting`, `down`, `other` |
| auth_type     | string | required, `Missing: unknown`: `none`, `simple`, `md5`, `sha`, `other`, `unknown` |
| dr_router_id  | string | optional, when the device reports a DR on the segment: a router ID, or `unknown` |
| bdr_router_id | string | optional, when the device reports a BDR on the segment: a router ID, or `unknown` |

`dr_router_id` and `bdr_router_id` are router IDs on every transport, never segment addresses
(research R5, spec FR-004a). `unknown` means a router holds the role and its router ID could not be
resolved; absent means no router holds it. The EOS CLI prints router IDs, so the SSH recipe never
writes `unknown` there. If recordings show EOS printing them on a point-to-point interface, `OnlyWith` is
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
| area                | string | required, `DottedID`: dotted form, a plain integer converted |
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
