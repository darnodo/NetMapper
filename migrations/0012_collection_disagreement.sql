-- +goose Up
-- A row the commands of one collection disagree about, such as a BGP peer that connected or left
-- between two commands: kept out of the observation and raised as a finding naming the device and
-- the row (issue #44, feature 010 research R14).
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict',
                        'link_disagreement', 'vlan_mismatch', 'collection_disagreement'));

-- +goose Down
-- The narrower constraint is validated against the rows already there, so these findings go first.
DELETE FROM finding_evidence WHERE finding_id IN (
    SELECT id FROM finding WHERE category = 'collection_disagreement');
DELETE FROM finding WHERE category = 'collection_disagreement';
ALTER TABLE finding DROP CONSTRAINT finding_category_check;
ALTER TABLE finding ADD CONSTRAINT finding_category_check
    CHECK (category IN ('unknown_platform', 'parse_failed', 'credential_denied', 'identity_conflict',
                        'link_disagreement', 'vlan_mismatch'));
