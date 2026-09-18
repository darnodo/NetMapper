-- Control plane: configuration, jobs, the task frontier and the audit trail.

-- +goose Up
CREATE TABLE config_version (
    id        bigserial PRIMARY KEY,
    posted_at timestamptz NOT NULL DEFAULT now(),
    posted_by text NOT NULL,
    document  text NOT NULL -- YAML exactly as given, never rewritten
);

CREATE TABLE perimeter (
    id             bigserial PRIMARY KEY,
    config_version bigint NOT NULL REFERENCES config_version,
    name           text NOT NULL,
    include        cidr[] NOT NULL CHECK (cardinality(include) >= 1),
    exclude        cidr[] NOT NULL DEFAULT '{}',
    UNIQUE (config_version, name)
);

CREATE TABLE credential_set (
    id                      bigserial PRIMARY KEY,
    config_version          bigint NOT NULL REFERENCES config_version,
    name                    text NOT NULL,
    position                int NOT NULL,
    kind                    text NOT NULL CHECK (kind IN ('ssh', 'snmp_v2c', 'snmp_v3')),
    username                text NULL,
    secret_ref              text NOT NULL CHECK (secret_ref LIKE 'env:%' OR secret_ref LIKE 'vault:%'),
    max_attempts_per_device int NOT NULL CHECK (max_attempts_per_device >= 1),
    perimeter_ids           bigint[] NOT NULL CHECK (cardinality(perimeter_ids) >= 1),
    UNIQUE (config_version, name),
    UNIQUE (config_version, position)
);

CREATE TABLE seed_set (
    id             bigserial PRIMARY KEY,
    config_version bigint NOT NULL REFERENCES config_version,
    name           text NOT NULL,
    targets        text[] NOT NULL,
    UNIQUE (config_version, name)
);

CREATE TABLE job (
    id             bigserial PRIMARY KEY,
    type           text NOT NULL DEFAULT 'discovery',
    state          text NOT NULL CHECK (state IN ('running', 'cancelling', 'cancelled', 'succeeded', 'failed')),
    config_version bigint NOT NULL REFERENCES config_version,
    snapshot_id    bigint NULL, -- FK added with snapshot in 0002
    parameters     jsonb NOT NULL DEFAULT '{}',
    requested_by   text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz NULL,
    ended_at       timestamptz NULL,
    error          text NULL
);

CREATE TABLE task (
    id             bigserial,
    job_id         bigint NOT NULL REFERENCES job,
    kind           text NOT NULL CHECK (kind IN ('find', 'scrape')),
    target         inet NOT NULL,
    target_name    text NULL,
    platform       text NULL,
    state          text NOT NULL DEFAULT 'pending'
                   CHECK (state IN ('pending', 'claimed', 'done', 'duplicate', 'skipped', 'failed', 'cancelled')),
    claimed_by     text NULL,
    lease_expires  timestamptz NULL,
    attempts       int NOT NULL DEFAULT 0,
    cred_attempts  jsonb NOT NULL DEFAULT '{}', -- {"<credential_set_id>": {"n": int, "ok": bool}}
    last_error     text NULL,
    parent_task_id bigint NULL, -- same job, audit only, no FK
    skip_reason    text NULL CHECK (skip_reason IS NULL OR state = 'skipped'),
    PRIMARY KEY (job_id, id),
    UNIQUE (job_id, kind, target)
) PARTITION BY LIST (job_id);

CREATE INDEX task_claimable ON task (job_id, state, id);

CREATE TABLE audit_log (
    id      bigserial PRIMARY KEY,
    ref     uuid NOT NULL, -- pairs the `sent` row with its result row
    at      timestamptz NOT NULL DEFAULT now(),
    actor   text NOT NULL,
    action  text NOT NULL CHECK (action IN ('ssh.command', 'ssh.auth', 'snmp.get', 'snmp.walk', 'snmp.auth')),
    target  inet NOT NULL,
    command text NULL, -- CLI command, OID or username; never a credential value
    result  text NOT NULL -- `sent`, then `ok`, `timeout`, `auth_failed` or `error: ...`
);

-- Only the owner of a partitioned table may add partitions, hence SECURITY DEFINER.
-- SET search_path FROM CURRENT pins the schema the migration ran in.
-- +goose StatementBegin
CREATE FUNCTION create_task_partition(p_job_id bigint) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
BEGIN
    EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF task FOR VALUES IN (%s)',
                   'task_' || p_job_id, p_job_id);
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION create_task_partition(bigint);
DROP TABLE audit_log, task, job, seed_set, credential_set, perimeter, config_version;
