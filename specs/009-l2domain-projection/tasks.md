---
description: "Task list for projecting layer 2 domains from VLAN data"
---

# Tasks: Project layer 2 domains from VLAN data

**Input**: Design documents from `/specs/009-l2domain-projection/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/l2domain.md, quickstart.md

**Tests**: Included. FR-016 lists the graph engine cases to cover from fixture snapshots, and the
constitution asks for tie rules tested with inputs that tie and for each role's path tested under
that role.

**Organization**: Tasks are grouped by user story. US1 (domains) is the MVP. US2 (confidence and
unchecked cables) and US3 (untagged mismatch) both build on the union-find of US1 and can then run
side by side: they touch different functions of `internal/graph/l2domain.go` and different test
functions.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an incomplete task)
- **[Story]**: US1 to US3 from spec.md
- Paths are relative to the repository root. Tests live next to the code, as in 001 to 008.
- **[VM]** in a description: needs the `NetLab` VM (ssh host `NetLab`). Every other task runs on the
  Mac with no lab.

Conventions used throughout:

- Every graph test runs a real crawl of fake devices, as the 004 tests do. `key("S001")` is the device
  key of a fakeos device, `MAC("S002")` its chassis MAC, both from `internal/graph/interfaces_test.go`
  and `internal/testutil`.
- A cable between two fake switches is a pair of neighbour entries naming each other's port and
  chassis MAC: sw1 gets `"p1 sw2 10.0.0.2 p1 "+MAC("S002")`, sw2 gets `"p1 sw1 10.0.0.1 p1 "+MAC("S001")`.
  fakeos renames `pN` to `portN`; `pc10` keeps its spelling.
- Fake VLAN output (research R9, fakeos test data only):

  ```text
  display vlans        <id> <status> [<name>]            e.g. "10 active USERS"
  display switchport   <port> access <vlan>              e.g. "p4 access 10"
                       <port> trunk <native> <active>    e.g. "p1 trunk 99 10,20,99" ("none" for no active VLAN)
                       <port> member <channel>           e.g. "p2 member pc10"
  ```

  A fake device without these commands in its `CLI` map prints nothing, which the parser records as
  `empty`. Stalling a command (`dev.Stall["display switchport"] = true`) records it `unreachable`.

---

## Phase 1: Setup

**Purpose**: The schema and the test data every later task writes against.

- [X] T001 Create migrations/0011_l2domain.sql (goose Up and Down, header comment naming feature 009 and research R8, in the style of 0007_graph.sql). Up: table `l2domain` with `id bigserial PRIMARY KEY`, `snapshot_id bigint NOT NULL REFERENCES snapshot`, `key_type text NOT NULL CHECK (key_type IN ('vlan', 'vni'))`, `key_id integer NOT NULL`, `label text NULL`, `confidence text NOT NULL CHECK (confidence IN ('complete', 'partial'))`, `pinned_name text NULL` (comment: reserved for an operator decision, never written by the projector). Table `l2domain_member` with `l2domain_id bigint NOT NULL REFERENCES l2domain ON DELETE CASCADE`, `snapshot_id bigint NOT NULL`, `entity_id bigint NOT NULL REFERENCES entity ON DELETE CASCADE`, `local_vlan_id integer NOT NULL CHECK (local_vlan_id BETWEEN 1 AND 4094)`, `vlans_observation_id bigint NOT NULL`, `ports_observation_id bigint NULL`, `PRIMARY KEY (l2domain_id, entity_id, local_vlan_id)`, `UNIQUE (snapshot_id, entity_id, local_vlan_id)` (comment: FR-004, one declared VLAN is in exactly one domain), `FOREIGN KEY (snapshot_id, vlans_observation_id) REFERENCES observation (snapshot_id, id)`, same for `ports_observation_id`. Table `l2domain_link` with `l2domain_id bigint NOT NULL REFERENCES l2domain ON DELETE CASCADE`, `edge_id bigint NOT NULL REFERENCES edge ON DELETE CASCADE`, `state text NOT NULL CHECK (state IN ('joined', 'unchecked'))`, `PRIMARY KEY (l2domain_id, edge_id)`. `ALTER TABLE projection ADD COLUMN l2domains integer NOT NULL DEFAULT 0`. Replace `finding_category_check` with the 0007 list plus `'vlan_mismatch'`. Owner `netmapper_owner` on the three tables; `GRANT SELECT, INSERT, UPDATE, DELETE` on the three to `netmapper_engine` and `netmapper_operator`, `GRANT USAGE ON SEQUENCE l2domain_id_seq` to both; a comment that `netmapper_api` gets nothing because domains are not served yet (FR-018). Down: delete `finding_evidence` then `finding` rows with category `vlan_mismatch`, restore the 0007 category check, drop the `l2domains` column, drop the three tables
- [X] T002 [P] Create the fake VLAN recipes in internal/graph/testdata/fakeos-l2/recipes/vlans.yaml and internal/graph/testdata/fakeos-l2/recipes/interface_vlans.yaml, and their templates in internal/graph/testdata/fakeos-l2/templates/display_vlans.textfsm and internal/graph/testdata/fakeos-l2/templates/display_switchport.textfsm, for the formats in the conventions above. Model them on internal/pack/testdata/fakeos/recipes/neighbours.yaml (`versions: '>=1.0'`, `transport: ssh`). `vlans`: map `vlan_id`, `name`, `status` (values `active`, `suspended`, `shutdown`, `'*': other`). `interface_vlans`: map `interface`, `mode`, `access_vlan`, `native_vlan`, `active_vlans`, `channel`; `split: { active_vlans: ',' }`; `values: { active_vlans: { none: '' } }`. One template rule per line shape, each ending in `-> Record`, the trunk rule first so a four-field line is never read as an access line. A header comment in each recipe: fakeos test data for feature 009 (research R9), not loaded by the shared fakeos pack, so `NoRecipeFamilies` stays true
- [X] T003 [P] Create internal/graph/l2domain_test.go with the helpers every later test uses: `l2lab(t, devices)`, which copies internal/pack/testdata/fakeos into `t.TempDir()` (as `TestMissingFamilies` in internal/collector/missing_test.go does), copies the recipes and templates of internal/graph/testdata/fakeos-l2 into it, loads `_base` and that copy with `pack.Load`, sets `NM_SNMP` and `NM_SSH` like `lab`, builds `NewLab`, replaces `l.Registry` before `l.Crawl(Doc)` and returns the lab; `vlanned(dev, vlans, switchport string) *fake.Device`, which sets `dev.CLI["display vlans"]` and `dev.CLI["display switchport"]` and returns dev; `domains(l) []string`, sorted lines `"<key_id> <confidence> <label or -> <device_key>:<local_vlan_id>,... | <state> <edge name>,..."` for every domain of the lab's snapshot, members and links each sorted; `members(l, vlan) []string`, the `device_key` of each member of the domains whose `key_id` is vlan, one line per domain

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The projector reads the two families and writes an empty domain set, with nothing else
changing. Every user story fills in what this phase leaves empty.

- [X] T004 In internal/graph/graph.go add `vlanState` reading (research R2): a query over `observation` joined to the active `parse_generation`, as `readFamily` does, returning per owning entity (task mapped through `p.byTask`, own task then parent, as in `readFamily`) whether `vlans` has a `collected` observation (`declared`) and whether `interface_vlans` has a `collected` or `empty` one (`portsKnown`). Store both flags on `entity`, plus the lowest observation id of each family (`vlansObs`, `portsObs`), for the member row's evidence columns. Leave `readFamily`'s signature unchanged
- [X] T005 Create internal/graph/l2domain.go with the package comment line for this file (what it builds, research R1), the in-memory types: `vlanRow` (id, name, obs), `portVLANs` (mode, access, native, active ranges, channel, obs), per-entity maps `vlans map[int]vlanRow` and `switchports map[string]portVLANs` filled from `readFamily(ctx, tx, "vlans")` and `readFamily(ctx, tx, "interface_vlans")`; for a VLAN ID or a port seen in two observations of one entity, keep the row of the lowest observation id (R2). Add `inRanges(v int, ranges []string) bool` for the normalised range lists (`"10"`, `"30-40"`); add `carries(e *entity, port string) (untagged int, tagged []int)` implementing research R3 exactly: a member row resolves to its channel's row; `access` gives `untagged = access`; `trunk` gives `untagged = native` when native is in the active list, and `tagged` = every VLAN declared on e (key of `e.vlans`) in the active list except native, sorted; anything else, or no row, gives nothing. Add a `domain` type (key, label, confidence, members, links, findings slot) and an empty `buildDomains()` method
- [X] T006 In internal/graph/write.go: in `clear`, delete `l2domain` rows of the snapshot first (members and links cascade), and extend the finding deletes to `category IN ('link_disagreement', 'vlan_mismatch')`; add an empty `writeDomains` called after `writeEdges` and before `writeFindings`; add `l2domains` to the `projection` insert and its `ON CONFLICT` update. In internal/graph/graph.go set `projectorVersion = 2` (comment: version 2 adds L2 domains, research R1), call `buildDomains` after `buildLinks` and `addAddressEdges`, and add `L2Domains int` and `VLANMismatches int` to `Result`
- [X] T007 Run `go test -count=1 ./internal/graph/... ./internal/jobrunner/... ./cmd/... ./internal/collector/...`: every existing test passes unchanged, since the shared fakeos pack collects no VLAN family and the projector writes no domain

**Checkpoint**: the schema exists, the projector reads VLAN data and writes an empty set.

---

## Phase 3: User Story 1 - See which devices share a broadcast domain (Priority: P1) (MVP)

**Goal**: Domains, members and joining links for networks configured as intended.

**Independent Test**: `go test -run 'TestL2' ./internal/graph/...` with no lab: a trunk carrying VLAN
10 gives one domain of two members; the same VLAN pruned at one end gives two domains of one.

### Tests for User Story 1

Each test below builds its fixture with `l2lab` and `vlanned`, and asserts with `domains` or `members`.
Write them first and see them fail.

- [X] T008 [US1] In internal/graph/l2domain_test.go add `TestL2TrunkSharesVLAN` (US1-1, FR-001): sw1 and sw2 declare `1 active` and `10 active USERS`, `p1 trunk 1 1,10` on both, cabled on p1. Expect one VLAN 10 domain with both devices, label `USERS`, confidence `complete`, the p1 edge as its only `joined` link; and one VLAN 1 domain with both (untagged native). Add `TestL2PrunedVLAN` (US1-2): same, but sw2's p1 is `p1 trunk 1 1`. Expect two VLAN 10 domains of one member each, no link on either
- [X] T009 [P] [US1] Add `TestL2ChainOfThree` (US1-3): sw1 p1 to sw2 p1, sw2 p2 to sw3 p1, VLAN 10 tagged on all four ports. Expect one VLAN 10 domain with three members and two links. Add `TestL2PortChannel` (US1-4): sw1 and sw2 cabled on p2 and p3, both `p2 member pc10`, `p3 member pc10`, `pc10 trunk 1 1,10`. Expect one VLAN 10 domain with both devices and both member cables as `joined` links
- [X] T010 [P] [US1] Add `TestL2AccessVLANDeclaredOnOneSide` (US1-5, FR-005): sw1 declares 30 and has `p4 access 30`; sw2 has `p4 access 30` and does not declare 30; cabled on p4. Expect sw1 alone in a VLAN 30 domain and sw2 in no VLAN 30 domain. Add `TestL2VLANNoCableCarries` (US1-6, FR-004): sw2 declares 50 and nothing carries it. Expect a VLAN 50 domain of one member with no link and confidence `complete`
- [X] T011 [P] [US1] Add `TestL2TaggedAgainstUntagged` (FR-002, FR-003): sw1 `p1 access 10`, sw2 `p1 trunk 1 10` (native 1 not in the active list, so sw2 carries nothing untagged on p1), both declare 1 and 10. Expect no VLAN 10 domain holding both devices and no `vlan_mismatch`: sw1 sends 10 untagged, sw2 expects it tagged. Add `TestL2NoMemberThroughUnknownEndpoint` (FR-005): sw1's p1 faces an unmanaged box (`"p1 unmanaged 10.0.0.9 eth0 aa:bb:cc:99:99:99"`), VLAN 10 on p1. Expect sw1 alone in VLAN 10
- [X] T012 [P] [US1] Add `TestL2EmptyFamilies` (spec edge cases): sw1 with no `display vlans` output (`vlans` is `empty`) is in no domain; sw2 with VLANs declared and no `display switchport` output (`interface_vlans` is `empty`) is alone in each VLAN it declares, with confidence `complete`, even though it is cabled to sw1
- [X] T013 [P] [US1] Add `TestL2Label` (FR-008, research R6), with inputs that tie: a three-switch chain where VLAN 10 is named `A` on sw1 and sw3 and `B` on sw2 gives no label; a two-switch domain named `A` and `B` (one each, a tie on count) gives no label; one where sw2 prints no name and sw1 prints `A` gives `A`; both unnamed gives no label
- [X] T014 [P] [US1] Add `TestL2OneDeclaredVLANOneDomain` (SC-002, FR-004): on the fixtures of T008 and T009, for every `vlans` row of a declared entity there is exactly one `l2domain_member` row with that entity and local VLAN (one SQL query comparing `observation.parsed` to `l2domain_member`). Add `TestL2SameDomainIffPath` (SC-003) on the chain fixture: every pair of members of one domain is joined by a path of `joined` links, and no `joined` link connects two domains
- [X] T015 [P] [US1] Add `TestL2RowTieBreak` (research R2, FR-011), with inputs that tie: after the crawl, insert with the superuser pool a second `vlans` observation for sw1, copied from the first (same task, same active parse generation, status `collected`) but naming VLAN 10 `OTHER`; it gets a higher id. Run `graph.Project` twice. Expect both runs to give the label of the original row (the lower observation id) and identical `domains(l)`. If the observation table refuses the insert (immutability trigger), insert through the same path the collector uses in internal/store and say so in the test comment
- [X] T016 [P] [US1] Add `TestL2Reproducible` (SC-004, FR-011) and `TestL2WipeAndRecompute` (SC-007, FR-013) on the port-channel plus chain fixture: three `graph.Project` calls give identical `domains(l)`; deleting `l2domain` and `projection` rows then `graph.Project` restores the same `domains(l)` with no session opened beyond the crawl's (`l.Net.Opens()`, as `TestWipeAndRecompute` in internal/graph/graph_test.go)
- [X] T017 [P] [US1] Add `TestL2Evidence` (FR-009, SC-005): every member's `vlans_observation_id` points at an observation of family `vlans` of the same entity's task tree and `ports_observation_id` at one of family `interface_vlans`; each has a `collected_at`; every link's edge has `edge_evidence` rows

### Implementation for User Story 1

- [X] T018 [US1] Implement `buildDomains` in internal/graph/l2domain.go (research R4): one node per `(entity, local VLAN ID)` for each declared VLAN of each `declared` entity; a plain union-find (`find` with path halving, `union`) over node indexes; for each `l1_link` edge in `p.edges` whose `fromPort` and `toPort` are both set and whose two entities are both `declared` and `portsKnown`, call `carries` on each end: union the nodes of each VLAN tagged at both ends and declared on both, and of the two untagged VLANs when both ends have one, each is declared on its device, and they are equal (unequal untagged VLANs are US3, skip them here); record the edge as a `joined` link of the VLANs it joined. Group nodes by root into domains; key = lowest local VLAN ID; order domains by key, then by the lowest `(device_key, local VLAN ID)` of their members; order members and links inside each. A comment: union-find's root choice never reaches the output
- [X] T019 [US1] Implement the label in internal/graph/l2domain.go (research R6): the name of every member's VLAN row; label it only if every non-empty name is the same and at least one is non-empty
- [X] T020 [US1] Implement `writeDomains` in internal/graph/write.go: for each domain in order, insert `l2domain` (`key_type` `'vlan'`, `key_id`, `label` through `null`, `confidence`), then in one `pgx.Batch` its `l2domain_member` rows (`snapshot_id`, `entity_id`, `local_vlan_id`, `vlans_observation_id`, `ports_observation_id` or NULL when `portsKnown` is false) and its `l2domain_link` rows (`edge_id` from the edge written by `writeEdges`, `state`). `writeEdges` must keep the inserted id on each `edge` for this (add an `id` field, set from `RETURNING id`). Set `Result.L2Domains`
- [X] T021 [US1] Run `go test -count=1 -run 'TestL2' ./internal/graph/...` until T008 to T017 pass, then the whole package

**Checkpoint**: US1 complete and testable alone; this is the MVP.

---

## Phase 4: User Story 2 - Know how well a domain is known (Priority: P1)

**Goal**: A domain that rests on a cable whose far end gave no VLAN data says so, and names the cable.

**Independent Test**: `go test -run 'TestL2Unchecked|TestL2Confidence' ./internal/graph/...`: a trunk
whose far switch has no VLAN data gives a `partial` domain listing the trunk as `unchecked`.

### Tests for User Story 2

- [X] T022 [US2] In internal/graph/l2domain_test.go add `TestL2UncheckedFarEnd` (US2-2, FR-007): sw1 declares 10 with `p1 trunk 1 1,10`; sw2 is cabled on p1 and stalls both `display vlans` and `display switchport`. Expect sw1 alone in VLAN 10 and VLAN 1, both `partial`, each with the p1 edge as an `unchecked` link and no `joined` link; sw2 in no domain. Add `TestL2PortsUnknown` (clarified 2026-10-12): sw2 declares 10 and 20 but stalls `display switchport`, cabled to sw1 on p1 and to sw3 on p2. Expect sw2 a member of VLAN 10 and VLAN 20, each of its domains `partial` with both of its cables `unchecked`, and nothing joined across them
- [X] T023 [P] [US2] Add `TestL2PortlessFarEnd` (research R4, R5): sw1 reports sw2 on p1 naming the chassis MAC but no far port (`"p1 sw2 10.0.0.2 - "+MAC("S002")`), sw2 does not report back, both have VLAN data, VLAN 10 on sw1's p1. Expect sw1's VLAN 10 domain `partial` with the `dev:` edge `unchecked`. Add `TestL2CompleteWithoutCables` (US2-3): a device declaring 10 with no cable at all gives a `complete` domain
- [X] T024 [P] [US2] Add `TestL2ConfidenceInvariant` (contracts/l2domain.md): over every fixture of this file, one SQL query asserts `confidence = 'partial'` if and only if the domain has an `unchecked` link, and that no `unchecked` link's two ends are members of the same domain through it (removing unchecked rows changes no component)

### Implementation for User Story 2

- [X] T025 [US2] In internal/graph/l2domain.go, after the unions of `buildDomains`, mark unchecked links (research R5): for each `l1_link` with one end able to join (`declared` and `portsKnown`) and the other end not, add the edge as `unchecked` to the domains holding each VLAN the able end carries on that port, tagged or untagged; for each `declared` entity without `portsKnown`, add each of its `l1_link` edges to another resolved entity as `unchecked` on every domain it is a member of; for a `dev:` edge (far entity set, far port nil) add it as `unchecked` on the domains of the VLANs the near end carries. Never add an `unchecked` link where the same edge is already `joined` on that domain. Then set confidence: `partial` when the domain has an `unchecked` link, `complete` otherwise
- [X] T026 [US2] Run `go test -count=1 ./internal/graph/...` until T022 to T024 pass along with every US1 test

**Checkpoint**: US1 and US2 both pass alone and together.

---

## Phase 5: User Story 3 - See untagged traffic leaking between VLANs (Priority: P2)

**Goal**: Two ends carrying different VLANs untagged join both VLANs into one domain and raise one
`vlan_mismatch` finding per pair of facing ports.

**Independent Test**: `go test -run 'TestL2Mismatch' ./internal/graph/...`: a trunk with native 1 at
one end and 99 at the other gives one domain keyed 1 and one finding.

### Tests for User Story 3

- [X] T027 [US3] In internal/graph/l2domain_test.go add `TestL2MismatchNative` (US3-1): sw1 `p1 trunk 1 1,99`, sw2 `p1 trunk 99 1,99`, both declare 1 and 99. Expect one domain keyed 1 with members sw1:1 and sw2:99 (untagged), plus whatever the tagged VLANs give (sw1's tagged 99 and sw2's tagged 1 match nothing tagged on the other side, so they stay alone), and exactly one finding with `domain = 'compliance'`, `category = 'vlan_mismatch'`, `severity = 'warning'`, `subject_ref` the lower of the two port references, `detail.ports` both port references, `detail.vlans` `[1, 99]` in the same order, `detail.edges` the p1 edge name, and `finding_evidence` holding both `interface_vlans` observations and the `neighbours` observations of the cable. Add `TestL2MismatchAccess` (US3-2): `p4 access 10` facing `p4 access 20`, both declared on both sides: one domain keyed 10, one finding
- [X] T028 [P] [US3] Add `TestL2NoMismatchWhenNativesMatch` (US3-3): both natives 99, no `vlan_mismatch`. Add `TestL2MismatchUndeclared` (spec edge case): sw2's untagged VLAN is not declared on sw2: nothing joined, no finding
- [X] T029 [P] [US3] Add `TestL2MismatchPortChannel` (US3-5, clarified 2026-10-12): two member cables of `pc10` on each side, natives 1 and 99. Expect exactly one finding, `detail.ports` the two `pc10` references, `detail.edges` both member cables sorted. Add `TestL2MemberTwiceInOneDomain` (spec edge case): sw1 p1 native 1 facing sw2 p1 native 99, and sw1 p2 tagged 99 facing sw2 p2 tagged 99, with VLAN 1 tagged on neither. Expect one domain keyed 1 holding sw1:1, sw2:99 and sw1:99, so sw1 appears twice
- [X] T030 [P] [US3] Add `TestL2MismatchReplacedAndPerSnapshot` (US3-4, FR-012): the native mismatch fixture projected three more times keeps exactly one `vlan_mismatch` with evidence (as `TestFindingReplacement` in internal/graph/graph_test.go); then change sw2's fake output to native 1 and crawl again (`l.Crawl(Doc)` a second time, as `TestLinkNameIsStableAcrossSnapshots` does): the second snapshot has no `vlan_mismatch` and the first keeps its own. Findings raised by the collector are untouched
- [X] T031 [P] [US3] In internal/api/findings.go add `"vlan_mismatch": "derived"` to `findingConfidence`, and in internal/api/findings_test.go a case that a snapshot holding a `vlan_mismatch` finding (inserted with the superuser pool) is served with confidence `derived` and its evidence

### Implementation for User Story 3

- [X] T032 [US3] In internal/graph/l2domain.go, in the untagged step of `buildDomains`, when both ends carry an untagged VLAN, both are declared on their devices and the IDs differ: union the two nodes, record the edge as `joined` on that domain, and record a mismatch keyed by the pair of facing ports where a member port is replaced by its channel's canonical name, lower reference first (research R7). Several cables with the same key make one mismatch whose `edges` list grows. Turn each mismatch into a finding: `subject_ref` the lower port reference, `detail` `{"ports": [...], "vlans": [...], "edges": [...]}` with `vlans` in the order of `ports` and `edges` sorted, evidence the `portsObs` of both entities plus the `edge_evidence` observations of every cable, deduplicated and sorted. Set `Result.VLANMismatches`
- [X] T033 [US3] In internal/graph/write.go raise the mismatch findings with `store.RaiseFinding(ctx, tx, p.snap.id, "compliance", "vlan_mismatch", "warning", subject, detail, evidence...)` in `subject_ref` order, after the `link_disagreement` ones
- [X] T034 [US3] Run `go test -count=1 ./internal/graph/... ./internal/api/...` until T027 to T031 pass along with every US1 and US2 test

**Checkpoint**: all three stories pass.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T035 In cmd/netmapper/project.go print `"%d interfaces, %d edges, %d disagreements, %d L2 domains, %d VLAN mismatches\n"`. In cmd/netmapper/project_test.go make `packRoot` also copy internal/graph/testdata/fakeos-l2 into its fakeos copy and give `projectLab`'s two switches VLAN output (VLAN 10 tagged on p1), then assert the printed line reports one or more L2 domains: this runs the domain inserts under `netmapper_operator`, the role contracts/cli.md gives the command (constitution workflow rule on roles). The engine role is covered by every `l2lab` crawl
- [X] T036 [P] In internal/fact/fact.go replace the comment above `vlans` ("The engine reads neither yet; the l2domain projection (#37) will.") with one saying the graph projector reads both to build L2 domains (feature 009)
- [X] T037 [P] Update docs/c4-model/04-data-model.md: notes for `l2domain` (confidence `complete` or `partial`, key is the lowest local VLAN, label only when members agree), `l2domain_member` (add `snapshot_id`, `vlans_observation_id`, `ports_observation_id` to the key columns; one row per declared VLAN), `l2domain_link` (add `state`, `joined` or `unchecked`); add `vlan_mismatch` to the `finding.category` list; add a paragraph after the graph projector one: what feature 009 built, the grants it gave the engine and the operator, none to the API, and that `vlan_mismatch` is replaced with the projection like `link_disagreement`. Regenerate docs/c4-model/diagrams/data-model.svg only if its source is in the repository; otherwise leave it and say so in the PR
- [X] T038 [P] In docs/how-to/deploy.md, upgrade section: after this version's `netmapper migrate`, snapshots projected earlier have no L2 domains until `netmapper project <snapshot-id>` is run on them; list them with `SELECT snapshot_id FROM projection WHERE projector_version < 2`. The engine does not redo them on its own
- [X] T039 Check constitution V: `grep -rniE 'arista|eos|cisco|junos|ethernet|port-channel' internal/graph/*.go` finds no vendor term outside comments citing feature numbers; then run `go vet ./...` and `go test -count=1 ./...`, all passing
- [X] T040 Check the migration's down step by hand (quickstart section 2): on a scratch schema with every migration applied, insert one `vlan_mismatch` finding, run goose down one step, confirm it succeeds and the finding is gone, run up again
- [X] T041 [VM] (Done on test/lab and arista-evpn-vxlan-clab; the native 20 mismatch check run locally after the merge, see research R10.) Quickstart section 4 on `test/lab`: crawl, list the domains, compare with research R10, check `GET /v1/findings` has no `vlan_mismatch`; optionally the native 20 mismatch check. Write any difference into specs/009-l2domain-projection/research.md R10 before treating it as a bug, and put the lab configuration back

---

## Dependencies & Execution Order

### Phase dependencies

- Phase 1: T001, T002, T003 have no dependency; T002 and T003 are parallel with T001.
- Phase 2: T004 to T006 need T001. T007 needs T004 to T006.
- US1 (Phase 3): needs Phase 2. Tests T008 to T017 need T002 and T003.
- US2 (Phase 4) and US3 (Phase 5): need T018 (union-find). They can then run side by side; T025 and
  T032 touch different steps of `buildDomains`, so merge carefully if done at the same time.
- Phase 6: T035 needs T020 and T032; T036 to T038 can start once US1 is done; T039 to T041 last.

### Within each story

Tests first, seen failing; then implementation; then the run task that closes the story.

### Parallel opportunities

- T002 and T003 beside T001.
- Inside US1: T009 to T017 are separate test functions in one file, written in any order once T008
  exists to settle the helpers; they are marked [P] for that reason, not because they touch
  different files.
- T031 (API) beside every graph task of US3.
- T036, T037, T038 beside each other.

## Parallel Example: User Story 1

```text
After T008 passes its compile step:
  T009 chain and port-channel tests
  T010 one-side and no-cable tests
  T011 tagged-against-untagged and unknown-endpoint tests
  T013 label tests
Then T018 to T020 in order, then T021.
```

## Implementation Strategy

### MVP first

1. Phase 1 and Phase 2 (T001 to T007).
2. US1 (T008 to T021). Stop and check: domains exist for a correctly configured network, with
   evidence, reproducible and rebuildable.

### Incremental delivery

3. US2 (T022 to T026): partial domains for unreadable devices.
4. US3 (T027 to T034): mismatches joined and reported.
5. Polish (T035 to T041), the VM check last since `NetLab` is often down.

## Phase 7: Convergence

- [X] T042 [VM] Deploy `test/lab` on NetLab (`sudo containerlab deploy -t test/lab/two-switch.clab.yaml`, sw4 taken off the network as 008's quickstart section 4 says), crawl it with this branch (quickstart section 4: collector on the VM, engine on the Mac), compare the domains with the test/lab table of research R10 (VLANs 1, 10, 20, 30, 32, 40-49, 99 shared by sw1 and sw2; 31 one domain per switch; 50 sw2 alone; all `complete`; no `vlan_mismatch`), and record the result in specs/009-l2domain-projection/research.md R10, replacing "the table above is still unconfirmed", per US1/AC7 (partial)
