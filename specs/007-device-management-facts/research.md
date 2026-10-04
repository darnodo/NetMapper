# Research: device management configuration facts on Arista EOS

Inputs: [spec.md](spec.md), the EVPN lab's survey of what each EOS command prints
(arista-evpn-vxlan-clab, `docs/observability/management-plane.md`, section "Secrets in command
output", checked on cEOS 4.36.0F), and the current code (`internal/fact`, `internal/pack`,
`internal/parse`, `internal/api`, `packs/packs_test.go`).

Both labs (`test/lab` and the EVPN lab) run on a remote VM (`NetLab`), which is not always up, and
it was not reachable while this plan was written. Every command output format below is what the plan
expects; recording them from `test/lab` is the one implementation step that needs the VM (R10, R11),
and a format that differs changes a template, not a decision, unless stated. Everything after the
recording runs from committed fixtures.

## R1. One dedicated `show` command per item, not one sanitized configuration dump

**Decision**: each family runs the `show` commands that print the effective state of its item. The
one exception is SNMP v2c communities, read from `show running-config sanitized | include
^snmp-server community` (R13 for why not `section`), since `show snmp community` prints the strings.

| Family | Steps (`| no-more` appended to each) |
| ------ | ------------------------------------ |
| `snmp` | `show running-config sanitized \| include ^snmp-server community` (v2c), `show snmp user` (v3) |
| `aaa_servers` | `show tacacs`, `show radius` |
| `local_users` | `show users accounts` |
| `management_apis` | `show management api gnmi`, `show management api http-commands`, `show management api netconf`, `show management ssh`, `show management telnet`, `show snmp` |
| `aaa_methods` | `show aaa methods all` |
| `management_servers` | `show running-config sanitized \| include ^ntp server`, `show logging \| include Logging to`, `show ip name-server` |

**Rationale**:

- The lab survey lists every one of these as printing no secret on 4.36, except `show snmp
  community`, which is why v2c goes through the sanitized configuration.
- A `show` command reports the effective state. SSH is on with no configuration line at all, and a
  default port is printed by the command but absent from the configuration. A configuration dump
  would report SSH as unknown and leave every default port out.
- Each observation's raw output holds its own family only. With one dump parsed by six families,
  every family would store the whole configuration, and a template change on one family would be
  verified against output it does not own.
- NTP is the second sanitized section: `show ntp associations` does not print the VRF, and the
  configuration line does (`ntp server vrf MGMT ...`). `sanitized` masks NTP keys.

**Alternatives considered**: one `show running-config sanitized` dump read by every family (one
command, but defaults invisible, trickier multi-block templates, whole configuration stored six
times per device); `show running-config all sanitized` (shows defaults, but large and still has no
runtime state).

## R2. SNMP VRF belongs to the agent on EOS: recorded on a `management_apis` row

**Decision**: `snmp` rows carry no VRF. The SNMP agent becomes one more management API: a
`management_apis` row with `api: snmp`, its enabled state and the VRFs the agent runs in, read from
`show snmp`.

**Rationale**: on EOS a community or a v3 user is not bound to a VRF; `snmp-server vrf <name>` puts
the whole agent in a VRF. Giving every community the agent's VRF would repeat one fact on every row
and, with an agent in two VRFs, would need one row per community and VRF, which a template cannot
produce. SNMP is a way into the device like SSH or eAPI, so the agent sits naturally next to them.

**Spec impact**: FR-002 and FR-008 are amended in the same change as this plan (snmp rows have no
VRF; the agent's VRFs are on the `management_apis` snmp row).

## R3. A management API serves a list of VRFs

**Decision**: `management_apis` has `vrfs`, an ordered list, instead of a single `vrf`. One row per
API.

**Rationale**: eAPI, SSH and the SNMP agent can each run in several VRFs at once, and `show
management api http-commands` prints them as one list. One row per API with its VRF list keeps "is
eAPI on" a one-row question. `aaa_servers` and `management_servers` keep a single `vrf`: a server is
reached through one VRF.

## R4. No security level field on `snmp`

**Decision**: the v3 row has `auth_protocol` and `priv_protocol`, and no `security_level`. A row
with no privacy protocol is authNoPriv.

**Rationale**: the level is fully derived from the two protocols, and EOS does not print it per user
(it is a property of the group). Storing it would mean a merge between `show snmp user` and `show
snmp group` for a value the row already implies. FR-002 is amended to say so.

## R5. Recipe additions: per-step map, defaults, split

The existing recipe format maps one template column to one field, for the whole implementation.
Three families need slightly more. Each addition is a few lines in `internal/pack` (load and
validation) and `internal/parse` (apply), carries no vendor name, and is data in the pack
(constitution V).

| Key | Where | What it does | Needed by |
| --- | ----- | ------------ | --------- |
| `map` on a step | step | merged over the implementation's `map` for rows of that step only | a literal per step: `protocol: '=tacacs'` / `'=radius'`, `api: '=gnmi'`, `service: '=ntp'` |
| `defaults` | implementation | field → literal, set when the row has no value for it after mapping | `vrf: default` (FR-008), `ssh_key: 'no'` |
| `split` | implementation | field → separator, turns the mapped string into an ordered list | `methods: ', '` in `aaa_methods` |

A TextFSM `List` value (already a `[]string`) is kept as a list when the schema field is a list,
instead of being joined with spaces as today. `split` covers the case where the device prints the
list on one line.

**Alternatives considered**: one implementation per API or protocol (an implementation is "the
first that fits wins", so a family cannot run several); literals produced by the template (TextFSM
cannot emit a constant); a post-processing hook in Go per family (vendor logic outside the pack).

## R6. No boolean type: `yes` / `no`

**Decision**: `enabled` and `ssh_key` are strings with enum `yes`, `no`.

**Rationale**: the schema has `string`, `int`, `strings`. An enum string is how `admin_state` already
says up or down, the values table translates the device's wording, and no new type reaches the
validator, the projector or the API encoder.

## R7. `empty` comes from the step's empty lines

**Decision**: each step whose command prints a fixed message when nothing is configured declares it
in `empty_lines` (existing mechanism). A family is `empty` when every step is empty. A section
filter on the sanitized configuration prints nothing at all when nothing matches, which is already
`empty` with no declaration.

**Consequence**: `management_apis` is never empty on EOS (SSH is always reported). That matches the
device: SSH exists whether configured or not.

## R8. Read endpoint: `GET /v1/devices/{name}/facts/{family}`

**Decision**: a new handler next to the device endpoints. It resolves `{name}` with the existing
`pickDevice` (same 404/409 answers), checks `{family}` against `fact.Families`, then reads the
observations of that family in the snapshot whose target is one of the device's `targets`, from the
active parse generation. The answer is a list of observations, each with its status, detail, rows
and evidence.

**Rationale**:

- A device is reachable on the addresses in `entity.attributes.targets`. A duplicate address
  completes its find as `duplicate` and is never scraped, so for the scraped families the list has
  one entry. `identity` can have several (one per address answered, duplicates marked). Returning the
  list serves both without a tie rule to invent and test.
- The active parse generation is the one replay produced; reading another would serve rows a parser
  fix has superseded.
- `netmapper_api` already holds SELECT on `observation`; the task table is not needed and stays
  ungranted. Filtering on the active parse generation needs SELECT on `parse_generation`, which the
  role did not have: migration 0010 grants it (found during implementation). The table holds ids, a
  timestamp and the `active` flag; the collector and engine roles already read it.
- `internal/fact` is not on the API's forbidden import list; reading the family names from it keeps
  one source of truth.
- `not_collected` (clarification Q2): the device exists and no observation of the family matches.

## R9. Secret check: a fixed list plus patterns, over every recorded lab output

**Decision**: a test in `packs/packs_test.go` reads every `packs/*/testdata/lab/**/*.raw` and fails
on any of:

- each secret written into `test/lab` by this feature (all prefixed `nm-lab-secret-`);
- the v3 passphrases already in `test/lab/sw2.cfg` (`lab-auth-sw2`, `lab-priv-sw2`), distinctive
  enough to list;
- each `evpnlab-` secret of the EVPN lab table, and `evpnlab-` itself as a catch-all;
- password hash shapes (`$1$`, `$5$`, `$6$`, `sha512 $`), a type 7 key (`key 7 ` followed by hex).

**Rationale**: a prefix makes a fixed list hard to get wrong and cheap to extend. The pre-existing
`test/lab` values (`admin`, `public`) are ordinary words that occur in legitimate output (`role
network-admin`, "public key"), so they are not listed; they never reach the new outputs anyway, since
the community comes through `sanitized`. The patterns catch a secret nobody thought to list.
Checking every recorded lab output, not only the new ones, costs nothing and covers a future recipe
too.

## R10. `test/lab` changes, and how to keep the crawl lab working

**Decision**: sw2 carries the management configuration. sw1 stays as it is, and its outputs are the
`empty` fixtures: no AAA server, no method list, no NTP, syslog or DNS server. Its `public` community
still gives one v2c `snmp` row, so `snmp` is not empty on sw1, which is correct.

What sw2 gets, all secrets prefixed `nm-lab-secret-`:

| Item | Configuration (sketch) |
| ---- | ---------------------- |
| VRF | `vrf instance MGMT`, configuration only, `Management0` stays in `default` |
| SNMP v2c | a ro community with an ACL and a rw community with an ACL, strings not in `netmapper.yaml` |
| SNMP v3 | existing `netmapper` user (authPriv) plus an authNoPriv user; `snmp-server vrf MGMT` |
| TACACS+ | two hosts on unused addresses, group `NM-TACACS`, `timeout 1` |
| RADIUS | one host on an unused address, group `NM-RADIUS`, `timeout 1` |
| Method lists | `local` first in every default list, then the group, plus a named `console` list; authorization `exec` and `commands all`; accounting `exec` and `commands all` start-stop |
| Users | one more account with a throwaway public SSH key, one with privilege 1 |
| APIs | gNMI, eAPI (also in VRF MGMT), NETCONF on; telnet `shutdown` |
| Servers | NTP in default and in MGMT; syslog in default, one on port 1514, one in MGMT; DNS in default and MGMT |

**Rationale**:

- sw2 already carries the v3 user and has no community the crawl relies on: adding communities whose
  strings are not in `netmapper.yaml` leaves "SNMPv3 only on sw2" true for the crawl.
- `local` first in every method list: with TACACS+ and RADIUS servers on dead addresses, `group`
  first would make each login and each authorized command wait for the server timeout (13 s per
  login in the EVPN lab). `local` first answers at once and still gives an ordered list of two
  methods to parse.
- sw1 keeps the `public` community the crawl uses; sw3 stays denied and sw4 stays a clone of sw1.

**Verification**: the quickstart re-runs the crawl of feature 001 and the v3 path of 006 against the
changed lab, and compares crawl duration with the unchanged lab (no login delay).

## R11. Fixtures and family-level tests

**Decision**:

- Recorded outputs go to `packs/arista_eos/testdata/lab/` as today, named after their template
  (`sw2_show_tacacs.raw`), with a `.yml` of expected template rows next to each (existing
  `parseRecorded` format). Outputs from the EVPN lab, when it runs, go next to them
  (`dc-leaf1_...`, `campus-access1_...`).
- A recorded output that is expected to give no row has `_empty` in its name; `TestPacks` then
  expects zero rows instead of failing on "yielded no row".
- A new test runs `parse.Parse` for each new family over sw2's recorded steps and compares the
  resulting fact rows to `testdata/lab/sw2_<family>.facts.yml`. This is what checks per-step map,
  defaults, split and validation together, with no lab running (SC-001).

## R12. Load warnings

The six families make every other pack log six more "no recipe" warnings (issue #25, intended).
`missingCost` gets one shared line for them ("no management inventory for these devices") so the
warning still says what is lost.

## R13. What the recordings changed (T005 to T007, 2026-10-04)

Recorded from `test/lab` sw1 and sw2 (`docker exec ... Cli`) and from arista-evpn-vxlan-clab
dc-leaf1 and campus-access1 (eAPI, text format), cEOS 4.36.0F. No recording holds a secret.

- **`| section` swallows the next pipe.** `show running-config sanitized | section snmp-server
  community | no-more` prints nothing: `section` takes the rest of the line, `| no-more` included,
  as its pattern. Both configuration reads use `| include ^snmp-server community` and
  `| include ^ntp server` instead; each item is one line, so nothing is lost.
- **`show logging` prints the whole log buffer** (12 KB on dc-leaf1 and growing): stored on every
  crawl and full of messages nobody reviewed for secrets. The step is `show logging | include
  Logging to`, which keeps the host lines only.
- **`docker exec ... Cli -c` echoes a piped command** as a first line `> <command>`. The collector's
  SSH session does not, so that line is stripped from the recordings.
- **EOS takes NTP servers in one VRF only**: a second `ntp server vrf` line in another VRF is refused
  at boot. sw2 has both NTP servers in MGMT; the `default` NTP case comes from dc-leaf1.
- **AAA server groups are a separate section of the same output** (`TACACS+ server-group: NM-TACACS`,
  then one line per member). The template gives one row per server line and one per group member,
  and `parse` merges rows of the same step on `merge_on: [protocol, address, port, vrf]` (today it
  only merges a later step into earlier ones). A server in two groups keeps the first group. A
  server in a VRF prints `192.0.2.12/49 (vrf MGMT)`; RADIUS prints `192.0.2.20, authentication port
  1812, accounting port 1813`, and `port` is the authentication port.
- **`show aaa methods all` names authorization and accounting lists after their service or level**
  (`name=exec`, `name=privilege0-15`), not after the configured list. `list` is recorded only where
  the device prints a list name (authentication: `default`, `console`) and is optional in the
  schema. `level` is what follows `privilege` (`0-15`), as printed: EOS never prints `all`.
  `default-action=startStop` maps to `start-stop`; `none` gives no `record`. `methods` can be empty
  (`methods=` on dot1x, `default-action=none`), so it is optional; `methods=none` is kept as
  `[none]`, as printed.
- **EOS always prints every method list**, configured or not (`system`, `dot1x`, `exec`): `aaa_methods`
  is never `empty` on EOS, like `management_apis` and `local_users` (an account always exists).
  `empty` happens for `aaa_servers` and `management_servers` (sw1), and for a step of `snmp`.
- **Management APIs**: gNMI prints `Enabled: no transports enabled` when off; eAPI prints its VRFs as
  `VRFs: MGMT, default` or `VRFs: None`; SSH and telnet print one status line per VRF
  (`SSHD status for Default VRF: enabled`), so their row's VRFs are the VRFs whose line says
  enabled, and `enabled` is `yes` when there is at least one; the SNMP agent prints `SNMP agent
  enabled in VRFs: MGMT, default`. `Default` is translated to `default` item by item: `values` now
  applies to each item of a list field.
- **A template must always give a row when the command answered**, or `parse` reports
  `parse_failed`. The SSH, telnet and SNMP templates capture one line that is always there (a
  session or size limit), not mapped to any field, so a switch with SSH off in every VRF still gives
  an `ssh` row with `enabled: no` (from `defaults`).
- **Versions: `>=4.36` until an older release is recorded.** Every recording, and so the secret scan,
  comes from cEOS 4.36.0F. A recipe that declared `>=4.20` would run commands on releases where
  nobody checked what they print. An older device gets `unsupported` with `no_matching_version`
  for the six families, which is visible, instead of an unchecked output in the database. Widening
  the range means recording that release and passing `TestNoSecretInRecordedOutput` on it.
- **The collector account needs privileged exec on EOS** (live crawl of `test/lab`, T044). `show
  running-config sanitized`, `show users accounts` and `show aaa methods all` answer `% Invalid
  input (privileged mode required)` to a privilege 1 account, and `snmp`, `local_users`,
  `aaa_methods` and `management_servers` came out `parse_failed`. Decision (2026-10-04): the account
  is `privilege 15 role network-operator`, which reaches privileged exec and still cannot configure.
  EOS applies that privilege only with `aaa authorization exec default local` (sw2 had it through
  its method lists, sw1 did not and stayed at privilege 1). Documented in `docs/how-to/deploy.md`.
  The offline recordings came from `docker exec ... Cli -p 15` and could not show this.
- **The first command of an SSH session sometimes carries its echo** on EOS: a late prompt and the
  command (`sw2#show aaa methods all | no-more`) or the tail of the command, as a first line. It hit
  `aaa_methods`, now the first family scraped, one crawl in two. Issue #15 fixed the same race for
  the generic driver only; the SSH session now drops a first line that ends with the whole command,
  or is a suffix of it of 8 characters or more (`internal/transport/ssh`, `dropEcho`). Three
  consecutive crawls after the fix: no family outside `collected` and `empty`.
- **Out of scope, tracked as #26**: on the full lab, sw1 and sw4 (same serial, different chassis MAC)
  resolve to one entity with no `identity_conflict`, against 003 quickstart section 4. `main` does
  the same and this feature changes nothing in resolution (T050).
- **Cleartext eAPI is its own row** (found when checking the crawl against issue #25). The eAPI
  template read the HTTPS line only, so `protocol http` went unseen. A second step, `show management
  api http-commands | include ^HTTP server`, gives an `eapi` row with `transport: http` when that
  server is `running` or `starting`; `shutdown`, and `enabled` (API off), are declared empty.
  Recorded on dc-leaf1 with `protocol http` set for the recording, then removed. The SSH and telnet
  port and the names of authorization and accounting method lists are not printed by the commands
  used: tracked as #28.
