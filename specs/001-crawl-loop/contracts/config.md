# Contract: configuration document (subset used by this feature)

One YAML document. Unknown top-level keys are kept in `config_version.document` and ignored here, so
later features can extend it.

```yaml
perimeters:
  - name: lab
    include: [172.20.20.0/24]
    exclude: [172.20.20.1/32]

credential_sets:            # tried in this order (FR-018)
  - name: ro-snmp
    kind: snmp_v2c          # ssh | snmp_v2c | snmp_v3
    secret_ref: env:LAB_SNMP_COMMUNITY
    max_attempts_per_device: 1
    perimeters: [lab]
  - name: ro-ssh
    kind: ssh
    username: netmapper
    secret_ref: vault:kv/data/netmapper/lab#password
    max_attempts_per_device: 2
    perimeters: [lab]

seed_sets:
  - name: lab-seeds
    targets: [172.20.20.2]

discovery:                  # optional; values shown are the defaults
  max_task_attempts: 3
  step_idle_timeout: 60s    # no byte received for this long: the family is `unreachable`, detail `timeout`
  step_deadline: 30m        # total per step: the task fails with last_error `deadline:` (FR-014)
  lease: 5m
  claim_batch: 16
  poll_interval: 1s
```

## Validation

- `perimeters[].include` non-empty; every entry a valid CIDR.
- `credential_sets[].secret_ref` must start with `env:` or `vault:`. A literal-looking value (no
  scheme) is rejected with `secret_ref must be a reference, not a value`.
- `credential_sets[].perimeters` must name existing perimeters.
- `kind: ssh` requires `username`; `snmp_v3` requires `username` and a `secret_ref` whose secret holds
  `auth` and `priv` fields.
- `snmp_v3` only: `auth_protocol` and `priv_protocol`, see below. Either key on another kind is
  refused, and so is a `vault:` reference with `#field` on a `snmp_v3` set.
- Names are unique within their list.
- `credential_sets[].max_attempts_per_device` is required and at least 1.
- `discovery` and each of its keys are optional. Defaults are starting points, not measured values
  (research R16); override them once the tool has run on your network.

## SNMPv3 protocols

Added by [006](../../006-snmpv3-protocols/contracts/config.md). Optional keys of a `snmp_v3` set:

```yaml
  - name: ro-snmp-v3
    kind: snmp_v3
    username: netmapper
    auth_protocol: sha256        # default sha
    priv_protocol: aes256        # default aes; none = authNoPriv
    secret_ref: env:LAB_SNMP_V3  # or vault:kv/data/netmapper/lab, without #field
    max_attempts_per_device: 1
    perimeters: [lab]
```

| Key             | Accepted values (exact, lowercase)                              | Default |
| --------------- | --------------------------------------------------------------- | ------- |
| `auth_protocol` | `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512`            | `sha` (SHA-1) |
| `priv_protocol` | `none`, `des`, `aes`, `aes192`, `aes256`, `aes192c`, `aes256c`  | `aes` (AES-128) |

| Condition                                              | Error                                                                                            |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `auth_protocol` not in the list                        | `credential set "<name>": auth_protocol must be one of md5, sha, sha224, sha256, sha384, sha512` |
| `priv_protocol` not in the list                        | `credential set "<name>": priv_protocol must be one of none, des, aes, aes192, aes256, aes192c, aes256c` |
| either key on a set whose kind is not `snmp_v3`        | `credential set "<name>": auth_protocol applies to snmp_v3 only` (same for `priv_protocol`)      |
| `snmp_v3` set with a `vault:` reference containing `#` | `credential set "<name>": a snmp_v3 secret_ref must reference the whole secret, without #field`  |

The secret behind a `snmp_v3` set:

| Reference         | The secret must be                                                               |
| ----------------- | -------------------------------------------------------------------------------- |
| `env:NAME`        | a JSON object in the variable: `{"auth": "...", "priv": "..."}`                  |
| `vault:<kv path>` | a KV v2 secret with string fields `auth` and `priv`, referenced without `#field` |

With `priv_protocol: none` only `auth` is needed. A missing or empty field the set needs is reported
at collection time as `credential_unresolved: <set> (missing <field>)`, and nothing is sent with
that set.

## Reference formats

| Scheme   | Form                         | Resolves to                                       |
| -------- | ---------------------------- | ------------------------------------------------- |
| `env:`   | `env:NAME`                   | the environment variable of the collector process |
| `vault:` | `vault:<kv v2 path>#<field>` | field of the secret, read at collection time      |
