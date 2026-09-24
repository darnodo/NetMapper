package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type snapshot struct {
	id          int64
	state       string
	closedAt    *time.Time
	perimeter   string
	perimeterID int64
	// The thresholds its own perimeter declared, nil each when it declared none.
	degradedAt       *float64
	quarantinedBelow *float64
}

// describe reads the snapshot, the name of the perimeter its run used, and that perimeter's row,
// whose ranges are the ones a baseline is filtered against.
func describe(ctx context.Context, db *pgxpool.Pool, id int64) (snapshot, error) {
	s := snapshot{id: id}
	err := db.QueryRow(ctx, `
		SELECT s.state, s.closed_at, p.name, p.id, p.degraded_at, p.quarantined_below
		FROM snapshot s
		JOIN job j ON j.snapshot_id = s.id
		JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
		WHERE s.id = $1`, id).Scan(&s.state, &s.closedAt, &s.perimeter, &s.perimeterID, &s.degradedAt, &s.quarantinedBelow)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, fmt.Errorf("snapshot %d: %w", id, ErrNotFound)
	}
	return s, err
}

// selectBaseline returns the snapshot this one is measured against: the same perimeter's
// immediately preceding closed snapshot that already carries an active judgement, or nil when
// there is none.
//
// The perimeter is matched by name, not by id: every `netmapper run` posts the configuration
// document whole, so each run inserts a fresh perimeter row and comparing ids would give every
// snapshot an empty history.
func selectBaseline(ctx context.Context, db *pgxpool.Pool, s snapshot) (*int64, error) {
	var id int64
	err := db.QueryRow(ctx, `
		SELECT s.id
		FROM snapshot s
		JOIN job j ON j.snapshot_id = s.id
		JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
		WHERE p.name = $1
		  AND s.state = 'closed'
		  AND (s.closed_at, s.id) < ($2, $3)
		  AND EXISTS (SELECT 1 FROM snapshot_judgement sj WHERE sj.snapshot_id = s.id AND sj.active)
		ORDER BY s.closed_at DESC, s.id DESC
		LIMIT 1`, s.perimeter, s.closedAt, s.id).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
