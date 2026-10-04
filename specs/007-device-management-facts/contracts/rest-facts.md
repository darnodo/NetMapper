# Contract addition: facts of one device

To be merged into `specs/005-read-api/contracts/rest.md` under "Endpoints", after
`GET /v1/devices/{name}`. Authentication, the common envelope and the error table of that contract
apply unchanged; this endpoint requires the `read` scope (clarification Q1).

### `GET /v1/devices/{name}/facts/{family}`

The observations of one fact family for one device, with their rows and evidence. `{name}` resolves
like `GET /v1/devices/{name}`. `{family}` is any family of the fact schema, not only the management
ones: `interfaces` or `neighbours` work too. `?snapshot=<id>` as for the device endpoint.

```json
{
  "snapshot": {"id": 12, "closed_at": "…", "state": "closed", "verdict": "accepted", "coverage": 1},
  "device_key": "chassis_mac:00:1c:73:aa:bb:02",
  "family": "aaa_servers",
  "observations": [
    {
      "status": "collected",
      "detail": null,
      "rows": [
        {"protocol": "tacacs", "address": "192.0.2.10", "port": 49, "vrf": "default", "group": "NM-TACACS"}
      ],
      "evidence": {
        "observation_id": 418, "snapshot_id": 12, "collected_at": "2026-10-04T10:44:58Z",
        "family": "aaa_servers", "target": "172.20.20.3"
      }
    }
  ]
}
```

- `observations` holds every observation of the family whose target is an address the device
  answered on, in the snapshot's active parse generation, ordered by collection time then id. For a
  scraped family it has one entry; `identity` has one per address the device answered on.
- `rows` is `[]` for any status but `collected`. `detail` is `null` when the status has none.
- Each entry carries its evidence; `/v1/observations/{observation_id}?snapshot={snapshot_id}` and
  its `raw/{step}` follow from it.

| Situation | Status | Body |
| --------- | ------ | ---- |
| Device resolved, family has observations | 200 | as above |
| `{family}` is not in the fact schema | 404 | `{"error": "no_such_family", "snapshot": {…}}` |
| Device resolved, no observation of the family | 404 | `{"error": "not_collected", "snapshot": {…}}` |
| Nothing matches `{name}` | 404 | `{"error": "no_such_device", "snapshot": {…}}` |
| `{name}` matches several devices | 409 | `{"error": "ambiguous", …}` as for the device endpoint |
| Snapshot carries no graph | 409 | `{"error": "not_projected", "snapshot": {…}}` |

`no_such_family` is checked before the device is resolved: a typo in the family name is not
reported as a missing device. `not_collected` is distinct from an `empty` or `unsupported`
observation, which is a 200 with that status (clarification Q2).
