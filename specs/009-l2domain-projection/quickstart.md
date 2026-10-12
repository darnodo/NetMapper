# Quickstart: Project layer 2 domains from VLAN data

How to check the feature works. Shapes are in [data-model.md](data-model.md) and
[contracts/l2domain.md](contracts/l2domain.md).

## 1. Offline: the gate

No lab needed. PostgreSQL and the S3 stand-in come from the usual test environment.

```sh
go test ./internal/graph/... ./internal/api/... ./internal/collector/...
```

Expected: the domain tests in `internal/graph` cover every case of spec FR-016 and pass. The
collector's `TestMissingFamilies` still finds `vlans` and `interface_vlans` as
`unsupported/no_recipe` on the plain fakeos pack (the VLAN recipes live only in the graph test
data, research R9).

Then the whole suite:

```sh
go test ./...
```

## 2. Migration

Tests apply every migration on a fresh schema, so section 1 already covers `0011_l2domain.sql` going
up. There is no automated rollback test in the repository. Check the down step once by hand on a
scratch database that holds a `vlan_mismatch` finding: it must delete those findings before
narrowing the category check, or the rollback fails.

## 3. Re-project an existing snapshot

Snapshots projected before this feature have `projector_version = 1` and no domains. Find and
re-project them:

```sh
psql -c "SELECT snapshot_id FROM projection WHERE projector_version < 2"
netmapper project <snapshot-id>
```

Expected output line: interfaces, edges, disagreements, then domains and VLAN mismatches.

## 4. Live check on test/lab (NetLab VM)

The lab runs on the `NetLab` host and is often down; this step is a check, not the gate.

1. Deploy `test/lab` as left by feature 008 and crawl it.
2. Once the engine has projected the snapshot, list the domains:

   ```sql
   SELECT d.key_id, d.confidence, string_agg(e.device_key || ':' || m.local_vlan_id, ' ' ORDER BY e.device_key)
   FROM l2domain d JOIN l2domain_member m ON m.l2domain_id = d.id JOIN entity e ON e.id = m.entity_id
   WHERE d.snapshot_id = (SELECT max(snapshot_id) FROM projection)
   GROUP BY d.id, d.key_id, d.confidence ORDER BY d.key_id, d.id;
   ```

3. Compare with the table in research R10: VLANs 1, 10, 20, 30, 32, 40 to 49 and 99 shared by sw1 and
   sw2, VLAN 31 one domain per switch, VLAN 50 on sw2 alone, every domain `complete`, and no
   `vlan_mismatch` in `GET /v1/findings`.

4. Optional mismatch check: change the native VLAN of sw2 `Ethernet1` to 20 (allowed on that trunk,
   so it is in the active list), crawl again, and expect one `vlan_mismatch` finding for the sw1/sw2
   Ethernet1 pair with VLANs 99 and 20. Since Port-Channel10 still carries 20 and 99 tagged, sw1:20,
   sw1:99, sw2:20 and sw2:99 end up in one domain keyed 20, each switch a member twice. Native 1
   would not do: VLAN 1 is not allowed on Ethernet1, so it would carry nothing untagged. Put the
   configuration back afterwards.

Record any difference from R10 in `research.md` before treating it as a bug.
