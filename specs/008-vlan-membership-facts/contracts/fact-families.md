# Fact families added by feature 008

To merge into `specs/001-crawl-loop/contracts/fact-families.md`, as a section after the management
families, in the same change as the code (FR-012).

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
| vlan_id | int    | yes                                         |
| name    | string | no                                          |
| status  | string | yes: `active`, `suspended`, `shutdown`, `other` |

`shutdown` is a VLAN shut down locally on that device.

### interface_vlans

One row per switched port (port-channels included), and one per port-channel member.

| Field         | Type                | Required                                 |
| ------------- | ------------------- | ---------------------------------------- |
| interface     | string              | yes, canonical                           |
| mode          | string              | no: `access`, `trunk`, or as the device names it |
| access_vlan   | int                 | no, access mode only                     |
| native_vlan   | int                 | no, trunk mode only                      |
| allowed_vlans | strings, VLAN list  | no, trunk mode only                      |
| active_vlans  | strings, VLAN list  | no, trunk mode only                      |
| channel       | string              | no, canonical, member rows only          |

- A member row has `interface` and `channel` only; VLAN settings are on the channel's row.
- On a trunk row, absent `allowed_vlans` means the trunk allows no VLAN; a trunk allowing every
  VLAN has `["1-4094"]`. The VLAN lists and the native VLAN are those of an active trunk: a trunk the
  device does not report as active may have `mode` only (to settle when recording, research R2).
- `active_vlans` is what the device reports as allowed and active on the port, not computed.
- Routed ports and management interfaces have no row. A device with none of the above is `empty`.
