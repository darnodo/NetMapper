# Research: Project layer 2 domains from VLAN data

Every decision below was taken by reading the projector of feature 004 (`internal/graph`), the
schema of 008 (`internal/fact/fact.go`) and the test harness (`internal/testutil`). Nothing here
needed a lab.

## R1. Where domains are built

**Decision**: a new file `internal/graph/l2domain.go`, called from `projectIn` after `buildLinks`
and before `write`, in the same transaction and under the same advisory lock.

**Rationale**: FR-010 says domains are part of the projection and replaced with it. `projectIn`
already holds the entity set, the ports and the `l1_link` edges in memory, with each edge's two
entities and two `iface` pointers filled in by `attach`. Domains need exactly that, so no second
read of the edges and no second transaction. `projectorVersion` goes from 1 to 2, which is what the
constant exists for: sets written by version 1 have no domains and can be found and re-projected
with `netmapper project`. The sweep does not redo them on its own (004 FR-022).

**Alternatives considered**: a separate `l2domain` step in the job runner, after projection. Rejected:
a second sweep, a second "current set" marker, and a window where a consumer reads edges with no
domains or domains pointing at edges that were just replaced.

## R2. Reading the two families, with their status

**Decision**: the rows come through the existing `readFamily` (collected observations of the active
parse generation, mapped to the entity). The status comes from a small new query that returns, per
entity and family, whether at least one observation of the active parse generation is `collected`
or `empty`. Two derived flags per entity:

- `declared`: the entity has a collected `vlans` observation. It makes the entity a member of each
  VLAN in it (FR-004).
- `ports known`: the entity has a `collected` or `empty` `interface_vlans` observation (FR-003).

An entity whose `vlans` is `empty` declares nothing and is a member of nothing. An entity whose
`interface_vlans` is `empty` has its ports known and carries nothing on any cable.

`readFamily` keeps its signature. `vlans` and `interface_vlans` are written by the scrape task like
`interfaces`, so the existing task-to-entity mapping finds their owner without a change.

**Two observations for one entity** (a device reached on two addresses): rows are merged. For one
VLAN ID, or one port, the row of the lowest observation id wins, the same tie rule as interface
aliases (004 research R5). Tested with two observations that disagree (FR-011).

**Alternatives considered**: widen `readFamily` to return every status. Rejected: interfaces and
neighbours never needed it, and each caller would then filter.

## R3. What a port carries

**Decision**: for each port with an `interface_vlans` row, two values are computed once:

- `untagged`: the access VLAN for mode `access`; for mode `trunk`, the native VLAN when it is in the
  active list; nothing otherwise.
- `tagged`: for mode `trunk`, every VLAN declared on the device that is in the active list, minus
  the native VLAN.

A member row (`channel` set) carries what its channel's row carries. A port with no row (routed,
management, or not reported) carries nothing. A mode other than `access` and `trunk` carries nothing:
feature 008 records any other mode word as `parse_failed`, so this only guards against a future pack.

The active list is a list of ranges (`["1-9", "11-4094"]`). Only declared VLAN IDs are checked
against it, so the ranges are never expanded to 4094 values: a small `inRanges(v, list)` helper
parses each item. The list is already validated and normalised by the parser (008 FR-006).

**Rationale**: these are the rules the spec's decisions list states. Checking against declared VLANs
also enforces FR-005 for free on the tagged side.

## R4. Building the components

**Decision**: a node is `(entity, local VLAN ID)`, one per declared VLAN of a `declared` entity. A
plain union-find over nodes (two short functions, no library). For each `l1_link` edge whose two
ends are ports of resolved entities, and whose two entities are both `declared` and `ports known`:

1. For each VLAN tagged at both ends and declared on both devices: union the two nodes, and record
   the edge as a joining link of that VLAN.
2. When both ends have an `untagged` VLAN and each is declared on its device: union
   `(a, untagged_a)` with `(b, untagged_b)` and record the edge as a joining link. When the two IDs
   differ, record a mismatch (R7).

Each component is one domain. Its key is the lowest local VLAN ID among its members (clarified
2026-10-12). Domains are written ordered by key, then by the lowest `(device_key, local VLAN ID)` of
their members, which is a total order since one node belongs to one domain. Union-find's internal
choice of root never reaches the output.

Edges whose far end is not a port of a resolved entity (`unknown:` references, a `dev:` reference
with no port, `has_address`) join nothing (FR-005). A `dev:` edge to an entity with known ports is
an unchecked cable (R5): the far port is not known, so neither is what it carries.

**Alternatives considered**: a breadth-first search per VLAN over an adjacency map. Same result,
more code once untagged mismatches link two different VLAN IDs, which union-find handles with no
special case.

## R5. Confidence and unchecked cables

**Decision**: `l2domain.confidence` is `complete` or `partial`. `l2domain_link` gets a `state`
column, `joined` or `unchecked`. A domain is `partial` if and only if it has at least one
`unchecked` link.

A cable is recorded as `unchecked` on a domain in these cases:

- One end can join (its entity is `declared` and `ports known`) and the other cannot: on each
  domain holding a VLAN the able end carries on that cable, tagged or untagged.
- An entity is `declared` but its ports are not known: on every domain it is in, for each of its
  `l1_link` edges to another resolved entity (clarified 2026-10-12).
- The far end is a resolved entity with no port named (`dev:` edge): on each domain holding a VLAN
  the near end carries on that cable.

An unchecked link never joins two nodes. It is in the table so a reader can see which cable made a
domain partial, from the domain, without recomputing anything (constitution I).

**Rationale**: `both_ends` and `one_end` on `edge` describe whether both devices reported the
cable. Reusing them here would mix two questions: a cable both ends reported can still be unchecked
for VLANs. Two new words keep them apart.

Spec wording adjusted in the same change: US2 scenario 2 says the trunk "is not a link that joins
that domain" and is listed against it as unchecked.

**Alternatives considered**: a count of unchecked cables on `l2domain`. Rejected: a number with no
pointer to the cables behind it is an answer stripped of its evidence.

## R6. Label

**Decision**: the label is the `name` of the VLAN rows behind the members when every member that has
a name has the same one; no label otherwise, and no label when no member has a name. EOS prints a
default name (`VLAN0010`) for an unnamed VLAN, so two switches with VLAN 10 unnamed get the label
`VLAN0010`; that is what both devices say. Tested with members that tie on count but disagree on
name, which must give no label rather than either name.

## R7. The VLAN mismatch finding

**Decision**: category `vlan_mismatch`, domain `compliance`, severity `warning`, raised from the
projection like `link_disagreement` and deleted with it in `clear` (FR-012).

- Grouping (clarified 2026-10-12): a mismatch is keyed by the pair of facing ports, where a member
  port is replaced by its channel. Two member cables of one port-channel pair give one finding. A
  plain cable gives one finding.
- `subject_ref`: the lower of the two port references (`if:<device_key>/<port>`), byte order, as
  edges use.
- `detail`: `ports` (both references, channels when bundled), `vlans` (the untagged VLAN at each of
  those ports, same order), `edges` (the names of the cables, sorted).
- Evidence: the `interface_vlans` observation of each end and the `neighbours` observations behind
  each cable.

`compliance` rather than `data_quality`: the data is right, the network is not configured the way
both ends assume. `data_quality` is what NetMapper's own data gets wrong (unknown platform, parse
failure, contradicting reports). The read API maps the category to confidence `derived`, since it
comes from comparing two observations (`internal/api/findings.go`, `findingConfidence`).

**Alternatives considered**: severity `high`. Rejected: `high` is used for a denied credential, which
blocks collection. A mismatch is worth fixing but blocks nothing NetMapper does.

## R8. Schema

**Decision**: migration `0011_l2domain.sql` creates the three tables the data model describes, with
what the evidence and the constraints need. Full shape in [data-model.md](data-model.md).

- `l2domain_member` has `snapshot_id` and a unique `(snapshot_id, entity_id, local_vlan_id)`: FR-004
  ("exactly one domain per declared VLAN") becomes a key, not a code path that has to remember.
- `l2domain_member` carries `vlans_observation_id` (the declaration) and `ports_observation_id` (the
  port data, null when not known). With the edge's own evidence, that is every observation behind a
  membership and a link (FR-009).
- `projection` gets an `l2domains` count, for the same reason it has `interfaces` and `edges`.
- `finding_category_check` gains `vlan_mismatch`.
- Grants: the engine and the operator get the same rights on the three tables as on `edge`. The API
  role gets nothing: domains are not served yet (FR-018).
- Cascades: members cascade from `entity`, links from `edge`, both from `l2domain`. A re-resolution
  leaves orphan `l2domain` rows that the sweep's re-projection removes in `clear`, the same path
  004 uses for a projection row whose resolution changed.

**Alternatives considered**: a separate `l2domain_evidence` table. Rejected: two columns on the
member row say the same thing with one table less.

## R9. Test fixtures with no lab

**Decision**: the graph tests run full crawls against fake devices, as every 004 test does. The VLAN
recipes are added to a copy of the fakeos pack in a temporary directory, the way
`internal/collector/missing_test.go` builds a pack without its interfaces recipe. The recipes and
templates live in `internal/graph/testdata/fakeos-l2/`.

The shared fakeos pack is not changed: `NoRecipeFamilies` and the tests proving that another platform
gets `unsupported/no_recipe` for `vlans` and `interface_vlans` (008 SC-004) keep working unchanged.

Fake output formats (fakeos only, not vendor data):

```text
display vlans          <id> <status> [<name>]
display switchport     <port> access <vlan>
                       <port> trunk <native> <active list>
                       <port> member <channel>
```

An unreachable far end is a fake device whose `display switchport` stalls (status `unreachable`),
which is how 004 tests make one family fail.

## R10. Live check on test/lab

**Decision**: no lab change. What feature 008 left gives, on a crawl:

| VLAN | Domain | Links |
| ---- | ------ | ----- |
| 10 | sw1 and sw2 | eth1 trunk (tagged), both Port-Channel10 members (tagged), eth4/eth5 access (untagged) |
| 99 | sw1 and sw2 | eth1 (native, untagged), Port-Channel10 members (tagged) |
| 1 | sw1 and sw2 | Port-Channel10 members (native 1, untagged) |
| 20, 30, 32, 40-49 | sw1 and sw2 | eth1 and Port-Channel10 members |
| 31 | sw1 alone, sw2 alone | none, expected: a suspended VLAN should not be in a trunk's active list |
| 50 | sw2 alone | none: sw1 does not declare it |

All `complete`. sw3 is denied, but sw2's port towards it is routed and carries nothing, so it marks
nothing unchecked. No `vlan_mismatch`. Expected values to confirm on the VM; the fixture tests are
the gate, the live crawl is a check.

**test/lab, checked on 2026-10-12 (T042).** Deployed on the NetLab VM, sw4 taken off the network
(`ip link set eth0 down`, it shares sw1's serial), crawled with this branch: collector on the VM,
engine and database on the Mac. The table above holds row for row: 19 domains, all `complete`, no
`vlan_mismatch`. VLAN 10 is joined over Ethernet1 (tagged), both Port-Channel10 members (tagged) and
the sw1 Ethernet4 / sw2 Ethernet5 access link (untagged); VLAN 99 over Ethernet1 (native) and the
Port-Channel10 members (tagged); VLAN 1 over the Port-Channel10 members only. VLAN 31 is one domain
per switch, VLAN 50 is sw2 alone. sw3 is `denied` and no entity, so it adds nothing. EOS's spanning
tree blocks Ethernet1 (the loop with Port-Channel10); the domain still lists it, since spanning tree
state is out of scope (spec edge case). The optional native-VLAN mismatch check was not run.

**arista-evpn-vxlan-clab, checked on 2026-10-12.** Crawled with this branch the same way (SSH as
`netops`, SNMPv3 `snmp-ro`).

- 28 entities. `vlans` collected on all 28; `interface_vlans` collected on 22 and `empty` on 6 (the
  spines and cores, routed only).
- 44 domains, all `complete`, no `unchecked` link, no `vlan_mismatch`.
- Each MLAG pair shares VLAN 1 and its peer VLANs 4090 (`mlag-peer`) and 4091 (`mlag-ibgp`) over its
  peer-link, one domain per pair. Each access switch joins its leaf pair on its service VLAN (34, 40,
  60, 70, 78), three members and three links. VLAN 40 is two domains, one per leaf pair: the fabric
  stitches them over VXLAN, which is VNI work (#33), not a VLAN join. Spines and cores are alone in
  VLAN 1.
- The snapshot was `quarantined` and 110 `parse_failed` findings were raised, all on the management
  families of feature 007 (`snmp`, `aaa_methods`, `local_users`, `management_servers`): `netops` is
  privilege 1, and 007 research R13 says those need privilege 15. Not this feature.

## R11. Documents updated in the same change

- `docs/c4-model/04-data-model.md`: the three rows (notes column), `vlan_mismatch` in the finding
  categories, and the paragraph on what the projector writes and the roles it grants.
- `cmd/netmapper/project.go` output line, and its mention in the README if any: domains and
  mismatches counted beside edges and disagreements.
- `docs/how-to/deploy.md`, upgrade section: snapshots projected before this version have no domains
  until `netmapper project <id>` is run on them; the sweep does not redo them (004 FR-022).
- No other README, how-to or Bruno change: no command, flag, key or endpoint changes. The API
  findings endpoint returns the new category with no change to its shape.
