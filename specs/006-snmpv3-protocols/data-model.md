# Data model: Configurable SNMPv3 authentication and privacy

Phase 1 of [plan.md](plan.md). Only what changes is described.

## Credential set (configuration document)

Two optional keys on a credential set. Full rules in [contracts/config.md](contracts/config.md).

| Key             | Kinds      | Values                                                         | Default |
| --------------- | ---------- | -------------------------------------------------------------- | ------- |
| `auth_protocol` | `snmp_v3`  | `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512`            | `sha`   |
| `priv_protocol` | `snmp_v3`  | `none`, `des`, `aes`, `aes192`, `aes256`, `aes192c`, `aes256c`  | `aes`   |

`priv_protocol: none` selects the authNoPriv security level; any other value selects authPriv.

## `credential_set` table (migration 0009)

| Column          | Type   | Rule                                                                  |
| --------------- | ------ | --------------------------------------------------------------------- |
| `auth_protocol` | `text` | NULL unless `kind = 'snmp_v3'`; for `snmp_v3`, one of the values above |
| `priv_protocol` | `text` | NULL unless `kind = 'snmp_v3'`; for `snmp_v3`, one of the values above |

- Written by `jobrunner` with defaults applied, so a stored `snmp_v3` row always names both.
- Up: add the columns, backfill existing `snmp_v3` rows with `sha` and `aes`, then add the check
  constraints. Down: drop the columns.
- Grants unchanged: table-level grants already cover the new columns for `netmapper_operator`,
  `netmapper_collector` and `netmapper_engine`; `netmapper_api` has none on this table.

## SNMPv3 secret (resolved at collection time)

Unchanged shape, now checked before use (research R4).

| Security level              | Required non-empty fields | Other fields |
| --------------------------- | ------------------------- | ------------ |
| authPriv (`priv` not none)  | `auth`, `priv`            | ignored      |
| authNoPriv (`priv` = none)  | `auth`                    | ignored, `priv` included |

A set whose secret misses a required field is treated as unresolved: no attempt counted, no request
sent, the task ends `credential_unresolved` (or `credential_partial` when another set was rejected),
with the message naming the set and the missing field.

## Unchanged

- Observation statuses. A refused v3 credential is `denied`, with the device's reason as evidence.
- The `transport.Credential` passed to a transport gains the two protocols; nothing else about it.
