# Contract: the `cisco_iosxe` pack

What the pack sends to a Catalyst and what it fills. The fields are those of
[fact-families.md](../../001-crawl-loop/contracts/fact-families.md), unchanged by this feature.
Wording marked "recorded" was seen on the Cat9kv 17.15.01 at privilege 1 (research); the rest is
settled when the lab is recorded (implementation step 1).

## pack.yaml

| Key | Value |
| --- | ----- |
| `name` | `cisco_iosxe` |
| `version` | `1` |
| `pack_schema` | `3`, or left out if no step uses a format 2 or 3 key |
| `read_only` | `["show "]` |
| `fingerprint.snmp` | one rule per sysObjectID: `1.3.6.1.4.1.9.1.3160` (Cat9kv, recorded) and the Catalyst 9300 and 9500 entries of CISCO-PRODUCTS-MIB; `version_regex: 'Version (?P<version>\d+\.\d+\.\d+\w*),'` on sysDescr |
| `fingerprint.ssh` | `show version`, `match: 'Cisco IOS XE Software'`, same `version_regex` |
| `identifiers` | `serial` strong (`System Serial Number\s+:\s+(?P<value>\S+)`, SNMP `1.3.6.1.2.1.47.1.1.1.1.11.1`); `chassis_mac` strong, `normalise: mac` (`Base Ethernet MAC Address\s+:\s+(?P<value>\S+)`); `hostname` weak (SNMP `1.3.6.1.2.1.1.5.0`) |
| `interface_names` | research R8, longest abbreviation first, each `^<abbr>(\d.*)$` |

## scrapli.yaml

- `platform-type: 'cisco_iosxe'`, `driver-type: 'network'`.
- One privilege level `exec`, pattern matching a `>` or `#` prompt, no `escalate` or
  `deescalate`, `default-desired-privilege-level: 'exec'`.
- `failed-when-contains`: `% Ambiguous command`, `% Incomplete command`, `% Invalid input
  detected`, `% Unknown command`.
- `network-on-open: []`, `network-on-close: []`.
- `options`: `transport-pty-height: 32767` (research R3).

## Recipes

Every implementation: `versions: '>=17.15'`, `transport: ssh`.

### neighbours (`lldp-cli`)

| Step | Command | Template | Empty when |
| ---- | ------- | -------- | ---------- |
| 1 | `show lldp neighbors detail` | `show_lldp_neighbors_detail.textfsm` | `^Total entries displayed: 0$` (recorded); the LLDP-off wording once recorded |

| Field | From |
| ----- | ---- |
| `protocol` | `=lldp` |
| `local_interface` | `Local Intf:` (short form, canonicalised) |
| `remote_chassis_id` | `Chassis id:` |
| `remote_system_name` | `System Name:` |
| `remote_interface` | `Port id:` |
| `remote_mgmt_address`, `remote_mgmt_address_type` | first `IP:` line (`ipv4`), else first `IPV6:` line (`ipv6`) |

### interfaces (`interfaces-cli`)

| Step | Command | Template |
| ---- | ------- | -------- |
| 1 | `show interfaces` | `show_interfaces.textfsm` |

| Field | From |
| ----- | ---- |
| `name` | `<name> is <state>, line protocol is <state>` (long form) |
| `description` | `Description:` |
| `admin_state` | `administratively down` gives `down`, else `up` |
| `oper_state` | line protocol `up` gives `up`, `down` gives `down`, else `other` |
| `speed_bps` | `<n>Mb/s` or `<n>Gb/s` on the duplex line, read as `<n>M` or `<n>G`; absent when the port is down |
| `mtu` | `MTU <n> bytes` |
| `mac` | `address is <mac>` |

### vlans (`vlans-cli`)

| Step | Command | Template |
| ---- | ------- | -------- |
| 1 | `show vlan brief` | `show_vlan_brief.textfsm` |

| Field | From |
| ----- | ---- |
| `vlan_id` | VLAN column; 1002 to 1005 give no row |
| `name` | Name column |
| `status` | `active`, `suspended`, `act/lshut` and `sus/lshut` give `shutdown`, else `other` |

Never `empty`: VLAN 1 always exists.

### interface_vlans (`interface-vlans-cli`)

| Step | Command | Template | Empty when |
| ---- | ------- | -------- | ---------- |
| 1 | `show interfaces switchport` | `show_interfaces_switchport.textfsm` | a device with no switched port (wording when recorded) |
| 2 | `show interfaces trunk` | `show_interfaces_trunk.textfsm` | blank output: no active trunk (recorded) |

`merge_on: [interface]`.

| Field | From |
| ----- | ---- |
| `interface` | `Name:` (step 1), Port column (step 2) |
| `mode` | `Operational Mode`: `trunk` gives `trunk`, `static access` gives `access`, other words as printed; absent on a member row |
| `access_vlan` | `Access Mode VLAN` on access ports only |
| `channel` | `(member of bundle <Po>)` on member rows only |
| `native_vlan` | step 2, first table |
| `allowed_vlans` | step 2, "Vlans allowed on trunk"; `none` gives no VLAN |
| `active_vlans` | step 2, "Vlans allowed and active in management domain"; `none` gives no VLAN |

A port with `Switchport: Disabled` gives no row.

## Families with no recipe

`snmp`, `aaa_servers`, `local_users`, `management_apis`, `aaa_methods`, `management_servers`,
`bgp_peers`, `ospf_interfaces`, `ospf_neighbours`: `unsupported`, detail `no_recipe`, and one load
warning each.
