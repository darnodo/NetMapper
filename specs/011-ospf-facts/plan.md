# Implementation Plan: OSPF interfaces and adjacencies on Arista EOS

**Branch**: `011-ospf-facts` | **Date**: 2026-10-15 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/011-ospf-facts/spec.md`

## Summary

Two new platform-neutral fact families, `ospf_interfaces` and `ospf_neighbours`, in
`internal/fact`, filled on Arista EOS by two recipes of three steps each: a guard, a row step and a
joined instance step. `show ip ospf interface vrf
all` gives one record per OSPF interface: VRF, instance, area, network type, cost, state, DR and BDR
router IDs, passive flag and authentication type (research R1). `show ip ospf neighbor detail vrf
all` gives one record per adjacency: neighbour router ID and address, interface, area, state (R3).
Neither view prints the local router ID or the area type, so both recipes end with `show ip ospf
vrf all`, joined onto the rows on VRF, instance and area (R2). No timer, counter or age is mapped
(R8).

Vendor-neutral additions: the per-step `guard` key above; a per-step recipe key, `join_on`, for a step that fills fields on rows
already there and never creates one, with a mandatory `unmatched: fail | ignore` saying what happens
to a join row that lands nowhere, and a new `pack_schema: 2` in `pack.yaml` that enables these keys while
packs without the key load as version 1 (R2); and a schema attribute, `Missing`, that sets
`auth_type` to `unknown` when no step gives a value, with a load check that refuses a recipe default
on such a field (R6). The `unknown` rule is tested on a parsed row with no `auth_type`; no recorded
output is edited (FR-013a).

"No OSPF here" is decided by a guard step, `show ip ospf vrf all | json`, whose "none" answer is the
non-empty document `{"vrfs": {}}`; a blank output on it is `parse_failed`, so a broken read cannot
pass for "no OSPF" (R14, new per-step key `guard`). Once the guard shows OSPF running, a blank
interface or neighbour output means none (R7).
`test/lab` gets OSPF on new VLAN SVIs over the existing Port-Channel10 between sw1 and sw2: area 0
with md5, a point-to-point stub area, an sha adjacency in VRF `blue`, an MTU mismatch that holds an
adjacency below `full`, and passive loopbacks (R10). No API, engine or discovery change (R11).

Planning checks on the EVPN lab (cEOS 4.36.0F, 2026-10-15): core1 and core2 for R1, R3, R5, R6,
dc-leaf1 for R7, and an SNMP walk of OSPF-MIB on core1 (R6). What only `test/lab` can show is
marked "to confirm when recording" and is settled in implementation step 1.

**Spec amendments from research, approved 2026-10-15 and applied to the spec**:

- FR-004a: the DR and BDR are recorded as **router IDs** (`dr_router_id`, `bdr_router_id`) on every
  transport. A source that gives addresses (SNMP) resolves them through the neighbour table, or
  writes `unknown`, never the raw address (R5). This feature ships no SNMP implementation.
- FR-009: on EOS the "nothing here" answer is a blank output, and only from a step that completed
  normally; a blank after a timeout, a dropped session or a missing prompt keeps its failure
  status (R7).

**Decisions of 2026-10-15**: `pack_schema` is created (absent means 1, `covers` valid in both); FR-009
names "or no observation, the task failing" and the collector is not changed; SNMP is out of scope,
the router ID resolution rule lives in the contract only; the prompt race is closed for "no OSPF
here" by the guard step (R14), and documented for the remaining case (R7).

**Follow-up outside this feature**: a step deadline or a dropped session fails the task and leaves
no observation for the family. That is inconsistent with the observation status being
non-nullable (constitution, architectural constraints): the outcome of that family on that device
is recorded nowhere as a status. To be raised as its own issue.

## Technical Context

**Language/Version**: Go 1.27

**Primary Dependencies**: existing only: `gotextfsm` (templates), `go.yaml.in/yaml/v3` (packs). No
new dependency.

**Storage**: none new. Rows go to `observation.parsed`, outputs to the object store. No migration.

**Testing**: `go test ./...`; template fixtures in `packs/arista_eos/testdata/lab/` (`TestPacks`),
`TestFamiliesFromLab` over sw1, sw2, sw4, sw4noadj, core1, core2 and dc-leaf1 recordings; unit tests
for `Missing` (`internal/fact`, `internal/parse`), `join_on` with both `unmatched` modes
(`internal/parse`), `pack_schema` and the `join_on` load checks (`internal/pack`), error outputs
giving `parse_failed`, a collector test on the fake transport that a blank output returned with an
idle timeout, a deadline or a dropped session gives no `empty` observation, a graph test that OSPF
rows change neither the graph nor the verdict.

**Target Platform**: Linux container (one binary, three roles); collector against Arista EOS 4.36+,
fixtures from cEOS 4.36.0F.

**Project Type**: single Go service with data packs.

**Performance Goals**: no target. Six more `show` commands per EOS device: four distinct ones,
with the guard and `show ip ospf vrf all` run by each family. About 12 lines per interface, 12 per
neighbour, 40 per instance, plus 3 lines of guard output without OSPF and about 50 per instance
with it. Outputs change on every run (timers, ages); rows do not.

**Constraints**: tests pass with no lab up; `NetLab` VM needed once to record fixtures and once for
the live crawl.

**Scale/Scope**: one row per OSPF interface and one per adjacency, a handful per device in the labs.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Touched | How the plan complies |
| --------- | ------- | --------------------- |
| I. Evidence travels with the answer | yes | Served by the existing facts endpoint with observation, snapshot and collection time. No new answer. |
| II. Observations immutable, rest rebuildable | yes | Rows live in `observation.parsed`. `join_on` and `Missing` run in the parser, so a rule fix is a replay over stored outputs. |
| III. Credentials stay in the collector | yes | No new credential or grant. Recipes use commands that print no OSPF key (R9); the secret check covers the lab keys and the EOS hash forms. The SNMP walk during planning used the EVPN lab's existing read-only community from inside the lab; nothing ships with it. |
| IV. Read only, outward | yes | Only `show` commands. |
| V. Vendor specifics are data | yes | Commands, templates, state and type words, `No authentication` and the `{"vrfs": {}}` "none" answer are in `packs/arista_eos`. `guard`, `join_on`, `unmatched`, `pack_schema` and `Missing` carry no vendor term. |

Workflow rules: the README, `docs/how-to/write-a-pack.md` (`pack_schema`, `guard`, `join_on` and
`unmatched`, the `Missing` default rule, the two families in the missing-recipe table) and the Bruno requests are updated (FR-017); the fact
families contract of 001 is updated in the same change (FR-011); the `test/lab` topology comment
and this quickstart document the lab change (FR-014). Every template change is checked by replay
over recorded outputs (`TestPacks`). The `unknown` against `none` rule is tested with inputs on
each side: an output with `No authentication` (gives `none`), a parsed row with no authentication
value (gives `unknown`), an unrecognised authentication line (gives `other`). The `join_on` empty
rule is tested with a device whose only non-blank output is the joined step (`sw4noadj`
neighbours), and `unmatched` with a join row that lands nowhere in each mode (`fail` gives
`parse_failed`, `ignore` gives the same rows and returns the key).

Gate: pass, no violation. Re-checked after Phase 1: unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/011-ospf-facts/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── fact-families.md   # merged into specs/001-crawl-loop/contracts/fact-families.md
└── tasks.md               # /speckit-tasks
```

### Source Code (repository root)

```text
internal/fact/fact.go              # ospf_interfaces, ospf_neighbours, Field.Missing
internal/parse/parse.go            # join_on steps, unmatched keys returned, Missing in settle
internal/pack/registry.go          # Pack.PackSchema, Step.JoinOn and Step.Unmatched and their load
                                   # checks, the defaults-on-Missing check, missingCost entries
internal/collector/scrape.go       # log ignored join keys; test: blank output with a transport
                                   # error is never empty
internal/graph/                    # test: OSPF rows change no projection
internal/testutil/lab.go           # both families in NoRecipeFamilies
packs/arista_eos/pack.yaml         # pack_schema: 2, version bump
packs/arista_eos/recipes/          # ospf_interfaces.yaml, ospf_neighbours.yaml
packs/arista_eos/templates/        # show_ip_ospf_vrf_all_json (guard), show_ip_ospf_interface_vrf_all,
                                   # show_ip_ospf_neighbor_detail_vrf_all, show_ip_ospf_vrf_all (.textfsm)
packs/arista_eos/testdata/lab/     # sw1_*, sw2_*, sw4_*_empty, sw4noadj_*, core1_*, core2_*,
                                   # dc-leaf1_*_empty recordings, <switch>_<family>.facts.yml
packs/packs_test.go                # (md5|sha\d*) 7 pattern, TestOSPFErrorsAreNotEmpty
test/lab/sw1.cfg, sw2.cfg          # VLANs 41 to 44, SVIs, VRF blue, router ospf 1 and 2
test/lab/two-switch.clab.yaml      # topology comment
specs/001-crawl-loop/contracts/fact-families.md
bruno/facts/ospf-interfaces.bru, bruno/facts/ospf-neighbours.bru
README.md, docs/how-to/write-a-pack.md
```

**Structure Decision**: the existing layout; no new Go file, everything extends files in place.

## Implementation order

1. `test/lab` configuration (R10). On the `NetLab` VM: deploy, check each adjacency reaches its
   intended state and the VLAN 44 one stays below `full`, confirm the guard's `{"vrfs": {}}` on
   sw4, check whether the guard's JSON exposes an interface count per area or instance (R14: if
   so, a blank interface step with a count above zero becomes `parse_failed`, as an added task;
   if not, the R7 residual stays documented), record the four step commands on sw1,
   sw2, sw4 and sw4noadj, and on core1, core2 and dc-leaf1 of the EVPN lab. Settle the "to confirm"
   items of R2, R4, R5 and R6, including the SNMP check on the md5 and sha interfaces, and update
   `research.md`.
2. `internal/fact`: both schemas and `Missing`, with tests.
3. `internal/pack`: `pack_schema` (absent is 1, above 2 refused, `covers` valid in both), `guard`,
   `join_on` and `unmatched` refused below 2; a guard not first, with a `map` or without
   `empty_lines`, or without `none`; `join_on` with no row-creating step before it, or without `unmatched`; a default
   on a `Missing` field. `internal/parse`: `guard` (none document pretty-printed and on one line,
   blank, contradiction, error line), `join_on` with both `unmatched` modes, and `Missing`, with the FR-013a test on
   a parsed row with no `auth_type`. `internal/collector`: the ignored keys logged, and the
   blank-output-with-error tests on the fake transport.
4. Templates and recipes: the guard step, the interface step, the neighbour step, the shared
   instance step, then
   the `facts.yml` files, then the error outputs giving `parse_failed`.
5. Secret pattern, graph test, `missingCost`, `NoRecipeFamilies`, pack version, contract, README,
   write-a-pack guide, Bruno requests.
6. Quickstart sections 4 and 5 on the VM.

Steps 2 to 5 need no lab. Step 1 must come first: the templates are tested against its recordings.

## Complexity Tracking

No constitution violation to justify.
