-- Feature 007: the facts endpoint serves observations of the active parse generation only, so a
-- parser fix replayed over stored output supersedes what it replaced (constitution II). It needs
-- to read which generation is active. parse_generation holds ids, a timestamp and that flag:
-- nothing a device credential could come from.

-- +goose Up
GRANT SELECT ON parse_generation TO netmapper_api;

-- +goose Down
REVOKE SELECT ON parse_generation FROM netmapper_api;
