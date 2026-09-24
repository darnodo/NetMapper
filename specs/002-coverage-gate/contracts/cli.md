# Contract: `netmapper` subcommands, changes for this feature

Only the deltas against [001's cli.md](../../001-crawl-loop/contracts/cli.md). Exit codes, logging and
`NETMAPPER_DSN` are unchanged: 0 success, 1 runtime error, 2 invalid input, JSON lines on stderr.

## netmapper engine [--interval 2s]

Unchanged flags. The job runner gains one step per tick: judge every closed snapshot that carries no
active judgement, oldest first by closing time.

- Judging opens no device session, resolves no secret and reads no pack, exactly like the rest of the
  engine.
- A snapshot whose immediate predecessor is closed but not yet judged is skipped this tick and picked
  up on a later one.
- A judgement that fails is logged and retried on the next tick; the snapshot stays unjudged rather
  than getting a wrong verdict.

## netmapper judge <snapshot-id> (new)

Re-judges one snapshot that already carries a judgement, writing a new active verdict and leaving the
superseded one readable (FR-009, FR-013). This is the only way a snapshot is ever judged twice.

- Prints the new judgement's classification and coverage on stdout, one line:
  `<classification> <carried_over>/<baseline_devices> (baseline snapshot <id>)`, or
  `<classification> no baseline` when there is none.
- Exit 2, one line on stderr, when:
  - `snapshot <id> not found`
  - `snapshot <id> is not closed` (FR-001: an open snapshot is never judged)
- Exit 0 and judges normally when the snapshot has no judgement yet; the sweep would have got to it
  anyway, and refusing would make the command useless for the case an operator most wants it in.
- Never re-judges anything but the snapshot named on the command line. Judging a snapshot does not
  cascade to its successors, even though their baselines may now read differently (FR-013).

## netmapper run / cancel / collector / migrate

Unchanged, except that `run` now also stores the two optional perimeter threshold keys from
[config.md](config.md) on the `perimeter` row it writes.
