# Data model: VLANs and interface VLAN membership

No table changes. Both families are rows in `observation.parsed`, like every fact family; the raw
outputs go to the object store. What changes is the schema in `internal/fact` and one field
attribute.

## New field attribute: `VLANList`

A `strings` field marked `VLANList` holds a set of VLAN IDs as sorted, merged ranges:

- each item is `N` or `lo-hi`, with 1 <= N, lo < hi <= 4094;
- items are in ascending order, and no two items overlap or touch (`10-11, 12` is `10-12`);
- one set has exactly one form: `["1-9", "11-4094"]`, `["10", "20", "30-32"]`, `["1-4094"]`.

The parser builds that form from whatever the template gives (research R4, R5). `fact.Validate`
rejects a row whose `VLANList` field is not in it, so a pack that bypasses the parser still cannot
store another form. No item means the field is absent (the parser's existing rule for lists).

## `vlans`

One row per VLAN the device lists as declared. Internal VLANs of routed ports are not rows (R1).

| Field | Type | Required | Notes |
| ----- | ---- | -------- | ----- |
| `vlan_id` | int | yes | 1 to 4094 |
| `name` | string | no | as printed; EOS prints a default such as `VLAN0010` when none is set |
| `status` | string | yes | `active`, `suspended`, `shutdown`, `other` |

Identity within an observation: `vlan_id`. Never `empty` on EOS (VLAN 1 always exists).

## `interface_vlans`

One row per switched port, port-channels included, and one per port-channel member.

| Field | Type | Required | Notes |
| ----- | ---- | -------- | ----- |
| `interface` | string | yes, canonical | joins `interfaces.name` and `neighbours.local_interface` |
| `mode` | string | no | `access`, `trunk`, or as printed (`dot1q-tunnel`, `tap`, `tool`); absent on a member row |
| `access_vlan` | int | no | access mode only |
| `native_vlan` | int | no | trunk mode only |
| `allowed_vlans` | strings, `VLANList` | no | trunk mode only; absent on an active trunk means no VLAN allowed |
| `active_vlans` | strings, `VLANList` | no | trunk mode only, as the device reports it; absent means none active |
| `channel` | string | no, canonical | member rows only: the port-channel this port belongs to |

Identity within an observation: `interface`.

Rules:

- A trunk that is not active may be missing from the device's trunk view (research R2): its row
  then has `mode: trunk` and no VLAN field.
- A member row has `interface` and `channel` and nothing else. The channel's VLAN settings are on
  the channel's own row (FR-008).
- A member of a routed port-channel still gets its member row; the channel has no row, which is what
  says it carries no VLAN.
- Routed ports and management interfaces have no row (FR-009).
- An unconfigured Ethernet port is an access port in VLAN 1 on EOS and has a row.

## Relationships

- `interface_vlans.interface` and `interface_vlans.channel` are canonical names, so they join
  `interfaces.name` and `neighbours.local_interface` of the same device and snapshot.
- `interface_vlans.access_vlan`, `native_vlan` and the items of `allowed_vlans` and `active_vlans`
  refer to VLAN IDs of the same device, which may or may not be rows of its `vlans` (an undeclared
  VLAN can be configured on a port).
- Nothing in this feature joins VLANs across devices. That is the `l2domain` projection (#37).

## Observation statuses

| Situation | `vlans` | `interface_vlans` |
| --------- | ------- | ----------------- |
| EOS 4.36 or later, normal | `collected` | `collected` |
| no switched port and no member (sw4, a spine) | n/a | `empty` |
| output the template does not understand | `parse_failed` | `parse_failed` |
| EOS before 4.36 | `unsupported` (`no_matching_version`) | same |
| another platform | `unsupported` (`no_recipe`) | same |

Neither family changes the snapshot verdict, which counts `identity` only.
