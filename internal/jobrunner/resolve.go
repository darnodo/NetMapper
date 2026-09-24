package jobrunner

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/entity"
)

// resolveStep gives an entity set to every closed snapshot that has none.
//
// Like the verdict before it, this is a sweep and not a step of the closing transaction, so a runner
// that dies between a snapshot closing and its resolution repairs itself on the next tick, and a
// grouping that fails can never keep a job from finishing (FR-016).
//
// Oldest first, and this one matters more than it does for judging: device keys are minted in the
// order snapshots closed, so replaying a wiped registry in that same order mints the same keys again
// (research R1, R5, SC-008).
func resolveStep(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, `
		SELECT s.id
		FROM snapshot s
		WHERE s.state = 'closed'
		  AND NOT EXISTS (SELECT 1 FROM resolution r WHERE r.snapshot_id = s.id)
		ORDER BY s.closed_at, s.id`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	// Every snapshot is attempted, and the errors are reported together. Stopping at the first one would
	// let a single snapshot that always fails block every snapshot closed after it, for ever, which is
	// the opposite of the self-repair FR-016 asks for.
	var errs []error
	for _, id := range ids {
		if _, err := entity.Resolve(ctx, db, id); err != nil {
			errs = append(errs, fmt.Errorf("snapshot %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
