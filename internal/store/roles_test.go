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
