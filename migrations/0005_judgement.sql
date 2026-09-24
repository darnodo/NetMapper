-- Reported: the coverage verdict on a closed snapshot (002-coverage-gate).

-- +goose Up
CREATE TABLE snapshot_judgement (
    id                   bigserial PRIMARY KEY,
    snapshot_id          bigint NOT NULL REFERENCES snapshot,
    baseline_snapshot_id bigint NULL REFERENCES snapshot,
    classification       text NOT NULL CHECK (classification IN ('published', 'degraded', 'quarantined')),
    coverage             numeric(5,4) NULL CHECK (coverage >= 0 AND coverage <= 1),
    baseline_devices     integer NOT NULL,
    carried_over         integer NOT NULL,
    reached              integer NOT NULL,
    breakdown            jsonb NOT NULL DEFAULT '{}',
    thresholds           jsonb NOT NULL,
    gate_version         integer NOT NULL,
    active               boolean NOT NULL DEFAULT true,
    computed_at          timestamptz NOT NULL DEFAULT now(),
    -- A row either has a baseline and a coverage figure, or neither.
    CHECK ((baseline_snapshot_id IS NULL) = (coverage IS NULL)),
    CHECK (carried_over <= baseline_devices)
);

-- The whole of "exactly one active judgement per snapshot".
CREATE UNIQUE INDEX snapshot_judgement_active ON snapshot_judgement (snapshot_id) WHERE active;

-- The only write path: supersede the current verdict, never rewrite one in place. The engine holds
-- EXECUTE on this and no UPDATE on the table, so a verdict cannot be edited after the fact.
-- +goose StatementBegin
CREATE FUNCTION judge_snapshot(
    p_snapshot_id          bigint,
    p_baseline_snapshot_id bigint,
    p_classification       text,
    p_coverage             numeric,
    p_baseline_devices     integer,
    p_carried_over         integer,
    p_reached              integer,
    p_breakdown            jsonb,
    p_thresholds           jsonb,
    p_gate_version         integer
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
DECLARE
    new_id bigint;
BEGIN
    UPDATE snapshot_judgement SET active = false WHERE snapshot_id = p_snapshot_id AND active;
    INSERT INTO snapshot_judgement (snapshot_id, baseline_snapshot_id, classification, coverage,
                                    baseline_devices, carried_over, reached, breakdown, thresholds,
                                    gate_version)
    VALUES (p_snapshot_id, p_baseline_snapshot_id, p_classification, p_coverage,
            p_baseline_devices, p_carried_over, p_reached, p_breakdown, p_thresholds, p_gate_version)
    RETURNING id INTO new_id;
    RETURN new_id;
END $$;
-- +goose StatementEnd

-- Per-perimeter coverage threshold. Null means the documented default. One number is enough:
-- published is fixed at full coverage by FR-005, so the only boundary left to place is the one
-- between degraded and quarantined.
ALTER TABLE perimeter
    ADD COLUMN degraded_at numeric(5,4) NULL CHECK (degraded_at > 0 AND degraded_at <= 1);

ALTER TABLE snapshot_judgement OWNER TO netmapper_owner;

GRANT SELECT, INSERT ON snapshot_judgement TO netmapper_engine;
GRANT USAGE ON SEQUENCE snapshot_judgement_id_seq TO netmapper_engine;
GRANT EXECUTE ON FUNCTION judge_snapshot(bigint, bigint, text, numeric, integer, integer, integer, jsonb, jsonb, integer) TO netmapper_engine;
GRANT SELECT ON snapshot_judgement TO netmapper_operator, netmapper_collector;

-- +goose Down
DROP FUNCTION judge_snapshot(bigint, bigint, text, numeric, integer, integer, integer, jsonb, jsonb, integer);
DROP TABLE snapshot_judgement;
ALTER TABLE perimeter DROP COLUMN degraded_at;
