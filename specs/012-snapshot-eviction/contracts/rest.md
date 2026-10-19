# REST contract changes: evicted snapshots

Merged into `specs/005-read-api/contracts/rest.md` in the same change. No new endpoint, no write.

## Snapshot envelope

Every `snapshot` object (envelope, list entries, `GET /v1/snapshots/{id}`) gains three fields,
`opened_at` (when the crawl started, kept on a tombstone, US3/AC1), `pinned` and `evicted_at`:

```json
{"id": 7, "opened_at": "2026-10-19T09:12:02Z", "closed_at": "2026-10-19T09:12:44Z", "state": "evicted", "verdict": "quarantined",
 "coverage": 0.12, "has_graph": false, "pinned": false, "evicted_at": "2026-10-19T10:03:10Z"}
```

`state` is one of `open`, `closed`, `evicting`, `evicted`. `evicted_at` is null unless the state is
`evicting` or `evicted`.

## New error

| Status | Body                                            | When                                                          |
| ------ | ----------------------------------------------- | ------------------------------------------------------------- |
| 410    | `{"error": "snapshot_evicted", "snapshot": {…}}` | `?snapshot=` names a snapshot that is `evicting` or `evicted` |

Applies to every endpoint that takes `?snapshot=`: devices, device, interface, facts, findings,
observation and observation raw. Takes precedence over `not_projected`.

## Unchanged on purpose

- `GET /v1/snapshots` lists tombstones with the rest.
- `GET /v1/snapshots/{id}` answers 200 with the tombstone and its verdict.
- Without `?snapshot=`, the default is the most recent `closed` snapshot with a graph; evicted ones
  are never chosen. With none left: 409 `not_projected` with `snapshot: null`, as on a fresh install.
- `GET /v1/observations/{id}` without `?snapshot=` for an observation of an evicted snapshot: 404
  `no_such_observation`.

## Judgement view

`GET /v1/snapshots/{id}` judgement gains `baseline_state`: the baseline snapshot's `state`, or null
when there is no baseline. `baseline_snapshot_id` and `coverage` are unchanged after the baseline is
evicted.
