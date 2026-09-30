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

## packs/_base/pack.yaml

The probe sent to a target before its platform is known. It is a pack like any other, so every
command the collector sends comes from pack data (principle IV). It names no vendor and has no
recipes.

```yaml
name: _base
version: 1
read_only: []                   # the base pack sends no CLI command
probe:
  snmp:
    - { name: sys_object_id, oid: 1.3.6.1.2.1.1.2.0 }
    - { name: sys_descr,     oid: 1.3.6.1.2.1.1.1.0 }
```

The registry matches the probe results against every pack's `fingerprint.snmp` rules. When SNMP is
silent, the SSH fingerprint commands of the loaded packs are tried in pack order.

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
        empty_lines: ['^Interface \S+ detected 0 LLDP neighbors:$']   # nothing to report
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

A step's `empty_lines` lists the lines its command prints when it has nothing to report. Output
whose every non-blank line matches one of them is `empty`; anything else that yields no row is
`parse_failed` (research R9). A step without `empty_lines` counts only blank output as empty.

A device's spelling of a value can be translated into the schema's with `values`, per field, with
`'*'` as the fallback. The same template field may feed several fact fields:

```yaml
    map:
      admin_state: STATUS
      oper_state: STATUS
    values:
      admin_state: { disabled: down, '*': up }
      oper_state: { connected: up, notconnect: down, '*': other }
```

## scrapli.yaml

The scrapligo platform definition is used as given, with two rules for a read-only collector:
no `on-open` or `on-close` commands (they would reach the device without going through a recipe or
the audit log), and privilege levels that never escalate. Paging is turned off in the recipe
command itself where the platform allows it (`| no-more` on EOS). While a target is not yet
identified, the fingerprint session uses scrapligo's generic driver. That session reads until the
device's first prompt before sending the fingerprint command, without writing anything: a command
sent while the CLI is still starting comes back as its own echo (issue #15).

## Load-time checks (collector refuses to start if any fails)

- Every `command` starts with an entry of `read_only`.
- Every referenced template exists and compiles.
- Every `map` target is a field of the family schema in [fact-families.md](fact-families.md).
- `name` is unique across loaded packs.
- Exactly one loaded pack declares `probe`, and it is named `_base`.
