package jobrunner

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/graph"
	"github.com/darnodo/NetMapper/internal/pack"
)

// projectStep gives interfaces and edges to every closed snapshot that carries an entity set and no
// current projection.
//
// Like the verdict and the entity set before it, this is a sweep and not a step of the resolving
// transaction, so a runner that dies between the two repairs itself on the next tick, and a
// projection that fails can never roll back a perfectly good entity set (FR-021).
//
// A snapshot whose entity set was replaced since it was projected is taken again: `interface`
// cascades from `entity`, so re-resolving already destroyed the projection, and the `projection` row
// that survived claims a set that is gone. That is self-repair on changed inputs, not the system
// deciding on its own to redo a projection whose inputs are unchanged, which still needs
// `netmapper project` (FR-022, research R12).
func projectStep(ctx context.Context, db *pgxpool.Pool, reg *pack.Registry) error {
	rows, err := db.Query(ctx, `
		SELECT s.id
		FROM snapshot s
		JOIN resolution r ON r.snapshot_id = s.id
		LEFT JOIN projection p ON p.snapshot_id = s.id
		WHERE s.state = 'closed'
		  AND (p.snapshot_id IS NULL OR p.resolution_at <> r.computed_at)
		ORDER BY s.closed_at, s.id`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	// Every snapshot is attempted and the errors are reported together, for the reason resolveStep
	// gives: stopping at the first would let one snapshot that always fails block every snapshot
	// behind it, for ever.
	var errs []error
	for _, id := range ids {
		if _, err := graph.Project(ctx, db, reg, id); err != nil {
			errs = append(errs, fmt.Errorf("snapshot %d: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
