-- Computed: the interfaces of each resolved device and the edges between them, projected by replay
-- from a closed snapshot's collected zone and its entity set (004-graph-projector).
--
-- Everything here is per snapshot. Unlike 003 this feature adds no cross-snapshot table: an edge is
-- named by its endpoints, so there is no registry to keep and nothing two snapshots share.

-- +goose Up

-- One row per projected snapshot. The primary key alone is FR-018's "exactly one current set", and
-- the row is what tells an unprojected snapshot from one that projected to nothing.
--
-- resolution_at is what makes a re-resolution repair itself: interface cascades from entity, so
-- re-resolving a snapshot destroys its projection, and this row would otherwise claim a set that is
-- gone. The sweep takes a snapshot whose resolution_at no longer matches (research R12).
CREATE TABLE projection (
    snapshot_id       bigint PRIMARY KEY REFERENCES snapshot,
    projector_version integer NOT NULL,
    resolution_at     timestamptz NOT NULL,
    interfaces        integer NOT NULL,
    edges             integer NOT NULL,
    computed_at       timestamptz NOT NULL DEFAULT now()
);

-- One port of one device entity. The unique index is the whole of FR-002: a name identifies a port
-- inside one device and nowhere else, so two devices may each have a port1.
--
-- Everything but the name is nullable, because a port known only because a neighbour reported it has
-- no operational facts at all, and a device whose interfaces recipe failed still keeps its ports
-- through the reports about them. The two state checks are the interfaces fact family's own enums
-- verbatim, so a value the parser accepted cannot be rejected here.
CREATE TABLE interface (
    id             bigserial PRIMARY KEY,
    entity_id      bigint NOT NULL REFERENCES entity ON DELETE CASCADE,
    snapshot_id    bigint NOT NULL REFERENCES snapshot,
    canonical_name text NOT NULL,
    source         text NOT NULL CHECK (source IN ('device', 'neighbour')),
    description    text NULL,
    admin_state    text NULL CHECK (admin_state IN ('up', 'down')),
    oper_state     text NULL CHECK (oper_state IN ('up', 'down', 'other')),
    speed_bps      bigint NULL,
    mtu            integer NULL,
    mac            text NULL,
    first_seen     timestamptz NOT NULL,
    last_seen      timestamptz NOT NULL,
    UNIQUE (entity_id, canonical_name)
);

-- Every spelling of an interface, with where it came from: the record of why two reports of one
-- cable are one cable. A spelling equal to the canonical name is recorded like any other, so a
-- reader never has to infer the canonical spelling from a missing row (FR-004, research R5).
CREATE TABLE interface_alias (
    interface_id   bigint NOT NULL REFERENCES interface ON DELETE CASCADE,
    spelling       text NOT NULL,
    source         text NOT NULL CHECK (source IN ('device', 'neighbour')),
    snapshot_id    bigint NOT NULL,
    observation_id bigint NOT NULL,
    PRIMARY KEY (interface_id, spelling),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
);

-- FR-007 for interfaces: from a port to its observations, and from there to observation_raw, the
-- bytes and collected_at.
CREATE TABLE interface_evidence (
    interface_id   bigint NOT NULL REFERENCES interface ON DELETE CASCADE,
    snapshot_id    bigint NOT NULL,
    observation_id bigint NOT NULL,
    PRIMARY KEY (interface_id, observation_id),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
);

-- A relationship between two endpoints in one snapshot. An endpoint is a text reference:
--   if:<device_key>/<canonical>      a port of a resolved device
--   dev:<device_key>                 a resolved device as a whole
--   addr:<address>                   an address a device answered on
--   unknown:<kind>=<value>[/<port>]  a far end no entity accounts for
--
-- name is generated rather than written by the projector so it cannot drift from the columns it is
-- built from, and FR-009's "one link, not two" then rests on a key rather than on a code path that
-- has to remember to deduplicate (research R6).
CREATE TABLE edge (
    id                bigserial PRIMARY KEY,
    snapshot_id       bigint NOT NULL REFERENCES snapshot,
    type              text NOT NULL CHECK (type IN ('l1_link', 'has_address')),
    from_ref          text NOT NULL,
    to_ref            text NOT NULL,
    name              text GENERATED ALWAYS AS (type || ':' || from_ref || '|' || to_ref) STORED,
    from_entity_id    bigint NULL REFERENCES entity ON DELETE CASCADE,
    to_entity_id      bigint NULL REFERENCES entity ON DELETE CASCADE,
    from_interface_id bigint NULL REFERENCES interface ON DELETE CASCADE,
    to_interface_id   bigint NULL REFERENCES interface ON DELETE CASCADE,
    confidence        text NOT NULL CHECK (confidence IN ('both_ends', 'one_end', 'direct')),
    attributes        jsonb NOT NULL DEFAULT '{}',
    first_seen        timestamptz NOT NULL,
    last_seen         timestamptz NOT NULL,
    UNIQUE (snapshot_id, name),
    -- One cable has one name whichever end is read. The C collation is not optional: the projector
    -- orders in Go, which compares bytes, and the database's own collation does not. A reference
    -- contains : and /, so without it the two disagree on some pairs of port names and an insert
    -- fails months later (research R7). A report whose two references are equal, a port claiming to
    -- see itself, is dropped by the projector rather than written.
    CONSTRAINT edge_l1_link_is_ordered
        CHECK (type <> 'l1_link' OR from_ref COLLATE "C" < to_ref COLLATE "C"),
    -- Both types start at a device the snapshot resolved. For l1_link this holds by construction,
    -- since 'if:' sorts before 'unknown:'; for has_address the type fixes the direction.
    CONSTRAINT edge_from_is_resolved CHECK (from_entity_id IS NOT NULL)
);

-- side is what makes "both ends agreed" readable from the evidence and not only from the confidence
-- column: a both_ends edge has a row per side, a one_end edge has one.
CREATE TABLE edge_evidence (
    edge_id        bigint NOT NULL REFERENCES edge ON DELETE CASCADE,
    snapshot_id    bigint NOT NULL,
    observation_id bigint NOT NULL,
    side           text NOT NULL CHECK (side IN ('from', 'to')),
    PRIMARY KEY (edge_id, observation_id, side),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
);

-- Two devices contradicting each other about which ports are cabled is a data quality problem of the
-- same shape an identity collision is, so it goes on the surface 001 established rather than a third
-- one (research R9).
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict',
                        'link_disagreement'));

ALTER TABLE projection OWNER TO netmapper_owner;
ALTER TABLE interface OWNER TO netmapper_owner;
ALTER TABLE interface_alias OWNER TO netmapper_owner;
ALTER TABLE interface_evidence OWNER TO netmapper_owner;
ALTER TABLE edge OWNER TO netmapper_owner;
ALTER TABLE edge_evidence OWNER TO netmapper_owner;

-- The engine owns the computed zone and must be able to replace it, which is what DELETE means here.
GRANT SELECT, INSERT, UPDATE, DELETE
    ON projection, interface, interface_alias, interface_evidence, edge, edge_evidence
    TO netmapper_engine;
GRANT USAGE ON SEQUENCE interface_id_seq, edge_id_seq TO netmapper_engine;

-- netmapper project runs the projector in the operator's own process, so the operator holds the same
-- write rights. The reads it needs on observation, entity, entity_claim and identifier_claim it
-- already holds from 001 and 003, and so are the rights on finding: 003 gave both roles
-- SELECT, INSERT, DELETE plus the sequence, which is all a link_disagreement needs.
GRANT SELECT, INSERT, UPDATE, DELETE
    ON projection, interface, interface_alias, interface_evidence, edge, edge_evidence
    TO netmapper_operator;
GRANT USAGE ON SEQUENCE interface_id_seq, edge_id_seq TO netmapper_operator;
-- No read grant is added for either role. The projector reads snapshot, resolution, entity,
-- entity_claim, identifier_claim, observation, parse_generation and task, and both roles already hold
-- SELECT on all of them from 001 and 003.

-- The collector gains nothing: it never reads an interface or an edge.

-- +goose Down
-- The narrower constraint is validated against the rows already there, so the findings this feature
-- raised have to go first or the rollback fails on any snapshot that had a disagreement.
DELETE FROM finding_evidence WHERE finding_id IN (
    SELECT id FROM finding WHERE category = 'link_disagreement');
DELETE FROM finding WHERE category = 'link_disagreement';
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict'));
DROP TABLE edge_evidence, edge, interface_evidence, interface_alias, interface, projection;
