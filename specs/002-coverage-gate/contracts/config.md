# Contract: configuration document, changes for this feature

Only the deltas against [001's config.md](../../001-crawl-loop/contracts/config.md). The document is
still posted whole by `netmapper run` and stored verbatim as a `config_version`.

## `perimeters[].degraded_at`

One optional key per perimeter, a fraction in `(0, 1]`.

```yaml
perimeters:
  - name: lab
    include: [172.20.20.0/24]
    exclude: [172.20.20.1/32]
    degraded_at: 0.9          # optional: coverage at or above this is degraded, below it quarantined
```

Meaning, given a coverage fraction `c` against the snapshot's baseline:

| Condition | Classification |
| --- | --- |
| `c == 1.0` | published |
| `degraded_at <= c < 1.0` | degraded |
| `c < degraded_at` | quarantined |

Absent means the documented default of `0.9`: published only when every device of the baseline is
reached again, degraded from 90%, quarantined below.

Published is fixed at full coverage by FR-005, so `degraded_at` is the only boundary a perimeter has
to place. An earlier draft of this contract had a second key, `quarantined_below`; it could not
describe a band the first key did not already decide, and declaring one of the two without the other
silently crossed them. It is gone.

A snapshot with no baseline is published with no coverage figure, whatever the threshold says
(FR-004).

### Validation (exit 2, one line per problem, same as 001)

- `perimeter "<name>": degraded_at must be greater than 0 and at most 1`

The threshold is copied onto the `perimeter` row at `netmapper run` time, so a judgement is always
read against the threshold of the config version its own snapshot ran under, and editing the
document later never changes an existing verdict.
