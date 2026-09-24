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
	Breakdown      Breakdown
	Thresholds     map[string]any
}

// Breakdown is what a verdict shows beyond its classification: which baseline devices did not come
// back, and why. Every reason is always present, so a reader asking for one never has to tell "none
// of those" from "this judgement does not report that".
type Breakdown struct {
	NoBaseline        bool                 `json:"no_baseline"`
	Missing           map[string]Addresses `json:"missing"`
	PerimeterFiltered Addresses            `json:"perimeter_filtered"`
}

// Addresses is a count and the addresses behind it.
type Addresses struct {
	Count   int      `json:"count"`
	Targets []string `json:"targets"`
}

// The reasons a baseline device can fail to come back. not_attempted is the one this feature
// exists for: the others are outcomes the snapshot already records about itself.
var reasons = []string{"unreachable", "denied", "unsupported", "parse_failed", "not_attempted"}

func newBreakdown() Breakdown {
	b := Breakdown{Missing: map[string]Addresses{}, PerimeterFiltered: Addresses{Targets: []string{}}}
	for _, r := range reasons {
		b.Missing[r] = Addresses{Targets: []string{}}
	}
	return b
}

func (b *Breakdown) add(reason, target string) {
	a := b.Missing[reason]
	a.Count++
	a.Targets = append(a.Targets, target)
	b.Missing[reason] = a
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

	j := Judgement{SnapshotID: snapshotID, Reached: reached, BaselineID: baseline, Breakdown: newBreakdown()}
	if baseline == nil {
		j.Classification = Published
		j.Breakdown.NoBaseline = true
	} else {
		devices, err := compare(ctx, db, *baseline, snapshotID, s.perimeterID)
		if err != nil {
			return Judgement{}, err
		}
		for _, d := range devices {
			switch {
			case d.filtered:
				j.Breakdown.PerimeterFiltered.Count++
				j.Breakdown.PerimeterFiltered.Targets = append(j.Breakdown.PerimeterFiltered.Targets, d.target)
			case d.carried:
				j.BaselineCount++
				j.CarriedOver++
			default:
				j.BaselineCount++
				j.Breakdown.add(d.reason, d.target)
			}
		}
		coverage := 1.0
		if j.BaselineCount > 0 {
			coverage = float64(j.CarriedOver) / float64(j.BaselineCount)
		}
		j.Coverage = &coverage
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

// reachedCount counts devices, not identity observations: a device that answered on several
// addresses has one observation per address, all collected, and only the one without
// duplicate_of_task stands for the device itself.
func reachedCount(ctx context.Context, db *pgxpool.Pool, snapshotID int64) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT count(*) FROM observation
		WHERE snapshot_id = $1 AND fact_family = 'identity' AND status = 'collected'
		  AND NOT (parsed -> 0 ? 'duplicate_of_task')`, snapshotID).Scan(&n)
	return n, err
}
