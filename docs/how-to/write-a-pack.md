# Writing a platform pack

A platform pack teaches NetMapper a vendor: how to recognise it, which read-only commands to send,
and how to turn their output into facts. A pack is a directory of YAML and TextFSM files. Adding one
needs no change to Go code, and a pull request that puts a vendor name outside `packs/` is refused.

Two packs to copy from:

- [`packs/arista_eos`](../../packs/arista_eos): a real platform, tested against a live lab.
- [`internal/pack/testdata/fakeos`](../../internal/pack/testdata/fakeos): an invented one, the
  smallest pack that works.

The format reference is [pack-format.md](../../specs/001-crawl-loop/contracts/pack-format.md).
The examples below use a hypothetical `cisco_ios` pack; its OIDs, commands and regular expressions
are illustrative, not tested against a device.

## 1. Lay out the directory

```text
packs/cisco_ios/                     # the directory name is the pack name
├── pack.yaml                        # recognition, identifiers, interface names
├── scrapli.yaml                     # SSH platform definition (prompts, privilege levels)
├── recipes/
│   ├── interfaces.yaml              # one file per fact family
│   └── neighbours.yaml
├── templates/                       # TextFSM templates the recipes name
│   ├── show_interfaces.textfsm
│   └── show_lldp_neighbors_detail.textfsm
├── testdata/lab/                    # recorded outputs, checked by go test
└── NOTICE                           # only if you reuse third-party templates
```

## 2. pack.yaml: recognise the device

```yaml
name: cisco_ios                      # must equal the directory name
version: 1                           # bump on any template or recipe change
read_only: ["show "]                 # every command in the pack must start with one of these
fingerprint:
  snmp:
    - sys_object_id_prefix: 1.3.6.1.4.1.9.1
      version_regex: 'Version (?P<version>[\d.()A-Za-z]+)'   # applied to sysDescr
  ssh:
    - command: show version          # tried when SNMP is silent
      match: 'Cisco IOS'
      version_regex: 'Version (?P<version>[\d.()A-Za-z]+)'
```

Before a device is identified, the collector asks it for `sysObjectID` and `sysDescr` over SNMP
(the `_base` pack) and matches them against every pack's `fingerprint.snmp`. When SNMP does not
answer, it opens an SSH session and tries each pack's `fingerprint.ssh` command. The `version`
captured here is what recipes match their `versions` constraint against.

`read_only` is a lint, not a suggestion: the collector refuses to start if any command in the pack
does not start with one of these prefixes. NetMapper never changes a device, and this is where that
is enforced for commands.

## 3. pack.yaml: identifiers

Identifiers are how NetMapper recognises the same box across addresses and crawls:

```yaml
identifiers:
  - kind: serial
    strength: strong
    ssh: { command: show version, regex: 'Processor board ID (?P<value>\S+)' }
    snmp: { oid: 1.3.6.1.2.1.47.1.1.1.1.11.1 }
  - kind: chassis_mac
    strength: strong
    normalise: mac                   # any MAC spelling becomes aa:bb:cc:dd:ee:ff
    ssh: { command: show version, regex: 'Base ethernet MAC Address\s+:\s+(?P<value>\S+)' }
  - kind: hostname
    strength: weak
    snmp: { oid: 1.3.6.1.2.1.1.5.0 }
```

Mark an identifier `strong` only if no two boxes can share it: a serial number, a chassis MAC. Two
answers carrying the same strong identifier become one device. A hostname or a management address
is `weak`: it names a device but never merges two, because in a real network they repeat.

Use the same `kind` names as the other packs (`serial`, `chassis_mac`, `hostname`) so devices of
different vendors are compared on the same terms.

## 4. pack.yaml: interface names

A device and its neighbours rarely spell a port the same way (`Gi0/1`, `GigabitEthernet0/1`).
Rules rewrite every spelling to one canonical name, applied in order:

```yaml
interface_names:
  - { match: '^Gi(\d.*)$', replace: 'GigabitEthernet$1' }
  - { match: '^Te(\d.*)$', replace: 'TenGigabitEthernet$1' }
  - { match: '^Po(\d.*)$', replace: 'Port-channel$1' }
```

The canonical name is what links are keyed on, so getting it wrong splits one cable into two. The
original spellings are kept as aliases.

## 5. scrapli.yaml: the SSH session

A [scrapligo](https://github.com/scrapli/scrapligo) platform definition, with two rules for a
read-only collector:

- no `network-on-open` or `network-on-close` commands: nothing reaches a device unless a recipe sends
  it and the audit log records it;
- one privilege level, never escalating: no `enable`, no `configure terminal`.

Copy [`packs/arista_eos/scrapli.yaml`](../../packs/arista_eos/scrapli.yaml) and adjust the prompt
pattern and `failed-when-contains`. Turn paging off in the command itself (`| no-more`, `| no-pager`)
rather than with `terminal length 0`.

## 6. Recipes: one per fact family

A recipe says how to collect one fact family. The families and their fields are fixed and
platform-neutral, listed in [fact-families.md](../../specs/001-crawl-loop/contracts/fact-families.md):
`neighbours` and `interfaces` for the graph, and six management families that describe the device
itself (`snmp`, `aaa_servers`, `local_users`, `management_apis`, `aaa_methods`,
`management_servers`). `identity` comes from the fingerprint and identifiers above.

Every recipe is optional: a pack only has to fingerprint its platform. What a missing recipe costs:

| Family       | Without a recipe |
| ------------ | ---------------- |
| `neighbours` | The crawl does not expand from these devices and they report no link. A device can still appear at the far end of a link a neighbour reports. |
| `interfaces` | Only ports named by a neighbour exist, with no description, state, speed or MTU. |
| the six management families | No management inventory for these devices: no SNMP access, AAA servers, accounts, management APIs, method lists or NTP, syslog and DNS servers. The graph is unaffected. |

NetMapper logs a warning when it loads the packs, once for each family a pack has no recipe for,
and each device gets an observation `unsupported` with detail `no_recipe` for it. A recipe whose
`versions` fits none of a device's software gives `no_matching_version` instead.

`recipes/interfaces.yaml`:

```yaml
family: interfaces
implementations:                     # the first one whose version and transport fit wins
  - id: status-cli
    versions: '>=15.0'               # '', '>=4.20', or clauses like '<17,>=15.2'
    transport: ssh
    steps:
      - command: show interfaces status
        template: show_interfaces_status.textfsm
    map:                             # fact field: template value
      name: PORT
      description: NAME
      admin_state: STATUS
      oper_state: STATUS
    values:                          # device spelling -> schema value, '*' is the fallback
      admin_state: { disabled: down, '*': up }
      oper_state: { connected: up, notconnect: down, '*': other }
```

- `map` targets must be fields of the family; the collector refuses to start otherwise. A value
  starting with `=` is a literal (`protocol: '=lldp'`).
- Interface names (`name`, `local_interface`) go through the rules of step 4 automatically.
- `empty_lines` on a step lists what the command prints when it has nothing to report, such as
  `'^Interface \S+ detected 0 LLDP neighbors:$'`. Such output is recorded as `empty`; anything else
  that yields no row is `parse_failed`, which shows up as a finding instead of passing silently.
- A family with several commands uses several steps and `merge_on` to join rows on key fields.
  Rows of the same step that share the key merge too: `show tacacs` lists a server, then the same
  server again as a member of its group, and the two become one row.
- A step can carry its own `map`, merged over the implementation's for the rows of that step. It is
  how one family gives each command its literal:

  ```yaml
  steps:
    - command: show tacacs | no-more
      template: show_tacacs.textfsm
      map: { protocol: '=tacacs' }
    - command: show radius | no-more
      template: show_radius.textfsm
      map: { protocol: '=radius' }
  ```

- `defaults` sets a field when a row has no value for it, after `map` and `values`:
  `defaults: { vrf: default }`. The value must be valid for the field.
- `split` cuts a mapped string into a list, for a list field such as `methods`:
  `split: { methods: ', ' }`. A TextFSM `List` value already gives a list. `values` applies to each
  item of a list.
- **No command may print a secret.** Every output is stored as evidence and served by the API: a
  community string, a server key or a password hash in it is a secret in the database. Use a
  command that does not print it, or the platform's sanitized configuration (`show running-config
  sanitized` on EOS) filtered with `include`. `TestNoSecretInRecordedOutput` scans every recording
  under `testdata/lab/` for the lab secrets, hashes and type 7 keys; record from a lab whose
  secrets are distinctive strings so that scan means something.
- SNMP steps use `walk: <oid>` instead of a command and template. The parser supports them, but no
  shipped pack uses one yet, so expect to be the first to exercise that path.

A new fact field, or a new family, is a Go change to `internal/fact` first, then pack data. A pack
cannot invent a field.

## 7. Templates

[TextFSM](https://github.com/google/textfsm/wiki/TextFSM) templates, run with the Go implementation.
[ntc-templates](https://github.com/networktocode/ntc-templates) covers most platforms and can be
reused under its Apache 2.0 licence: copy the template, keep the licence, and write down in a
`NOTICE` what you took and what you changed, as
[`packs/arista_eos/NOTICE`](../../packs/arista_eos/NOTICE) does.

Go regular expressions have no lookbehind or lookahead: templates that use them need rewriting.

## 8. Test with recorded output

Put real outputs from a device in `testdata/<source>/`, one file per command, with the template's
base name in the file name:

```text
testdata/lab/sw1_show_interfaces_status.raw
testdata/lab/sw1_show_lldp_neighbors_detail.raw
```

A `.yml` file next to a `.raw`, in the ntc-templates `parsed_sample` format, is compared field by
field with what the template extracts. A recording with `_empty` in its name must give no row:
it is a device with nothing to report.

A `<switch>_<family>.facts.yml` file in `testdata/lab/` (`status:` and `rows:`) is compared with
what the whole recipe produces from that switch's recordings, one per step: maps, values, split,
defaults, merge and the schema check together. Then:

```sh
go test ./packs/
```

This loads every pack with the collector's own loader, so every load-time check runs:
- commands start with a `read_only` prefix;
- templates exist and compile;
- `map` targets exist in their family;
- pack names are unique.

It then parses every recorded output. Record outputs from as many software versions as you can: a
template that breaks on a new release is the usual failure.

## 9. Run it

Point the collector and the engine at the directory that holds your pack and `_base`:

```sh
netmapper collector --packs ./packs
netmapper engine --packs ./packs
```

After a crawl, check `/v1/findings` for `unknown_platform` (the fingerprint did not match) and
`parse_failed` (a template did not fit the output). Each finding cites the observation whose raw
output shows what the device actually printed. An observation `unsupported` with detail `no_recipe`
or `no_matching_version` means a family was never asked for (step 6).

When you change a template or a recipe, bump `version` in `pack.yaml` so people can tell the
releases apart. Each observation also records a `recipe_id` of the form
`<pack>/<recipe>@<hash of the pack's files>`, computed automatically, so a result can always be
traced to the exact files that produced it.
