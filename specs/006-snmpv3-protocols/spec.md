# Feature Specification: Configurable SNMPv3 authentication and privacy

**Feature Branch**: `006-snmpv3-protocols`

**Created**: 2026-09-28

**Status**: Draft

**Input**: User description: "Configurable SNMPv3 authentication and privacy (GitHub issue #13).
`snmp_v3` credential sets only work with SHA-1 authentication, AES-128 privacy and the authPriv
security level, all three fixed in the collector. Devices using SHA-256/AES-256, MD5, DES or
authNoPriv fail authentication and the collector moves to the next set with no hint about the
mismatch. Concrete case: the arista-evpn-vxlan-clab lab user `auth sha256 ... priv aes ...` cannot be
used today, so every device has an empty hostname, since the Arista pack reads the hostname over SNMP
only. Wanted: choosing the authentication and privacy protocols per credential set, authNoPriv,
possibly a context name, validation at load time, a missing secret field reported as
`credential_unresolved`, the documentation gap on how to reference a v3 secret, and tests that run
the v3 path end to end. Constraints: read only (GET and GETBULK), secrets stay references."

## Decisions taken in this spec

- **Protocols are part of the credential set, not of the secret.** An authentication or privacy
  protocol is not key material: knowing that a network uses SHA-256 opens nothing. Keeping it in the
  configuration document means it is versioned, recorded with every run and validated when the
  document is loaded, like every other field of a credential set. The secret keeps holding only the
  two passphrases.
- **Today's behaviour is the default.** A credential set that names no protocol behaves exactly as
  it does now (SHA-1, AES-128, authPriv). No existing configuration document changes meaning.
- **noAuthNoPriv is out of scope.** It carries no credential at all, so it has nothing to do with
  credential sets as they are defined, and the networks this tool targets do not run it for
  read access.
- **The SNMPv3 context name is deferred.** No pack reads per-context data today, and a context fixed
  per credential set could not reach several VRFs or instances on the same device: the recipe that
  needs the data is where the context belongs. It is left to a later feature, when a pack needs it.

## Clarifications

### Session 2026-09-28

- Q: Is the SNMPv3 context name part of this feature, or deferred until a pack needs per-context
  data? → A: Deferred. No context name field on credential sets in this feature.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Crawl devices whose SNMPv3 user is not SHA-1/AES-128 (Priority: P1)

An operator whose devices are configured with, say, SHA-256 authentication and AES-128 privacy names
those protocols on the `snmp_v3` credential set and runs a crawl. The devices are identified over
SNMP, and the facts the pack reads only over SNMP (the hostname for Arista EOS) appear in the graph.

**Why this priority**: this is the whole point of the issue. Without it, `snmp_v3` is usable only
on networks that happen to match the one combination the collector knows, and the operator has no
way to make it work short of reconfiguring every device.

**Independent Test**: point a crawl at one SNMP agent configured with a non-default combination,
with a credential set naming that combination, and check that the device is identified over SNMP.

**Acceptance Scenarios**:

1. **Given** a device whose SNMPv3 user uses SHA-256 and AES-128, and a credential set naming
   `sha256` and `aes`, **When** a crawl runs, **Then** the device's identity observation over SNMP
   is `collected` and its SNMP-only identifiers (hostname) are recorded.
2. **Given** a device whose SNMPv3 user uses SHA-512 and AES-256, and a credential set naming
   those, **When** a crawl runs, **Then** the device is identified over SNMP.
3. **Given** an existing configuration document with a `snmp_v3` set that names no protocol,
   **When** it is loaded and used against a SHA-1/AES-128 device, **Then** the result is the same as
   before this feature.

---

### User Story 2 - Use an SNMPv3 user with authentication but no privacy (Priority: P2)

An operator whose devices expose an authNoPriv user declares a credential set with privacy set to
`none`. The secret only needs the authentication passphrase.

**Why this priority**: authNoPriv is common on older platforms and in labs. It is a smaller
audience than P1, and P1 already covers the case where both passphrases exist.

**Independent Test**: crawl one agent whose user is authNoPriv, with a set whose privacy protocol is
`none` and a secret that holds only `auth`.

**Acceptance Scenarios**:

1. **Given** a device with an authNoPriv user and a set with privacy `none`, **When** a crawl runs,
   **Then** the device is identified over SNMP.
2. **Given** a set with privacy `none` whose secret holds only `auth`, **When** the secret is
   resolved, **Then** it is not reported as unresolved.

---

### User Story 3 - Find out why an SNMPv3 set does not work (Priority: P2)

An operator whose `snmp_v3` set is wrong (a typo in a protocol name, a secret missing a field, a
secret referenced the wrong way, or protocols that do not match the device) finds out where and why,
instead of seeing devices silently fall back to the next set.

**Why this priority**: today every one of these mistakes looks the same from outside: the set does
not work and nothing says why. Once protocols become configurable, the number of ways to get a set
wrong grows, so the diagnosis has to come with them.

**Independent Test**: load documents and run crawls with each kind of mistake and check what is
reported for each.

**Acceptance Scenarios**:

1. **Given** a set naming an authentication or privacy protocol that is not in the supported list,
   **When** the document is loaded, **Then** it is rejected with an error naming the set, the field
   and the accepted values, and no crawl starts.
2. **Given** a `snmp_v3` set whose secret reference points at a single field of a secret (`#field`),
   **When** the document is loaded, **Then** it is rejected with an error saying that a v3 set must
   reference the whole secret.
3. **Given** an authPriv set whose resolved secret has no `priv` field (or no `auth` field, or an
   empty one), **When** the collector tries it, **Then** it is recorded as `credential_unresolved`,
   naming the missing field and not its value, and no request is sent to the device with that set.
4. **Given** a set whose protocols differ from the device's, **When** the device answers with an
   SNMPv3 error report, **Then** the attempt is recorded as `denied` and the recorded evidence
   carries the device's reason (unknown user, wrong digest, decryption error), so a wrong protocol
   can be told apart from a wrong user.

---

### User Story 4 - Configure an SNMPv3 set correctly the first time (Priority: P3)

An operator setting up `snmp_v3` for the first time reads the deployment guide and the configuration
reference, and finds a complete example: the set with its protocols, and the shape of the secret
behind each kind of reference.

**Why this priority**: the documentation gap is real (the guide never says how a two-field secret is
referenced), but P3 because User Story 3 catches the mistakes the missing documentation causes.

**Independent Test**: follow the guide from an empty setup to a working `snmp_v3` crawl, using the
guide alone.

**Acceptance Scenarios**:

1. **Given** the deployment guide, **When** an operator looks for how to reference an SNMPv3 secret,
   **Then** it states that `env:NAME` holds a JSON object with `auth` and `priv` fields, and that
   `vault:` points at the whole secret, without `#field`.
2. **Given** the configuration reference, **When** an operator looks up `snmp_v3`, **Then** it lists
   every accepted protocol value, the defaults, and a complete example.

### Edge Cases

- Protocol names written in another case (`SHA256`, `Sha256`): accepted or refused consistently, and
  the error for a refused value lists the accepted spelling.
- A `snmp_v2c` or `ssh` set carrying `auth_protocol` or `priv_protocol`: refused at load, since the
  fields mean nothing there and a silent ignore would hide a misplaced block.
- A secret with extra fields beyond `auth` and `priv`: extra fields are ignored.
- An authNoPriv set whose secret also holds a `priv` field: the field is ignored, not sent.
- A device that stays silent instead of answering a protocol mismatch with an error report: recorded
  as the collector records silence today, since no answer proves nothing about the credential.
- Two sets for the same perimeter differing only by protocol (to cover a network in the middle of a
  migration): both are tried in order, as for any other sets.
- DES and MD5 are weak. They are accepted, because refusing them leaves the device unmapped rather
  than protected, and they appear in the documentation as legacy.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A `snmp_v3` credential set MUST accept an optional authentication protocol, one of
  `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512`. Absent, it MUST mean `sha`.
- **FR-002**: A `snmp_v3` credential set MUST accept an optional privacy protocol, one of `none`,
  `des`, `aes`, `aes192`, `aes256`, `aes192c`, `aes256c`. Absent, it MUST mean `aes` (AES-128).
  `none` MUST mean the authNoPriv security level.
- **FR-003**: Loading a configuration document MUST reject an unknown protocol value, naming the
  credential set, the field and the accepted values.
- **FR-004**: Loading a configuration document MUST reject either protocol field on a set that is
  not `snmp_v3`.
- **FR-005**: Loading a configuration document MUST reject a `snmp_v3` set whose `vault:` reference
  names a single field (`#field`).
- **FR-006**: When the resolved secret of an authPriv set lacks a non-empty `auth` or `priv` field,
  or that of an authNoPriv set lacks a non-empty `auth` field, the collector MUST record the attempt
  as `credential_unresolved`, naming the missing field, and MUST NOT send any request with that set.
- **FR-007**: The collector MUST use the protocols and security level of the set for every SNMP
  request made with it.
- **FR-008**: When a device answers an SNMPv3 request with an authentication error (unknown user,
  wrong digest, decryption error, unsupported security level), the attempt MUST be recorded as
  `denied`, with the device's reason in the recorded evidence.
- **FR-009**: A configuration document written before this feature MUST load and behave as before.
- **FR-010**: The protocol fields MUST hold protocol names only. No passphrase, key or derived key
  MUST be accepted in the configuration document, stored, logged or returned by any interface.
- **FR-011**: Every SNMP request remains a GET or a GETBULK. This feature MUST NOT add any other
  kind of request.
- **FR-012**: The deployment guide and the configuration reference MUST describe the new fields,
  their defaults and accepted values, the shape of a v3 secret for `env:` and `vault:` references,
  and give a complete `snmp_v3` example, in the same change.
- **FR-013**: The repository's test lab MUST include at least one device reachable over SNMPv3 with
  a non-default protocol combination, and its configuration document MUST exercise it.
- **FR-014**: The automated test suite MUST run the SNMPv3 path end to end against a real SNMP agent
  for at least: one non-default combination identified successfully, one authNoPriv set, and one
  protocol mismatch recorded as `denied`.
### Key Entities

- **SNMPv3 credential set**: a credential set of kind `snmp_v3`. Gains an authentication protocol
  and a privacy protocol, both optional with defaults. Keeps its username, its secret reference, its
  attempt budget and its perimeters.
- **SNMPv3 secret**: what the reference resolves to, holding `auth` and, for authPriv, `priv`.
  Unchanged in shape; what changes is that its completeness is checked before use.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the arista-evpn-vxlan-clab lab (SHA-256 / AES-128), a crawl with a `snmp_v3` set
  identifies every reachable device over SNMP, and every device in the graph has a hostname.
- **SC-002**: Every configuration document in the repository and in the documentation written before
  this feature loads without change and produces the same credential behaviour.
- **SC-003**: Each of the four mistakes of User Story 3 (bad protocol name, `#field` reference,
  missing secret field, protocol mismatch) produces a distinct message or record that names its cause,
  checked by one test each.
- **SC-004**: An operator can set up a working `snmp_v3` set from the deployment guide alone, without
  reading the source.

## Assumptions

- The SNMP library already used by the collector supports every protocol listed in FR-001 and FR-002;
  no new dependency is needed. To be confirmed in planning.
- Devices answer a protocol mismatch with an SNMPv3 error report most of the time; the silent case is
  handled as silence is today and is not made more precise by this feature.
- The recorded evidence of a `denied` attempt already carries the error text from the device; this
  feature relies on it rather than adding a new record.
- Only the `snmp_v3` kind changes. `snmp_v2c` and `ssh` sets are untouched.
- The repository's test lab is the reference for FR-013; the external arista-evpn-vxlan-clab lab is
  used for SC-001 and is not modified by this feature.
- Touches principle III (secrets stay references and in the collector) and principle IV (read only);
  both are kept, see FR-010 and FR-011.
