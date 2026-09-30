-- SNMPv3 authentication and privacy protocols per credential set (006 data-model.md, research R3).

-- +goose Up
-- Stored with defaults applied, so a snmp_v3 row always names both and a later change of default
-- cannot change what an old run used. The backfill is what existing snmp_v3 rows already meant:
-- the collector sent SHA-1 and AES-128 before the protocols could be chosen. No grant change: the
-- table-level grants cover the new columns, and netmapper_api has none on this table.
ALTER TABLE credential_set
    ADD COLUMN auth_protocol text NULL,
    ADD COLUMN priv_protocol text NULL;

UPDATE credential_set SET auth_protocol = 'sha', priv_protocol = 'aes' WHERE kind = 'snmp_v3';

ALTER TABLE credential_set
    ADD CONSTRAINT credential_set_auth_protocol_v3 CHECK ((kind = 'snmp_v3') = (auth_protocol IS NOT NULL)),
    ADD CONSTRAINT credential_set_priv_protocol_v3 CHECK ((kind = 'snmp_v3') = (priv_protocol IS NOT NULL)),
    ADD CONSTRAINT credential_set_auth_protocol CHECK (auth_protocol IN ('md5', 'sha', 'sha224', 'sha256', 'sha384', 'sha512')),
    ADD CONSTRAINT credential_set_priv_protocol CHECK (priv_protocol IN ('none', 'des', 'aes', 'aes192', 'aes256', 'aes192c', 'aes256c'));

-- +goose Down
ALTER TABLE credential_set DROP COLUMN auth_protocol, DROP COLUMN priv_protocol;
