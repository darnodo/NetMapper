-- What was collected: append only (principle II). Partitioned per snapshot, composite keys
-- (data-model.md, Partitioning and keys).

-- +goose Up
CREATE TABLE snapshot (
    id        bigserial PRIMARY KEY,
    job_id    bigint NOT NULL REFERENCES job,
    opened_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz NULL,
    state     text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    CHECK ((state = 'closed') = (closed_at IS NOT NULL))
);

-- A closed snapshot is immutable.
-- +goose StatementBegin
CREATE FUNCTION snapshot_closed_is_final() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state = 'closed' THEN
        RAISE EXCEPTION 'snapshot % is closed', OLD.id;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER snapshot_closed_is_final BEFORE UPDATE ON snapshot
    FOR EACH ROW EXECUTE FUNCTION snapshot_closed_is_final();

ALTER TABLE job ADD FOREIGN KEY (snapshot_id) REFERENCES snapshot;

CREATE TABLE parse_generation (
    snapshot_id     bigint NOT NULL REFERENCES snapshot,
    id              bigserial,
    parser_versions jsonb NULL, -- not written in this slice
    created_at      timestamptz NOT NULL DEFAULT now(),
    active          bool NOT NULL,
    PRIMARY KEY (snapshot_id, id)
);
CREATE UNIQUE INDEX parse_generation_one_active ON parse_generation (snapshot_id) WHERE active;

CREATE TABLE raw_object (
    hash      bytea PRIMARY KEY, -- SHA-256 of the bytes; the bytes live at raw/sha256/<hex>
    size      bigint NOT NULL,
    stored_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE observation (
    snapshot_id         bigint NOT NULL REFERENCES snapshot,
    id                  bigserial,
    collected_at        timestamptz NOT NULL DEFAULT now(),
    collector_id        text NOT NULL,
    task_id             bigint NOT NULL,
    target              inet NOT NULL,
    transport           text NULL CHECK (transport IN ('ssh', 'snmp')),
    platform            text NULL,
    recipe_id           text NULL,
    fact_family         text NOT NULL,
    status              text NOT NULL
                        CHECK (status IN ('collected', 'empty', 'unsupported', 'parse_failed', 'unreachable', 'denied')),
    detail              text NULL,
    parse_generation_id bigint NOT NULL,
    parsed              jsonb NULL CHECK (parsed IS NULL OR status = 'collected'),
    PRIMARY KEY (snapshot_id, id),
    UNIQUE (snapshot_id, target, fact_family, task_id),
    FOREIGN KEY (snapshot_id, parse_generation_id) REFERENCES parse_generation (snapshot_id, id)
) PARTITION BY LIST (snapshot_id);

CREATE TABLE observation_raw (
    snapshot_id    bigint NOT NULL,
    observation_id bigint NOT NULL,
    step_id        text NOT NULL,
    command        text NOT NULL,
    hash           bytea NOT NULL REFERENCES raw_object,
    PRIMARY KEY (snapshot_id, observation_id, step_id),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
) PARTITION BY LIST (snapshot_id);

CREATE TABLE identifier_claim (
    snapshot_id    bigint NOT NULL,
    id             bigserial,
    observation_id bigint NOT NULL,
    kind           text NOT NULL,
    subtype        text NULL,
    value          text NOT NULL,
    strength       text NOT NULL CHECK (strength IN ('strong', 'weak')),
    collected_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (snapshot_id, id),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
) PARTITION BY LIST (snapshot_id);
CREATE INDEX identifier_claim_strong ON identifier_claim (snapshot_id, kind, value) WHERE strength = 'strong';

-- The single definition of the advisory lock key for a strong identifier (research R4).
CREATE FUNCTION claim_lock_key(snapshot_id bigint, kind text, value text) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$ SELECT hashtextextended(snapshot_id::text || '/' || kind || '/' || value, 0) $$;

-- +goose StatementBegin
CREATE FUNCTION create_snapshot_partitions(p_snapshot_id bigint) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['observation', 'observation_raw', 'identifier_claim'] LOOP
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES IN (%s)',
                       t || '_' || p_snapshot_id, t, p_snapshot_id);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION create_snapshot_partitions(bigint);
DROP FUNCTION claim_lock_key(bigint, text, text);
DROP TABLE identifier_claim, observation_raw, observation, raw_object, parse_generation;
ALTER TABLE job DROP CONSTRAINT job_snapshot_id_fkey;
DROP TABLE snapshot;
DROP FUNCTION snapshot_closed_is_final();
