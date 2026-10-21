# cisco_iosxe pack

Platform pack for Cisco IOS-XE on Catalyst 9300 and 9500, recorded and tested on a Cat9kv 17.15.01
(`vrnetlab/cisco_cat9kv`, [test/lab-iosxe](../../test/lab-iosxe)), which stands in for both. How a
pack is built is in [docs/how-to/write-a-pack.md](../../docs/how-to/write-a-pack.md); the fact
families it fills are in
[fact-families.md](../../specs/001-crawl-loop/contracts/fact-families.md).

It fills four families: `neighbours` (LLDP), `interfaces`, `vlans` and `interface_vlans`. Every
recipe fits IOS-XE 17.15 and later; a device on an older release gets `no_matching_version`.

## Recognition

- SNMP: one rule per sysObjectID, the Catalyst 9300, 9300L, 9300X, 9500 and 9500X models of
  CISCO-PRODUCTS-MIB and the Cat9kv's `1.3.6.1.4.1.9.1.3160`. Only the Cat9kv's was seen on a
  device. A model that is not listed matches over SSH instead (`show version`).
- Identifiers: the serial (`System Serial Number`, `entPhysicalSerialNum.1`) and the base MAC
  (`Base Ethernet MAC Address`), both strong; sysName, weak. The base MAC is also the LLDP chassis
  ID the switch advertises, which is how a neighbour's report resolves to it.
- Interface names: the long form `show interfaces` prints (`GigabitEthernet1/0/1`,
  `TwentyFiveGigE1/1/1`, `Port-channel1`); every abbreviation maps to it.

## Behaviour to know

- Paging: IOS-XE has no per-command pager switch, so the session asks for a pty 32767 rows tall,
  which IOS-XE takes as its terminal length (`scrapli.yaml`). An output longer than that would
  page; a 9300 stack of eight 48-port members prints about 12000 lines of `show interfaces`.
- VLANs 1002 to 1005, the legacy FDDI and Token Ring defaults every IOS-XE switch lists as
  `act/unsup`, are not `vlans` rows.
- `interface_vlans`: a port that is down keeps the administrative mode (`trunk`, `dynamic auto`),
  since its operational mode only says `down`; a port-channel member gives its channel only.
- `interfaces`: the speed is that of a port that is up; the management port `GigabitEthernet0/0`
  prints `Auto Speed` even when up and has none. A port-channel prints the speed of one member.

## Known gaps

- C9200: there is no virtual image, and its sysObjectIDs are not listed. A C9200 on IOS-XE 17.15
  or later still matches over SSH and is collected like a 9300, but no family is claimed as
  supported on it until fixtures from real hardware exist.
- IOS-XE runs CDP and not LLDP by default: a Catalyst without `lldp run` shows no neighbours
  (`neighbours` is `empty`). CDP is not collected.
- The management families (`snmp`, `aaa_servers`, `local_users`, `management_apis`,
  `aaa_methods`, `management_servers`) and the routing families (`bgp_peers`, `ospf_interfaces`,
  `ospf_neighbours`) have no recipe yet: they are `unsupported` with `no_recipe`.
- A command refused at the account's privilege level is `parse_failed`, not `denied`: IOS-XE
  answers it with `% Invalid input detected`, the same as a command that does not exist. Every
  command of this pack runs at privilege 1.
- Stacks: a stack is treated as a single device, with no stack-specific logic. After a
  switchover, its serial and base MAC may change, so the stack may then be seen as a new device.
- Catalyst 9300 and 9500 differences: none is visible on the Cat9kv, so any found on hardware is a
  gap until recorded.
