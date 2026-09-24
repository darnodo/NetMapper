# Contract: `netmapper` subcommands, changes for this feature

Only the deltas against [001's cli.md](../../001-crawl-loop/contracts/cli.md) and
[002's cli.md](../../002-coverage-gate/contracts/cli.md). Exit codes, logging and `NETMAPPER_DSN` are
unchanged: 0 success, 1 runtime error, 2 invalid input, JSON lines on stderr.

## netmapper engine [--interval 2s]

Unchanged flags. The job runner gains a second step per tick, after judging: resolve every closed
snapshot that carries no `resolution` row, oldest first by closing time.

- Resolution opens no device session, resolves no secret and reads no pack.
- It does not wait on a verdict and does not produce one: a quarantined snapshot is resolved like any
  other (FR-025).
- A resolution that fails is logged and retried on the next tick; the snapshot stays unresolved rather
  than carrying a half-built entity set.
- Oldest first matters for the registry: keys are minted in the order snapshots closed, so a rebuild
  replays to the same keys (R5).

## netmapper resolve <snapshot-id> (new)

Resolves one snapshot again, replacing its entity set (FR-017). This is the only way a snapshot is ever
resolved twice.

- Prints one line on stdout: `<entities> entities, <weak> weakly identified, <conflicts> conflicts`.
- Exit 2, one line on stderr, when:
  - `snapshot <id> not found`
  - `snapshot <id> is not closed` (FR-001)
- Exit 0 and resolves normally when the snapshot has no entity set yet; the sweep would have got to it
  anyway.
- Resolves nothing but the snapshot named on the command line. It does not cascade to later snapshots,
  even when a decision recorded since would change theirs too: re-resolving those is another explicit
  call (FR-017).

## netmapper decide <kind> [flags] (new)

Records an operator decision (FR-009). Writes one `entity_decision` row and nothing else: no entity
changes until the affected snapshots are resolved again.

```sh
netmapper decide merge       --perimeter lab --keys serial:FGE1234,serial:FGE9999 [--note "warranty swap"]
netmapper decide never-merge --perimeter lab --keys serial:FGE1234,serial:FGE9999 [--note "cloned image"]
netmapper decide split       --perimeter lab --key serial:FGE1234 --identifier chassis_mac=00:1c:73:aa:bb:cc [--note "..."]
```

- `--perimeter` is the perimeter name, which is what a device key is scoped to.
- `merge` and `never-merge` take exactly two keys; `split` takes one key and one `kind=value`
  identifier.
- `--actor` defaults to the OS user, recorded verbatim.
- Prints `decision <id> recorded` on stdout, then a reminder naming the snapshots that carry the
  subjects and are therefore now stale: `resolve 12, 13 to apply`.
- Exit 2 when a subject key is unknown in that perimeter, when the two keys of a merge are the same, or
  when the identifier of a split is not attributed to the named device. Recording a decision about
  something that does not exist is a typo, not an intention.
- Never modifies or deletes an earlier decision, including one it contradicts. The later row governs at
  resolution time (FR-011) and both stay readable.

## netmapper run / cancel / judge / collector / migrate

Unchanged.
