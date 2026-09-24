package collector

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/store"
)

// claim is find step 2 (research R4): a short transaction with no network I/O. It locks every
// strong identifier through claim_lock_key in sorted order, looks for a strong claim with the
// same kind and value held by another task's collected identity, then writes this task's identity
// and claims either way. If another task holds the device, this task ends duplicate. A reclaimed
// task finds its own claims, which are not someone else's, and continues.
func (c *Collector) claim(ctx context.Context, j *job, t *frontier.Task, obs store.Observation) (duplicate bool, err error) {
	var kinds, vals []string
	for _, cl := range obs.Claims {
		if cl.Strength == "strong" {
			kinds, vals = append(kinds, cl.Kind), append(vals, cl.Value)
		}
	}
	err = pgx.BeginFunc(ctx, c.DB, func(tx pgx.Tx) error {
		if err := frontier.Hold(ctx, tx, *t); err != nil {
			return err
		}
		var holder int64
		if len(kinds) > 0 {
			rows, err := tx.Query(ctx, `SELECT DISTINCT claim_lock_key($1, k, v) FROM unnest($2::text[], $3::text[]) AS u(k, v)`,
				j.SnapshotID, kinds, vals)
			if err != nil {
				return err
			}
			keys, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return err
			}
			slices.Sort(keys)
			for _, k := range keys {
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, k); err != nil {
					return err
				}
			}
			// A duplicate's claims are not a holder: only the task that went on to collect is.
			err = tx.QueryRow(ctx, `
				SELECT o.task_id FROM identifier_claim c
				JOIN observation o ON o.snapshot_id = c.snapshot_id AND o.id = c.observation_id
				WHERE c.snapshot_id = $1 AND c.strength = 'strong'
				  AND (c.kind, c.value) IN (SELECT * FROM unnest($2::text[], $3::text[]))
				  AND o.fact_family = 'identity' AND o.status = 'collected' AND o.task_id <> $4
				  AND NOT (o.parsed -> 0 ? 'duplicate_of_task')
				ORDER BY o.id LIMIT 1`, j.SnapshotID, kinds, vals, t.ID).Scan(&holder)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if holder != 0 {
			obs.Parsed[0]["duplicate_of_task"] = holder
		}
		if _, _, err := store.WriteObservation(ctx, tx, obs); err != nil {
			return err
		}
		if holder != 0 {
			duplicate = true
			return frontier.Complete(ctx, tx, *t, "duplicate")
		}
		return nil
	})
	return duplicate, err
}

// write stores obs in a short transaction that first checks the task is still held, raises the
// finding the outcome calls for when the row is new, then runs more in the same transaction.
func (c *Collector) write(ctx context.Context, t *frontier.Task, obs store.Observation, parseErr error, more func(pgx.Tx) error) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, c.DB, func(tx pgx.Tx) error {
		if err := frontier.Hold(ctx, tx, *t); err != nil {
			return err
		}
		var inserted bool
		var err error
		if id, inserted, err = store.WriteObservation(ctx, tx, obs); err != nil {
			return err
		}
		if inserted {
			if err := raise(ctx, tx, obs, id, parseErr); err != nil {
				return err
			}
		}
		if more != nil {
			return more(tx)
		}
		return nil
	})
	return id, err
}

// raise records the finding an outcome calls for (FR-015, FR-021), citing the observation.
func raise(ctx context.Context, tx pgx.Tx, obs store.Observation, id int64, parseErr error) error {
	subject := "target:" + obs.Target.String()
	detail := map[string]any{"family": obs.Family}
	switch {
	case obs.Status == "parse_failed":
		if parseErr != nil {
			detail["error"] = parseErr.Error()
		}
		return store.RaiseFinding(ctx, tx, obs.SnapshotID, "data_quality", "parse_failed", "warning", subject, detail, id)
	case obs.Detail == "unknown_platform":
		return store.RaiseFinding(ctx, tx, obs.SnapshotID, "data_quality", "unknown_platform", "warning", subject, detail, id)
	case obs.Status == "denied" && obs.Family == "identity":
		return store.RaiseFinding(ctx, tx, obs.SnapshotID, "compliance", "credential_denied", "high", subject, detail, id)
	}
	return nil
}
