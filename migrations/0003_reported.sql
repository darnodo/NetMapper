-- Reported: findings and the observations they cite.

-- +goose Up
CREATE TABLE finding (
    id          bigserial PRIMARY KEY,
    snapshot_id bigint NOT NULL REFERENCES snapshot,
    domain      text NOT NULL CHECK (domain IN ('data_quality', 'compliance')),
    category    text NOT NULL CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied')),
    severity    text NOT NULL,
    subject_ref text NOT NULL,
    detail      jsonb NOT NULL DEFAULT '{}',
    state       text NOT NULL DEFAULT 'open'
);

CREATE TABLE finding_evidence (
    finding_id     bigint NOT NULL REFERENCES finding,
    snapshot_id    bigint NOT NULL,
    observation_id bigint NOT NULL,
    PRIMARY KEY (finding_id, snapshot_id, observation_id),
    FOREIGN KEY (snapshot_id, observation_id) REFERENCES observation (snapshot_id, id)
);

-- +goose Down
DROP TABLE finding_evidence, finding;
