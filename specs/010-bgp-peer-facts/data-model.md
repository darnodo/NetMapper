# Data model: BGP sessions

No table changes. The family is rows in `observation.parsed`, like every fact family; the raw
outputs go to the object store. What changes is the schema in `internal/fact`, two field
attributes, and how the parser merges list fields.

## New field attribute: `EmptyList`

A `strings` field marked `EmptyList` is stored as `[]` when it has no item, instead of being left
out. It is the explicit exception to feature 008's rule that an empty set is an absent field, and
only the two address family lists of `bgp_peers` use it. With `Required`, the field is on every row.

## New field attribute: `OnlyWith`

A field marked `OnlyWith: {state, established}` is present exactly when the row's `state` is
`established`. After merging the steps, the parser drops it from rows where the condition does not
hold, and, for an `EmptyList` field, sets `[]` on rows where it holds and no item was found.
`fact.Validate` rejects a row with the field when the condition does not hold, and a row without
it when the condition holds and the field is `EmptyList`.

## New field attribute: `Set`

A `strings` field marked `Set` holds items whose order means nothing. The parser sorts it and
removes duplicates after the merge; `fact.Validate` rejects a list that is not sorted or holds a
duplicate. Two runs whose commands or capability lines come in a different order give the same row.

## New recipe key: `covers`

An implementation may declare, per list field, the values its steps can put there:

```yaml
covers:
  configured_address_families: [ipv4 unicast, ipv6 unicast, l2vpn evpn]
```

The registry refuses the pack at load when the literal values the steps map into that field are not
exactly that set. A list holding none of them is empty and means "none of the covered values".

## List merge

When two rows merge on `merge_on` and both carry a `strings` field, the later row's items are
appended to the earlier row's, skipping those already there. Before this feature the later value
was dropped; no recipe merged a list field, so no existing family changes (research R5).

A default on a `merge_on` field still fills each row before the merge, so rows join on it. Any
other default now fills the merged row: before, step 1's row carried `dynamic: no` from the default
and the peer-group step's `dynamic: yes` was dropped by the merge. Without `merge_on` the result is
the same as before.

## `bgp_peers`

One row per BGP peer per VRF.

| Field | Type | Required | Notes |
| ----- | ---- | -------- | ----- |
| `vrf` | string | yes | `default` for the global table |
| `peer_address` | string | yes | IPv4 or IPv6, as printed |
| `peer_as` | int | yes | as configured (asplain) |
| `type` | string | yes | `ebgp`, `ibgp` |
| `state` | string | yes | `idle`, `connect`, `active`, `opensent`, `openconfirm`, `established`, `other` |
| `shutdown` | string | yes | `yes`, `no`: shut by configuration |
| `dynamic` | string | yes | `yes`, `no`: accepted from a listen range |
| `local_as` | int | yes | in effect for this peer: the VRF instance's AS, or a per-peer override |
| `router_id` | string | yes | the local router ID in that VRF |
| `configured_address_families` | strings, `EmptyList`, `Set` | yes | `[]` when no covered family is activated |
| `negotiated_address_families` | strings, `EmptyList`, `Set`, `OnlyWith: state=established` | no | absent when not established, `[]` when established with none |
| `peer_router_id` | string, `OnlyWith: state=established` | no | learned from the peer |
| `peer_group` | string | no | |
| `description` | string | no | |

Identity within an observation: `vrf`, `peer_address`.

Address family names: `ipv4 unicast`, `ipv6 unicast`, `l2vpn evpn`, or the name as the device
prints it for any other family. The configured list holds only the families the recipe `covers`
(on `arista_eos`: those three, research R2); the negotiated list holds any family. Both are sorted,
with no duplicate.

Rules:

- `negotiated_address_families` has three forms: absent (not established, unknown), `[]`
  (established, none negotiated), non-empty (FR-005a).
- A family in `configured_address_families` and not in `negotiated_address_families` of an
  established row is a capability mismatch. Nothing stores it; a reader compares the two lists.
- A shut peer reads `state: idle`, `shutdown: yes`. A peer down on its own reads `shutdown: no`.
- A dynamic peer is a row while it is connected (or listed by the device) and has no row once it is
  gone; `dynamic: yes` says that was a state change.
- No uptime, counters, timers, last error or local TCP address (FR-007, research R6).
- On a row not established, a negotiated list or peer router ID found in the output (from the last
  session) is dropped by the parser; the row is kept. Any row that breaks a rule after that makes
  the whole observation `parse_failed`, with a finding (research R12).

## Relationships

- Nothing in this feature joins a peer to another device. The same session seen from both ends is
  two rows on two devices. Matching `peer_address` or `peer_router_id` to an entity is open in #30.
- `peer_group` and `description` are names local to the device.

## Observation statuses

| Situation | `bgp_peers` |
| --------- | ----------- |
| EOS 4.36 or later, BGP with at least one peer | `collected` |
| BGP not running (exactly `% BGP inactive`), or running with no peer | `empty` |
| a refused or invalid command, any other error line | `parse_failed`, with a finding |
| output the template does not understand, a summary peer with no neighbour record, a row breaking a rule | `parse_failed`, with a finding |
| EOS before 4.36 | `unsupported` (`no_matching_version`) |
| another platform | `unsupported` (`no_recipe`) |

The family does not change the snapshot verdict, which counts `identity` only, and is read by no
projection.
