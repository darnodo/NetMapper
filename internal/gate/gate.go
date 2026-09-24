// Package gate judges a closed snapshot: how much of what the perimeter's previous run reached it
// found again, and whether that is enough to publish. It contacts no device and reads no pack.
package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The three verdicts a snapshot can carry.
const (
	Published   = "published"
	Degraded    = "degraded"
	Quarantined = "quarantined"
)

// What Judge refuses to do, for callers that need to tell an operator why.
var (
	ErrNotFound  = errors.New("snapshot not found")
	ErrNotClosed = errors.New("snapshot is not closed")
)

// gateVersion identifies the calculation that produced a judgement. Bump it by hand when a change
// to the comparison alters results, so the judgements the old version wrote can be found and
// replayed instead of being re-crawled.
const gateVersion = 1

// Judgement is one verdict, as stored in snapshot_judgement.
type Judgement struct {
	SnapshotID     int64
	BaselineID     *int64
	Classification string
	Coverage       *float64
	BaselineCount  int
	CarriedOver    int
	Reached        int
	Breakdown      map[string]any
	Thresholds     map[string]any
}

// Judge writes a new active judgement for a closed snapshot, superseding its current one if it has
// one. Judging the same snapshot again with the same inputs produces the same verdict.
func Judge(ctx context.Context, db *pgxpool.Pool, snapshotID int64) (Judgement, error) {
	s, err := describe(ctx, db, snapshotID)
	if err != nil {
		return Judgement{}, err
	}
	if s.state != "closed" {
		return Judgement{}, fmt.Errorf("snapshot %d: %w", snapshotID, ErrNotClosed)
	}

	baseline, err := selectBaseline(ctx, db, s)
	if err != nil {
		return Judgement{}, err
	}
	reached, err := reachedCount(ctx, db, snapshotID)
	if err != nil {
		return Judgement{}, err
	}

	j := Judgement{SnapshotID: snapshotID, Reached: reached, BaselineID: baseline}
	if baseline == nil {
		j.Classification = Published
		j.Breakdown = map[string]any{"no_baseline": true}
	} else {
		devices, err := compare(ctx, db, *baseline, snapshotID, s.perimeterID)
		if err != nil {
			return Judgement{}, err
		}
		missing := map[string][]string{}
		var filtered []string
		for _, d := range devices {
			switch {
			case d.filtered:
				filtered = append(filtered, d.target)
			case d.carried:
				j.BaselineCount++
				j.CarriedOver++
			default:
				j.BaselineCount++
				missing[d.reason] = append(missing[d.reason], d.target)
			}
		}
		coverage := 1.0
		if j.BaselineCount > 0 {
			coverage = float64(j.CarriedOver) / float64(j.BaselineCount)
		}
		j.Coverage = &coverage
		j.Breakdown = map[string]any{
			"no_baseline":        false,
			"missing":            missing,
			"perimeter_filtered": filtered,
		}
		j.Classification = classify(coverage, defaultThresholds)
	}
	j.Thresholds = defaultThresholds.record()

	return j, write(ctx, db, j)
}

func write(ctx context.Context, db *pgxpool.Pool, j Judgement) error {
	breakdown, err := json.Marshal(j.Breakdown)
	if err != nil {
		return err
	}
	thresholds, err := json.Marshal(j.Thresholds)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `SELECT judge_snapshot($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		j.SnapshotID, j.BaselineID, j.Classification, j.Coverage,
		j.BaselineCount, j.CarriedOver, j.Reached, breakdown, thresholds, gateVersion)
	return err
}

func reachedCount(ctx context.Context, db *pgxpool.Pool, snapshotID int64) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT count(*) FROM observation
		WHERE snapshot_id = $1 AND fact_family = 'identity' AND status = 'collected'`, snapshotID).Scan(&n)
	return n, err
}
