# Contract: platform pack format

A pack is a directory under `packs/`. Loading it requires no code change (SC-006). The registry is
the only code that reads it.

```text
packs/arista_eos/
├── pack.yaml
├── scrapli.yaml            # scrapligo platform definition: prompts, paging, privilege levels
├── recipes/
│   ├── neighbours.yaml
│   └── interfaces.yaml
└── templates/
    ├── show_lldp_neighbors_detail.textfsm
    └── show_interfaces_status.textfsm
```

## pack.yaml

```yaml
name: arista_eos
version: 1                      # bump on any template or recipe change
read_only: ["show "]            # every CLI command in the pack must start with one of these
fingerprint:
  snmp:
    - sys_object_id_prefix: 1.3.6.1.4.1.30065
      version_regex: 'EOS version (?P<version>\S+)'   # applied to sysDescr
  ssh:
    - command: show version
      match: 'Arista'
      version_regex: 'Software image version: (?P<version>\S+)'
identifiers:                    # extracted during find, written as identifier claims
  - kind: serial
    strength: strong
    ssh: { command: show version, regex: 'Serial number:\s+(?P<value>\S+)' }
    snmp: { oid: 1.3.6.1.2.1.47.1.1.1.1.11.1 }
  - kind: chassis_mac
    strength: strong
    normalise: mac
    ssh: { command: show version, regex: 'System MAC address:\s+(?P<value>\S+)' }
  - kind: hostname
    strength: weak
    snmp: { oid: 1.3.6.1.2.1.1.5.0 }
interface_names:                # spelling -> canonical, applied by the parser
  - { match: '^Et(\d.*)$', replace: 'Ethernet$1' }
```

## recipes/<family>.yaml

```yaml
family: neighbours
implementations:                # first one whose version and transport fit wins
  - id: lldp-cli
    versions: '>=4.20'
    transport: ssh
    steps:
      - command: show lldp neighbors detail
        template: show_lldp_neighbors_detail.textfsm
    map:                        # template field -> fact family field
      local_interface: LOCAL_INTERFACE
      remote_chassis_id: CHASSIS_ID
      remote_system_name: NEIGHBOR_NAME
      remote_interface: NEIGHBOR_INTERFACE
      remote_mgmt_address: MGMT_ADDRESS
      protocol: '=lldp'           # literal
```

SNMP steps use `walk: <oid>` and map column OIDs instead of template fields. A recipe with several
steps merges rows on the keys listed in `merge_on`.

## Load-time checks (collector refuses to start if any fails)

- Every `command` starts with an entry of `read_only`.
- Every referenced template exists and compiles.
- Every `map` target is a field of the family schema in [fact-families.md](fact-families.md).
- `name` is unique across loaded packs.
