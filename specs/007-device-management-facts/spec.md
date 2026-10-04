# Feature Specification: Device management configuration facts on Arista EOS

**Feature Branch**: `007-device-management-facts`

**Created**: 2026-10-04

**Status**: Draft

**Input**: User description: "Implement GitHub issue darnodo/NetMapper#25: collect device management
config (SNMP v2c/v3, AAA servers RADIUS/TACACS+, AAA method lists, local users, management APIs
gNMI/eAPI/NETCONF/SSH/telnet, NTP/syslog/DNS servers) on Arista EOS, as new platform-neutral fact
families, then Arista recipes and templates. Each row records its VRF. Main constraint: no secret may
reach stored raw output, the raw output endpoint or any fact field. A test asserts that no recorded
lab output used by the new templates contains a known lab secret. Out of scope: other platforms,
compliance rules, graph engine use. Test data: the repo's own two-switch lab has none of this
configuration; the arista-evpn-vxlan-clab lab has all of it but is not always running, so the local
test lab configuration has to be updated too."

## Decisions taken in this spec

- **Six families, not one.** The rows have different shapes: an SNMP user, an AAA server, an account,
  an API, a method list and a service endpoint share almost no field. One family per shape keeps each
  schema small and lets a pack support some of them and not others. Names: `snmp`, `aaa_servers`,
  `local_users`, `management_apis`, `aaa_methods`, `management_servers`.
- **A v2c community is recorded by its existence and access level, never by its string.** A
  community string is a credential (constitution III). The row says "a read-write community exists,
  bound to ACL SNMP-RW, in VRF default", which is what an audit asks, and nothing that opens a
  session.
- **Nothing configured is `empty`, not `collected` with zero rows.** `empty` already means "the
  command ran and there was nothing to report" for every family. A device with no SNMP and a device
  whose SNMP output could not be parsed must not read the same, and `empty` versus `parse_failed`
  already makes that difference.
- **The secret rule applies to the stored output, not only to the facts.** Every command output is
  kept as evidence and served by the raw output endpoint. A command that prints a secret is not
  allowed in these recipes, whatever the template keeps from it.
- **One answer per device and family in the read interface.** The device answer is built from the
  projected graph and these facts are not projected. A separate answer per family reads the
  observation as stored, carries its evidence, and works for any family added later without changing
  the device answer.
- **The repository's test lab carries the configuration.** The arista-evpn-vxlan-clab lab has
  every item, but it is a separate project and is not always running. Fixtures that the test suite
  depends on must be reproducible from this repository alone, so `test/lab` gets the management
  configuration. The EVPN lab stays a second source, for the variety a two-switch lab cannot give.
- **Which commands, and whether one configuration dump feeds several families, is a planning
  choice.** It depends on what the device masks in each output, which has to be checked against the
  lab. The spec fixes the outcome (no secret stored, every row type covered by a recorded fixture),
  not the commands.

## Clarifications

### Session 2026-10-04

- Q: How does the read interface expose these facts: a section of the existing device answer, an
  endpoint per device and family, or deferred? → A: An endpoint per device and family, reading the
  observation directly, generic over every fact family.
- Q: Does the per-family endpoint need its own token scope, or does the existing `read` scope cover
  it? → A: The existing `read` scope covers it, like every other endpoint.
- Q: What does the endpoint return when the device exists in the snapshot but has no observation for
  that family (a snapshot taken before this feature)? → A: 404 `{"error": "not_collected",
  "snapshot": {…}}`.
- Q: Does an `aaa_methods` row record the privilege level the list applies to? → A: Yes, an
  optional `level` field (`all` or `0` to `15`), set only for the `commands` service.
- Q: How does `aaa_methods` tell authentication, authorization and accounting apart on the same
  service? → A: A `type` field (`authentication`, `authorization`, `accounting`) beside `service`,
  and an optional `record` field (`start-stop`, `stop-only`) for accounting.
- Q: When the device states no port for a server, is `port` left absent or filled with the
  protocol's default? → A: Recorded only when the device's output shows it, including a default port
  the device prints itself; absent otherwise, never filled in by the pack.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See how a device is managed, with no secret stored (Priority: P1)

An operator crawls an Arista network. For each device, NetMapper now also records which SNMP
communities and v3 users exist, which AAA servers it points to, which local accounts exist, which
management APIs are on, which method lists drive login and authorization, and which NTP, syslog and
DNS servers it uses, each with its VRF. None of the stored outputs or facts contains a community
string, a server key or a password hash.

**Why this priority**: this is the data the issue asks for, and the secret constraint is the
condition for collecting it at all. Without the guarantee, the feature cannot ship.

**Independent Test**: deploy the repository's test lab, crawl it, and check, per family, that the
rows of the switch carrying the management configuration match that configuration and that no
stored output contains any of its secrets or a password hash. Then run the template tests with the
lab stopped: they pass from the recorded fixtures alone.

**Acceptance Scenarios**:

1. **Given** the configured test lab switch with a read-only and a read-write v2c community, each
   bound to an ACL, and an authPriv and an authNoPriv v3 user, **When** a crawl runs, **Then** the
   `snmp` observation is `collected` with two v2c rows giving access and ACL and no community string,
   and two v3 rows giving user, group, authentication and privacy protocols.
2. **Given** that switch with TACACS+ servers in a named group and a RADIUS server in another,
   **When** a crawl runs, **Then** `aaa_servers` has one row per server with protocol, address, port
   when the output shows one, VRF and group, and no key.
3. **Given** that switch with an account holding an SSH key and accounts without one, **When** a
   crawl runs, **Then** `local_users` has one row per account with role and privilege, the key holder
   marked as having an SSH key, and no row holds a password hash or the key itself.
4. **Given** that switch with gNMI, eAPI and NETCONF on and telnet shut down, **When** a crawl runs,
   **Then** `management_apis` has a row per API with its enabled state, and telnet reads as not
   enabled.
5. **Given** that switch with authentication (login, enable), authorization (exec, commands) and
   accounting (exec, commands, start-stop) method lists using a server group then `local`, and a
   `console` login list using `local` only, **When** a crawl runs, **Then** `aaa_methods` has one row
   per list with its type, service, list name, level for commands, record mode for accounting, and
   methods in order.
6. **Given** that switch with NTP servers, syslog hosts (one on a non-default port) and DNS name
   servers, at least one of them in a non-default VRF, **When** a crawl runs, **Then**
   `management_servers` has one row per server with service, address, port when the device states
   one, and VRF.
7. **Given** the arista-evpn-vxlan-clab lab running, **When** it is crawled, **Then** dc-leaf1 and
   campus-access1 give the rows their configuration describes, with the same templates.
8. **Given** any recorded output used by the new recipes, from either lab, **When** the test suite
   runs, **Then** it fails if that output contains any secret from the configuration it was recorded
   from.

---

### User Story 2 - Tell "not configured" from "not collected" (Priority: P2)

An auditor looks at a device with no SNMP, no RADIUS or no syslog configured. They need to know that
the device was asked and has none, as opposed to the question never having been asked or the answer
not being understood.

**Why this priority**: an audit built on these facts draws conclusions from absence ("no SNMPv2c on
this device"). That conclusion is only valid if absence is recorded explicitly.

**Independent Test**: crawl the test lab switch that is left without management configuration, and
check the observations are `empty` (or carry only the rows of defaults the device
always reports, such as SSH), distinct from `parse_failed` and `unsupported`.

**Acceptance Scenarios**:

1. **Given** a device with nothing configured for a family, **When** a crawl runs, **Then** the
   observation for that family is `empty`.
2. **Given** an output the template does not understand, **When** a crawl runs, **Then** the
   observation is `parse_failed`, never `empty`.
3. **Given** a device of another platform, **When** a crawl runs, **Then** each new family is
   `unsupported` with detail `no_recipe`, and the snapshot verdict is unchanged.

---

### User Story 3 - Read the management facts through the read interface (Priority: P3)

An operator or an agent asks NetMapper, for one device, what its management configuration is, and
gets the rows with the observation they came from and when it was collected.

**Why this priority**: until this exists the facts are only in the database. It is useful once
P1 data exists, and it is where constitution I (evidence travels with the answer) applies.

**Independent Test**: after a crawl, request the management facts of dc-leaf1 and check each family
comes back with its status, its rows and its evidence.

**Acceptance Scenarios**:

1. **Given** a crawled device, **When** its management facts are requested, **Then** each family
   comes back with status, rows, observation id, snapshot id and collection time.
2. **Given** a family that is `empty` or `unsupported` on that device, **When** it is requested,
   **Then** the status says so; it is not left out.

3. **Given** a family name that is not a known fact family, **When** it is requested, **Then** the
   answer is a not-found error distinct from "unknown device".
4. **Given** a device present in the snapshot with no observation for the family asked, for
   example a snapshot taken before this feature, **When** it is requested, **Then** the answer is
   404 `{"error": "not_collected", "snapshot": {…}}`, distinct from `no_such_device`, from `empty`
   and from `unsupported`: there is no evidence to serve, so no 200 is given.

---

### Edge Cases

- A service bound to a non-default VRF: the VRF name is recorded as given. A service with no VRF
  stated is recorded as `default`, so every row has a VRF and a consumer never guesses.
- A v3 user whose group has no matching `snmp-server group` line: the row is kept with what the
  device reports.
- Two communities with the same access and ACL: two rows, indistinguishable by design. The count is
  still right.
- A server given by hostname rather than address: stored as given, never resolved (same rule as
  neighbour names).
- An AAA server not in any named group: recorded with no group (it belongs to the protocol's
  default group).
- A management API that is configured but shut down, or never configured: reads as not enabled. SSH
  is on by default on EOS and has no configuration block; it is reported from the device's state,
  not from the presence of configuration.
- A method list naming a group that does not exist: the methods are recorded as the device prints
  them; checking them is compliance work, out of scope.
- An NTP key, a TACACS key in type 7 form, a sha512 password hash: none appears in stored output,
  whatever the configuration encoding.
- A device on an EOS version where a command is missing: `unsupported` with detail
  `no_matching_version` if the recipe restricts versions, otherwise `parse_failed`.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The fact schema MUST define six new platform-neutral families: `snmp`, `aaa_servers`,
  `local_users`, `management_apis`, `aaa_methods`, `management_servers`, with no vendor term in any
  field name or enum value.
- **FR-002**: `snmp` MUST hold one row per v2c community or v3 user, with version, access level,
  ACL and VRF for v2c, and user, group, authentication protocol, privacy protocol and security level
  for v3. It MUST NOT hold a community string or a passphrase.
- **FR-003**: `aaa_servers` MUST hold one row per RADIUS or TACACS+ server, with protocol, address,
  port when the device's output shows one, VRF and group. It MUST NOT hold a key.
- **FR-004**: `local_users` MUST hold one row per local account, with name, role, privilege and
  whether an SSH key is set. It MUST NOT hold a password, a hash or a key.
- **FR-005**: `management_apis` MUST hold one row per management API (gNMI, eAPI, NETCONF, SSH,
  telnet), with whether it is enabled, and its transport, port and VRF when the device states them.
- **FR-006**: `aaa_methods` MUST hold one row per method list, with its type (`authentication`,
  `authorization`, `accounting`), its service (`login`, `enable`, `exec`, `commands`, `system`, or
  another the device names), the list name, the ordered list of methods, for the `commands` service
  only the privilege level it applies to (`all` or `0` to `15`, `all` kept as given), and for
  accounting only the record mode (`start-stop`, `stop-only`). Type, service, list name and level
  together identify a row.
- **FR-007**: `management_servers` MUST hold one row per NTP, syslog or DNS server, with service,
  address, port when stated, and VRF.
- **FR-007a**: A `port` field in any new family MUST hold only what the device's output shows,
  including a default port the device prints itself. A pack MUST NOT fill in a protocol's default
  port the device did not print.
- **FR-008**: Every row of `snmp`, `aaa_servers`, `management_apis` and `management_servers` MUST
  carry a VRF; a service with no VRF configured MUST be recorded as `default`.
- **FR-009**: Commands in the new Arista recipes MUST NOT print a secret: community strings, RADIUS,
  TACACS+ and NTP keys in any encoding, passwords and password hashes. This holds for the stored raw
  output, not only for the facts.
- **FR-010**: The schema list for the new families MUST be published in the fact families contract
  in the same change as the code.
- **FR-011**: The Arista EOS pack MUST provide a recipe for each new family; no other pack changes.
- **FR-012**: A family with nothing configured MUST produce an `empty` observation; an output the
  template does not understand MUST produce `parse_failed`.
- **FR-013**: The repository's test lab (`test/lab`) MUST carry the management configuration on one
  switch, enough for every row type of story 1 including a non-default VRF, and MUST keep one
  reachable switch without it for the `empty` cases. What the lab already proves (identification
  over SSH and SNMP v2c and v3, the denied switch, the serial conflict of sw4) MUST keep working.
- **FR-013a**: Every row type of story 1 MUST be covered by a template test against output recorded
  from `test/lab`, runnable with no lab deployed. Outputs recorded from arista-evpn-vxlan-clab MAY
  be added as extra fixtures.
- **FR-014**: A test MUST fail if any recorded output used by the new recipes contains a secret from
  the configuration it was recorded from. Secrets added to `test/lab` for this feature MUST be
  distinctive strings, so the check cannot match ordinary words; for the EVPN lab, the `evpnlab-`
  secrets and the `admin` password hash.
- **FR-014a**: The configuration added to `test/lab` and the way to record fixtures from it MUST be
  documented in the same change (topology comments and the feature quickstart).
- **FR-015**: The new families MUST NOT be read by the graph engine, and MUST NOT change the snapshot
  verdict.
- **FR-016**: The read interface MUST offer one answer per device and fact family, giving the
  family's status, its rows, and its evidence (observation, snapshot, collection time). It reads the
  device's observation for that family in the requested snapshot, and is not limited to the six new
  families: any family in the schema can be asked for. It requires the existing `read` scope, like every
  other endpoint: the raw output of the same commands is already served under `read`, so a separate
  scope would protect nothing.
- **FR-017**: The README and the how-to guides MUST be updated in the same change where they list
  fact families or what a pack collects.

### Key Entities

- **SNMP access entry**: a v2c community (by access level, ACL and VRF, never its string) or a v3
  user (by name, group, protocols and security level).
- **AAA server**: a RADIUS or TACACS+ server the device sends authentication to, with its group and
  VRF.
- **Local account**: an account defined on the device, its role and privilege, and whether key-based
  login is set up for it.
- **Management API**: a way into the device for management (gNMI, eAPI, NETCONF, SSH, telnet) and
  whether it is on.
- **AAA method list**: for one type (authentication, authorization, accounting), one service
  (login, enable, exec, commands, system), one list name and, for commands, one privilege level, the
  ordered methods the device tries, and for accounting when records are sent.
- **Management server**: an NTP, syslog or DNS server the device talks to, with its VRF.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: With no lab running, the test suite covers every row type of the six families and
  the secret check, and passes.
- **SC-002**: On a crawl of `test/lab`, the configured switch's every community, v3 user, AAA server,
  local account, management API, method list and NTP, syslog and DNS server appears as exactly one
  row, and zero lab secrets appear in stored outputs or facts.
- **SC-003**: On a crawl of the 28 arista-evpn-vxlan-clab switches, when that lab is running, every
  device has an observation for each of the six new families, none is `parse_failed`, and zero lab
  secrets are stored.
- **SC-004**: A device of another platform gets six `unsupported` (`no_recipe`) observations and the
  same snapshot verdict as before the feature.

## Assumptions

- `test/lab` is the primary fixture source and is changed by this feature. Which switch carries
  the configuration is a planning choice; sw2 is the natural candidate, since it already carries the
  v3 user and sw1 the v2c community the crawl credentials rely on, and sw1 then stays bare enough
  for the `empty` cases. sw3 (denied) cannot serve, since nothing is collected from it.
- The non-default VRF in `test/lab` is configuration only: the services pointed into it need not be
  reachable, and the management interface stays where it is so the crawl keeps reaching the switch.
- The arista-evpn-vxlan-clab lab (cEOS 4.36.0F) is not always running. It is a second fixture source
  and a check on 28 switches when available. It uses VRF `default` everywhere and has RADIUS on
  campus-access1 and campus-access2 only.
- A public SSH key is not a secret, but it is not useful to an audit either: only its presence is
  recorded.
- The SNMP engine ID, group names, ACL names and server addresses are not secrets.
- The existing recipe mechanisms (steps, merge, empty lines, value mapping) are enough; if a family
  needs a boolean or an ordered list type the schema does not have yet, adding it is part of this
  feature.
- Compliance checks on these facts and other platforms are separate work.
