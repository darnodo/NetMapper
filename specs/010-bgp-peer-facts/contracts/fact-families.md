# Fact families added by feature 010

To merge into `specs/001-crawl-loop/contracts/fact-families.md`, as a section after the layer 2
families, in the same change as the code (FR-011).

## Routing families (feature 010)

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
