// Package store writes what the collector learnt: raw output by hash, observations, identifier
// claims, findings and audit rows. Everything here is insert only.
package store

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/darnodo/NetMapper/internal/fact"
)

// DB is a pool or a transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

type Observation struct {
	SnapshotID        int64
	ParseGenerationID int64
	TaskID            int64
	CollectorID       string
	Target            netip.Addr
	Transport         string // "" when nothing answered
	Platform          string
	RecipeID          string
	Family            string
	Status            string
	Detail            string
	Parsed            []map[string]any // nil unless Status is collected
	Raw               []Raw
	Claims            []Claim
}

// Raw links one step's output, already stored in the object store, to the observation.
type Raw struct {
	StepID  string
	Command string
	Hash    []byte
	Size    int
}

type Claim struct {
	Kind     string
	Subtype  string
	Value    string
	Strength string
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// WriteObservation inserts o with its raw links and claims. A task retried after it committed
// gets the existing row back with inserted = false, and nothing is written twice.
func WriteObservation(ctx context.Context, db DB, o Observation) (id int64, inserted bool, err error) {
	if o.Status == "collected" {
		if err := fact.Validate(o.Family, o.Parsed); err != nil {
			return 0, false, err
		}
	} else if o.Parsed != nil {
		return 0, false, fmt.Errorf("%s observation with status %s carries rows", o.Family, o.Status)
	}
	var parsed any
	if o.Parsed != nil {
		parsed = o.Parsed
	}
	err = db.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO observation (snapshot_id, parse_generation_id, task_id, collector_id, target,
				transport, platform, recipe_id, fact_family, status, detail, parsed)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (snapshot_id, target, fact_family, task_id) DO NOTHING
			RETURNING id)
		SELECT id, true FROM ins
		UNION ALL
		SELECT id, false FROM observation
		WHERE snapshot_id = $1 AND target = $5 AND fact_family = $9 AND task_id = $3`,
		o.SnapshotID, o.ParseGenerationID, o.TaskID, o.CollectorID, o.Target,
		null(o.Transport), null(o.Platform), null(o.RecipeID), o.Family, o.Status, null(o.Detail), parsed,
	).Scan(&id, &inserted)
	if err != nil || !inserted {
		return id, inserted, err
	}
	b := &pgx.Batch{}
	for _, r := range o.Raw {
		b.Queue(`INSERT INTO raw_object (hash, size) VALUES ($1, $2) ON CONFLICT (hash) DO NOTHING`, r.Hash, r.Size)
		b.Queue(`INSERT INTO observation_raw (snapshot_id, observation_id, step_id, command, hash) VALUES ($1, $2, $3, $4, $5)`,
			o.SnapshotID, id, r.StepID, r.Command, r.Hash)
	}
	for _, c := range o.Claims {
		b.Queue(`INSERT INTO identifier_claim (snapshot_id, observation_id, kind, subtype, value, strength) VALUES ($1, $2, $3, $4, $5, $6)`,
			o.SnapshotID, id, c.Kind, null(c.Subtype), c.Value, c.Strength)
	}
	return id, true, db.SendBatch(ctx, b).Close()
}

// RaiseFinding records a finding and the observations it cites.
func RaiseFinding(ctx context.Context, db DB, snapshotID int64, domain, category, severity, subjectRef string, detail map[string]any, evidence ...int64) error {
	if detail == nil {
		detail = map[string]any{}
	}
	var id int64
	if err := db.QueryRow(ctx, `
		INSERT INTO finding (snapshot_id, domain, category, severity, subject_ref, detail)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		snapshotID, domain, category, severity, subjectRef, detail).Scan(&id); err != nil {
		return err
	}
	for _, obs := range evidence {
		if _, err := db.Exec(ctx, `INSERT INTO finding_evidence (finding_id, snapshot_id, observation_id) VALUES ($1, $2, $3)`,
			id, snapshotID, obs); err != nil {
			return err
		}
	}
	return nil
}
