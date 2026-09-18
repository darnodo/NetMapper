# Contract: `netmapper` subcommands used by this feature

All subcommands read `NETMAPPER_DSN` (PostgreSQL). Exit code 0 on success, 1 on runtime error,
2 on invalid input. Logs are JSON lines on stderr (`log/slog`).

## netmapper migrate

Applies embedded migrations. Idempotent.

## netmapper run --config <file.yaml> --perimeter <name> --seed-set <name>

Validates the document (see [config.md](config.md)), stores it as a `config_version`, and starts a
discovery.

- Prints the job id on stdout, nothing else.
- Exit 2 and one line on stderr per problem when FR-001 fails:
  - `perimeter "<name>" not found` / `perimeter "<name>" has no include range`
  - `seed "<target>" is outside perimeter "<name>"` / `seed "<target>" does not resolve`
  - `no credential set covers perimeter "<name>"`
- Opens no device session and resolves no secret.

## netmapper cancel <job-id>

Sets the job to `cancelling`. Exit 2 if the job is not `running`.

## netmapper collector [--id <name>] [--workers 64] [--consume find,scrape] [--packs <dir>]

Claims and runs tasks until SIGTERM. On SIGTERM it stops claiming, lets in-flight steps finish for up
to the lease duration, and exits. Tasks it did not finish return to the frontier when their lease
expires.

Also reads: `NETMAPPER_S3_ENDPOINT`, `NETMAPPER_S3_BUCKET`, `NETMAPPER_S3_ACCESS_KEY`,
`NETMAPPER_S3_SECRET_KEY`, optionally `NETMAPPER_S3_REGION` (default `garage`) and
`NETMAPPER_S3_INSECURE` (any value: plain HTTP), and `VAULT_ADDR` / `VAULT_TOKEN` when a `vault:`
reference is used. Exits 2 at start if an `NETMAPPER_S3_*` variable is missing.
Exits 2 at start if any pack fails to load or fails the read-only lint.

## netmapper engine [--interval 2s]

Runs the job runner: final retry pass, `failed` target sweep, snapshot close, cancellation. Never
reads S3 credentials, Vault variables or packs.
