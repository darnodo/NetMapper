# NetMapper

NetMapper crawls a network read-only over SNMP and SSH, works out which answers came from the same
device, and builds a graph of devices, interfaces and links. It serves that graph over a REST API
where every element carries the observations it was derived from, when they were collected, and how
well it is known, down to the raw command output.

It never changes a device's configuration and never writes back to a source of truth.

## Status

Early. What works today:

- discovery from seed addresses within a perimeter, over SNMP and SSH, with credentials resolved from
  environment variables or Vault/OpenBao;
- a coverage verdict on every crawl (published, degraded, quarantined);
- identity resolution across addresses and crawls;
- interfaces and links (LLDP), with the evidence behind each;
- a read-only REST API behind bearer tokens.

Supported platforms: Arista EOS. Others are added as data, see [writing a pack](docs/how-to/write-a-pack.md).

Not built yet: MCP server, web UI, snapshot diffs, reconciliation with an intent source.

## Install

Binaries for Linux and macOS (amd64, arm64) are attached to each
[release](https://github.com/darnodo/NetMapper/releases). The archive holds the `netmapper` binary,
the platform packs and the licence:

```sh
tar -xzf netmapper_<version>_linux_amd64.tar.gz
cd netmapper_<version>_linux_amd64
./netmapper
```

Or the container image, one image for every role:

```sh
docker pull ghcr.io/darnodo/netmapper:latest
```

## Quick start

A local trial against devices you can reach. It needs Docker and a clone of this repository for the
development PostgreSQL and object store.

```sh
docker compose -f deploy/compose.yaml up -d --wait postgres garage
docker compose -f deploy/compose.yaml run --rm garage-init

export NETMAPPER_DSN='postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable'
export NETMAPPER_S3_ENDPOINT=localhost:3900 NETMAPPER_S3_BUCKET=netmapper NETMAPPER_S3_INSECURE=1 \
       NETMAPPER_S3_ACCESS_KEY=GK0123456789abcdef01234567 \
       NETMAPPER_S3_SECRET_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
export SNMP_COMMUNITY=public SSH_PASSWORD=changeme

./netmapper migrate
./netmapper engine &
./netmapper collector &
```

Describe what to crawl in `netmapper.yaml`:

```yaml
perimeters:
  - name: lab
    include: [192.0.2.0/24]
credential_sets:
  - { name: snmp, kind: snmp_v2c, secret_ref: env:SNMP_COMMUNITY, max_attempts_per_device: 1, perimeters: [lab] }
  - { name: ssh, kind: ssh, username: netmapper, secret_ref: env:SSH_PASSWORD, max_attempts_per_device: 2, perimeters: [lab] }
seed_sets:
  - { name: seeds, targets: [192.0.2.1] }
```

Crawl, then read the result:

```sh
./netmapper run --config netmapper.yaml --perimeter lab --seed-set seeds
TOKEN=$(./netmapper token create --name me --scope read)
./netmapper api &
curl -H "Authorization: Bearer $TOKEN" localhost:8080/v1/devices
```

`run` returns at once. Until the crawl is over and its graph is built, `/v1/devices` answers
`not_projected`; `/v1/snapshots` shows the snapshot `closed` with `has_graph: true` when it is ready.

The development stack logs in as a PostgreSQL superuser, which is fine for a trial and wrong for
anything else: [deploying NetMapper](docs/how-to/deploy.md) sets up one login per process.

## Guides

- [Deploying NetMapper](docs/how-to/deploy.md): database roles, object store, secrets, running each
  process, reading the API.
- [Writing a platform pack](docs/how-to/write-a-pack.md): adding a vendor without changing Go code.

## Reference

- API contract: [specs/005-read-api/contracts/rest.md](specs/005-read-api/contracts/rest.md)
- Configuration document: [specs/001-crawl-loop/contracts/config.md](specs/001-crawl-loop/contracts/config.md)
- Pack format: [specs/001-crawl-loop/contracts/pack-format.md](specs/001-crawl-loop/contracts/pack-format.md)
- Architecture: [docs/c4-model/](docs/c4-model/00-overview.md)
- Project principles: [.specify/memory/constitution.md](.specify/memory/constitution.md)

Each feature's specification, plan and design decisions are in [specs/](specs/).

## Development

```sh
docker compose -f deploy/compose.yaml up -d --wait postgres garage
docker compose -f deploy/compose.yaml run --rm garage-init
export NETMAPPER_TEST_DSN='postgres://netmapper:netmapper@localhost:5432/netmapper?sslmode=disable'
export NETMAPPER_TEST_S3_ENDPOINT=localhost:3900
go test ./...
```

Without the two variables the integration tests skip; the CI refuses a skipped test. Pull requests
run gofmt, go vet, govulncheck and the full suite. Pushing a `v*` tag publishes the binaries and the
image.

## License

[MIT](LICENSE)
