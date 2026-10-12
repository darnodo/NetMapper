# Contract: layer 2 domains and the VLAN mismatch finding

Two consumers read what this feature produces. Neither gets a new endpoint.

## 1. The computed tables (database readers)

Readers: `netmapper_engine`, `netmapper_operator`, and Grafana through the constitution's named
read-only exception. Not `netmapper_api` (FR-018); serving domains is #7.

Columns and keys are in [data-model.md](../data-model.md). What a reader may rely on:

- A snapshot with a `projection` row has its domains, or has none because nothing declared a VLAN.
  `projection.l2domains` says which.
- Every declared VLAN of an entity whose `vlans` observation was collected is in exactly one
  `l2domain_member` row of that snapshot.
- `key_type` is `vlan` and `key_id` is a VLAN ID from 1 to 4094. A `vni` domain will use the same
  tables (#33); a reader filtering on `key_type = 'vlan'` keeps working then.
- `local_vlan_id` equals `key_id` unless the domain contains an untagged mismatch, in which case a
  `vlan_mismatch` finding of the same snapshot names the cable.
- `confidence = 'partial'` if and only if at least one `l2domain_link` row of the domain has
  `state = 'unchecked'`.
- An `unchecked` link never joins: removing every `unchecked` row leaves the components unchanged.
- Re-projecting the same snapshot from the same inputs gives the same rows, ids aside.
- `l2domain.id` is not stable across projections or snapshots. Join on it, never store it.

Example: the VLAN 10 domain of a two-switch trunk.

```sql
SELECT d.key_id, d.label, d.confidence, e.device_key, m.local_vlan_id
FROM l2domain d
JOIN l2domain_member m ON m.l2domain_id = d.id
JOIN entity e ON e.id = m.entity_id
WHERE d.snapshot_id = $1 AND d.key_type = 'vlan' AND d.key_id = 10
ORDER BY d.id, e.device_key;
```

## 2. The finding (read API, `GET /v1/findings`)

No change to the endpoint or its response shape (005 contract). One more category value:

```json
{
  "domain": "compliance",
  "category": "vlan_mismatch",
  "severity": "warning",
  "subject_ref": "if:chassis_mac:aa:bb:cc:00:00:01/pc10",
  "state": "open",
  "detail": {
    "ports": ["if:chassis_mac:aa:bb:cc:00:00:01/pc10", "if:chassis_mac:aa:bb:cc:00:00:02/pc10"],
    "vlans": [1, 99],
    "edges": ["l1_link:if:chassis_mac:aa:bb:cc:00:00:01/port2|if:chassis_mac:aa:bb:cc:00:00:02/port2",
              "l1_link:if:chassis_mac:aa:bb:cc:00:00:01/port3|if:chassis_mac:aa:bb:cc:00:00:02/port3"]
  },
  "confidence": "derived",
  "evidence": ["..."]
}
```

`confidence` is `derived`, from the API's category map: the finding compares two observations.
