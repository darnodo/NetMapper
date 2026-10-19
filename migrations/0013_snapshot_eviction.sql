-- Snapshot eviction and pinning (012-snapshot-eviction). A snapshot can be evicted: everything
-- collected or computed for it goes, its stored outputs no other snapshot uses go from the object
-- store, and its row stays as a tombstone with its verdict. A pinned snapshot is never evicted.
--
-- The operator runs all of it, but no role but the owner may delete the collected zone or drop its
-- partitions (0004), so each action is an owner function with a narrow EXECUTE grant, as
-- create_snapshot_partitions and judge_snapshot already are (research R1).

-- +goose Up

-- Lifecycle metadata, which is not content (constitution, Principle II): the pin flag, the two
-- eviction states and when the eviction started. A tombstone keeps its close time.
ALTER TABLE snapshot DROP CONSTRAINT snapshot_state_check;
ALTER TABLE snapshot DROP CONSTRAINT snapshot_check;
ALTER TABLE snapshot
    ADD COLUMN pinned     boolean NOT NULL DEFAULT false,
    ADD COLUMN evicted_at timestamptz NULL,
    ADD CONSTRAINT snapshot_state_check CHECK (state IN ('open', 'closed', 'evicting', 'evicted')),
    ADD CONSTRAINT snapshot_closed_at_check CHECK ((state = 'open') = (closed_at IS NULL)),
    ADD CONSTRAINT snapshot_evicted_at_check CHECK ((state IN ('evicting', 'evicted')) = (evicted_at IS NOT NULL));

-- Replaces snapshot_closed_is_final. On a closed snapshot, only the pin flag may change, or the
-- move to evicting; an evicting one may only become evicted; an evicted one is final. Rows are
-- compared whole, minus the columns a transition may change, so a column added later is covered
-- without touching this function.
-- +goose StatementBegin
CREATE FUNCTION snapshot_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.state = 'open' THEN
        RETURN NEW;
    END IF;
    IF OLD.state = 'closed' THEN
        IF NEW.state = 'closed' AND to_jsonb(NEW) - 'pinned' = to_jsonb(OLD) - 'pinned' THEN
            RETURN NEW;
        END IF;
        IF NEW.state = 'evicting' AND NEW.evicted_at IS NOT NULL
           AND to_jsonb(NEW) - 'state' - 'evicted_at' = to_jsonb(OLD) - 'state' - 'evicted_at' THEN
            RETURN NEW;
        END IF;
    ELSIF OLD.state = 'evicting' THEN
        IF NEW.state = 'evicted' AND to_jsonb(NEW) - 'state' = to_jsonb(OLD) - 'state' THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'snapshot % is %: update refused', OLD.id, OLD.state;
END $$;
-- +goose StatementEnd

DROP TRIGGER snapshot_closed_is_final ON snapshot;
CREATE TRIGGER snapshot_lifecycle BEFORE UPDATE ON snapshot
    FOR EACH ROW EXECUTE FUNCTION snapshot_lifecycle();
ALTER FUNCTION snapshot_lifecycle() OWNER TO netmapper_owner;

-- The audit trail takes the three operator actions. They act on a snapshot, not on a device, so
-- they have no target; the snapshot is named in command (research R11).
ALTER TABLE audit_log DROP CONSTRAINT audit_log_action_check;
ALTER TABLE audit_log
    ALTER COLUMN target DROP NOT NULL,
    ADD CONSTRAINT audit_log_action_check
        CHECK (action IN ('ssh.command', 'ssh.auth', 'snmp.get', 'snmp.walk', 'snmp.auth',
                          'snapshot.evict', 'snapshot.pin', 'snapshot.unpin')),
    ADD CONSTRAINT audit_log_target_check CHECK (target IS NOT NULL OR action LIKE 'snapshot.%');
GRANT INSERT ON audit_log TO netmapper_operator;

-- The crawl gate: one advisory lock taken by every crawl start and every eviction, so that a crawl
-- never starts while an eviction is unfinished and an eviction never runs while a crawl does
-- (research R4, R6).
CREATE FUNCTION crawl_gate_key() RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$ SELECT hashtextextended('netmapper/crawl-gate', 0) $$;
ALTER FUNCTION crawl_gate_key() OWNER TO netmapper_owner;
REVOKE ALL ON FUNCTION crawl_gate_key() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION crawl_gate_key() TO netmapper_operator;

-- The stored outputs an eviction still has to delete from the object store. Written in the
-- transaction that removes the rows, emptied as objects go: once the rows are gone, nothing else
-- says which objects the snapshot used (research R5). size is kept for the report.
CREATE TABLE eviction_object (
    snapshot_id bigint NOT NULL REFERENCES snapshot,
    hash        bytea NOT NULL,
    size        bigint NOT NULL,
    PRIMARY KEY (snapshot_id, hash)
);
ALTER TABLE eviction_object OWNER TO netmapper_owner;
GRANT SELECT, DELETE ON eviction_object TO netmapper_operator;

-- The preview and the eviction are one function, so they cannot disagree (research R2). It takes
-- the crawl gate, the resolver's perimeter lock and the projector's snapshot lock, in that order,
-- then runs the refusal checks of research R7 and counts. With p_apply it removes the snapshot's
-- data in the order of research R3 and leaves it evicting; the caller deletes the listed objects
-- and calls finish_snapshot_eviction.
-- +goose StatementBegin
CREATE FUNCTION evict_snapshot(p_snapshot_id bigint, p_apply boolean)
RETURNS TABLE (outcome text, reason text, blocking_snapshot bigint, blocking_job bigint,
               observations bigint, objects_deleted bigint, bytes_deleted bigint,
               objects_kept bigint, devices_deleted bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
#variable_conflict use_column
DECLARE
    v_state     text;
    v_pinned    boolean;
    v_job       bigint;
    v_perimeter text;
    p           text;
    attempt     integer;
    v_resolved  boolean;
BEGIN
    outcome := NULL; reason := NULL; blocking_snapshot := NULL; blocking_job := NULL;
    observations := 0; objects_deleted := 0; bytes_deleted := 0; objects_kept := 0;
    devices_deleted := 0;

    PERFORM pg_advisory_xact_lock(crawl_gate_key());

    SELECT s.job_id, per.name INTO v_job, v_perimeter
    FROM snapshot s
    JOIN job j ON j.id = s.job_id
    LEFT JOIN perimeter per ON per.id = (j.parameters->>'perimeter_id')::bigint
    WHERE s.id = p_snapshot_id;
    IF NOT FOUND THEN
        outcome := 'unknown';
        RETURN NEXT;
        RETURN;
    END IF;
    IF v_perimeter IS NOT NULL THEN
        -- The resolver's lock (internal/entity/entity.go): the registry does not shrink under a
        -- resolution of the same perimeter.
        PERFORM pg_advisory_xact_lock(hashtextextended(v_perimeter, 0));
    END IF;
    -- The projector's lock (projectionLockClass = 4 in internal/graph/graph.go).
    PERFORM pg_advisory_xact_lock(4, p_snapshot_id::integer);
    SELECT s.state, s.pinned INTO v_state, v_pinned FROM snapshot s WHERE s.id = p_snapshot_id FOR UPDATE;

    IF v_state = 'evicted' THEN
        outcome := 'noop';
        RETURN NEXT;
        RETURN;
    END IF;
    IF v_state = 'evicting' THEN
        outcome := 'resume';
        SELECT count(*), coalesce(sum(o.size), 0) INTO objects_deleted, bytes_deleted
        FROM eviction_object o WHERE o.snapshot_id = p_snapshot_id;
        RETURN NEXT;
        RETURN;
    END IF;

    outcome := 'refused';
    IF v_state = 'open' THEN
        reason := 'open';
        RETURN NEXT;
        RETURN;
    END IF;
    IF v_pinned THEN
        reason := 'pinned';
        RETURN NEXT;
        RETURN;
    END IF;
    SELECT j.id, j.snapshot_id INTO blocking_job, blocking_snapshot
    FROM job j WHERE j.state IN ('running', 'cancelling') ORDER BY j.id LIMIT 1;
    IF blocking_job IS NOT NULL THEN
        reason := 'crawl_running';
        RETURN NEXT;
        RETURN;
    END IF;
    -- A resolved snapshot carries entities, so evicting it shrinks the registry (R8), which is
    -- only safe once every other snapshot of the perimeter has its entities too. An unresolved one
    -- carries none: evicting it removes no reference, so it skips both this check and the cleanup.
    -- Otherwise two unresolved snapshots whose resolution keeps failing would each block the
    -- other's eviction for ever.
    v_resolved := EXISTS (SELECT 1 FROM resolution r WHERE r.snapshot_id = p_snapshot_id);
    IF v_resolved THEN
        SELECT s.id INTO blocking_snapshot
        FROM snapshot s
        JOIN job j ON j.id = s.job_id
        JOIN perimeter per ON per.id = (j.parameters->>'perimeter_id')::bigint
        WHERE per.name = v_perimeter AND s.id <> p_snapshot_id AND s.state = 'closed'
          AND NOT EXISTS (SELECT 1 FROM resolution r WHERE r.snapshot_id = s.id)
        ORDER BY s.id LIMIT 1;
        IF blocking_snapshot IS NOT NULL THEN
            reason := 'unresolved_sibling';
            RETURN NEXT;
            RETURN;
        END IF;
    END IF;

    -- What would go. Only outputs this snapshot referenced are candidates; an output another
    -- snapshot still references is kept, and outputs orphaned before are not looked at (R4).
    CREATE TEMP TABLE IF NOT EXISTS evict_candidate (hash bytea PRIMARY KEY, size bigint NOT NULL, shared boolean NOT NULL) ON COMMIT DROP;
    TRUNCATE evict_candidate;
    INSERT INTO evict_candidate (hash, size, shared)
    SELECT m.hash, o.size,
           EXISTS (SELECT 1 FROM observation_raw r WHERE r.hash = m.hash AND r.snapshot_id <> p_snapshot_id)
    FROM (SELECT DISTINCT r.hash FROM observation_raw r WHERE r.snapshot_id = p_snapshot_id) m
    JOIN raw_object o ON o.hash = m.hash;

    SELECT count(*) INTO observations FROM observation o WHERE o.snapshot_id = p_snapshot_id;
    SELECT count(*) FILTER (WHERE NOT c.shared), coalesce(sum(c.size) FILTER (WHERE NOT c.shared), 0),
           count(*) FILTER (WHERE c.shared)
    INTO objects_deleted, bytes_deleted, objects_kept
    FROM evict_candidate c;

    -- Registry entries of the perimeter that no entity of a remaining snapshot carries (R8).
    CREATE TEMP TABLE IF NOT EXISTS evict_device (key text PRIMARY KEY) ON COMMIT DROP;
    TRUNCATE evict_device;
    INSERT INTO evict_device (key)
    SELECT d.key FROM device d
    WHERE v_resolved AND d.perimeter_name = v_perimeter
      AND NOT EXISTS (
          SELECT 1 FROM entity e
          JOIN snapshot s ON s.id = e.snapshot_id
          JOIN job j ON j.id = s.job_id
          JOIN perimeter per ON per.id = (j.parameters->>'perimeter_id')::bigint
          WHERE e.device_key = d.key AND per.name = v_perimeter
            AND s.id <> p_snapshot_id AND s.state NOT IN ('evicting', 'evicted'));
    SELECT count(*) INTO devices_deleted FROM evict_device;

    IF NOT p_apply THEN
        outcome := 'preview';
        RETURN NEXT;
        RETURN;
    END IF;

    -- Detaching and dropping a partition takes an ACCESS EXCLUSIVE lock on its parent and on every
    -- table a foreign key ties to it. Waited for one by one, those locks deadlock with API reads,
    -- which hold theirs until the request ends and read the tables in different orders depending on
    -- the endpoint: no fixed order avoids them all. So they are taken all at once without waiting;
    -- if any table is busy, the subtransaction rolls back, which releases what it took, and the
    -- eviction tries again a moment later. It never holds one of these locks while waiting for
    -- another (research R3). The list is every table the steps below lock exclusively; a foreign key
    -- added to one of the partitioned tables adds its table here.
    FOR attempt IN 1..600 LOOP
        BEGIN
            LOCK TABLE observation_raw, identifier_claim, observation, task,
                       snapshot, job, parse_generation, raw_object,
                       finding_evidence, entity_claim, interface_alias, interface_evidence,
                       edge_evidence, l2domain_member
                IN ACCESS EXCLUSIVE MODE NOWAIT;
            EXIT;
        EXCEPTION WHEN lock_not_available THEN
            IF attempt = 600 THEN
                RAISE EXCEPTION 'snapshot %: the collected tables stayed busy for 30s, nothing was changed; retry', p_snapshot_id;
            END IF;
            PERFORM pg_sleep(0.05);
        END;
    END LOOP;

    INSERT INTO eviction_object (snapshot_id, hash, size)
    SELECT p_snapshot_id, c.hash, c.size FROM evict_candidate c WHERE NOT c.shared;

    -- The computed zone. Deleting the entities cascades to their claims, interfaces, aliases, edges,
    -- evidence and L2 domain members and links; the explicit deletes keep the order readable.
    DELETE FROM l2domain l WHERE l.snapshot_id = p_snapshot_id;
    DELETE FROM finding_evidence fe
    WHERE fe.snapshot_id = p_snapshot_id
       OR fe.finding_id IN (SELECT f.id FROM finding f WHERE f.snapshot_id = p_snapshot_id);
    DELETE FROM finding f WHERE f.snapshot_id = p_snapshot_id;
    DELETE FROM projection pr WHERE pr.snapshot_id = p_snapshot_id;
    DELETE FROM edge e WHERE e.snapshot_id = p_snapshot_id;
    DELETE FROM interface i WHERE i.snapshot_id = p_snapshot_id;
    DELETE FROM entity e WHERE e.snapshot_id = p_snapshot_id;
    DELETE FROM resolution r WHERE r.snapshot_id = p_snapshot_id;

    -- The collected zone, a partition drop (001 data-model.md). observation last: the other two
    -- reference it.
    FOREACH p IN ARRAY ARRAY['observation_raw', 'identifier_claim', 'observation'] LOOP
        IF to_regclass(p || '_' || p_snapshot_id) IS NOT NULL THEN
            EXECUTE format('ALTER TABLE %I DETACH PARTITION %I', p, p || '_' || p_snapshot_id);
            EXECUTE format('DROP TABLE %I', p || '_' || p_snapshot_id);
        END IF;
    END LOOP;
    -- The crawl's task list: task is partitioned by job, and a job has one snapshot.
    IF to_regclass('task_' || v_job) IS NOT NULL THEN
        EXECUTE format('ALTER TABLE task DETACH PARTITION %I', 'task_' || v_job);
        EXECUTE format('DROP TABLE %I', 'task_' || v_job);
    END IF;
    DELETE FROM parse_generation g WHERE g.snapshot_id = p_snapshot_id;
    DELETE FROM raw_object o WHERE o.hash IN (SELECT c.hash FROM evict_candidate c WHERE NOT c.shared);

    DELETE FROM device_identifier di
    WHERE di.perimeter_name = v_perimeter AND di.key IN (SELECT k.key FROM evict_device k);
    DELETE FROM device d
    WHERE d.perimeter_name = v_perimeter AND d.key IN (SELECT k.key FROM evict_device k);

    UPDATE snapshot s SET state = 'evicting', evicted_at = now() WHERE s.id = p_snapshot_id;
    outcome := 'evicting';
    RETURN NEXT;
END $$;
-- +goose StatementEnd
ALTER FUNCTION evict_snapshot(bigint, boolean) OWNER TO netmapper_owner;
REVOKE ALL ON FUNCTION evict_snapshot(bigint, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION evict_snapshot(bigint, boolean) TO netmapper_operator;

-- The last step of an eviction, once every listed object is gone from the store.
-- +goose StatementBegin
CREATE FUNCTION finish_snapshot_eviction(p_snapshot_id bigint) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM snapshot WHERE id = p_snapshot_id AND state = 'evicting') THEN
        RAISE EXCEPTION 'snapshot % is not being evicted', p_snapshot_id;
    END IF;
    IF EXISTS (SELECT 1 FROM eviction_object WHERE snapshot_id = p_snapshot_id) THEN
        RAISE EXCEPTION 'snapshot % still has stored outputs to delete', p_snapshot_id;
    END IF;
    UPDATE snapshot SET state = 'evicted' WHERE id = p_snapshot_id;
END $$;
-- +goose StatementEnd
ALTER FUNCTION finish_snapshot_eviction(bigint) OWNER TO netmapper_owner;
REVOKE ALL ON FUNCTION finish_snapshot_eviction(bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION finish_snapshot_eviction(bigint) TO netmapper_operator;

-- Pinning protects from eviction and changes nothing else. Returns ok, noop, refused or unknown.
-- +goose StatementBegin
CREATE FUNCTION set_snapshot_pinned(p_snapshot_id bigint, p_pinned boolean) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
DECLARE
    v_state  text;
    v_pinned boolean;
BEGIN
    SELECT state, pinned INTO v_state, v_pinned FROM snapshot WHERE id = p_snapshot_id FOR UPDATE;
    IF NOT FOUND THEN
        RETURN 'unknown';
    END IF;
    IF v_state IN ('evicting', 'evicted') THEN
        RETURN 'refused';
    END IF;
    IF v_pinned = p_pinned THEN
        RETURN 'noop';
    END IF;
    UPDATE snapshot SET pinned = p_pinned WHERE id = p_snapshot_id;
    RETURN 'ok';
END $$;
-- +goose StatementEnd
ALTER FUNCTION set_snapshot_pinned(bigint, boolean) OWNER TO netmapper_owner;
REVOKE ALL ON FUNCTION set_snapshot_pinned(bigint, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION set_snapshot_pinned(bigint, boolean) TO netmapper_operator;

-- A verdict is never written for a snapshot, or against a baseline, that is no longer closed: a
-- sweep that chose them just before an eviction committed stops here, and the next one chooses
-- again (research R6). Same signature, so the owner and the grants are kept.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION judge_snapshot(
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
    n      integer;
BEGIN
    SELECT count(*) INTO n FROM (
        SELECT 1 FROM snapshot
        WHERE id IN (p_snapshot_id, p_baseline_snapshot_id) AND state = 'closed'
        FOR SHARE) x;
    IF n <> 1 + (p_baseline_snapshot_id IS NOT NULL)::integer THEN
        RAISE EXCEPTION 'snapshot % or its baseline % is not closed', p_snapshot_id, p_baseline_snapshot_id;
    END IF;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION judge_snapshot(
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
DROP FUNCTION set_snapshot_pinned(bigint, boolean);
DROP FUNCTION finish_snapshot_eviction(bigint);
DROP FUNCTION evict_snapshot(bigint, boolean);
DROP TABLE eviction_object;
DROP FUNCTION crawl_gate_key();

-- The narrower audit_log constraints are validated against the rows already there.
DELETE FROM audit_log WHERE action LIKE 'snapshot.%';
REVOKE INSERT ON audit_log FROM netmapper_operator;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_check;
ALTER TABLE audit_log DROP CONSTRAINT audit_log_action_check;
ALTER TABLE audit_log
    ALTER COLUMN target SET NOT NULL,
    ADD CONSTRAINT audit_log_action_check
        CHECK (action IN ('ssh.command', 'ssh.auth', 'snmp.get', 'snmp.walk', 'snmp.auth'));

DROP TRIGGER snapshot_lifecycle ON snapshot;
DROP FUNCTION snapshot_lifecycle();
CREATE TRIGGER snapshot_closed_is_final BEFORE UPDATE ON snapshot
    FOR EACH ROW EXECUTE FUNCTION snapshot_closed_is_final();

-- Tombstones cannot go back to a state the old check allows; they stay as closed rows with nothing
-- behind them.
ALTER TABLE snapshot DISABLE TRIGGER snapshot_closed_is_final;
UPDATE snapshot SET state = 'closed', evicted_at = NULL WHERE state IN ('evicting', 'evicted');
ALTER TABLE snapshot ENABLE TRIGGER snapshot_closed_is_final;
ALTER TABLE snapshot DROP CONSTRAINT snapshot_evicted_at_check;
ALTER TABLE snapshot DROP CONSTRAINT snapshot_closed_at_check;
ALTER TABLE snapshot DROP CONSTRAINT snapshot_state_check;
ALTER TABLE snapshot DROP COLUMN evicted_at, DROP COLUMN pinned;
ALTER TABLE snapshot
    ADD CONSTRAINT snapshot_state_check CHECK (state IN ('open', 'closed')),
    ADD CONSTRAINT snapshot_check CHECK ((state = 'closed') = (closed_at IS NOT NULL));
