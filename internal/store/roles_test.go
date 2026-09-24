package store_test

import (
	"context"
	"testing"

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
