# Quickstart: validating snapshot eviction and pinning

Run guide for proving the feature end to end. The commands and outputs are in
[contracts/cli.md](contracts/cli.md), the API changes in [contracts/rest.md](contracts/rest.md), the
schema in [data-model.md](data-model.md), the decisions in [research.md](research.md).

Section 1 needs no lab. Section 2 needs two ordinary crawls; nothing is changed on any device. The
second crawl uses a seed nothing answers on, so no device is even contacted for it.

## 1. Automated checks (no lab)

The dev stack (`deploy/compose.yaml`) for `NETMAPPER_TEST_DSN` and `NETMAPPER_TEST_S3_ENDPOINT`.
It shares host ports with the `netmapper-trial` stack: check `docker ps` first.

```sh
go test -count=1 ./...
```

Expected: everything passes, nothing skipped, and in particular:

| Test | Proves |
| ---- | ------ |
| `cmd/netmapper` `TestEvictProjectedSnapshot` | on a resolved, projected snapshot with findings and L2 domains, under `netmapper_operator`: preview exits 3 and changes nothing; `--yes` exits 0; every per-snapshot table has 0 rows for it; the tombstone and its verdict remain (SC-001) |
| `cmd/netmapper` `TestEvictSharedOutputs` | two snapshots sharing outputs: after evicting one, every output of the other is readable, outputs only the evicted one used are gone from the store and from `raw_object`, older orphans are untouched (SC-002) |
| `cmd/netmapper` `TestEvictRefusals` | open, pinned, unknown id, crawl running in another perimeter, unresolved sibling: exit 2, database and store unchanged, one `refused:` audit row each (SC-005) |
| `cmd/netmapper` `TestEvictResume` | object store unreachable after the database part: exit 1, state `evicting`, `run` refused; rerun with the store back: exit 0, state `evicted`, `eviction_object` empty (SC-004) |
| `cmd/netmapper` `TestEvictTwice` | second `--yes` exits 0, `noop` audit row, nothing changed |
| `cmd/netmapper` `TestPinUnpin` | pin, pin again, unpin, unpin again: idempotent, one audit row each; pin on `evicted` exits 2 |
| `cmd/netmapper` `TestEvictRegistry` | entries only the evicted snapshot's entities carried are gone with their identifiers; entries a remaining snapshot carries keep key, times and `minted_from`; an entry left over from an earlier evicted snapshot goes too (SC-007) |
| `cmd/netmapper` `TestEvictBaseline` | a later snapshot's verdict keeps `baseline_snapshot_id` and `coverage`; re-judging it picks the closest earlier non-evicted snapshot |
| `internal/jobrunner` `TestStartRefusedWhileEvicting` | `Start` refuses while a snapshot is `evicting` |
| `internal/entity`, `internal/graph` | a resolution or projection that read `closed` before an eviction committed stops after its lock, writes nothing |
| `internal/gate` | `judge_snapshot` raises on a snapshot or baseline that is not `closed` |
| `internal/store` `roles_test.go` | operator: `EXECUTE` on the three functions, `INSERT` on `audit_log`, `SELECT, DELETE` on `eviction_object`, still no `DELETE` on any collected table and no `UPDATE` on `snapshot`; api: no new grant |
| `internal/api` `TestEvictedSnapshot` | `?snapshot=<evicted>` on devices, device, interface, facts, findings, observation, raw: 410 `snapshot_evicted`; `/v1/snapshots` and `/v1/snapshots/{id}` show the tombstone with `pinned`, `evicted_at`, `baseline_state`; default snapshot skips it (SC-006) |

## 2. Live check (two crawls, no device change)

Any running stack: the dev stack with a collector that reaches a lab, or the `netmapper-trial`
stack against the EVPN lab on `NetLab`. Do not use the trial stack without asking its owner.

1. Crawl the lab as usual. Note the snapshot id, say `A`. Wait for `has_graph: true`.
2. Pin it: `netmapper snapshot pin --id A`. Expect `pinned`, then `/v1/snapshots` shows
   `pinned: true`.
3. Start a second crawl with a seed set holding one address inside the perimeter that nothing
   answers on (a documentation address added to the perimeter, say `192.0.2.1/32`). It closes with
   `unreachable` observations. Note its id, `B`, and wait until it is judged and resolved.
4. `netmapper snapshot evict --id A` exits 2: pinned.
5. `netmapper snapshot evict --id B` exits 3 and prints the preview: few observations, 0 or a
   handful of outputs to delete, 0 devices.
6. Start a third crawl and, while it runs, `netmapper snapshot evict --id B --yes`: exits 2,
   `crawl … is running`. Let it finish and wait until it is resolved (snapshot `C`).
7. `netmapper snapshot evict --id B --yes` exits 0. Check:
   - `/v1/snapshots` lists `B` as `evicted` with `evicted_at`;
   - `/v1/devices?snapshot=B` answers 410 `snapshot_evicted`;
   - `/v1/devices` without a snapshot answers from `C`;
   - every raw output link of a device of `C` still answers 200;
   - `SELECT * FROM audit_log WHERE action LIKE 'snapshot.%'` shows the pin, the refusals and the
     eviction with the OS user as actor.
8. Run the same command again: exits 0, `nothing to do`.
9. Clean up: `netmapper snapshot unpin --id A`, and evict `C` if it is not wanted.

Nothing in this section is written to a device: crawls are read only, and step 3 contacts none.
