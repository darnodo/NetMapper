# Contract: `netmapper` subcommands, changes for this feature

Only the deltas against [001's cli.md](../../001-crawl-loop/contracts/cli.md),
[002's](../../002-coverage-gate/contracts/cli.md) and
[003's](../../003-identity-resolution/contracts/cli.md). Exit codes, logging and `NETMAPPER_DSN` are
unchanged: 0 success, 1 runtime error, 2 invalid input, JSON lines on stderr.

## netmapper engine [--interval 2s] [--packs packs]

`--packs` is new, and defaults to `packs`, the same directory and the same default
`netmapper collector` uses. The engine loads the packs at startup and refuses to start if they do not
load, exactly as the collector does. It needs them for one thing: the naming rules that canonicalise
the port a neighbour reports for the far end of a cable (FR-003, [research R3](../research.md)). This
is a change against 003, which recorded that the engine reads no pack.

The job runner gains a third step per tick, after judging and resolving: project every closed snapshot
that carries a `resolution` row and no current `projection` row, oldest first by closing time.

- Projection opens no device session and resolves no secret. It reads the packs as data.
- It reads only the active parse generation, and writes nothing outside its own tables and the
  `link_disagreement` findings it raises (FR-019, FR-020).
- A snapshot with no entity set is not projected and is not an error: the sweep does not select it
  (FR-001).
- A quarantined snapshot is projected like any other. Whether to trust the result stays the consumer's
  call, as it does for resolution.
- A projection that fails is logged and retried on the next tick; the snapshot stays unprojected rather
  than carrying a half-built set. Every snapshot in the sweep is attempted, and the errors are reported
  together, so one snapshot that always fails cannot block the ones behind it.
- A snapshot whose entity set was replaced since it was projected is projected again, because
  re-resolving it destroyed the old one. Redoing a projection whose inputs are unchanged needs
  `netmapper project` (FR-021, FR-022).

## netmapper project <snapshot-id> [--packs packs] (new)

Projects one snapshot again, replacing its interfaces and edges (FR-022). This is the only way a
snapshot whose inputs have not changed is ever projected twice.

- Prints one line on stdout:
  `<interfaces> interfaces, <edges> edges, <disagreements> disagreements`.
- Exit 2, one line on stderr, when:
  - `snapshot <id> not found`
  - `snapshot <id> is not closed`
  - `snapshot <id> has no entity set` (FR-001; resolve it first)
- Exit 0 and projects normally when the snapshot has no projection yet; the sweep would have got to it
  anyway.
- Projects nothing but the snapshot named on the command line. It does not cascade and it does not
  resolve: a stale entity set is `netmapper resolve`'s business.
- Runs the projector in the operator's own process, under `netmapper_operator`, which is why that role
  holds the same write rights on the projected tables as the engine.

## Reading the result

There is no interface yet, so FR-024 is SQL until the API feature ships, which is the same limit 003
recorded for entities. The queries are in [quickstart.md](../quickstart.md).

## netmapper run / cancel / judge / resolve / decide / collector / migrate

Unchanged. `netmapper migrate` applies `0007_graph.sql` like any other.
