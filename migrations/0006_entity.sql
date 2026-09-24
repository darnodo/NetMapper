-- Computed: device entities resolved from a closed snapshot's identifier claims, the per-perimeter
-- registry that keeps a device key meaning the same thing in two runs, and the operator decisions
-- that shape both (003-identity-resolution).

-- +goose Up

-- The registry. One row per device ever seen in a perimeter, and the only cross-snapshot state this
-- feature adds. The perimeter is named, not referenced by id: every run posts the configuration
-- document whole, so each run inserts a fresh perimeter row and an id would give every device an
-- empty history (002, research R1).
CREATE TABLE device (
    perimeter_name text NOT NULL,
    key            text NOT NULL,
    weak           boolean NOT NULL DEFAULT false,
    first_seen     timestamptz NOT NULL,
    last_seen      timestamptz NOT NULL,
    minted_from    bigint NOT NULL REFERENCES snapshot,
    PRIMARY KEY (perimeter_name, key)
);

-- Every strong identifier ever attributed to a device. The primary key is the invariant "one
-- identifier belongs to one device": a claim group carrying an identifier already attributed
-- elsewhere is the research R6 case, never an insert conflict to swallow.
CREATE TABLE device_identifier (
    perimeter_name text NOT NULL,
    kind           text NOT NULL,
    value          text NOT NULL,
    key            text NOT NULL,
    first_seen     timestamptz NOT NULL,
    PRIMARY KEY (perimeter_name, kind, value),
    FOREIGN KEY (perimeter_name, key) REFERENCES device
);

-- One row per resolved snapshot. The primary key alone is FR-015's "exactly one current entity
-- set", and the row is what tells an unresolved snapshot from one that resolved to nothing.
CREATE TABLE resolution (
    snapshot_id       bigint PRIMARY KEY REFERENCES snapshot,
    resolver_version  integer NOT NULL,
    decisions_applied bigint NOT NULL,
    entities          integer NOT NULL,
    computed_at       timestamptz NOT NULL DEFAULT now()
);

-- One row per device in one snapshot. The kind check is deliberately narrow: the day the graph
-- projector adds its kinds is a migration, not a silent widening (FR-026).
CREATE TABLE entity (
    id          bigserial PRIMARY KEY,
    snapshot_id bigint NOT NULL REFERENCES snapshot,
    kind        text NOT NULL CHECK (kind = 'device'),
    device_key  text NOT NULL,
    weak        boolean NOT NULL,
    attributes  jsonb NOT NULL DEFAULT '{}',
    first_seen  timestamptz NOT NULL,
    last_seen   timestamptz NOT NULL,
    -- The whole of FR-024: a split or a never-merge that keeps two devices apart has to give them
    -- different keys or fail loudly.
    UNIQUE (snapshot_id, device_key)
);

-- From an entity, its claims; from a claim, its observation; from the observation, observation_raw
-- and the bytes. The first link of the chain Principle I asks for (FR-005).
CREATE TABLE entity_claim (
    entity_id           bigint NOT NULL REFERENCES entity ON DELETE CASCADE,
    snapshot_id         bigint NOT NULL,
    identifier_claim_id bigint NOT NULL,
    PRIMARY KEY (entity_id, snapshot_id, identifier_claim_id),
    FOREIGN KEY (snapshot_id, identifier_claim_id) REFERENCES identifier_claim (snapshot_id, id)
);

-- Append only, replayed on every resolution, never applied in place (FR-009, Principle II).
-- Subjects are device keys, not row ids, which is what makes a decision survive a recomputation.
CREATE TABLE entity_decision (
    id             bigserial PRIMARY KEY,
    perimeter_name text NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('merge', 'split', 'never_merge')),
    subjects       text[] NOT NULL,
    identifier     jsonb NULL,
    actor          text NOT NULL,
    at             timestamptz NOT NULL DEFAULT now(),
    note           text NULL,
    CHECK ((kind = 'split') = (identifier IS NOT NULL)),
    CHECK (array_length(subjects, 1) = CASE WHEN kind = 'split' THEN 1 ELSE 2 END)
);

-- A collision is a data quality problem in the same sense a parse failure is, so it goes on the
-- findings surface 001 already established rather than on a second one (research R11).
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict'));

ALTER TABLE device OWNER TO netmapper_owner;
ALTER TABLE device_identifier OWNER TO netmapper_owner;
ALTER TABLE resolution OWNER TO netmapper_owner;
ALTER TABLE entity OWNER TO netmapper_owner;
ALTER TABLE entity_claim OWNER TO netmapper_owner;
ALTER TABLE entity_decision OWNER TO netmapper_owner;

-- The engine owns the computed zone and must be able to replace it, which is what DELETE means
-- here. The DELETE on finding is what the replace rule of FR-015 costs: it is unrestricted in SQL
-- and restricted in code to identity_conflict rows of the snapshot being resolved (research R12).
GRANT SELECT, INSERT, UPDATE, DELETE ON resolution, entity, entity_claim, device, device_identifier
    TO netmapper_engine;
GRANT USAGE ON SEQUENCE entity_id_seq TO netmapper_engine;
GRANT INSERT, DELETE ON finding, finding_evidence TO netmapper_engine;
-- 001 gave the engine only SELECT on finding, so raising one needs the sequence too. This is the
-- same gap 002 hit with its own sequence (research R12).
GRANT USAGE ON SEQUENCE finding_id_seq TO netmapper_engine;
-- Decisions are the append-only part: read them, never write them from the engine.
GRANT SELECT ON entity_decision TO netmapper_engine;

-- netmapper resolve runs the resolver in the operator's own process, so the operator holds the same
-- write rights on the computed tables, and the same rights on findings: a resolution that finds a
-- collision raises one whichever process ran it. Recording a decision is an insert and never more:
-- no role but the owner may UPDATE or DELETE entity_decision (FR-009).
GRANT SELECT, INSERT, UPDATE, DELETE ON resolution, entity, entity_claim, device, device_identifier
    TO netmapper_operator;
-- The resolver reads the claims before it writes anything, and 001 gave the operator no access to that
-- zone: it is the collector's and the engine's. Read only, on an append-only zone, and it is what
-- quickstart's own queries already assume an operator can do.
GRANT SELECT ON observation, identifier_claim TO netmapper_operator;
GRANT USAGE ON SEQUENCE entity_id_seq TO netmapper_operator;
-- SELECT as well as INSERT and DELETE: PostgreSQL reads the columns of a DELETE's WHERE clause and of
-- a RETURNING clause, so without it the resolver's own statements are refused under this role.
GRANT SELECT, INSERT, DELETE ON finding, finding_evidence TO netmapper_operator;
GRANT SELECT, INSERT ON entity_decision TO netmapper_operator;
GRANT USAGE ON SEQUENCE entity_decision_id_seq TO netmapper_operator;

-- The collector gains nothing: it never reads an entity (data-model.md).

-- +goose Down
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied'));
DROP TABLE entity_decision, entity_claim, entity, resolution, device_identifier, device;
