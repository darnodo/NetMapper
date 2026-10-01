# Deploying NetMapper

How to run NetMapper for real: one database login per process, an object store for raw output,
secrets kept out of the configuration, and the API reachable only where it should be. For a quick
local trial, the [README](../../README.md#quick-start) is enough.

## What runs

One binary (or one image), several processes. The first argument picks the role:

| Process                    | What it does                                                        | Needs                                   |
| -------------------------- | ------------------------------------------------------------------- | --------------------------------------- |
| `netmapper collector`      | the only process that talks to devices; writes observations and raw output | database, object store (read/write), device secrets, network reach to the devices |
| `netmapper engine`         | closes snapshots, judges coverage, resolves identities, projects the graph | database, packs                          |
| `netmapper api`            | serves the read API                                                 | database, object store (read only)      |
| `netmapper migrate`, `run`, `token`, ... | operator commands, run on demand                      | database                                |

Only the collector holds device credentials and reaches devices. Keep it that way when you place the
processes: the API is the one part anyone can reach, and it can log into nothing.

Every process reads `NETMAPPER_DSN`. Logs are JSON lines on stderr. Exit code 1 is a runtime error,
2 is invalid input.

## 1. PostgreSQL

Tested with PostgreSQL 17. Create an empty database, then apply the schema as a superuser:

```sh
NETMAPPER_DSN='postgres://admin:...@db:5432/netmapper' netmapper migrate
```

`migrate` is idempotent; run it again after every upgrade. It creates the group roles the grants are
written against: `netmapper_owner`, `netmapper_operator`, `netmapper_collector`, `netmapper_engine`
and `netmapper_api`. They cannot log in. Create one login per process and make it a member of its
role:

```sql
CREATE ROLE nm_collector LOGIN PASSWORD '...' IN ROLE netmapper_collector;
CREATE ROLE nm_engine    LOGIN PASSWORD '...' IN ROLE netmapper_engine;
CREATE ROLE nm_api       LOGIN PASSWORD '...' IN ROLE netmapper_api;
CREATE ROLE nm_operator  LOGIN PASSWORD '...' IN ROLE netmapper_operator;
```

Each process then gets its own DSN, for example
`postgres://nm_api:...@db:5432/netmapper`. The grants are what bound a compromise: `netmapper_api`
can read what it serves and update one column, and cannot read the configuration where secret
references live.

## 2. Object store

Any S3-compatible store; the development stack uses [Garage](https://garagehq.deuxfleurs.fr/).
Create a bucket and two keys: one with read and write for the collector, one read only for the API.

| Variable                  | Meaning                                         |
| ------------------------- | ----------------------------------------------- |
| `NETMAPPER_S3_ENDPOINT`   | `host:port`                                     |
| `NETMAPPER_S3_BUCKET`     | bucket name                                     |
| `NETMAPPER_S3_ACCESS_KEY` | access key                                      |
| `NETMAPPER_S3_SECRET_KEY` | secret key                                      |
| `NETMAPPER_S3_REGION`     | optional, default `garage`                      |
| `NETMAPPER_S3_INSECURE`   | optional; any value means plain HTTP            |

Both exit at start when a variable is missing; the API also exits when the bucket does not exist.

## 3. Device secrets

The configuration document never holds a secret, only a reference that the collector resolves when
it needs it:

| Reference                     | Resolves to                                                    |
| ----------------------------- | -------------------------------------------------------------- |
| `env:NAME`                    | the environment variable `NAME` of the collector process       |
| `vault:<kv v2 path>#<field>`  | a field of a Vault or OpenBao secret; reads `VAULT_ADDR` and `VAULT_TOKEN` |

`snmp_v3` sets need a secret with two fields, `auth` and `priv` (only `auth` when the set has
`priv_protocol: none`). How to reference it:

| Reference         | The secret must be                                                                |
| ----------------- | --------------------------------------------------------------------------------- |
| `env:NAME`        | a JSON object in the variable: `NAME='{"auth": "...", "priv": "..."}'`            |
| `vault:<kv path>` | a KV v2 secret with string fields `auth` and `priv`, referenced without `#field`  |

With `#field` a reference resolves to that one value, so a v3 set would get empty passphrases; the
configuration is refused when it is loaded. A secret that lacks a field the set needs is reported as
`credential_unresolved`, naming the set and the field, and nothing is sent to the device with it.

Give the collector a Vault token that can read those paths and nothing else.

## 4. The configuration document

One YAML document, posted whole on every run and kept with the snapshot it produced:

```yaml
perimeters:
  - name: campus
    include: [10.10.0.0/16]
    exclude: [10.10.255.0/24]
credential_sets:                     # tried in this order
  - name: ro-snmp
    kind: snmp_v2c                   # ssh | snmp_v2c | snmp_v3
    secret_ref: vault:kv/data/netmapper/campus#community
    max_attempts_per_device: 1
    perimeters: [campus]
  - name: ro-snmp-v3
    kind: snmp_v3
    username: netmapper
    auth_protocol: sha256            # md5 | sha | sha224 | sha256 | sha384 | sha512, default sha
    priv_protocol: aes               # none | des | aes | aes192 | aes256 | aes192c | aes256c, default aes
    secret_ref: vault:kv/data/netmapper/campus-v3   # fields auth and priv, no #field
    max_attempts_per_device: 1
    perimeters: [campus]
  - name: ro-ssh
    kind: ssh
    username: netmapper
    secret_ref: vault:kv/data/netmapper/campus#password
    max_attempts_per_device: 2
    perimeters: [campus]
seed_sets:
  - name: core
    targets: [10.10.0.1, 10.10.0.2]
discovery:                           # optional, defaults shown
  step_idle_timeout: 60s
  step_deadline: 30m
  lease: 5m
```

The protocols of a `snmp_v3` set must match the device's user: `sha` is SHA-1 and `aes` is AES-128,
the protocols used before they could be chosen. `aes192c` and `aes256c` are the key-extension
variants some Cisco platforms use; `aes192` and `aes256` are what most other platforms call AES-192
and AES-256. `none` selects authNoPriv. A set whose protocols do not match is refused by the device
and the attempt is recorded as `denied`; the device's answer does not say whether the protocol, the
passphrase or the user was wrong.

Nothing outside a perimeter is ever contacted, including neighbours a device reports. The full
reference is [config.md](../../specs/001-crawl-loop/contracts/config.md).

## 5. Run the processes

```sh
# engine: needs the packs to name the far end of a link
NETMAPPER_DSN=$ENGINE_DSN netmapper engine --packs /path/to/packs

# collector: the only process with device secrets and network reach
NETMAPPER_DSN=$COLLECTOR_DSN VAULT_ADDR=... VAULT_TOKEN=... NETMAPPER_S3_...=... \
  netmapper collector --packs /path/to/packs --workers 64

# api: plain HTTP, see "Exposing the API" below
NETMAPPER_DSN=$API_DSN NETMAPPER_S3_...=... netmapper api --listen :8080
```

Several collectors can run at once; they share the work through the database. With the container
image, packs are at `/packs`, which is where `--packs` looks by default, and the role is the first
argument: `docker run ghcr.io/darnodo/netmapper:latest engine`.

## 6. Crawl

```sh
NETMAPPER_DSN=$OPERATOR_DSN netmapper run --config campus.yaml --perimeter campus --seed-set core
```

`run` prints a job id and returns. The job succeeds when every reachable device inside the
perimeter has been collected; the engine then judges its coverage against the previous crawl,
resolves identities and projects the graph, usually within seconds. `netmapper cancel <job-id>`
stops a crawl.

## 7. Read the API

Issue a token, shown once and stored only as a hash:

```sh
NETMAPPER_DSN=$OPERATOR_DSN netmapper token create --name grafana --scope read
NETMAPPER_DSN=$OPERATOR_DSN netmapper token list
NETMAPPER_DSN=$OPERATOR_DSN netmapper token revoke --name grafana   # refused from the next request
```

Then:

```sh
H="Authorization: Bearer $TOKEN"
curl -H "$H" api:8080/v1/snapshots              # what can be asked about
curl -H "$H" api:8080/v1/devices                # the devices of the latest graph
curl -H "$H" api:8080/v1/devices/sw1            # by device key, hostname or address
curl -H "$H" api:8080/v1/findings               # what the crawl reported against itself
curl -H "$H" api:8080/v1/observations/42?snapshot=7        # the commands behind one piece of evidence
curl -H "$H" api:8080/v1/observations/42/raw/0?snapshot=7  # the bytes the device printed
```

Every answer names the snapshot it was built from and that snapshot's coverage verdict. A
`quarantined` verdict means the crawl reached much less than the previous one: the graph is served,
and you decide whether to trust it. `?snapshot=<id>` reads an older one. On the observation
endpoints, pass the `snapshot_id` the evidence item carries: without it the lookup searches every
snapshot ever taken and slows down as they pile up. Endpoints, fields and errors are in
[contracts/rest.md](../../specs/005-read-api/contracts/rest.md).

The same calls are in a [Bruno](https://www.usebruno.com) collection, [bruno/](../../bruno/): open
the folder in Bruno, pick the `local` environment and set its secret `token` variable.

## Exposing the API

The API speaks plain HTTP and has no rate limit. Keep it on a private network (a VPN or an overlay
such as Tailscale), or put a TLS-terminating proxy in front of it. Do not expose it to the internet
as it is. There is no unauthenticated endpoint, not even a health check; supervise the process or
its port instead.

## Upgrading

Stop the processes, run `netmapper migrate` with the new binary, start them again. A migration never
rewrites collected data.
