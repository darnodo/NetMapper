-- One role per kind of process, grant matrix of research R11. No role but the owner may UPDATE or
-- DELETE the collected zone or audit_log (FR-010). Each process logs in as a member of its role.

-- +goose Up
-- Roles are cluster wide; several schemas (and parallel tests) may run this at once.
-- +goose StatementBegin
DO $$
DECLARE
    r text;
BEGIN
    FOREACH r IN ARRAY ARRAY['netmapper_owner', 'netmapper_operator', 'netmapper_collector', 'netmapper_engine'] LOOP
        BEGIN
            EXECUTE format('CREATE ROLE %I NOLOGIN', r);
        EXCEPTION WHEN duplicate_object OR unique_violation THEN
            NULL;
        END;
    END LOOP;
    EXECUTE format('GRANT USAGE, CREATE ON SCHEMA %I TO netmapper_owner', current_schema());
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO netmapper_operator, netmapper_collector, netmapper_engine', current_schema());
END $$;
-- +goose StatementEnd

ALTER TABLE config_version OWNER TO netmapper_owner;
ALTER TABLE perimeter OWNER TO netmapper_owner;
ALTER TABLE credential_set OWNER TO netmapper_owner;
ALTER TABLE seed_set OWNER TO netmapper_owner;
ALTER TABLE job OWNER TO netmapper_owner;
ALTER TABLE task OWNER TO netmapper_owner;
ALTER TABLE audit_log OWNER TO netmapper_owner;
ALTER TABLE snapshot OWNER TO netmapper_owner;
ALTER TABLE parse_generation OWNER TO netmapper_owner;
ALTER TABLE raw_object OWNER TO netmapper_owner;
ALTER TABLE observation OWNER TO netmapper_owner;
ALTER TABLE observation_raw OWNER TO netmapper_owner;
ALTER TABLE identifier_claim OWNER TO netmapper_owner;
ALTER TABLE finding OWNER TO netmapper_owner;
ALTER TABLE finding_evidence OWNER TO netmapper_owner;
ALTER FUNCTION create_task_partition(bigint) OWNER TO netmapper_owner;
ALTER FUNCTION create_snapshot_partitions(bigint) OWNER TO netmapper_owner;
ALTER FUNCTION claim_lock_key(bigint, text, text) OWNER TO netmapper_owner;
ALTER FUNCTION snapshot_closed_is_final() OWNER TO netmapper_owner;

REVOKE ALL ON FUNCTION create_task_partition(bigint), create_snapshot_partitions(bigint),
    claim_lock_key(bigint, text, text) FROM PUBLIC;

-- operator: netmapper run, netmapper cancel
GRANT SELECT, INSERT ON config_version, perimeter, credential_set, seed_set, task, snapshot, parse_generation
    TO netmapper_operator;
GRANT SELECT, INSERT, UPDATE ON job TO netmapper_operator;
GRANT EXECUTE ON FUNCTION create_task_partition(bigint), create_snapshot_partitions(bigint) TO netmapper_operator;

-- collector
GRANT SELECT ON config_version, perimeter, credential_set, seed_set, job, snapshot, parse_generation
    TO netmapper_collector;
GRANT SELECT, INSERT, UPDATE ON task TO netmapper_collector;
GRANT SELECT, INSERT ON observation, observation_raw, raw_object, identifier_claim, finding, finding_evidence
    TO netmapper_collector;
GRANT INSERT ON audit_log TO netmapper_collector;
GRANT EXECUTE ON FUNCTION claim_lock_key(bigint, text, text) TO netmapper_collector;

-- engine
GRANT SELECT ON config_version, perimeter, credential_set, seed_set, parse_generation, observation,
    observation_raw, raw_object, identifier_claim, finding, finding_evidence, audit_log TO netmapper_engine;
GRANT SELECT, UPDATE ON job, task, snapshot TO netmapper_engine;
GRANT EXECUTE ON FUNCTION claim_lock_key(bigint, text, text) TO netmapper_engine;

-- bigserial columns
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format('GRANT USAGE ON ALL SEQUENCES IN SCHEMA %I TO netmapper_operator, netmapper_collector', current_schema());
END $$;
-- +goose StatementEnd

-- +goose Down
-- Roles are shared by every schema of the cluster and are left in place.
