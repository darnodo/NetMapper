# Data model: Project layer 2 domains from VLAN data

Computed zone, written by the projector only, replaced with the snapshot's interfaces and edges.
Migration `0011_l2domain.sql`. Research references are to [research.md](research.md).

## Tables

### `l2domain`

One broadcast domain of one snapshot.

| Column | Type | Rule |
| ------ | ---- | ---- |
| `id` | bigserial | primary key |
| `snapshot_id` | bigint | references `snapshot`, not null |
| `key_type` | text | `vlan` or `vni`; only `vlan` is written by this feature |
| `key_id` | integer | the lowest `local_vlan_id` among the members (FR-006) |
| `label` | text, null | the VLAN name every named member agrees on, otherwise null (FR-008, R6) |
| `confidence` | text | `complete` or `partial` (FR-007, R5) |
| `pinned_name` | text, null | reserved by the data model for an operator decision; never written here |

Check: `confidence = 'partial'` exactly when the domain has an `unchecked` link. Enforced by the
projector and tested, not by a constraint (a check across tables would need a trigger for a rule the
code already computes in one place).

### `l2domain_member`

One device entity in one domain, on one local VLAN.

| Column | Type | Rule |
| ------ | ---- | ---- |
| `l2domain_id` | bigint | references `l2domain` on delete cascade |
| `snapshot_id` | bigint | not null |
| `entity_id` | bigint | references `entity` on delete cascade |
| `local_vlan_id` | integer | 1 to 4094 |
| `vlans_observation_id` | bigint | the `vlans` observation that declared the VLAN; FK `(snapshot_id, observation)` |
| `ports_observation_id` | bigint, null | the `interface_vlans` observation of the entity, null when its ports are not known |

Primary key `(l2domain_id, entity_id, local_vlan_id)`. Unique `(snapshot_id, entity_id,
local_vlan_id)`: one declared VLAN is in exactly one domain (FR-004). One entity may appear twice in
one domain with two local VLAN IDs, after an untagged mismatch (spec edge case).

### `l2domain_link`

One cable recorded against one domain.

| Column | Type | Rule |
| ------ | ---- | ---- |
| `l2domain_id` | bigint | references `l2domain` on delete cascade |
| `edge_id` | bigint | references `edge` on delete cascade; always an `l1_link` |
| `state` | text | `joined` (the cable joins two members) or `unchecked` (it might carry the VLAN, but one end's VLAN data is missing) |

Primary key `(l2domain_id, edge_id)`. A cable can be a link of many domains, one per VLAN it
carries.

### Changes to existing tables

- `projection`: new column `l2domains integer NOT NULL DEFAULT 0`, the number of domains in the set.
  `projector_version` written as 2.
- `finding.category` check: adds `vlan_mismatch`.

## Evidence chain (FR-009)

- Membership: `l2domain_member.vlans_observation_id` and `ports_observation_id`, each to
  `observation` (collection time) and `observation_raw` (the bytes).
- Link: `l2domain_link.edge_id` to `edge_evidence` (the neighbour reports behind the cable), plus the
  `ports_observation_id` of the members at its two ends (what each port carries).
- Mismatch finding: `finding_evidence` holds both ends' `interface_vlans` observations and the
  `neighbours` observations of each cable.

## Derived values in memory (not stored)

| Name | Per | Meaning |
| ---- | --- | ------- |
| declared | entity | has a collected `vlans` observation (R2) |
| ports known | entity | has a `collected` or `empty` `interface_vlans` observation (R2) |
| untagged | port | access VLAN, or native VLAN when in the active list (R3) |
| tagged | port | declared VLANs in the active list, native excluded (R3) |
| node | entity, local VLAN | union-find element, one per declared VLAN (R4) |

## The `vlan_mismatch` finding

| Field | Value |
| ----- | ----- |
| `domain` | `compliance` |
| `category` | `vlan_mismatch` |
| `severity` | `warning` |
| `subject_ref` | the lower of the two port references, byte order |
| `detail.ports` | both port references, channels when bundled |
| `detail.vlans` | the untagged VLAN at each port, same order as `ports` |
| `detail.edges` | names of the cables between them, sorted |

One finding per pair of facing ports, a port-channel pair counting once (R7).

## Roles

| Role | `l2domain`, `l2domain_member`, `l2domain_link` | `l2domain_id_seq` |
| ---- | ---------------------------------------------- | ----------------- |
| `netmapper_engine` | SELECT, INSERT, UPDATE, DELETE | USAGE |
| `netmapper_operator` | SELECT, INSERT, UPDATE, DELETE | USAGE |
| `netmapper_api` | none (FR-018) | none |
| `netmapper_collector` | none | none |

`finding` rights are unchanged: both writing roles already hold SELECT, INSERT, DELETE on it from 003.

## What this feature does not touch

Observations, raw output, claims, entities, decisions, judgements: read only. No new fact family, no
pack change outside test data, no API endpoint.
