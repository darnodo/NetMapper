package frontier_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/testutil"
)

const doc = `
perimeters: [{name: lab, include: [10.0.0.0/24]}]
credential_sets: [{name: ro, kind: ssh, username: u, secret_ref: env:X, max_attempts_per_device: 1, perimeters: [lab]}]
seed_sets: [{name: s, targets: [10.0.0.1, 10.0.0.2, 10.0.0.3, 10.0.0.4]}]
`

func setup(t *testing.T) (db, col *pgxpool.Pool, job int64) {
	db = testutil.DB(t)
	job, err := jobrunner.Start(context.Background(), testutil.As(t, db, "netmapper_operator"), []byte(doc), "lab", "s", "test")
	if err != nil {
		t.Fatal(err)
	}
	return db, testutil.As(t, db, "netmapper_collector"), job
}

func state(t *testing.T, db *pgxpool.Pool, task frontier.Task) (s, lastError string) {
	var le *string
	db.QueryRow(context.Background(), `SELECT state, last_error FROM task WHERE job_id = $1 AND id = $2`, task.JobID, task.ID).Scan(&s, &le)
	if le != nil {
		lastError = *le
	}
	return s, lastError
}

func TestClaimIsExclusive(t *testing.T) {
	_, col, job := setup(t)
	ctx := context.Background()
	a, err := frontier.Claim(ctx, col, "a", job, []string{"find"}, 3, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := frontier.Claim(ctx, col, "b", job, []string{"find"}, 3, time.Minute, 3)
	if len(a) != 3 || len(b) != 1 || b[0].ClaimedBy != "b" || b[0].Attempts != 1 {
		t.Fatalf("a=%v b=%v", a, b)
	}
	if none, _ := frontier.Claim(ctx, col, "c", job, []string{"scrape"}, 3, time.Minute, 3); len(none) != 0 {
		t.Errorf("claimed a kind not asked for: %v", none)
	}
	// Same target queued twice is one task.
	frontier.Enqueue(ctx, col, job, "find", netip.MustParseAddr("10.0.0.1"), "", "", 0, nil)
	if more, _ := frontier.Claim(ctx, col, "c", job, []string{"find"}, 3, time.Minute, 3); len(more) != 0 {
		t.Errorf("duplicate target became a new task: %v", more)
	}
}

func TestLeaseExpiryAndFencing(t *testing.T) {
	db, col, job := setup(t)
	ctx := context.Background()
	first, _ := frontier.Claim(ctx, col, "a", job, []string{"find"}, 1, time.Millisecond, 2)
	time.Sleep(10 * time.Millisecond)
	second, _ := frontier.Claim(ctx, col, "b", job, []string{"find"}, 1, time.Millisecond, 2)
	if len(second) != 1 || second[0].ID != first[0].ID || second[0].Attempts != 2 {
		t.Fatalf("expired task not reclaimed: %v", second)
	}
	if err := frontier.Complete(ctx, col, first[0], "done"); !errors.Is(err, frontier.ErrLost) {
		t.Errorf("stale holder completed the task: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	// Attempts are spent: the next claim fails it instead of taking it a third time.
	third, _ := frontier.Claim(ctx, col, "c", job, []string{"find"}, 1, time.Minute, 2)
	if len(third) == 1 && third[0].ID == first[0].ID {
		t.Fatal("task claimed beyond max attempts")
	}
	if s, le := state(t, db, first[0]); s != "failed" || le != "lease_expired: 2 attempts" {
		t.Errorf("got %s %q", s, le)
	}
}

func TestFailRules(t *testing.T) {
	db, col, job := setup(t)
	ctx := context.Background()
	tasks, _ := frontier.Claim(ctx, col, "a", job, []string{"find"}, 4, time.Minute, 3)
	for i, c := range []struct{ kind, wantState string }{
		{frontier.KindError, "pending"},
		{frontier.KindDeadline, "failed"},
		{frontier.KindCredentialUnresolved, "failed"},
		{frontier.KindCredentialPartial, "failed"},
	} {
		if err := frontier.Fail(ctx, col, tasks[i], c.kind, "why", 3); err != nil {
			t.Fatal(err)
		}
		if s, le := state(t, db, tasks[i]); s != c.wantState || le != c.kind+": why" {
			t.Errorf("%s: %s %q", c.kind, s, le)
		}
	}
	// KindError at the last attempt fails.
	again, _ := frontier.Claim(ctx, col, "a", job, []string{"find"}, 1, time.Minute, 1)
	frontier.Fail(ctx, col, again[0], frontier.KindError, "boom", 2)
	if s, _ := state(t, db, again[0]); s != "failed" {
		t.Errorf("error at max attempts: %s", s)
	}
}

func TestCredentialCounts(t *testing.T) {
	db, col, job := setup(t)
	ctx := context.Background()
	tasks, _ := frontier.Claim(ctx, col, "a", job, []string{"find"}, 1, time.Minute, 3)
	frontier.CountCredential(ctx, col, tasks[0], "7", 1, false)
	frontier.CountCredential(ctx, col, tasks[0], "7", 1, false)
	frontier.CountCredential(ctx, col, tasks[0], "7", 0, true)
	var got string
	db.QueryRow(ctx, `SELECT cred_attempts::text FROM task WHERE job_id = $1 AND id = $2`, job, tasks[0].ID).Scan(&got)
	if !strings.Contains(got, `"7": {"n": 2, "ok": true}`) {
		t.Errorf("cred_attempts = %s", got)
	}
}
