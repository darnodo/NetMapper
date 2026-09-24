package jobrunner

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/gate"
)

// judgeStep gives a verdict to every closed snapshot that has none.
//
// It is a sweep rather than a step of the closing transaction, so a runner that dies between a
// snapshot closing and its verdict being written repairs itself on the next tick, and a judgement
// that fails can never keep a job from finishing. Oldest first: a snapshot's baseline is the
// preceding snapshot that already carries a verdict, so judging in closing order lets a run of
// unjudged snapshots resolve one after another instead of skipping over each other.
func judgeStep(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, `
		SELECT s.id
		FROM snapshot s
		WHERE s.state = 'closed'
		  AND NOT EXISTS (SELECT 1 FROM snapshot_judgement j WHERE j.snapshot_id = s.id AND j.active)
		ORDER BY s.closed_at, s.id`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := gate.Judge(ctx, db, id); err != nil {
			return err
		}
	}
	return nil
}
