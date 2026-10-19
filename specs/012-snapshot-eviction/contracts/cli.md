# CLI contract: netmapper snapshot

Merged into `specs/001-crawl-loop/contracts/cli.md` in the same change. Runs as
`netmapper_operator` (`NETMAPPER_DSN`), like `token` and `decide`.

```text
netmapper snapshot evict --id N [--yes]
netmapper snapshot pin   --id N
netmapper snapshot unpin --id N
```

## Exit codes

| Code | Meaning                                                                             |
| ---- | ----------------------------------------------------------------------------------- |
| 0    | done, or nothing to do (already evicted, already pinned, not pinned)                |
| 1    | runtime error, including an eviction left `evicting` (object store unreachable)     |
| 2    | invalid input (missing or bad `--id`, unknown snapshot) or refusal                  |
| 3    | `evict` without `--yes`: preview printed, nothing changed                           |

## evict

Without `--yes`, prints the preview and exits 3. Needs only `NETMAPPER_DSN`. A snapshot that would
be refused prints the refusal and exits 2; an evicted one prints `nothing to do` and exits 0. An
`evicting` one prints the block with `result: resume`, the counts of what is left, and one
`pending: <hex hash>` line per stored output still to delete, and exits 3.

With `--yes`, also needs `NETMAPPER_S3_ENDPOINT`, `NETMAPPER_S3_BUCKET`, `NETMAPPER_S3_ACCESS_KEY`,
`NETMAPPER_S3_SECRET_KEY` (and the optional `NETMAPPER_S3_REGION`, `NETMAPPER_S3_INSECURE`), set to
the operator's own key, which can read and delete (deploy guide). Missing variables exit 2 before
anything changes.

Output on stdout, one `key: value` per line, same keys for preview and eviction:

```text
snapshot: 7
closed_at: 2026-10-19T09:12:44Z
verdict: quarantined
pinned: false
observations: 412
objects_deleted: 37
bytes_deleted: 1048576
objects_kept: 288
devices_deleted: 3
result: preview            # or: evicted, resumed, nothing to do
```

Refusals, on stderr, exit 2:

```text
snapshot 7 is open: cancel or wait for its crawl first
snapshot 7 is pinned: unpin it first
crawl 12 (snapshot 9) is running: retry when it ends
snapshot 8 of perimeter campus is not resolved yet: retry when it is
```

Object store failure after the database part, on stderr, exit 1:

```text
snapshot 7 eviction incomplete: 12 stored outputs left; run the same command again
```

## pin, unpin

Print `pinned`, `unpinned`, or `nothing to do`, exit 0. On an `evicting` or `evicted` snapshot:
`snapshot 7 is evicted: nothing to pin`, exit 2.

## Audit

Every `evict --yes`, `pin` and `unpin` on an existing snapshot, accepted or refused, writes one
`audit_log` row (data-model.md). Invalid input writes none: a missing or bad `--id`, an unknown
snapshot, missing `NETMAPPER_S3_*` variables. A preview writes none.

## Interaction with `netmapper run`

`run` refuses to start while a snapshot is `evicting`:

```text
snapshot 7 is being evicted: finish it with netmapper snapshot evict --id 7 --yes
```

exit 2.
