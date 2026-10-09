# Contract addition: management fact families

To be merged into `specs/001-crawl-loop/contracts/fact-families.md` in the implementation (FR-010),
after `interfaces`. Field tables are in [../data-model.md](../data-model.md); this file states the
rules a consumer relies on.

## snmp, aaa_servers, local_users, management_apis, aaa_methods, management_servers

Written for every identified device, like `interfaces`. They describe the device, not its links:
the engine does not read them, and they do not count toward the snapshot verdict (FR-015).

Rules common to the six:

- **No secret, ever.** No field holds a community string, a server or NTP key in any encoding, a
  password or a hash. The stored raw output of the commands behind them holds none either: a pack
  uses only commands that print none (FR-009). A community is recorded by its access level and ACL.
- **A port is what the device printed.** `port` is absent when the output shows none. A pack never
  fills in a protocol's default.
- **A VRF is always set where the schema has one.** A server with no VRF stated is `vrf: default`.
  `snmp` has no VRF: where a platform binds SNMP to VRFs per agent, they are on the
  `management_apis` row `api: snmp`.
- **Addresses are stored as given** and never resolved, like a neighbour's system name.
- **Nothing configured is `empty`.** An output the template does not understand is `parse_failed`.
  A consumer may read `empty` as "the device was asked and has none"; it may not read a missing
  observation that way.
- `yes`/`no` fields are strings, by the same convention as `admin_state`.

Status mapping: `collected`; `empty`; `parse_failed`; `unsupported` with `no_recipe` for a pack with
no recipe; `unreachable` and `denied` as for every scraped family.
