# Research: VLANs and interface VLAN membership on Arista EOS

Sources: the EVPN lab configurations (`arista-evpn-vxlan-clab/configs/*.cfg`, cEOS 4.36.0F), the
`show interfaces status` recordings already in `packs/arista_eos/testdata/lab/` (leaf2, spine2, sw1),
and the parser and pack code. The `NetLab` VM was unreachable when the plan was first written. It came
back the same day and `show vlan`, `show vlan internal usage`, `show interfaces switchport`,
`show interfaces status` and `show interfaces trunk` were run read only on the EVPN lab (dc-leaf1,
dc-spine1, campus-access1, and `show vlan` on all 28 switches). R1, R2, R3 and R8 were corrected
from that output. What only `test/lab` could show (suspended and locally shut VLANs, a trunk that is
down, `none`, a list long enough to wrap) was settled from the `test/lab` recordings on the same day
(tasks T008); each item says what was recorded.

## R1. `vlans` comes from `show vlan`

- **Decision**: one step, `show vlan | no-more`, one row per VLAN line. The ports column and its
  continuation lines are skipped (membership comes from R2). The footnote line about dynamic VLANs
  is skipped. A VLAN ID followed by `*` (dynamic) is recorded like any other.
- **Rationale**: it is the only command that lists every declared VLAN with name and status in one
  table. EOS does not list the internal VLANs of routed ports in it; they are shown only by
  `show vlan internal usage`. So FR-003 holds by choice of command, with no filter in the pack.
- **Confirmed on the EVPN lab**: dc-leaf1's `show vlan internal usage` lists 1006 and 1007 (its
  routed Ethernet11 and Ethernet12), and `show vlan` does not list them. `show vlan internal usage`
  is recorded beside it as test evidence, and a test asserts that no ID it lists is a `vlans` row.
- **Seen**: header `VLAN  Name ... Status    Ports`, a dashed line, one line per VLAN, a blank
  line at the end. The Ports column may be empty (`1     default   active    ` on spines). No
  dynamic VLAN marker on any of the 28 switches; the template still accepts an optional `*` after
  the ID. Only `active` was seen; R6 covers the other statuses.
- **Alternatives considered**: `show vlan brief` (same content on EOS, no gain);
  `show running-config section vlan` (misses dynamic VLANs and the device's own status).

## R2. `interface_vlans` comes from two steps, merged on the interface

| Step | Command | Gives |
| ---- | ------- | ----- |
| 1 | `show interfaces status \| no-more` | every port: its Vlan column says access VLAN (a number), `trunk`, `routed`, or `in <channel>` for a member |
| 2 | `show interfaces trunk \| no-more` | per trunk: native VLAN, allowed VLANs, active VLANs ("allowed and active in management domain") |

`merge_on: [interface]`, so one row per port whatever step produced it.

- **Why not `show interfaces switchport`** (the plan's first choice): on dc-leaf1 it prints a block
  for the member ports Et1 and Et10 as ordinary `static access` ports in VLAN 1, with nothing saying
  they are members. Merged with membership from another step, a member row would carry an access
  VLAN, which FR-008 forbids, and no recipe key can remove a field. It also prints `Vx1` as a trunk,
  which is VXLAN (#33) and not a port. `show interfaces status` has neither problem: members read
  `in Po1`, `in Po999`, `in Po10`; `Vx1` is not listed.
- **Step 1 template**, `show_interfaces_status_vlan.textfsm` (new; the `interfaces` template is not
  touched): one Value `VLAN` for the Vlan column when it is not `in ...`, one Value `CHANNEL` for the
  channel after `in`. Lines whose Vlan column is `routed` are matched first and not recorded
  (FR-009). Members and switched ports then share one rule (`in ${CHANNEL}` or `${VLAN}`) with a
  greedy Name, as in `show_interfaces_status.textfsm`, so a description holding a status word
  ("connected in rack3") is read as the Name. Changed in code review: with separate rules and a lazy
  Name, that description made an access port a member of a channel named `rack3`. The
  step's `empty_lines` lists the header and the `routed` lines, so a device with only routed ports
  (dc-spine1, sw4) skips the step instead of giving `parse_failed`; the rule needs every line to
  match, so any other line still reaches the template.
- **Mode and access VLAN from one column.** `mode: VLAN` with values `{ trunk: trunk, '*': access }`
  and `access_vlan: VLAN` with values `{ trunk: '' }`. Only words a recording shows go in the
  tables: `trunk` is the only one. A dot1q-tunnel port prints a number (R3), and `tap` and `tool`
  were not recorded. Any other word is not an integer and the row fails as `parse_failed`, so a new
  Vlan column value is never silently read as an access port.
- **Step 2 template**, `show_interfaces_trunk.textfsm`: one state per table. Table 1 records `PORT`,
  `NATIVE`; table 2 `PORT`, `ALLOWED`; table 3 `PORT`, `ACTIVE`; table 4 (STP forwarding) is
  skipped. `merge_on` joins the three records of a port. `values`: `All` maps to `1-4094`, `None` to `''`
  (the only spelling recorded). On a device with no trunk it prints `There are no active trunk ports`, listed in
  the step's `empty_lines`.
- **Recorded on sw1 (2026-10-09)**: with `Port-Channel10` shut down, the trunk view lists only
  Ethernet1 (`sw1_show_interfaces_trunk_po10_down.raw`); the status table still prints Po10 as
  `disabled trunk` and its members as `errdisabled in Po10`. So a trunk that is not active has a row
  with `mode: trunk` and no VLAN field. The contract, the data model and spec FR-005 say so, and
  `interfaces.oper_state` tells a reader why. `switchport trunk allowed vlan none` prints `None` in
  both the allowed and the active table (`sw1_show_interfaces_trunk_none.raw`).
- **Alternatives considered**: `show interfaces switchport` (above); `show port-channel` for
  membership (another format, and not needed once the status table gives it); `show vlan` ports
  column (no native, no allowed list); computing active VLANs (rejected in the spec clarification).

## R3. Mode comes from the status table

- **Decision**: `mode` is `access` when the Vlan column of `show interfaces status` is a number,
  `trunk` when it says `trunk`, the word as printed for another mode a recording shows (R2 values).
  A member row has no mode. An access row has `access_vlan` only, a trunk row the step 2 fields only.
- **Rationale**: the status table is the one view that tells members apart (R2). Its Vlan column is
  what the switch applies; for VLAN membership that is what #37 needs.
- **Recorded on sw1**: an access port shut down still prints its VLAN (`disabled  10`,
  `sw1_show_interfaces_status_vlan_access_down.raw`).
- **dot1q-tunnel reads as access.** With `switchport mode dot1q-tunnel` on Ethernet4, the switchport
  view says `Administrative Mode: tunnel` but the status table prints `10`, exactly like an access
  port (`sw1_show_interfaces_status_vlan_tunnel.raw`). The pack records it as `access` in its outer
  VLAN. For VLAN membership that is what the port is: a member of VLAN 10. Telling the mode apart
  would need the switchport view, rejected in R2, for a mode nobody in these labs uses. `tap` and
  `tool` were not recorded, so the `mode` table holds `trunk` only and every number reads as access;
  any other word fails as `parse_failed` on `access_vlan`.

## R4. VLAN lists: one neutral form, normalised by the parser

- **Decision**: a new field attribute in `internal/fact`, `VLANList`, on `allowed_vlans` and
  `active_vlans` (type `strings`). The parser normalises such a field after `values` and `split`:
  join the items with `,`, parse every token as an ID or `lo-hi`, sort, merge adjacent and
  overlapping ranges, print `N` or `lo-hi`. `fact.Validate` rejects a list that is not already in
  that form or holds an ID outside 1 to 4094. The normaliser sits beside `NormaliseMAC` in
  `internal/pack`. The recipe's `values` maps `All` (as the trunk view prints it) to `1-4094` and `None` to `''`.
- **"No VLAN" is an absent field.** The parser drops a list that ends up with no item (the existing
  rule: `''` is the device's way of saying none, as for `vrfs`). On a trunk row, absent
  `allowed_vlans` therefore means no VLAN is allowed. The device always prints the allowed list of a
  trunk, so absence on a trunk row has no other meaning. The contract says so. This amends the
  spec's wording of FR-007 ("an empty set") in this change.
- **Rationale**: the range list was chosen in the spec clarification. Normalising in the parser, by
  a schema attribute with no vendor term, means every pack gets the same form and a pack cannot get
  it wrong (constitution V). It is a replay, not a re-crawl, if the rule changes (II).
- **Alternatives considered**: normalising in each pack's templates (every pack re-implements it);
  storing `[]` for none (changes the parser's empty-list rule for every family, `vrfs` included).

## R5. Long VLAN lists may wrap

- **Decision**: the `ALLOWED` and `ACTIVE` values are TextFSM `List` values that take a
  continuation line as another item. R4's normaliser joins items with `,` and skips empty tokens, so
  a list cut after a comma comes back whole.
- **Seen**: the trunk view prints the list after the port name in a fixed column
  (`Po999           1,40,4090-4091`). No list on the EVPN lab is long enough to wrap.
- **Recorded on sw1**: every even VLAN from 2 to 200 (`sw1_show_interfaces_trunk_wrapped.raw`). EOS
  wraps the list over eight lines, each continuation line indented to the list column, and cuts
  between items with no trailing comma (`...,34,36` / `38,40,...`). Joining the items with `,` gives
  the list back.

## R6. Status mapping

- **Decision**: `values` for `status`: `active` to `active`, `suspended` to `suspended`, `*` to
  `other`. Nothing maps to `shutdown` on EOS.
- **Recorded**: EOS has no local VLAN shutdown. `shutdown` under a VLAN is refused with "The
  'shutdown' command is not supported. Please use 'state suspend' instead." (and silently dropped
  from a startup-config). `act/lshut` is a Cisco IOS status, assumed during clarification by mistake.
  `shutdown` stays in the platform-neutral enum for platforms that have it; no fixture is edited to
  fake it.

## R7. `test/lab` layout

Three new links between sw1 and sw2, one change on sw2's existing link to sw3. VLANs 10, 20, 30,
31, 32, 40 to 49 and 99 on both switches.

| Ports | Configuration | Proves |
| ----- | ------------- | ------ |
| sw1:eth1 - sw2:eth1 (existing) | trunk, native VLAN 99, allowed `10,20,30-32,40-49,99` | pruned list with ranges, native other than 1, VLAN 31 allowed but suspended (not active) |
| sw1:eth2, eth3 - sw2:eth3, eth4 (new) | `Port-Channel10`, LACP, trunk allowing every VLAN | port-channel row, two member rows, `["1-4094"]` |
| sw1:eth4 - sw2:eth5 (new) | access VLAN 10 on both ends | access row |
| sw2:eth2 (existing, to sw3) | `no switchport` | routed port: no row, internal VLAN not in `vlans` |

VLAN 31 is suspended (`state suspend`); VLAN 32 is a plain VLAN, since EOS cannot shut one down locally (R6). VLAN 50 is allowed on no
trunk and declared on sw2 only, so a VLAN can be declared and carried nowhere.

- **Rationale**: every row type of FR-014 on one crawl of two switches. sw1 already carries the
  `empty` cases of feature 007; VLAN configuration does not touch those families. sw3 is denied and
  is not collected, so making its far end routed changes nothing it proves. sw4 is deployed alone,
  with no link: its only interface is `Management0`, so it gives the `empty` `interface_vlans` case
  (R8).
- **Effect on earlier features**: three more LLDP links on a live crawl. The recorded fixtures of
  features 001 to 007 are files and do not change. The counts printed in
  `specs/004-graph-projector/quickstart.md` ("6 interfaces, 10 edges") record a run of 2026-09-25
  and stay as they are; the 008 quickstart gives the new counts.

## R8. The `empty` case and the families that are never empty

- **Decision**: `interface_vlans` `empty` is recorded from sw4 (no Ethernet port) and from
  dc-spine1, whose status table is only `routed` lines and whose trunk view is
  `There are no active trunk ports` (both seen). `vlans` is never `empty` on EOS: VLAN 1 is listed on
  every one of the 28 switches, spines included. The contract says so, as it does for `aaa_methods`.

## R9. Versions, read interface, docs

- **Versions**: `versions: '>=4.36'`. Only 4.36 is recorded; an older device gets `unsupported`
  (`no_matching_version`) rather than a guess. Widening is a recipe change once a recording exists.
- **Read interface**: none. `GET /v1/devices/{name}/facts/{family}` checks the family against
  `fact.Families`, so both families are served as soon as they are in the schema (FR-017).
- **Load warning**: `missingCost` gains `vlans` and `interface_vlans`: "no VLAN data for these
  devices; they will join no L2 domain".
- **Docs** (FR-018): README fact family list, `docs/how-to/write-a-pack.md` (families, the
  `VLANList` normalisation), two Bruno requests in `bruno/facts/`, the fact families contract of 001.
