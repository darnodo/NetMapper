# Research: Cisco IOS-XE platform pack

Sources: a throwaway probe topology deployed on NetLab on 2026-10-21 (one Cat9kv
`vrnetlab/cisco_cat9kv:17.15.01` and one cEOS 4.36.0F, one link, `~/probe013`, destroyed after
use), queried with the `netmapper` account at privilege 1 over SSH, and over SNMP v2c from inside
the lab network. The parser, pack registry, collector and SSH transport code. What the probe could
not show (a port-channel, an access port towards an Arista, a down trunk, LLDP turned off) is
marked "to confirm when recording" and is settled in implementation step 1, on the lab of R11.

## R1. Fingerprint: SNMP by sysObjectID, SSH by `show version`

- **Recorded**: sysObjectID `1.3.6.1.4.1.9.1.3160` on the Cat9kv. sysDescr: `Cisco IOS Software
  [IOSXE], Catalyst L3 Switch Software (CAT9K_IOSXE), Version 17.15.1, RELEASE SOFTWARE (fc4)`
  followed by the support and copyright lines. `show version` starts with `Cisco IOS XE Software,
  Version 17.15.01`, and its second line is the sysDescr line, `Version 17.15.1,` included.
- **Finding**: an SNMP rule matches on the sysObjectID prefix alone (`Registry.Fingerprint`). The
  Cisco enterprise prefix `1.3.6.1.4.1.9.1` also covers classic IOS, IOS-XR routers, ASAs and
  wireless controllers, so it cannot be the rule. When SNMP answers and no pack matches, `find`
  goes on to SSH (`internal/collector/find.go`), so an unlisted model is still identified.
- **Decision**: one SNMP rule per sysObjectID for the Catalyst 9300 and 9500 models taken from
  CISCO-PRODUCTS-MIB, plus `1.3.6.1.4.1.9.1.3160` for the Cat9kv. The pack README says which ones
  were seen on a device (only 3160 today). SSH rule: `show version` matching `Cisco IOS XE
  Software`. Both use the same `version_regex`, `Version (?P<version>\d+\.\d+\.\d+\w*),`, which
  hits the sysDescr line in both outputs, so SNMP and SSH give the same version, `17.15.1` (spec
  US1 scenario 2). The bracketed word is the release train on real hardware (`[Amsterdam]`,
  `[Dublin]`), so it is not part of either pattern.
- **Version ranges**: `versionParts` reads `17.15.1` and `17.15.01` as `[17 15 1]`. Recipes use
  `>=17.15`, the only release recorded (same convention as `>=4.36` on EOS, feature 008 R9).
- **Overlap**: the Arista SSH rule matches `Arista`, which the IOS-XE `show version` does not
  print, and the reverse is true too. Both packs run `show version`, which
  `FingerprintCommands` sends once.
- **Alternatives considered**: a `sys_descr` match key on SNMP rules (a registry change and a new
  `pack_schema`, for a gain the SSH fallback already gives); the bare prefix `1.3.6.1.4.1.9.1`
  (claims every Cisco box).

## R2. Identifiers: serial, base MAC, hostname

- **Recorded** in `show version`: `System Serial Number : 9CU1IEK3GDG` (also `Processor board ID
  9CU1IEK3GDG`) and `Base Ethernet MAC Address : 0c:00:c8:9a:54:00`. SNMP `entPhysicalSerialNum.1`
  (`1.3.6.1.2.1.47.1.1.1.1.11.1`) is `9CU1IEK3GDG`, the same index as on EOS. sysName is
  `cat1.example.com`.
- **Recorded on the Arista side**: the Cat9kv's LLDP chassis ID is `0c00.c89a.5400`, the base MAC
  in dotted form. `NormaliseMAC` turns both into `0c:00:c8:9a:54:00`, so the projector resolves
  the far end of the Arista's LLDP row by the strong identifier (`resolveFar`), not by address.
- **Decision**: `serial` (strong, SSH `System Serial Number`, SNMP `.11.1`), `chassis_mac`
  (strong, `normalise: mac`, SSH `Base Ethernet MAC Address`), `hostname` (weak, SNMP sysName),
  the same kinds as `arista_eos`. Stacks get no extra rule (spec clarification 2026-10-21).

## R3. SSH session: privilege 1, no on-open command, paging off by pty height

- **Finding**: IOS-XE has no per-command pager switch like EOS's `| no-more`, and the scrapligo
  `cisco_iosxe` asset sends `enable`, `terminal width 512` and `terminal length 0` on open, which
  the pack rules forbid (`write-a-pack.md` section 5).
- **Recorded**: IOS-XE takes its terminal length from the SSH pty request. `show terminal` gives
  `Length: 255 lines` with a pty of 255 rows, 512, 1000 and 32767 with those, and keeps its
  default of 24 when the pty asks for 0. With 24 rows, `show interfaces` (289 lines here) stops at
  `--More--`. The collector's sessions ask for scrapligo's default height, 255, which is too small.
- **Recorded**: scrapligo applies a platform file's `options` before the caller's
  (`platform/definition.go`, `setDriver`), and the collector only sets the width. A
  `transport-pty-height` option in the pack's `scrapli.yaml` therefore holds.
- **Decision**: `packs/cisco_iosxe/scrapli.yaml` copies the EOS file's structure: one `exec`
  privilege level matching `>` and `#` prompts (a privilege 1 account sees `cat1>`; an account
  deployed at 15 must not hang the session), no escalation, empty `network-on-open` and
  `network-on-close`, and `options: [{option: transport-pty-height, value: 32767}]`. Paging stops
  only after 32767 lines; a 9300 stack of eight 48-port members prints about 12000 lines of `show
  interfaces`. Written in the pack README as a limit.
- **To confirm when recording**: the exact place of `options` in the platform file, and that the
  collector's session reads `show interfaces` whole.
- **Alternatives considered**: `terminal length 0` as a recipe step (not a `show ` command, and a
  session setting the audit log would have to explain); setting the height for every platform in
  `internal/transport/ssh` (a transport change FR-009 rules out, for one platform's need).

## R4. A refused command is `parse_failed`, not `denied`

- **Recorded**: at privilege 1, `show running-config` prints `% Invalid input detected at '^'
  marker.` in an interactive session, the same answer as a command that does not exist. Over an
  SSH exec channel it prints `Line has invalid autocommand "show running-config"`.
- **Finding**: the collector gives `denied` only when the transport fails to open a session
  (`transport.AuthError`, `internal/collector/scrape.go`). A command's output is never read as a
  refusal; an error line yields no template row and the family is `parse_failed`. It is the
  current EOS behaviour too (`sw2_show_bgp_neighbors_vrf_all_denied_empty.raw`, feature 010).
- **Decision**: no collector change. The spec's edge case and its clarification are amended:
  a command refused at privilege 1 makes its family `parse_failed`, with the device's answer in
  the raw output, and the refusal is a known gap. Every command of R5 to R10 was run at
  privilege 1 on 17.15.01 and answered.
- **Spec amendment, approved 2026-10-21** (edge case "Privilege level", clarification 1, FR-014):
  "`denied`" becomes "`parse_failed`", and the pack README lists it as a known gap.

## R5. `neighbours`: `show lldp neighbors detail`

- **Recorded**: one block per neighbour: `Local Intf: Gi1/0/1`, `Chassis id: 001c.7337.d886`,
  `Port id: Ethernet1`, `System Name: eos1`, `Management Addresses:` then `IP: 172.20.21.3`.
  Ends with `Total entries displayed: N`. With no neighbour: blank lines and `Total entries
  displayed: 0`.
- **Decision**: one step, template based on ntc-templates `cisco_ios_show_lldp_neighbors_detail`
  (Apache 2.0, NOTICE as for EOS), with an address subtype value: `IP:` gives `ipv4`, `IPV6:`
  gives `ipv6`. The first IPv4 address is kept, else the first IPv6. `empty_lines`:
  `'^Total entries displayed: 0$'`. The local interface is the short form and goes through the
  naming rules (R8); the remote port is stored as the neighbour prints it, canonicalised by the
  projector with the neighbour's pack.
- **To confirm when recording**: the output with LLDP turned off (`no lldp run`). The spec makes it
  `empty`; it goes into `empty_lines` once recorded.

## R6. The lab's management address in LLDP

- **Recorded**: with vrnetlab's default management mode, the Cat9kv's `GigabitEthernet0/0` holds
  `10.0.0.15/24` (description `Containerlab management interface`), and that is the address the
  Arista receives in LLDP, with `2001:db8::2`. The collector reaches the node at 172.20.21.2,
  which vrnetlab forwards. A crawl seeded on the Arista would get a `find` for 10.0.0.15, out of
  the perimeter, and never reach the Catalyst.
- **Decision**: the Catalyst node is deployed with `CLAB_MGMT_PASSTHROUGH: "true"`, so the VM's
  `GigabitEthernet0/0` holds the container's management address and advertises it.
- **Recorded** (second probe, same topology with `CLAB_MGMT_PASSTHROUGH: "true"`):
  `GigabitEthernet0/0` holds `172.20.21.2/24`, the Arista's LLDP view of the Catalyst gives
  `Management Address : 172.20.21.2` (IPv4 only), and SSH as `netmapper` and SNMP still answer at
  that address. Confirmed.
- **How the startup-config is loaded** (read in the image's `/launch.py`, then checked on a third
  probe in passthrough mode, 2026-10-21): vrnetlab writes a bootstrap configuration (hostname,
  `admin` privilege 15, `line vty 0 4` with `login local` and `transport input all`, `ip route
  vrf Mgmt-vrf` to the gateway, `GigabitEthernet0/0` with its description and address, RSA key,
  NETCONF and RESTCONF), appends the lab's startup-config to it, and IOS-XE applies the whole file
  once, at first boot (`CVAC-4-CONFIG_DONE`, then `Startup complete`). Passthrough changes only
  the `GigabitEthernet0/0` address. On the third probe, after `Startup complete`: `show privilege`
  as `netmapper` gives level 1; the running-config holds `username netmapper secret 9 ...`,
  `lldp run` and `snmp-server community nm-probe-ro RO`; SNMP answers that community and not
  `public`, so the community comes from the startup-config and not from a vrnetlab default;
  `GigabitEthernet0/0` is in `Mgmt-vrf` with 172.20.21.2; the Arista's LLDP view gives
  172.20.21.2. The boot log's `%PARSER-4-BADCFG: Unexpected end of configuration file` and
  `invalid magic number` lines appear on every boot and change nothing.
- **Rules for `cat1.cfg`**, written in its header comment: it must not configure
  `GigabitEthernet0/0`, `Mgmt-vrf`, the vty lines or the `admin` account, which the bootstrap
  owns. A change to it takes effect only on a new deploy (12 minutes or more):
  `containerlab deploy --reconfigure`.
- **Alternatives considered**: seeding the Catalyst as well (proves the crawl from the Catalyst to
  the second Arista, not from the first Arista to the Catalyst); `lldp management-address` (no
  such per-interface choice on IOS-XE 17.15).

## R7. `interfaces`: `show interfaces`, one step

- **Recorded**: `GigabitEthernet1/0/1 is up, line protocol is up (connected)`, `Hardware is
  Gigabit Ethernet, address is 0c00.c89a.5401 (bia ...)`, `MTU 1500 bytes, BW 1000000 Kbit/sec`,
  `Full-duplex, 1000Mb/s, link type is auto`; a down port prints `Auto-duplex, Auto-speed`. Names
  are in long form. `Vlan1 is administratively down`; `GigabitEthernet0/0` is the management
  port.
- **Decision**: one step, template based on ntc-templates `cisco_ios_show_interfaces`. Admin
  state: `administratively down` gives `down`, anything else `up`. Oper state from the line
  protocol: `up`, `down`, anything else (`down (err-disabled)` and such) `other` or `down` as
  recorded. Speed: a template value `(\d+(?:\.\d+)?[MG])` followed by the literal `b/s`, so
  `1000Mb/s` gives `1000M` and `10Gb/s` gives `10G`, which `SpeedBPS` reads, and a down port gives
  nothing (feature #57 rule). No `show interfaces status` step: its speed column prints a bare
  `a-1000`, which is not a speed token, and it gives nothing `show interfaces` does not.
- **Lab artefact**: vrnetlab adds dummy NICs, and Gi1/0/2 and Gi1/0/4 to Gi1/0/8 report
  `connected` with nothing behind them. The rows are what the device prints; the lab README says
  so.

## R8. Interface names

- **Decision**: the canonical name is the long form `show interfaces` prints. Rules, longest
  prefix first: `Twe` TwentyFiveGigE, `Tw` TwoGigabitEthernet, `Te` TenGigabitEthernet, `Fi`
  FiveGigabitEthernet, `Fo` FortyGigabitEthernet, `Hu` HundredGigE, `Gi` GigabitEthernet, `Fa`
  FastEthernet, `Ap` AppGigabitEthernet, `Po` Port-channel, `Vl` Vlan, `Lo` Loopback, `Tu`
  Tunnel, each anchored `^<abbr>(\d.*)$`, so a long name never matches. `Port-channel` keeps
  IOS-XE's lowercase `c`; canonical is per platform.
- **Why this is enough**: the Catalyst's own rows use these rules in the parser, and an Arista's
  LLDP row naming `Gi1/0/1` is canonicalised by the projector with the Catalyst's pack
  (`readReports`, `internal/graph/links.go`).
- **Test**: every abbreviation and its long form give the same canonical name, and `Tw1/0/1`
  and `Twe1/0/1` give different ones.

## R9. `vlans`: `show vlan brief`

- **Recorded** (`show vlan`): `1 default active`, `10 users active`, then `1002 fddi-default`,
  `1003 token-ring-default`, `1004 fddinet-default`, `1005 trnet-default`, all `act/unsup`, then a
  second table of VLAN types and empty remote SPAN and private VLAN tables.
- **Decision**: `show vlan brief`, which prints the first table only. The template skips lines for
  VLANs 1002 to 1005 (spec clarification 2026-10-21). Status: `active` gives `active`, `suspended`
  gives `suspended`, `act/lshut` and `sus/lshut` give `shutdown`, anything else `other`. VLAN 1
  always exists, so the family is never `empty` on IOS-XE.
- **To confirm when recording**: `show vlan brief` at privilege 1, and the `lshut` wordings.

## R10. `interface_vlans`: `show interfaces switchport` and `show interfaces trunk`

- **Recorded**: `show interfaces switchport` gives one block per switched port: `Name: Gi1/0/1`,
  `Switchport: Enabled`, `Administrative Mode: trunk`, `Operational Mode: trunk`, `Access Mode
  VLAN: 1 (default)`, `Trunking Native Mode VLAN: 99 (Inactive)`, `Trunking VLANs Enabled:
  1,10,99`. An access port prints `Administrative Mode: dynamic auto` and `Operational Mode:
  static access`. `show interfaces trunk` lists active trunks only, in four tables: mode and
  native VLAN, VLANs allowed, VLANs allowed and active, VLANs forwarding.
- **Decision**: two steps, merged on `interface`, the same split as EOS (feature 008 R2):
  - `show interfaces switchport`: interface, mode and access VLAN. `Switchport: Disabled` (a
    routed port) gives no row. Mode from `Operational Mode`: `trunk` gives `trunk`, `static
    access` gives `access`, any other word is kept as the device prints it. The access VLAN is
    captured for access ports only, by template state, since a trunk also prints an `Access Mode
    VLAN` line. A port-channel member, `Operational Mode: ... (member of bundle Po1)`, gives
    `channel` and nothing else.
  - `show interfaces trunk`: native VLAN, allowed VLANs (second table), active VLANs (third
    table). `none` maps to no VLAN. A trunk that is down is absent from this view and keeps its
    mode only, as on EOS.
  - `empty_lines` on the trunk step for a device with no active trunk (blank output, recorded
    here before the link came up).
- **To confirm when recording**: a port-channel member and its channel, a trunk with a long
  allowed list wrapped over lines (EOS needed a TextFSM `List`, feature 008), an access port
  towards the second Arista, and a down trunk.
- **Alternatives considered**: `show interfaces status` for the mode (its Vlan column gives
  `trunk` or a number, but the member case is not shown there); `show etherchannel summary` for
  members (a third step for what the switchport view already prints).

## R11. The mixed lab

- **Decision**: `test/lab-iosxe/`, separate from `test/lab` so both can run at once (spec US4
  scenario 3): containerlab topology `netmapper-iosxe`, management network
  `netmapper-iosxe-mgmt` on `172.20.22.0/24`. Nodes: `eos-a` (cEOS 4.36.0F, the seed), `cat1`
  (Cat9kv 17.15.01, `CLAB_MGMT_PASSTHROUGH`, R6) and `eos-b` (cEOS 4.36.0F). Links: `eos-a`
  eth1 and eth2 to `cat1` Gi1/0/1 and Gi1/0/2 as a port-channel trunk with a native VLAN and a
  pruned allowed list; `cat1` Gi1/0/3 to `eos-b` eth1 as an access port in VLAN 10. LLDP is on
  everywhere (`lldp run` on the Catalyst). The `netmapper` account is privilege 1 on `cat1`
  (FR-016) and set up as in `test/lab` on the Arista nodes; SNMP v2c with the `test/lab`
  community on all three. Its own `netmapper.yaml`: perimeter `172.20.22.0/24` minus the gateway,
  the `test/lab` credential sets, one seed, `eos-a`.
- **Resources**: the Cat9kv VM takes 18 GB and 4 vCPU by default; NetLab has 62 GB and 16 vCPU.
  It fits beside `test/lab`; with the 28-node EVPN fabric running, the lab README says to stop one
  of them.
- **Boot time**: two deploys measured. Default management mode: 12 min 33 s from deploy to
  vrnetlab's `Startup complete`, then about 3 more minutes before the data ports came up
  (`notconnect` until then). Passthrough mode: 12 min 20 s to `Startup complete`, with LLDP
  already up on the Arista side by then. Between 12 and 16 minutes, not the 5 to 10 the spec
  assumed. Readiness: `docker logs clab-netmapper-iosxe-cat1 | grep "Startup
  complete"`, then `eos-a` lists `cat1` in `show lldp neighbors`. Nothing is recorded or crawled
  before both.
- **Spec amendment, approved 2026-10-21**: FR-013 and the "Catalyst still booting" edge case
  state 12 to 16 minutes (measured) instead of 5 to 10; SC-005 becomes 25 minutes instead of 20.

## R12. No Go change

- **Finding**: `TestPacks`, `TestFamiliesFromLab` and `TestNoSecretInRecordedOutput` glob every
  pack's `testdata`. `TestFamiliesFromLab` also asks every pack for version `4.36.0F`, which
  `>=17.15` does not fit, so it needs a version per pack (a test-only change, tasks T009); the
  other two need none. Fingerprint, identifiers,
  naming, MAC normalisation, speed tokens, VLAN lists and the far-end canonicalisation are all
  vendor-neutral and cover what R1 to R10 need. `internal/testutil` uses `fakeos` only.
- **Decision**: the feature is pack data, lab files, documentation, and tests in
  `packs/packs_test.go` for the rules a generic test does not cover: naming (R8), the legacy VLANs
  (R9), a refused command giving `parse_failed` (R4), and the two `show version` outputs each
  matching only their own pack (R1). Principle V holds with no vendor name outside `packs/`.

## R13. Determinism and secrets

- None of the commands prints a secret: `show running-config` is not used. No timer, counter or
  age is mapped (`show interfaces` counters and the LLDP `Time remaining` are skipped), so two
  collections of an unchanged lab give the same rows (SC-003).
- The lab passwords stay the `test/lab` ones; the Catalyst's `secret 9` hash appears only in its
  running-config, which no recipe reads and no recording holds.

## Recorded on 2026-10-21 (tasks T008)

One deploy of `test/lab-iosxe` on NetLab, all recordings, then destroyed.

- **Times**: deploy started 13:27:23 UTC; `Startup complete in: 0:12:27`; `cat1` on both `eos-a`
  data ports in LLDP at 13:43:08, 15 min 45 s after the deploy started. Within R11's 12 to 16
  minutes.
- **Lab change**: on the management network every node is every other node's LLDP neighbour
  (`eos-a` saw `eos-b` and `cat1` on Management0), so the crawl would reach `eos-b` without going
  through `cat1` (FR-012). LLDP is turned off on the Arista nodes' Management0 (`no lldp
  transmit`, `no lldp receive`); `cat1.cfg` still does not touch `GigabitEthernet0/0`. Applied
  live, then `cat1`'s Gi0/0 entries aged out: `cat1` lists `eos-a` twice and `eos-b` once. Put
  first in `eos-a.cfg` and `eos-b.cfg`, the lines did not hold on the next deploy (T033):
  containerlab's `arista_ceos` kind rewrites the Management0 block of the startup-config. They
  are now an `exec` of each Arista node in `iosxe.clab.yaml`. The first form of that `exec`
  (`bash -c "printf ... | Cli -p 15"`) failed (containerlab split it and dropped the `\n`); the
  form in the file now, `Cli -p 15 -c '...'` with YAML `\n`, was applied with `containerlab
  exec` on the running lab and worked on both nodes. Then checked on a plain deploy (tasks T035,
  15:13:35 UTC, no command by hand): containerlab logged `Executed command` for `eos-a` and
  `eos-b`, both carry `no lldp transmit` and `no lldp receive` under Management0, and once `cat1`
  was up (`Startup complete in: 0:12:27`, LLDP on both links at 15:29:23, 15 min 48 s after the
  deploy) `eos-a` listed `cat1` on Et1 and Et2 and `eos-b` on Et1, nothing on Ma0.
- **Privilege and startup-config** (R6): `show privilege` gives level 1 as `netmapper`; LLDP, SNMP
  and the account came from `cat1.cfg`.
- **Port-channel** (R10): Po1 is up over LACP between `cat1` and `eos-a` (`Po1(SU)`, both members
  `P`). Members print `Operational Mode: trunk (member of bundle Po1)`, and `down (suspended member
  of bundle Po1)` once Po1 is shut. A port with no link prints `Operational Mode: down`, so the
  mode is read from `Administrative Mode` in that case.
- **LLDP off** (R5): `% LLDP is not enabled`, one line.
- **Wrapped allowed list** (R10): not wrapped. With every even VLAN from 2 to 200 plus 99, the
  "Vlans allowed on trunk" line is one line of about 390 characters on a 200-column terminal, with
  ranges merged (`98-100`). No TextFSM `List` needed.
- **Down trunk** (R10): with Po1 shut, `show interfaces trunk` prints nothing at all;
  `show interfaces switchport` gives Po1 `Administrative Mode: trunk`, `Operational Mode: down`.
- **VLAN status words** (R9): `act/lshut` (after `shutdown` under the VLAN) and `suspended`
  (after `state suspend`) recorded; `show vlan brief` answers at privilege 1.
- **Pty height** (R3): the collector's own SSH transport, built from the branch with
  `scrapli.yaml`, read `show interfaces` (315 lines) and `show interfaces switchport` (283 lines)
  whole, with no `--More--`. Its output equals the interactive recording once each line's
  trailing spaces and the blank lines around the output are trimmed, which scrapligo does; every
  `cat1` recording was trimmed that way so it is what the collector stores.
- **Refusal** (R4): `show running-config` at privilege 1 gives a caret line and `% Invalid input
  detected at '^' marker.`
- **SNMP** (R1, R2): sysObjectID `1.3.6.1.4.1.9.1.3160`; sysDescr as in R1; sysName
  `cat1.example.com`; `entPhysicalSerialNum.1` equals `System Serial Number` (each Cat9kv deploy
  generates a new serial and base MAC).
