package jobrunner

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run ticks the job runner until ctx ends. It is the engine's only component in this feature.
func Run(ctx context.Context, db *pgxpool.Pool, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := Tick(ctx, db); err != nil && ctx.Err() == nil {
			log.Error("job runner", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick moves every active job one step towards its end (research R12), then sweeps for closed
// snapshots that still need a verdict or an entity set:
//   - running, queue empty, no final pass yet: requeue every failed task once, attempts reset;
//   - running, queue empty after the final pass: close the snapshot, job succeeded;
//   - cancelling: pending tasks become cancelled; once no lease is live, close, job cancelled.
//
// Failed tasks get nothing written for them here: their last_error is the record (FR-014).
func Tick(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, `SELECT id, state, snapshot_id, parameters FROM job WHERE state IN ('running', 'cancelling') ORDER BY id`)
	if err != nil {
		return err
	}
	type active struct {
		id, snapshot int64
		state        string
		p            Params
	}
	jobs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (active, error) {
		var a active
		return a, r.Scan(&a.id, &a.state, &a.snapshot, &a.p)
	})
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.state == "cancelling" {
			if err := cancelStep(ctx, db, j.id, j.snapshot); err != nil {
				return err
			}
			continue
		}
		var busy bool
		if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM task WHERE job_id = $1 AND state IN ('pending', 'claimed'))`, j.id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			continue
		}
		if !j.p.FinalPassDone {
			err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE task SET state = 'pending', attempts = 0 WHERE job_id = $1 AND state = 'failed'`, j.id); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `UPDATE job SET parameters = parameters || '{"final_pass_done": true}' WHERE id = $1 AND state = 'running'`, j.id)
				return err
			})
		} else {
			err = closeJob(ctx, db, j.id, j.snapshot, "running", "succeeded")
		}
		if err != nil {
			return err
		}
	}
	if err := judgeStep(ctx, db); err != nil {
		return err
	}
	return resolveStep(ctx, db)
}

func cancelStep(ctx context.Context, db *pgxpool.Pool, job, snapshot int64) error {
	if _, err := db.Exec(ctx, `UPDATE task SET state = 'cancelled' WHERE job_id = $1 AND state = 'pending'`, job); err != nil {
		return err
	}
	var live bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM task WHERE job_id = $1 AND state = 'claimed' AND lease_expires > now())`, job).Scan(&live); err != nil || live {
		return err
	}
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		// Expired leases will never be reclaimed: claims only take tasks of running jobs.
		if _, err := tx.Exec(ctx, `UPDATE task SET state = 'cancelled' WHERE job_id = $1 AND state = 'claimed'`, job); err != nil {
			return err
		}
		return closeIn(ctx, tx, job, snapshot, "cancelling", "cancelled")
	})
}

func closeJob(ctx context.Context, db *pgxpool.Pool, job, snapshot int64, from, to string) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error { return closeIn(ctx, tx, job, snapshot, from, to) })
}

func closeIn(ctx context.Context, tx pgx.Tx, job, snapshot int64, from, to string) error {
	tag, err := tx.Exec(ctx, `UPDATE job SET state = $2, ended_at = now() WHERE id = $1 AND state = $3`, job, to, from)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE snapshot SET state = 'closed', closed_at = now() WHERE id = $1 AND state = 'open'`, snapshot)
	return err
}
