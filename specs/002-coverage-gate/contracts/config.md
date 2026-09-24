# Contract: configuration document, changes for this feature

Only the deltas against [001's config.md](../../001-crawl-loop/contracts/config.md). The document is
still posted whole by `netmapper run` and stored verbatim as a `config_version`.

## `perimeters[].degraded_at`, `perimeters[].quarantined_below`

Two optional keys per perimeter, both fractions in `(0, 1]`.

```yaml
perimeters:
  - name: lab
    include: [172.20.20.0/24]
    exclude: [172.20.20.1/32]
    degraded_at: 0.9          # optional: coverage at or above this is at worst degraded
    quarantined_below: 0.9    # optional: coverage below this is quarantined
```

Meaning, given a coverage fraction `c` against the snapshot's baseline:

| Condition | Classification |
| --- | --- |
| `c == 1.0` | published |
| `c >= degraded_at` and `c < 1.0` | degraded |
| `c < quarantined_below` | quarantined |

Both keys absent means the documented defaults, `degraded_at: 0.9` and `quarantined_below: 0.9`:
published only when every device of the baseline is reached again, degraded from 90%, quarantined
below. A perimeter that declares one key and not the other takes the default for the other.

A snapshot with no baseline is published with no coverage figure, whatever the thresholds say
(FR-004).

### Validation (exit 2, one line per problem, same as 001)

- `perimeter "<name>": degraded_at must be greater than 0 and at most 1`
- `perimeter "<name>": quarantined_below must be greater than 0 and at most 1`
- `perimeter "<name>": quarantined_below must not be greater than degraded_at`

The thresholds are copied onto the `perimeter` row at `netmapper run` time, so a judgement is always
read against the thresholds of the config version its own snapshot ran under, and editing the
document later never changes an existing verdict.
