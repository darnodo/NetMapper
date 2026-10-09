# Research: VLANs and interface VLAN membership on Arista EOS

Sources: the EVPN lab configurations (`arista-evpn-vxlan-clab/configs/*.cfg`, cEOS 4.36.0F), the
`show interfaces status` recordings already in `packs/arista_eos/testdata/lab/` (leaf2, spine2, sw1),
and the parser and pack code. The `NetLab` VM was unreachable on 2026-10-09 (ssh timeout), so no
VLAN command was run for this plan. Every item below that depends on what EOS prints is marked
**to confirm when recording**, with what to do if the device prints something else. Recording is
implementation step 1 and happens before any template is written.

## R1. `vlans` comes from `show vlan`

- **Decision**: one step, `show vlan | no-more`, one row per VLAN line. The ports column and its
  continuation lines are skipped (membership comes from R2). The footnote line about dynamic VLANs
  is skipped. A VLAN ID followed by `*` (dynamic) is recorded like any other.
- **Rationale**: it is the only command that lists every declared VLAN with name and status in one
  table. EOS does not list the internal VLANs of routed ports in it; they are shown only by
  `show vlan internal usage`. So FR-003 holds by choice of command, with no filter in the pack.
- **To confirm when recording**: that internal VLANs are absent from `show vlan` on sw2 once it has
  a routed port (R7). `show vlan internal usage` is recorded beside it as test evidence, and a test
  asserts that no ID it lists is a `vlans` row. If EOS does list them, the template skips them by the
  marker it prints, and the test stays the same.
- **Alternatives considered**: `show vlan brief` (same content on EOS, no gain);
  `show running-config section vlan` (misses dynamic VLANs and the device's own status).

## R2. `interface_vlans` comes from three steps, merged on the interface

| Step | Command | Gives |
| ---- | ------- | ----- |
| 1 | `show interfaces switchport \| no-more` | per switched port: mode, access VLAN, native VLAN, allowed VLANs |
| 2 | `show interfaces trunk \| no-more` | per trunk: active VLANs ("allowed and active in management domain") |
| 3 | `show interfaces status \| no-more` | per port-channel member: its channel (`in Po1` in the Vlan column) |

`merge_on: [interface]`, so one row per port whatever step produced it.

- **Rationale**: no single command gives everything. The switchport view has mode, native and
  allowed (the spec's chosen source). Active VLANs, recorded as the device prints them (spec
  clarification), are only in the trunk view. The `show interfaces status` recordings already in the
  repository show `in Po1` and `in Po999` for members on leaf2 and `routed` for routed ports, which
  answers membership without a new command family.
- **Step 3 template**: a new template, `show_interfaces_status_channel.textfsm`, records only the
  lines whose Vlan column is `in <channel>`, so routed, access and trunk ports get no row from it
  (FR-009). The `interfaces` template is not touched. A device with no member port yields no row
  from non-empty output, which the parser reads as `parse_failed` (`template yielded no row`). The
  step's `empty_lines` lists the header and every line whose Vlan column is not `in ...`, so such a
  device skips the step instead. The `empty_lines` rule needs every line to match, so drift still
  fails.
- **Step 2 sections**: the trunk view prints four tables (mode and native, allowed, allowed and
  active, forwarding). The template uses a state per table and records from the third only; the
  fourth is STP state, out of scope. The first two repeat what step 1 gives.
- **To confirm when recording**: (a) whether `show interfaces switchport` prints a block for a
  member port. If it does, its template skips the block (by the line that says it is a member), so
  a member row carries only `interface` and `channel` (FR-008). (b) What `show interfaces trunk`
  prints when there is no trunk: headers only (then `empty_lines` lists them) or nothing.
- **Alternatives considered**: `show port-channel` or `show port-channel dense` for membership
  (another output format to learn, and the dense form puts several members on one line);
  `show vlan` ports column (no native, no allowed list, abbreviations wrapped across lines);
  computing active VLANs from allowed and `vlans` (rejected in the spec clarification: it misses
  trunk groups, as on the EVPN lab's MLAG peer-links).

## R3. Mode is the administrative mode, fields follow the mode

- **Decision**: `mode` comes from `Administrative Mode`. Values: `static access` maps to `access`,
  `trunk` to `trunk`, anything else is kept as printed (`dot1q-tunnel`, `tap`, `tool`). The step 1
  template switches state on the mode line: an access block captures the access VLAN only, a trunk
  block the native VLAN and the allowed list only. A port therefore never carries fields of the
  other mode, although EOS prints both on every block.
- **Rationale**: VLAN membership is configuration (spec edge case on shut ports). The administrative
  mode does not change when the link goes down.
- **To confirm when recording**: the exact mode strings and that the mode line comes before the VLAN
  lines in each block, which the state switch relies on.

## R4. VLAN lists: one neutral form, normalised by the parser

- **Decision**: a new field attribute in `internal/fact`, `VLANList`, on `allowed_vlans` and
  `active_vlans` (type `strings`). The parser normalises such a field after `values` and `split`:
  join the items with `,`, parse every token as an ID or `lo-hi`, sort, merge adjacent and
  overlapping ranges, print `N` or `lo-hi`. `fact.Validate` rejects a list that is not already in
  that form or holds an ID outside 1 to 4094. The normaliser sits beside `NormaliseMAC` in
  `internal/pack`. The recipe's `values` maps `ALL` to `1-4094` and `NONE` to `''`.
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

- **Decision**: the allowed and active VLAN values are TextFSM `List` values that take a
  continuation line as another item. R4's normaliser joins items with `,` and skips empty tokens, so
  a list cut after a comma comes back whole.
- **To confirm when recording**: whether EOS wraps a long list in `show interfaces switchport` and
  `show interfaces trunk`, and where it cuts. The `test/lab` trunk gets an allowed list long enough to
  wrap (R7). If EOS cuts inside a range (`11-` / `4094`), the join changes to "no separator when the
  previous item ends with `-`", tested on that recording.

## R6. Status mapping

- **Decision**: `values` for `status`: `active` to `active`, `suspended` to `suspended`, `act/lshut`
  to `shutdown`, `*` to `other`.
- **To confirm when recording**: the exact strings, and the configuration that puts a VLAN in
  `act/lshut` on cEOS. If cEOS cannot produce it, the mapping stays, a parse test covers it on a copy
  of the sw2 recording with that one line edited and named as such, and the divergence is written in
  the quickstart.

## R7. `test/lab` layout

Three new links between sw1 and sw2, one change on sw2's existing link to sw3. VLANs 10, 20, 30,
31, 32, 40 to 49 and 99 on both switches.

| Ports | Configuration | Proves |
| ----- | ------------- | ------ |
| sw1:eth1 - sw2:eth1 (existing) | trunk, native VLAN 99, allowed `10,20,30-32,40-49,99` | pruned list with ranges, native other than 1, VLAN 31 allowed but suspended (not active) |
| sw1:eth2, eth3 - sw2:eth3, eth4 (new) | `Port-Channel10`, LACP, trunk allowing every VLAN | port-channel row, two member rows, `["1-4094"]` |
| sw1:eth4 - sw2:eth5 (new) | access VLAN 10 on both ends | access row |
| sw2:eth2 (existing, to sw3) | `no switchport` | routed port: no row, internal VLAN not in `vlans` |

VLAN 31 is suspended (`state suspend`), VLAN 32 shut down locally (R6). VLAN 50 is allowed on no
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

- **Decision**: `interface_vlans` `empty` is recorded from sw4 (no Ethernet port), and from an EVPN
  lab spine when that lab is up (spine2's `show interfaces status` already shows only routed ports).
  `vlans` is never `empty` on EOS: VLAN 1 always exists. The contract says so, as it does for
  `aaa_methods`.
- **To confirm when recording**: what each of the three commands prints on sw4, so the `_empty`
  recordings and `empty_lines` match it.

## R9. Versions, read interface, docs

- **Versions**: `versions: '>=4.36'`. Only 4.36 is recorded; an older device gets `unsupported`
  (`no_matching_version`) rather than a guess. Widening is a recipe change once a recording exists.
- **Read interface**: none. `GET /v1/devices/{name}/facts/{family}` checks the family against
  `fact.Families`, so both families are served as soon as they are in the schema (FR-017).
- **Load warning**: `missingCost` gains `vlans` and `interface_vlans`: "no VLAN data for these
  devices; they will join no L2 domain".
- **Docs** (FR-018): README fact family list, `docs/how-to/write-a-pack.md` (families, the
  `VLANList` normalisation), two Bruno requests in `bruno/facts/`, the fact families contract of 001.
