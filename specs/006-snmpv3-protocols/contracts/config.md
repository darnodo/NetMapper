# Contract: configuration document, SNMPv3 credential sets

Delta on [001's config contract](../../001-crawl-loop/contracts/config.md), which stays the reference
and is updated in the same change (constitution, Development Workflow). Nothing else in the document
changes.

## New keys

```yaml
credential_sets:
  - name: ro-snmp-v3
    kind: snmp_v3
    username: netmapper
    auth_protocol: sha256        # optional, default sha
    priv_protocol: aes256        # optional, default aes; none = authNoPriv
    secret_ref: env:LAB_SNMP_V3  # or vault:kv/data/netmapper/lab, without #field
    max_attempts_per_device: 1
    perimeters: [lab]
```

| Key             | Accepted values                                                 | Default | Meaning                           |
| --------------- | --------------------------------------------------------------- | ------- | --------------------------------- |
| `auth_protocol` | `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512`            | `sha`   | authentication protocol (`sha` is SHA-1) |
| `priv_protocol` | `none`, `des`, `aes`, `aes192`, `aes256`, `aes192c`, `aes256c`  | `aes`   | privacy protocol (`aes` is AES-128); `none` selects authNoPriv |

`aes192c` and `aes256c` are the key-extension variants some Cisco platforms use (Reeder draft);
`aes192` and `aes256` follow the Blumenthal draft, which most other platforms use. `md5` and `des`
are accepted for legacy devices.

## Validation (added)

Values are matched exactly, lowercase.

| Condition                                                     | Error                                                                                         |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `auth_protocol` not in the list                               | `credential set "<name>": auth_protocol must be one of md5, sha, sha224, sha256, sha384, sha512` |
| `priv_protocol` not in the list                               | `credential set "<name>": priv_protocol must be one of none, des, aes, aes192, aes256, aes192c, aes256c` |
| either key on a set whose kind is not `snmp_v3`               | `credential set "<name>": auth_protocol applies to snmp_v3 only` (same for `priv_protocol`)   |
| `snmp_v3` set with a `vault:` reference containing `#`        | `credential set "<name>": a snmp_v3 secret_ref must reference the whole secret, without #field` |

As today, every problem in the document is reported, joined, and nothing runs.

## The secret behind a `snmp_v3` set

| Reference         | The secret must be                                                                 |
| ----------------- | ---------------------------------------------------------------------------------- |
| `env:NAME`        | a JSON object in the variable: `{"auth": "...", "priv": "..."}`                    |
| `vault:<kv path>` | a KV v2 secret with string fields `auth` and `priv`, referenced without `#field`   |

With `priv_protocol: none`, only `auth` is needed. A missing or empty required field is reported at
collection time as `credential_unresolved`, naming the set and the field, and nothing is sent to the
device with that set.

## Compatibility

A document without the new keys loads as before and behaves as `auth_protocol: sha`,
`priv_protocol: aes`, which is what the collector did before this feature.
