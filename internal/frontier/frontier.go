// Package frontier is the task queue of a run, in PostgreSQL (research R3). A worker holds a task
// through (claimed_by, attempts): every write it makes checks both, so a worker whose lease
// expired and whose task was claimed again cannot write for it.
package frontier

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/store"
)

// ErrLost means the task was reclaimed or finished by someone else. Stop and write nothing more.
var ErrLost = errors.New("task no longer held")

// Failure kinds (data-model.md). Only KindError is requeued before the final pass.
const (
	KindError                = "error"
	KindDeadline             = "deadline"
	KindCredentialUnresolved = "credential_unresolved"
	KindCredentialPartial    = "credential_partial"
)

type CredCount struct {
	N  int  `json:"n"`
	OK bool `json:"ok"`
}

type Task struct {
	JobID        int64
	ID           int64
	Kind         string
	Target       netip.Addr
	TargetName   string
	Platform     string
	ParentTaskID int64
	ClaimedBy    string
	Attempts     int
	CredAttempts map[string]CredCount
}

const fence = `job_id = $1 AND id = $2 AND state = 'claimed' AND claimed_by = $3 AND attempts = $4`

func (t Task) fence() []any { return []any{t.JobID, t.ID, t.ClaimedBy, t.Attempts} }

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Enqueue adds a pending task. The same target queued twice in a job is one task.
func Enqueue(ctx context.Context, db store.DB, jobID int64, kind string, target netip.Addr, name, platform string, parent int64, cred map[string]CredCount) error {
	if cred == nil {
		cred = map[string]CredCount{}
	}
	var p any
	if parent != 0 {
		p = parent
	}
	_, err := db.Exec(ctx, `
		INSERT INTO task (job_id, kind, target, target_name, platform, parent_task_id, cred_attempts)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (job_id, kind, target) DO NOTHING`,
		jobID, kind, target, null(name), null(platform), p, cred)
	return err
}

// InsertSkipped records a find that will never run, such as a neighbour outside the perimeter.
func InsertSkipped(ctx context.Context, db store.DB, jobID int64, target netip.Addr, name string, parent int64, reason string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO task (job_id, kind, target, target_name, state, parent_task_id, skip_reason)
		VALUES ($1, 'find', $2, $3, 'skipped', $4, $5) ON CONFLICT (job_id, kind, target) DO NOTHING`,
		jobID, target, null(name), parent, reason)
	return err
}

// Claim takes up to batch tasks of a running job. It first fails the expired tasks that already
// used every attempt, since a lease that keeps expiring means a worker died or hung on them.
func Claim(ctx context.Context, db store.DB, collectorID string, jobID int64, kinds []string, batch int, lease time.Duration, maxAttempts int) ([]Task, error) {
	if _, err := db.Exec(ctx, `
		UPDATE task SET state = 'failed', last_error = 'lease_expired: ' || attempts || ' attempts'
		WHERE job_id = $1 AND state = 'claimed' AND lease_expires < now() AND attempts >= $2`,
		jobID, maxAttempts); err != nil {
		return nil, err
	}
	// MATERIALIZED: an IN (... LIMIT) subquery may be evaluated more than once on a partitioned
	// table, and then claims more than the limit.
	rows, err := db.Query(ctx, `
		WITH next AS MATERIALIZED (
			SELECT id FROM task
			WHERE job_id = $3 AND kind = ANY($4)
			  AND (state = 'pending' OR (state = 'claimed' AND lease_expires < now()))
			  AND EXISTS (SELECT 1 FROM job WHERE job.id = $3 AND job.state = 'running')
			ORDER BY id LIMIT $5 FOR UPDATE SKIP LOCKED)
		UPDATE task SET state = 'claimed', claimed_by = $1, lease_expires = now() + $2::interval, attempts = attempts + 1
		FROM next WHERE task.job_id = $3 AND task.id = next.id
		RETURNING task.job_id, task.id, kind, target, coalesce(target_name, ''), coalesce(platform, ''),
			coalesce(parent_task_id, 0), claimed_by, attempts, cred_attempts`,
		collectorID, lease.String(), jobID, kinds, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.JobID, &t.ID, &t.Kind, &t.Target, &t.TargetName, &t.Platform,
			&t.ParentTaskID, &t.ClaimedBy, &t.Attempts, &t.CredAttempts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func exec(ctx context.Context, db store.DB, t Task, set string, args ...any) error {
	tag, err := db.Exec(ctx, "UPDATE task SET "+set+" WHERE "+fence, append(t.fence(), args...)...)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrLost
	}
	return err
}

// Hold locks the task row in tx and checks it is still ours, before a write that depends on it.
// A row lock, not an update: an updated row would make concurrent enqueues that conflict with it
// wait on this transaction, and two finds enqueueing each other would deadlock.
func Hold(ctx context.Context, db store.DB, t Task) error {
	var one int
	err := db.QueryRow(ctx, "SELECT 1 FROM task WHERE "+fence+" FOR UPDATE", t.fence()...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLost
	}
	return err
}

// Renew extends the lease. ErrLost means stop now.
func Renew(ctx context.Context, db store.DB, t Task, lease time.Duration) error {
	return exec(ctx, db, t, "lease_expires = now() + $5::interval", lease.String())
}

// Complete ends the task: done, duplicate.
func Complete(ctx context.Context, db store.DB, t Task, state string) error {
	return exec(ctx, db, t, "state = $5", state)
}

// Skip ends a task without sending anything.
func Skip(ctx context.Context, db store.DB, t Task, reason string) error {
	return exec(ctx, db, t, "state = 'skipped', skip_reason = $5", reason)
}

// Fail records why the task could not record an outcome (FR-014). KindError is requeued while
// attempts remain; the other kinds wait for the final pass.
func Fail(ctx context.Context, db store.DB, t Task, kind, message string, maxAttempts int) error {
	return exec(ctx, db, t, `last_error = $5,
		state = CASE WHEN $6::bool AND attempts < $7::int THEN 'pending' ELSE 'failed' END`,
		fmt.Sprintf("%s: %s", kind, message), kind == KindError, maxAttempts)
}

// CountCredential adds dn attempts to a credential set's budget on this device and sets its ok
// flag, in its own statement so a crash never resets the count (research R7).
func CountCredential(ctx context.Context, db store.DB, t Task, setID string, dn int, ok bool) error {
	return exec(ctx, db, t, `cred_attempts = jsonb_set(cred_attempts, ARRAY[$5::text],
		jsonb_build_object('n', coalesce((cred_attempts -> $5::text ->> 'n')::int, 0) + $6::int, 'ok', $7::bool))`,
		setID, dn, ok)
}
