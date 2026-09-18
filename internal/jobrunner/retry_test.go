package jobrunner_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/testutil"
)

// US3-2, FR-014, FR-016: a task whose worker keeps dying is reclaimed up to max_task_attempts,
// then failed with lease_expired; the final pass gives it one more round, after which the run
// closes with the task failed and nothing written for it.
func TestTaskThatKeepsCrashing(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	doc := doc + "discovery: {max_task_attempts: 2}\n"
	job, err := jobrunner.Start(ctx, testutil.As(t, db, "netmapper_operator"), []byte(doc), "lab", "good", "test")
	if err != nil {
		t.Fatal(err)
	}
	col, eng := testutil.As(t, db, "netmapper_collector"), testutil.As(t, db, "netmapper_engine")
	crashUntilFailed := func() {
		for range 3 { // two claims that die, then the sweep fails them
			frontier.Claim(ctx, col, "c1", job, []string{"find"}, 10, time.Millisecond, 2)
			time.Sleep(5 * time.Millisecond)
		}
	}
	state := func() []string {
		return strings.Fields(strings.Join(func() []string {
			rows, _ := db.Query(ctx, `SELECT state || ':' || coalesce(last_error, '') FROM task WHERE job_id = $1 ORDER BY id`, job)
			var out []string
			for rows.Next() {
				var s string
				rows.Scan(&s)
				out = append(out, strings.ReplaceAll(s, " ", "_"))
			}
			return out
		}(), " "))
	}

	crashUntilFailed()
	if got := state(); got[0] != "failed:lease_expired:_2_attempts" {
		t.Fatalf("after crashes: %v", got)
	}
	if err := jobrunner.Tick(ctx, eng); err != nil {
		t.Fatal(err)
	}
	var attempts int
	db.QueryRow(ctx, `SELECT attempts FROM task WHERE job_id = $1 ORDER BY id LIMIT 1`, job).Scan(&attempts)
	if got := state(); !strings.HasPrefix(got[0], "pending:") || attempts != 0 {
		t.Fatalf("final pass did not requeue: %v, attempts %d", got, attempts)
	}
	crashUntilFailed()
	jobrunner.Tick(ctx, eng) // nothing pending or claimed, final pass done: close
	var jobState, snapState string
	db.QueryRow(ctx, `SELECT j.state, s.state FROM job j JOIN snapshot s ON s.id = j.snapshot_id WHERE j.id = $1`, job).Scan(&jobState, &snapState)
	if got := state(); got[0] != "failed:lease_expired:_2_attempts" || jobState != "succeeded" || snapState != "closed" {
		t.Errorf("tasks %v, job %s, snapshot %s", got, jobState, snapState)
	}
	var obs int
	db.QueryRow(ctx, `SELECT count(*) FROM observation`).Scan(&obs)
	if obs != 0 {
		t.Errorf("%d observations written for failed tasks", obs)
	}
}
