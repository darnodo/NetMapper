package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/testutil"
)

// FR-010: the collector writes the collected zone and can never change it afterwards.
func TestCollectorCannotRewriteCollectedZone(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	col := testutil.As(t, db, "netmapper_collector")
	for _, q := range []string{
		"UPDATE observation SET status = 'collected'",
		"DELETE FROM observation",
		"UPDATE observation_raw SET command = ''",
		"DELETE FROM observation_raw",
		"UPDATE raw_object SET size = 0",
		"DELETE FROM raw_object",
		"UPDATE identifier_claim SET value = ''",
		"DELETE FROM identifier_claim",
		"UPDATE audit_log SET result = ''",
		"DELETE FROM audit_log",
	} {
		if _, err := col.Exec(ctx, q); err == nil {
			t.Errorf("%s: allowed", q)
		}
	}
	eng := testutil.As(t, db, "netmapper_engine")
	if _, err := eng.Exec(ctx, "DELETE FROM observation"); err == nil {
		t.Error("engine may delete observations")
	}
}

// T008, FR-009: a verdict is never rewritten or removed. The engine writes judgements only through
// judge_snapshot, which supersedes rather than edits, so the constraint holds in the database and
// not merely in the code that calls it.
func TestEngineCannotRewriteJudgements(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	eng := testutil.As(t, db, "netmapper_engine")
	for _, q := range []string{
		"UPDATE snapshot_judgement SET classification = 'published'",
		"UPDATE snapshot_judgement SET active = false",
		"DELETE FROM snapshot_judgement",
	} {
		if _, err := eng.Exec(ctx, q); err == nil {
			t.Errorf("%s: allowed", q)
		}
	}
}

// T011, FR-009 and FR-014: resolution gains the computed zone and, for the first time, the right to
// remove a finding it raised. That must not have widened anything else: a decision stays append only
// and the collected zone stays untouchable.
func TestResolutionGrantsStayNarrow(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	eng := testutil.As(t, db, "netmapper_engine")

	for _, q := range []string{
		"UPDATE entity_decision SET kind = 'merge'",
		"DELETE FROM entity_decision",
		"INSERT INTO entity_decision (perimeter_name, kind, subjects, actor) VALUES ('lab', 'merge', ARRAY['a', 'b'], 'x')",
	} {
		if _, err := eng.Exec(ctx, q); err == nil {
			t.Errorf("engine: %s: allowed", q)
		}
	}
	var decisions int
	if err := eng.QueryRow(ctx, "SELECT count(*) FROM entity_decision").Scan(&decisions); err != nil {
		t.Errorf("engine cannot read decisions: %v", err)
	}

	// The computed zone is the engine's to replace, and a finding is part of what a resolution
	// replaces (FR-015).
	for _, q := range []string{
		"DELETE FROM entity",
		"DELETE FROM entity_claim",
		"DELETE FROM resolution",
		"DELETE FROM device_identifier",
		"DELETE FROM device",
		"DELETE FROM finding_evidence",
		"DELETE FROM finding",
	} {
		if _, err := eng.Exec(ctx, q); err != nil {
			t.Errorf("engine: %s: %v", q, err)
		}
	}

	// The new grants must not have reached the collected zone.
	for _, q := range []string{
		"UPDATE observation SET status = 'collected'",
		"DELETE FROM observation",
		"UPDATE identifier_claim SET value = ''",
		"DELETE FROM identifier_claim",
	} {
		if _, err := eng.Exec(ctx, q); err == nil {
			t.Errorf("engine: %s: allowed", q)
		}
	}

	// netmapper resolve runs the resolver in the operator's process, so the operator needs every right
	// the resolver uses, on the same tables. Testing only the engine is what let a missing SELECT on
	// finding through: PostgreSQL reads the columns of a DELETE's WHERE clause and of a RETURNING
	// clause, so the statements below fail without it however many rows exist.
	op := testutil.As(t, db, "netmapper_operator")
	for _, q := range []string{
		`DELETE FROM finding_evidence WHERE finding_id IN
			(SELECT id FROM finding WHERE snapshot_id = 0 AND category = 'identity_conflict')`,
		"DELETE FROM finding WHERE snapshot_id = 0 AND category = 'identity_conflict'",
		"DELETE FROM entity WHERE snapshot_id = 0",
		"DELETE FROM resolution WHERE snapshot_id = 0",
		"DELETE FROM device_identifier",
		"DELETE FROM device",
	} {
		if _, err := op.Exec(ctx, q); err != nil {
			t.Errorf("operator: %s: %v", q, err)
		}
	}
	var findings int
	if err := op.QueryRow(ctx, "SELECT count(*) FROM finding").Scan(&findings); err != nil {
		t.Errorf("operator cannot read findings, so raising one cannot return its id: %v", err)
	}
	// And the operator's own boundary still holds: a decision is insert only for it too.
	for _, q := range []string{
		"UPDATE entity_decision SET kind = 'merge'",
		"DELETE FROM entity_decision",
	} {
		if _, err := op.Exec(ctx, q); err == nil {
			t.Errorf("operator: %s: allowed", q)
		}
	}
}

// T014, contracts/cli.md and research R15: the graph projector runs under the engine in the sweep and
// under the operator in `netmapper project`, so both roles must be able to replace the projected
// tables, and the collector must still see none of them. The constitution added this check because 003
// granted the operator the writes `netmapper resolve` needed but not the reads, and every test
// connected as the engine, so the operator's own command could not run at all.
func TestProjectionGrants(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()

	// The collector never reads an interface or an edge, and nothing here changed that.
	col := testutil.As(t, db, "netmapper_collector")
	for _, q := range []string{
		"SELECT count(*) FROM interface",
		"SELECT count(*) FROM interface_alias",
		"SELECT count(*) FROM interface_evidence",
		"SELECT count(*) FROM edge",
		"SELECT count(*) FROM edge_evidence",
		"SELECT count(*) FROM projection",
	} {
		if _, err := col.Exec(ctx, q); err == nil {
			t.Errorf("collector: %s: allowed", q)
		}
	}

	// Both roles run the same projector, so both are checked on the same statements. The deletes carry
	// a WHERE clause on purpose: PostgreSQL reads the columns a DELETE filters on, so a missing SELECT
	// shows up here and not in a bare DELETE.
	for _, role := range []string{"netmapper_engine", "netmapper_operator"} {
		p := testutil.As(t, db, role)
		for _, q := range []string{
			"SELECT count(*) FROM interface WHERE snapshot_id = 0",
			"SELECT count(*) FROM edge WHERE snapshot_id = 0 AND name = ''",
			`DELETE FROM edge_evidence WHERE edge_id IN (SELECT id FROM edge WHERE snapshot_id = 0)`,
			"DELETE FROM edge WHERE snapshot_id = 0",
			`DELETE FROM interface_alias WHERE interface_id IN
				(SELECT id FROM interface WHERE snapshot_id = 0)`,
			`DELETE FROM interface_evidence WHERE interface_id IN
				(SELECT id FROM interface WHERE snapshot_id = 0)`,
			"DELETE FROM interface WHERE snapshot_id = 0",
			"DELETE FROM projection WHERE snapshot_id = 0",
			`DELETE FROM finding WHERE snapshot_id = 0 AND category = 'link_disagreement'`,
		} {
			if _, err := p.Exec(ctx, q); err != nil {
				t.Errorf("%s: %s: %v", role, q, err)
			}
		}
		// The projector reads these before it writes anything, and neither role was granted them here:
		// both already held them from 001 and 003. A failure means that assumption broke.
		for _, q := range []string{
			"SELECT count(*) FROM resolution",
			"SELECT count(*) FROM entity",
			"SELECT count(*) FROM entity_claim",
			"SELECT count(*) FROM identifier_claim",
			"SELECT count(*) FROM observation",
			"SELECT count(*) FROM parse_generation",
			"SELECT count(*) FROM task",
		} {
			if _, err := p.Exec(ctx, q); err != nil {
				t.Errorf("%s: %s: %v", role, q, err)
			}
		}
		// Projection writes nothing in the collected zone (FR-020).
		for _, q := range []string{
			"UPDATE observation SET status = 'collected'",
			"DELETE FROM observation",
			"DELETE FROM identifier_claim",
		} {
			if _, err := p.Exec(ctx, q); err == nil {
				t.Errorf("%s: %s: allowed", role, q)
			}
		}
	}
}

// 005 T006, data-model.md and research R3: the role the exposed process logs in as cannot read the
// configuration, where secret references live, nor anything it does not serve.
func TestAPICannotReadCredentials(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	api := testutil.As(t, db, "netmapper_api")
	for _, table := range []string{
		"credential_set", "config_version", "perimeter", "seed_set",
		"task", "job", "entity_decision", "audit_log",
	} {
		if _, err := api.Exec(ctx, "SELECT count(*) FROM "+table); !denied(err) {
			t.Errorf("api: SELECT on %s: %v, want permission denied", table, err)
		}
	}
}

// 005 T006, FR-016, SC-008 and research R3: nothing the api reads can be written by it. The one
// exception is last_used_at on api_token (research R6).
func TestAPICannotWriteAnyZone(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	api := testutil.As(t, db, "netmapper_api")
	served := []string{
		"snapshot", "snapshot_judgement", "projection", "resolution",
		"entity", "entity_claim", "identifier_claim",
		"interface", "interface_alias", "interface_evidence", "edge", "edge_evidence",
		"observation", "observation_raw", "raw_object",
		"finding", "finding_evidence", "api_token",
	}
	for _, table := range served {
		if _, err := api.Exec(ctx, "SELECT count(*) FROM "+table); err != nil {
			t.Errorf("api: SELECT on %s: %v", table, err)
		}
		for _, q := range []string{
			"INSERT INTO " + table + " DEFAULT VALUES",
			"DELETE FROM " + table,
		} {
			if _, err := api.Exec(ctx, q); !denied(err) {
				t.Errorf("api: %s: %v, want permission denied", q, err)
			}
		}
	}
	for _, q := range []string{
		"UPDATE snapshot SET state = 'closed'",
		"UPDATE snapshot_judgement SET active = false",
		"UPDATE entity SET weak = true",
		"UPDATE interface SET source = 'device'",
		"UPDATE edge SET confidence = 'one_end'",
		"UPDATE observation SET status = 'collected'",
		"UPDATE finding SET state = 'open'",
	} {
		if _, err := api.Exec(ctx, q); !denied(err) {
			t.Errorf("api: %s: %v, want permission denied", q, err)
		}
	}
	if _, err := api.Exec(ctx, "UPDATE api_token SET last_used_at = now()"); err != nil {
		t.Errorf("api cannot record last_used_at: %v", err)
	}
}

// 005 T006, research R6: the one write is one column wide. A compromised api process cannot revive a
// revoked token, widen its scopes or swap its hash.
func TestAPICanOnlyTouchLastUsed(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	api := testutil.As(t, db, "netmapper_api")
	for _, q := range []string{
		"UPDATE api_token SET revoked_at = NULL",
		"UPDATE api_token SET scopes = ARRAY['read']",
		"UPDATE api_token SET hash = '\\x00'",
		"UPDATE api_token SET name = 'x'",
		"DELETE FROM api_token",
	} {
		if _, err := api.Exec(ctx, q); !denied(err) {
			t.Errorf("api: %s: %v, want permission denied", q, err)
		}
	}
}

// 005 T006, data-model.md: the operator issues and revokes tokens and can never delete one.
func TestOperatorCannotDeleteTokens(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	op := testutil.As(t, db, "netmapper_operator")
	if _, err := op.Exec(ctx, `INSERT INTO api_token (name, hash, scopes) VALUES ('t', '\x01', ARRAY['read'])`); err != nil {
		t.Fatalf("operator cannot issue a token: %v", err)
	}
	if _, err := op.Exec(ctx, "UPDATE api_token SET revoked_at = now() WHERE name = 't'"); err != nil {
		t.Errorf("operator cannot revoke a token: %v", err)
	}
	if _, err := op.Exec(ctx, "DELETE FROM api_token"); !denied(err) {
		t.Errorf("operator: DELETE FROM api_token: %v, want permission denied", err)
	}
}

// denied reports a permission failure, as opposed to a statement that failed for another reason, such
// as a NOT NULL column an INSERT with no values would trip whatever the grants say.
func denied(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42501"
}

// closedSnapshot inserts a job and its snapshot as the owner and closes it.
func closedSnapshot(t *testing.T, db *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := db.QueryRow(ctx, `
		WITH c AS (INSERT INTO config_version (posted_by, document) VALUES ('test', '') RETURNING id),
		     j AS (INSERT INTO job (state, config_version, requested_by) SELECT 'succeeded', id, 'test' FROM c RETURNING id)
		INSERT INTO snapshot (job_id) SELECT id FROM j RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE snapshot SET state = 'closed', closed_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// T008, 012 research R9: on a closed snapshot only lifecycle metadata changes (constitution,
// Principle II): the pin flag alone, or the move to evicting; then evicting to evicted; evicted is
// final. Run as the owner, so it is the trigger that refuses, not a grant.
func TestSnapshotLifecycleTrigger(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	id := closedSnapshot(t, db)
	// A second, real job, so moving the snapshot to it passes the foreign key and only the trigger
	// can refuse it.
	if _, err := db.Exec(ctx, `INSERT INTO job (state, config_version, requested_by)
		SELECT 'succeeded', config_version, 'test' FROM job`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q  string
		ok bool
	}{
		{"UPDATE snapshot SET pinned = true WHERE id = $1", true},
		{"UPDATE snapshot SET pinned = false, closed_at = now() - interval '1 day' WHERE id = $1", false},
		{"UPDATE snapshot SET job_id = (SELECT max(id) FROM job) WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'open', closed_at = NULL WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'evicted', evicted_at = now() WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'evicting', evicted_at = now(), pinned = false WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'evicting', evicted_at = now() WHERE id = $1", true},
		{"UPDATE snapshot SET pinned = true WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'evicted' WHERE id = $1", true},
		{"UPDATE snapshot SET pinned = true WHERE id = $1", false},
		{"UPDATE snapshot SET state = 'closed', evicted_at = NULL WHERE id = $1", false},
	} {
		_, err := db.Exec(ctx, c.q, id)
		if (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), "update refused")) {
			t.Errorf("%s: err %v, want allowed=%t, refused by the trigger", c.q, err, c.ok)
		}
	}
}

// T008, 012 research R1, R11: eviction gives the operator owner functions, an INSERT on audit_log
// for its own actions and the work list, and still no write on the collected zone or the snapshot.
func TestOperatorCannotTouchCollectedZone(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	op := testutil.As(t, db, "netmapper_operator")
	for _, q := range []string{
		"DELETE FROM observation",
		"DELETE FROM observation_raw",
		"DELETE FROM identifier_claim",
		"DELETE FROM raw_object",
		"UPDATE snapshot SET pinned = true",
		"UPDATE audit_log SET result = ''",
		"DELETE FROM audit_log",
		"INSERT INTO eviction_object (snapshot_id, hash, size) VALUES (1, '\\x00', 0)",
	} {
		if _, err := op.Exec(ctx, q); !denied(err) {
			t.Errorf("operator: %s: %v, want permission denied", q, err)
		}
	}
	if _, err := op.Exec(ctx, `INSERT INTO audit_log (ref, actor, action, target, command, result)
		VALUES (gen_random_uuid(), 'test', 'snapshot.pin', NULL, 'snapshot 1', 'ok')`); err != nil {
		t.Errorf("operator: audit row for a snapshot action: %v", err)
	}
	if _, err := op.Exec(ctx, `INSERT INTO audit_log (ref, actor, action, target, command, result)
		VALUES (gen_random_uuid(), 'test', 'ssh.command', NULL, 'show version', 'sent')`); err == nil {
		t.Error("a device action without a target was accepted")
	}
	for _, q := range []string{
		"SELECT * FROM evict_snapshot(1, false)",
		"SELECT set_snapshot_pinned(1, true)",
		"SELECT crawl_gate_key()",
		"SELECT count(*) FROM eviction_object",
		"DELETE FROM eviction_object WHERE false",
	} {
		if _, err := op.Exec(ctx, q); err != nil {
			t.Errorf("operator: %s: %v", q, err)
		}
	}
}

// T008, 012 FR-016: the exposed role gains nothing from eviction.
func TestAPIGainsNothingFromEviction(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	api := testutil.As(t, db, "netmapper_api")
	for _, q := range []string{
		"SELECT * FROM evict_snapshot(1, false)",
		"SELECT finish_snapshot_eviction(1)",
		"SELECT set_snapshot_pinned(1, true)",
		"SELECT crawl_gate_key()",
		"SELECT count(*) FROM eviction_object",
		"DELETE FROM eviction_object",
		"INSERT INTO audit_log (ref, actor, action, command, result) VALUES (gen_random_uuid(), 'x', 'snapshot.pin', 'snapshot 1', 'ok')",
		"UPDATE snapshot SET pinned = true",
	} {
		if _, err := api.Exec(ctx, q); !denied(err) {
			t.Errorf("api: %s: %v, want permission denied", q, err)
		}
	}
}
