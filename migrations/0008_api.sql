-- The read API: one control-plane table for bearer tokens, and the role the exposed process logs in
-- as (005 data-model.md, research R3 to R6).

-- +goose Up
-- The token value is never stored: hash is the SHA-256 of the string a caller presents, and it is
-- what authentication looks up (research R4). Scope values are deliberately not checked here: the
-- interface refuses a value it does not define (R13), and pinning the set in the schema would make
-- adding the second scope a migration before it is a decision.
CREATE TABLE api_token (
    id           bigserial PRIMARY KEY,
    name         text NOT NULL UNIQUE,
    hash         bytea NOT NULL UNIQUE,
    scopes       text[] NOT NULL CHECK (cardinality(scopes) > 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NULL,
    revoked_at   timestamptz NULL
);
ALTER TABLE api_token OWNER TO netmapper_owner;

-- Roles are cluster wide; several schemas (and parallel tests) may run this at once.
-- +goose StatementBegin
DO $$
BEGIN
    BEGIN
        CREATE ROLE netmapper_api NOLOGIN;
    EXCEPTION WHEN duplicate_object OR unique_violation THEN
        NULL;
    END;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO netmapper_api', current_schema());
END $$;
-- +goose StatementEnd

-- api: SELECT on what it serves, nothing else. entity_claim and identifier_claim are the device's own
-- evidence chain (data-model.md, "The evidence chain"). No grant on credential_set, config_version,
-- perimeter or seed_set: it cannot return a secret reference it cannot read. No grant on task, job,
-- entity_decision or audit_log either.
GRANT SELECT ON snapshot, snapshot_judgement, projection, resolution,
                entity, entity_claim, identifier_claim,
                interface, interface_alias, interface_evidence, edge, edge_evidence,
                observation, observation_raw, raw_object,
                finding, finding_evidence,
                api_token
    TO netmapper_api;
-- The one write the exposed process makes (research R6), narrowed to the one column it needs, so the
-- role can never un-revoke a token, change its scopes or replace its hash.
GRANT UPDATE (last_used_at) ON api_token TO netmapper_api;

-- operator: netmapper token create|list|revoke. UPDATE is what revocation is. No role may DELETE a
-- token, so a revoked one stays visible and a name is never quietly reused.
GRANT SELECT, INSERT, UPDATE ON api_token TO netmapper_operator;
GRANT USAGE ON SEQUENCE api_token_id_seq TO netmapper_operator;

-- +goose Down
-- The role is shared by every schema of the cluster and is left in place.
DROP TABLE api_token;
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE format('REVOKE USAGE ON SCHEMA %I FROM netmapper_api', current_schema());
END $$;
-- +goose StatementEnd
