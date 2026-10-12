-- Computed: the layer 2 domains of a projected snapshot, built with its interfaces and edges from the
-- vlans and interface_vlans families and replaced with them (009-l2domain-projection, research R8).
--
-- Like the rest of the projection, everything here is per snapshot and rebuildable by replay.

-- +goose Up

-- One broadcast domain of one snapshot. key_id is the lowest local VLAN ID among the members, so it
-- equals every member's VLAN ID unless an untagged mismatch joined two of them. A domain is partial
-- when at least one of its links is unchecked; the projector computes both in one place, so no
-- trigger repeats the rule here.
CREATE TABLE l2domain (
    id          bigserial PRIMARY KEY,
    snapshot_id bigint NOT NULL REFERENCES snapshot,
    key_type    text NOT NULL CHECK (key_type IN ('vlan', 'vni')),
    key_id      integer NOT NULL,
    label       text NULL,
    confidence  text NOT NULL CHECK (confidence IN ('complete', 'partial')),
    -- Reserved for an operator decision naming a domain across snapshots. Never written by the
    -- projector.
    pinned_name text NULL
);

-- One device entity in one domain, on one local VLAN. The two observation columns are the evidence
-- of the membership: the vlans observation that declared the VLAN, and the interface_vlans
-- observation that said what the device's ports carry, null when that was not collected (FR-009).
CREATE TABLE l2domain_member (
    l2domain_id          bigint NOT NULL REFERENCES l2domain ON DELETE CASCADE,
    snapshot_id          bigint NOT NULL,
    entity_id            bigint NOT NULL REFERENCES entity ON DELETE CASCADE,
    local_vlan_id        integer NOT NULL CHECK (local_vlan_id BETWEEN 1 AND 4094),
    vlans_observation_id bigint NOT NULL,
    ports_observation_id bigint NULL,
    PRIMARY KEY (l2domain_id, entity_id, local_vlan_id),
    -- FR-004: one declared VLAN of one device is in exactly one domain of the snapshot. A key rather
    -- than a code path that has to remember.
    UNIQUE (snapshot_id, entity_id, local_vlan_id),
    FOREIGN KEY (snapshot_id, vlans_observation_id) REFERENCES observation (snapshot_id, id),
    FOREIGN KEY (snapshot_id, ports_observation_id) REFERENCES observation (snapshot_id, id)
);

-- One cable recorded against one domain. joined: it joins two members. unchecked: it might carry the
-- domain's VLAN but one end's VLAN data is missing, so it joins nothing and makes the domain partial.
CREATE TABLE l2domain_link (
    l2domain_id bigint NOT NULL REFERENCES l2domain ON DELETE CASCADE,
    edge_id     bigint NOT NULL REFERENCES edge ON DELETE CASCADE,
    state       text NOT NULL CHECK (state IN ('joined', 'unchecked')),
    PRIMARY KEY (l2domain_id, edge_id)
);

ALTER TABLE projection ADD COLUMN l2domains integer NOT NULL DEFAULT 0;

-- Two ends of a cable carrying different VLANs untagged: raised and replaced with the projection, as
-- a link_disagreement is (research R7).
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict',
                        'link_disagreement', 'vlan_mismatch'));

ALTER TABLE l2domain OWNER TO netmapper_owner;
ALTER TABLE l2domain_member OWNER TO netmapper_owner;
ALTER TABLE l2domain_link OWNER TO netmapper_owner;

-- Same rights as on edge: the engine sweep and `netmapper project` both replace the set.
GRANT SELECT, INSERT, UPDATE, DELETE ON l2domain, l2domain_member, l2domain_link
    TO netmapper_engine, netmapper_operator;
GRANT USAGE ON SEQUENCE l2domain_id_seq TO netmapper_engine, netmapper_operator;

-- netmapper_api gets nothing: domains are not served yet (FR-018, #7). The collector gets nothing
-- either: it never reads the projection.

-- +goose Down
-- The narrower constraint is validated against the rows already there, so the findings this feature
-- raised go first.
DELETE FROM finding_evidence WHERE finding_id IN (
    SELECT id FROM finding WHERE category = 'vlan_mismatch');
DELETE FROM finding WHERE category = 'vlan_mismatch';
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict',
                        'link_disagreement'));
ALTER TABLE projection DROP COLUMN l2domains;
DROP TABLE l2domain_link, l2domain_member, l2domain;
