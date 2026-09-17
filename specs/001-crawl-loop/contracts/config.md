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

discovery:                  # optional, defaults shown
  max_task_attempts: 3
  step_timeout: 120s
  step_output_limit: 64MiB
```

## Validation

- `perimeters[].include` non-empty; every entry a valid CIDR.
- `credential_sets[].secret_ref` must start with `env:` or `vault:`. A literal-looking value (no
  scheme) is rejected with `secret_ref must be a reference, not a value`.
- `credential_sets[].perimeters` must name existing perimeters.
- `kind: ssh` requires `username`; `snmp_v3` requires `username` and a `secret_ref` whose secret holds
  `auth` and `priv` fields.
- Names are unique within their list.

## Reference formats

| Scheme   | Form                         | Resolves to                                       |
| -------- | ---------------------------- | ------------------------------------------------- |
| `env:`   | `env:NAME`                   | the environment variable of the collector process |
| `vault:` | `vault:<kv v2 path>#<field>` | field of the secret, read at collection time      |
