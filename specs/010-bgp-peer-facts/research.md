# Research: BGP sessions on Arista EOS

Sources: read-only `show` commands run on the EVPN lab on 2026-10-14 (cEOS 4.36.0F, NetLab VM):
dc-leaf1, dc-border-leaf1, campus-border-leaf1, core1 and dc-access1. The parser, pack and test
code. What only `test/lab` could show (a session that is down, a shut peer, a dynamic peer, a
password, a description, a capability mismatch) was settled from its recordings on the same day
(tasks T004 to T008, `test/lab` on the NetLab VM, cEOS 4.36.0F); each item says what was recorded.

## R1. The peer rows come from `show bgp neighbors vrf all`

- **Decision**: the first step, `show bgp neighbors vrf all | no-more`, gives one record per peer:
  - `BGP neighbor is <addr>, remote AS <n>, (external|internal) link`: peer address, peer AS, type;
  - `BGP version 4, remote router ID <id>, VRF <vrf>`: peer router ID, VRF;
  - `Inherits configuration from and member of peer-group <name>`: peer group;
  - `BGP state is <State>, ...`: state;
  - `Multiprotocol <family>: ... negotiated` under `Neighbor Capabilities`: negotiated families
    (R4);
  - `Local AS is <n>, local router ID <id>`: local AS and router ID. This line comes after the
    peer's capability and statistics blocks, so the template records a peer when the next
    `BGP neighbor is` line starts, or at the end of the output.
- **Rationale**: it is the only text command listing every peer of every VRF, whatever its address
  families and whatever its state. The summaries list a peer only under the families it is
  activated for: dc-leaf1's EVPN peers are missing from `show ip bgp summary vrf all`.
- **Seen**: dc-leaf1 (5 peers, VRF `default`, eBGP underlay, iBGP to its MLAG peer, eBGP EVPN on
  loopbacks), core1 (5 peers in `default`, 5 in `gold`). Everything else in the output (timers,
  message and prefix statistics, TCP socket state, dropped-update counters) is skipped by the
  template (FR-007).
- **Local AS line**: it is per peer, so a `local-as` override on one peer is what that peer's row
  carries. Without an override it is the instance's AS and router ID for the VRF, as FR-003 says.
  Recorded: the line is printed for every peer, including sw1's shut peer and its wrong-AS peer
  that never connected.
- **Description** (R7): ` Description: TO-SW2`, one space of indent, on the line after
  `BGP neighbor is`.
- **Alternatives considered**: `show ip bgp summary vrf all` as the main step (misses peers not
  activated for IPv4, has message counts and uptime); `show bgp peer-group` (lists only peers in a
  group); `| json` (the pack format has no JSON parser, and adding one is out of proportion here).

## R2. The configured families come from the per-family summaries

- **Decision**: three more steps, one per family, each giving `(vrf, peer_address)` rows and a
  literal family name:
  - `show ip bgp summary vrf all` gives `ipv4 unicast`;
  - `show ipv6 bgp summary vrf all` gives `ipv6 unicast`;
  - `show bgp evpn summary` gives `l2vpn evpn`.
  The rows merge with R1's on `vrf` and `peer_address`, and the family names add up into
  `configured_address_families` (R5).
- **Rationale**: EOS does not print per peer which families are configured. `show bgp neighbors`
  shows capabilities only once an OPEN has been exchanged, so a peer that is down shows none. A
  summary lists a peer exactly when it is activated for that family, in whatever state: dc-leaf1's
  `show ip bgp summary` lists the three underlay peers and not the two EVPN peers, `show bgp evpn
  summary` the reverse, which matches `address-family evpn / neighbor evpn activate` and
  `address-family ipv4 / neighbor underlay activate` in its configuration with
  `no bgp default ipv4-unicast`. Recorded on sw1: the shut peer (`Idle(Admin)`) and the wrong-AS
  peer (`Connect`) are listed in `show ip bgp summary vrf all`, and the password session, whose
  ipv6 unicast sw2 does not activate, is listed in `show ipv6 bgp summary vrf all` as
  `Estab(NotNegotiated)`.
- **Default activation** (recorded on sw2, convergence T038): with the explicit
  `neighbor 10.0.10.1 activate` removed from `address-family ipv4`, the peer is still listed in
  `show ip bgp summary vrf all` and negotiates IPv4 unicast, so a peer relying on EOS's default
  ipv4 activation gets `ipv4 unicast` in its configured list
  (`sw2_show_ip_bgp_summary_vrf_all_defaultactive.raw`).
- **Template**: the three outputs share one layout: a `BGP summary information for VRF <vrf>` line
  per VRF (filled down), a router identifier line, a status codes line, a column header, one line
  per peer. Recorded on `test/lab`: when any peer of the device has a description, every summary
  gains a leading `Description` column, blank for peers without one, so the neighbour is not always
  the first column. The template finds it as the address followed by the BGP version (`4`) and the
  AS. Only the `Neighbor` column is read. The message counts, `Up/Down`, prefix counts and
  even `State` are not mapped (state comes from R1).
- **Three template files with the same content**: `TestFamiliesFromLab` finds a step's recording
  by its template name, so three steps on one template would read the same file. Three short files
  keep the test harness unchanged; the duplication is ten lines of pack data.
- **Empty**: a family with no peer prints only the header lines (core1's `show ipv6 bgp summary`
  and `show bgp evpn summary`). Each step's `empty_lines` lists those header lines, so such a
  step adds nothing instead of failing on "template yielded no row".
- **Every VRF** (plan review): the two unicast summaries take `vrf all`. On core1 both print one
  `BGP summary information for VRF <vrf>` block per VRF, `default` then `gold`, and the five `gold`
  peers are listed in `show ip bgp summary vrf all` (seen 2026-10-14). The VRF is filled down from
  that line, so each summary row merges with the neighbour row of the same VRF. EVPN is an address
  family of the default VRF only on EOS: `show bgp evpn summary vrf all` is refused (`% Invalid
  input`), and tenant VRFs carry EVPN routes over default-VRF sessions. The EVPN step stays
  `show bgp evpn summary`, and its header still names VRF `default`. core1's `gold` rows are in the
  fixtures with `configured_address_families: ["ipv4 unicast"]`.
- **Declared coverage** (plan review, FR-005c): only these three families are read, so the recipe
  declares them, in a new implementation key:

  ```yaml
  covers:
    configured_address_families: [ipv4 unicast, ipv6 unicast, l2vpn evpn]
  ```

  At load, the registry refuses the pack when the literal values the steps map into that field
  (`'=ipv4 unicast'` and so on) are not exactly that set: the declaration cannot drift from the
  steps. A reader finds the set through the observation's recipe ID, and the contract lists it for
  `arista_eos`. A peer activated only for another family (VPN-IPv4, labeled unicast, flowspec) has
  an empty configured list, which reads "none of the covered families", and the family can appear in
  its negotiated list. Another family is a new step and a new `covers` item later.
- **Alternatives considered for coverage**: a per-row field naming the covered set (the same three
  names on every row of every device); leaving it to a comment in the recipe (a reader cannot
  check it, and it drifts).
- **Alternatives considered**: `show running-config sanitized section router bgp` (activation is
  set on peer groups and inherited, `no bgp default ipv4-unicast` changes the default, VRF blocks
  repeat it; resolving that is configuration logic the parser does not have and should not grow);
  capability lines with `advertised` (missing while the session is down, which is when the
  configured list matters most).

## R3. State, shutdown and dynamic peers

- **State**: the word after `BGP state is` maps through the recipe's `values`: `Idle`, `Connect`,
  `Active`, `OpenSent`, `OpenConfirm`, `Established` to their lowercase form, `'*': other`. Only
  `Established` is in the EVPN lab.
- **Shutdown**: recorded on sw1: `BGP state is Idle, Administratively shut down`. The template
  captures the reason after the comma, and the recipe maps `Administratively shut down` to
  `shutdown: yes`, with default `no`.
- **Dynamic**: recorded on sw2: `show bgp neighbors` prints nothing that tells the dynamic peer
  10.255.1.1 from the static one (compared line by line; only timers, the peer group line, TTL and
  addresses differ). So the fallback is used: a fifth step, `show bgp peer-group`, lists members
  under `Static peer-group members:` or `Dynamic peer-group members:`, each followed by
  `VRF <vrf>:` and `<addr>, state: <State>` lines. The template records both kinds, with the
  heading as `DYNAMIC`, and the recipe maps `Dynamic` to `yes`, `Static` to `no`. Recording static
  members too matters: dc-leaf1's groups have only static members, and a step whose output yields
  no row is `parse_failed`. A peer in no group is in no row of this step and gets the default `no`.
- **Peer groups with nothing to list**: recorded on sw1, `No such peer group exists` (no group at
  all); on sw2 with its dynamic peer gone, the group's header lines, then `Empty peer-groups:` and
  the group name. Both are `empty_lines` of that step (R8).
- **Both are `yes`/`no` strings**, like `management_apis.enabled`: the schema has no boolean type,
  and adding one for two fields is not worth it.

## R4. Negotiated families and the peer router ID: present only when established

- **Decision**: `negotiated_address_families` holds the families whose capability line ends with
  `negotiated` (`Multiprotocol L2VPN EVPN: advertised and received and negotiated`). It is absent
  when the state is not `established`, and `[]` when the session is established and no line
  ends with `negotiated`. `peer_router_id` follows the same rule, without the empty case.
- **Rationale**: spec FR-005a. The template cannot say "empty" on its own: no capability line
  means no list. And EOS keeps printing the remote router ID of the last session after it goes down:
  recorded on sw1 with the password session shut (`sw1_show_bgp_neighbors_vrf_all_down.raw`),
  `remote router ID 10.255.0.2` stays, while the capability lines become `advertised and received`,
  without `negotiated`. So the rule is a schema rule on the
  row, enforced after the merge, not template logic (R5).
- **`0.0.0.0`**: a peer that never connected is expected to print `remote router ID 0.0.0.0`; the
  rule above drops it anyway, since the state is not `established`.

## R5. Two schema attributes and one merge change

The parser today drops a list with no item, and a merge keeps the first value of a field. Both
need a change, vendor-neutral, in `internal/fact` and `internal/parse`:

- **`Field.EmptyList`**: a strings field that is stored as `[]` when it has no item, instead of
  being left out. With `Required`, the field is on every row (`configured_address_families`).
  This is the explicit exception to feature 008's "an empty set is an absent field" (spec
  clarifications), and it is opt-in so no existing family changes.
- **`Field.OnlyWith`**: a field and a value, such as `state` / `established`. The field is present
  exactly when that field holds that value. After the merge, the parser drops it from the other
  rows, and with `EmptyList` sets `[]` on matching rows that have no item. `fact.Validate` rejects a
  row that breaks the rule, so a pack that bypasses the parser cannot store it either.
- **Merge of lists**: when two merged rows both carry a strings field, the items of the later one
  are appended, skipping items already there, instead of being dropped. No recipe merges a list
  today (`interfaces`, `interface_vlans` and `aaa_servers` merge only scalars), so nothing
  changes for them.
- **`Field.Set`** (plan review, FR-005d): a strings field whose order means nothing. After the
  merge, the parser sorts it and removes duplicates; `fact.Validate` rejects a list that is not
  sorted or holds a duplicate. Both address family lists are `Set`, so the order of the steps or of
  the device's capability lines never changes a row. It is opt-in, and not applied to lists whose
  order matters, such as `aaa_methods.methods` (the order AAA methods are tried in).
- **Defaults after the merge** (found while implementing): defaults used to fill each row before
  the merge, so step 1's row of sw2's dynamic peer carried the default `dynamic: no`, and the
  peer-group step's `dynamic: yes` lost to it (a scalar keeps its first value). Now a default on a
  `merge_on` field fills each row before the merge, so rows still join on it (`aaa_servers` relies
  on `vrf: default` for that), and every other default fills the merged row. Every existing
  family's fixtures give the same rows.
- **Alternatives considered**: a template-level "always emit" flag (the template does not know the
  state rule); a stored `capability_mismatch` field (declined in the spec); treating `[]` and
  absent as the same everywhere (breaks feature 008's contract).

## R6. Update source is dropped from the family (spec amendment)

- **Finding**: `show bgp neighbors` does not print the configured `update-source`. dc-leaf1's
  `evpn` peer group has `neighbor evpn update-source Loopback0`, and the output for 10.0.250.1 has
  no line naming it. What it prints is `Local TCP address is 10.0.250.11, local port is 39427`,
  which is the address of the current TCP connection: absent when the session is down, and with a
  port that changes on every reconnect.
- **Decision**: no `update_source` field. FR-006 and story 1 are amended in this change.
- **Rationale**: the configured value is only in the running-config, where it is usually set on a
  peer group and inherited, the same resolution problem as R2's alternative. The local TCP address
  is connection state, not configuration.
- **Later**: if needed, a separate change that reads peer group settings and resolves inheritance,
  for update source and other per-peer settings together.

## R7. Description

- **Decision**: ` Description: <text>` in the peer record (R1), recorded on sw1 and sw2 for the
  password session. No peer in the EVPN lab has one.

## R8. A device with no BGP is `empty`

- **Seen**: dc-access1 answers `% BGP inactive` to all five commands (`docker exec ... Cli -c`
  adds ` at line 1`).
- **Recorded**: through an interactive SSH session, which is how the collector runs commands, sw4
  and dc-access1 print exactly `% BGP inactive`. ` at line 1` is added only by `Cli -c` and by
  SSH exec mode, which the collector does not use. The `_empty` fixtures hold the interactive form.
- **Decision** (plan review, FR-009): each step's `empty_lines` accepts that answer and nothing
  else: `^% BGP inactive$`. The
  parser already requires every non-blank line of an output to match an empty line before calling
  it nothing, so an output with that line and anything else reaches the template. When every step
  is nothing, `parse.Parse` returns `empty` with no row, never `collected`; that is the existing
  rule, tested here on these outputs.
- **Errors are not empty**: `% Authorization denied for command ...`, `% Invalid input` and any
  other `%` line match no empty line, reach the template, yield no row, and the observation is
  `parse_failed` ("template yielded no row") with a `parse_failed` finding. Test cases feed these
  outputs to each step.
- **BGP with no neighbour** (recorded on sw4, convergence T038): with `router bgp 65104` and no
  `ip routing`, EOS still answers `% BGP inactive` to all five commands. With `ip routing`, the
  neighbour view is blank, the summaries print their header lines only and the peer-group view
  prints `No such peer group exists`: every step is nothing, so the family is `empty`
  (`sw4nopeer_*` fixtures).
- **Recorded refusals**, through an interactive SSH session: on sw2, a temporary role denying
  `show bgp.*` gives `% Authorization denied for command 'show bgp neighbors vrf all'`
  (`sw2_show_bgp_neighbors_vrf_all_denied_empty.raw`; the role and user were removed after); on
  sw1, `show bgp evpn summary vrf all` gives `% Invalid input`
  (`sw1_show_bgp_evpn_summary_invalid_empty.raw`).

## R9. Passwords

- **Seen**: no session of the EVPN lab has a password, and no output of R1 or R2 prints a
  configuration line. Recorded on `test/lab`: for the password session, `show bgp neighbors` prints
  only `MD5 authentication is enabled`. No recording holds `nm-lab-secret-` or `password`.
- **Secret check**: the `test/lab` password starts with `nm-lab-secret-`, which
  `TestNoSecretInRecordedOutput` already looks for. A `password 7 <hash>` pattern is added for the
  encrypted form EOS writes in its configuration, in case a recorded output ever holds it.
- **Not recorded**: whether a password is set (spec decision).

## R10. The `test/lab` sessions

- **Decision**: sw1 (AS 65101) and sw2 (AS 65102), `ip routing` on both, an SVI on VLAN 10
  (10.0.10.1 and .2), loopbacks 10.255.0.1, 10.255.1.1 (sw1) and 10.255.0.2, 10.255.0.12 (sw2),
  static routes between them. Deployed and recorded on 2026-10-14; every session reached the
  intended state on first boot (the wrong-AS peer reads `Connect`). Sessions:
  1. eBGP sw1 to sw2 on the SVIs, with a password and a description, `ipv4 unicast` on both ends,
     `ipv6 unicast` activated on sw1 only. Established, and sw1's row shows the mismatch.
  2. On sw1, a peer on sw2's loopback with a wrong remote AS. Not established, not shut.
  3. On sw1, a peer on an unused address, `shutdown`. `idle`, shut.
  4. On sw2, a `bgp listen range` that sw1 connects into from a second loopback: the dynamic row
     is on sw2.
- **SVI on an existing VLAN**: VLAN 10 is already declared and carried by Port-Channel10, so no new
  VLAN or link is needed. It adds `Vlan10` and loopback rows to a live `interfaces` crawl; the
  recordings of features 007 to 009 are not re-recorded and their tests are unaffected.
- **sw4**: no BGP, the `empty` case, recorded with `_empty` in the names.
- **sw3**: denied, cannot serve.
- **What the lab does not cover**: an IPv6 peer address and a link-local one (no lab has
  either). Code review found the summaries skipped a zoned address such as `fe80::1%Et1` without
  error; the templates now accept the zone, and a hand-written device, `linklocal_*` in
  `packs/arista_eos/testdata/lab/`, covers a global IPv6 and a link-local peer (#45 tracks lines
  that are still skipped silently), a non-default VRF (core1's
  `gold` does), EVPN (dc-leaf1 does), iBGP (dc-leaf1 and core1 do).

## R11. The engine and discovery need no change

- **Seen**: the graph projector reads `neighbours`, `interfaces`, `vlans` and `interface_vlans` by
  name (`internal/graph/graph.go`); a new family is not read. Discovery queues `find` tasks from
  `neighbours` only (`internal/collector/find.go`, the only `find` source). This rests on code
  inspection: the fake test device cannot answer BGP commands without a `bgp_peers` recipe in the
  fake pack, and such a recipe would run in every crawl test. The snapshot verdict counts `identity` only. FR-015 holds with no code change;
  a test asserts that a snapshot with `bgp_peers` rows has the same graph and verdict.

## R12. A row that breaks a rule is never dropped

- **Seen in the code**: `parse.Parse` validates every row after the merge, and any error makes the
  whole observation `parse_failed`, with no row stored (`internal/parse/parse.go`). The collector
  then raises a `parse_failed` finding that carries the error and cites the observation
  (`internal/collector/claims.go`). There is no path where one bad row is removed and the others
  are kept.
- **Decision** (plan review): `OnlyWith`, `EmptyList` and `Set` breaches go through that path. A
  test feeds rows that break each rule to `fact.Validate` and a recording that would produce one to
  `parse.Parse`, and checks `parse_failed` with the field named in the error.
- **What the parser does on its own**: on a row whose state is not `established`, it drops a
  negotiated list or a peer router ID the template found (EOS may still print those of the last
  session, R4). That is FR-005a applied to stale output, not a rejection: the row is kept with all
  its other fields. It is the only field the parser removes, and it is listed in the contract.

## R13. Versions

- **Decision**: `versions: '>=4.36'`, the only version recorded and scanned for secrets, like the
  recipes of features 007 and 008.
