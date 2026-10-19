# Contract: `netmapper` subcommands used by this feature

All subcommands read `NETMAPPER_DSN` (PostgreSQL). Exit code 0 on success, 1 on runtime error,
2 on invalid input or refusal, 3 for a preview that changed nothing (`snapshot evict` without
`--yes`, feature 012). Logs are JSON lines on stderr (`log/slog`).

## netmapper migrate

Applies embedded migrations. Idempotent.

## netmapper run --config <file.yaml> --perimeter <name> --seed-set <name>

Validates the document (see [config.md](config.md)), stores it as a `config_version`, and starts a
discovery.

- Prints the job id on stdout, nothing else.
- Exit 2 and one line on stderr per problem when FR-001 fails:
  - `perimeter "<name>" not found` / `perimeter "<name>" has no include range`
  - `seed "<target>" is outside perimeter "<name>"` / `seed "<target>" does not resolve`
  - `no credential set covers perimeter "<name>"`
- Opens no device session and resolves no secret.

## netmapper cancel <job-id>

Sets the job to `cancelling`. Exit 2 if the job is not `running`.

## netmapper collector [--id <name>] [--workers 64] [--consume find,scrape] [--packs <dir>]

Claims and runs tasks until SIGTERM. On SIGTERM it stops claiming, lets in-flight steps finish for up
to the lease duration, and exits. Tasks it did not finish return to the frontier when their lease
expires.

Also reads: `NETMAPPER_S3_ENDPOINT`, `NETMAPPER_S3_BUCKET`, `NETMAPPER_S3_ACCESS_KEY`,
`NETMAPPER_S3_SECRET_KEY`, optionally `NETMAPPER_S3_REGION` (default `garage`) and
`NETMAPPER_S3_INSECURE` (any value: plain HTTP), and `VAULT_ADDR` / `VAULT_TOKEN` when a `vault:`
reference is used. Exits 2 at start if an `NETMAPPER_S3_*` variable is missing.
Exits 2 at start if any pack fails to load or fails the read-only lint.

## netmapper engine [--interval 2s]

Runs the job runner: final retry pass, `failed` target sweep, snapshot close, cancellation. Never
reads S3 credentials, Vault variables or packs.

## netmapper snapshot evict|pin|unpin (feature 012)

Feature 012. Runs as
`netmapper_operator` (`NETMAPPER_DSN`), like `token` and `decide`.

```text
netmapper snapshot evict --id N [--yes]
netmapper snapshot pin   --id N
netmapper snapshot unpin --id N
```

### Exit codes

| Code | Meaning                                                                             |
| ---- | ----------------------------------------------------------------------------------- |
| 0    | done, or nothing to do (already evicted, already pinned, not pinned)                |
| 1    | runtime error, including an eviction left `evicting` (object store unreachable)     |
| 2    | invalid input (missing or bad `--id`, unknown snapshot) or refusal                  |
| 3    | `evict` without `--yes`: preview printed, nothing changed                           |

### evict

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

The last one applies to a resolved snapshot only. An unresolved snapshot is evicted whatever the
other snapshots of its perimeter are, and its eviction leaves the device registry as it is.

The collected tables busy for 30 s (API requests that keep them in use), exit 1, nothing changed:

```text
snapshot 7: the collected tables stayed busy for 30s, nothing was changed; retry
```

Object store failure after the database part, on stderr, exit 1:

```text
snapshot 7 eviction incomplete: 12 stored outputs left; run the same command again
```

### pin, unpin

Print `pinned`, `unpinned`, or `nothing to do`, exit 0. On an `evicting` or `evicted` snapshot:
`snapshot 7 is evicted: nothing to pin`, exit 2.

### Audit

Every `evict --yes`, `pin` and `unpin` on an existing snapshot, accepted or refused, writes one
`audit_log` row (data-model.md). Invalid input writes none: a missing or bad `--id`, an unknown
snapshot, missing `NETMAPPER_S3_*` variables. A preview writes none.

### Interaction with `netmapper run`

`run` refuses to start while a snapshot is `evicting`:

```text
snapshot 7 is being evicted: finish it with netmapper snapshot evict --id 7 --yes
```

exit 2.
