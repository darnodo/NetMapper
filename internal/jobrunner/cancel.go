package jobrunner

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotRunning = errors.New("job is not running")

// Cancel asks a running job to stop (FR-025). Workers stop claiming its tasks, in-flight tasks
// finish, and the job runner closes the snapshot with what was collected.
func Cancel(ctx context.Context, db *pgxpool.Pool, job int64) error {
	tag, err := db.Exec(ctx, `UPDATE job SET state = 'cancelling' WHERE id = $1 AND state = 'running'`, job)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotRunning
	}
	return err
}
