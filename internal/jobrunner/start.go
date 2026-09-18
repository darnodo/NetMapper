// Package jobrunner starts, runs to completion and cancels discovery jobs. It never opens a
// device session and never resolves a secret (principle III).
package jobrunner

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/config"
	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/perimeter"
)

// Invalid lists why a run refused to start, one problem per entry (FR-001).
type Invalid []string

func (e Invalid) Error() string { return strings.Join(e, "\n") }

// Params is job.parameters.
type Params struct {
	PerimeterID   int64            `json:"perimeter_id"`
	SeedSet       string           `json:"seed_set"`
	Discovery     config.Discovery `json:"discovery"`
	FinalPassDone bool             `json:"final_pass_done,omitempty"`
}

// Start validates doc, checks FR-001, stores the document and starts a discovery. It runs as
// netmapper_operator. The API's job endpoint will call it too.
func Start(ctx context.Context, db *pgxpool.Pool, doc []byte, perimeterName, seedSetName, requestedBy string) (int64, error) {
	d, err := config.Parse(doc)
	if err != nil {
		return 0, Invalid(strings.Split(err.Error(), "\n"))
	}

	var problems []string
	var per perimeter.Perimeter
	pi := slices.IndexFunc(d.Perimeters, func(p config.Perimeter) bool { return p.Name == perimeterName })
	if pi < 0 {
		problems = append(problems, fmt.Sprintf("perimeter %q not found", perimeterName))
	} else {
		per = perimeter.Perimeter{Include: d.Perimeters[pi].Include, Exclude: d.Perimeters[pi].Exclude}
		if len(per.Include) == 0 {
			problems = append(problems, fmt.Sprintf("perimeter %q has no include range", perimeterName))
		}
		if !slices.ContainsFunc(d.CredentialSets, func(c config.CredentialSet) bool { return slices.Contains(c.Perimeters, perimeterName) }) {
			problems = append(problems, fmt.Sprintf("no credential set covers perimeter %q", perimeterName))
		}
	}

	type seed struct {
		addr netip.Addr
		name string
	}
	var seeds []seed
	si := slices.IndexFunc(d.SeedSets, func(s config.SeedSet) bool { return s.Name == seedSetName })
	switch {
	case si < 0:
		problems = append(problems, fmt.Sprintf("seed set %q not found", seedSetName))
	case len(d.SeedSets[si].Targets) == 0:
		problems = append(problems, fmt.Sprintf("seed set %q has no target", seedSetName))
	case pi >= 0:
		for _, target := range d.SeedSets[si].Targets {
			a, err := per.Resolve(ctx, target)
			switch {
			case errors.Is(err, perimeter.ErrOutOfPerimeter):
				problems = append(problems, fmt.Sprintf("seed %q is outside perimeter %q", target, perimeterName))
			case err != nil:
				problems = append(problems, fmt.Sprintf("seed %q does not resolve", target))
			default:
				s := seed{addr: a}
				if _, err := netip.ParseAddr(target); err != nil {
					s.name = target
				}
				seeds = append(seeds, s)
			}
		}
	}
	if len(problems) > 0 {
		return 0, Invalid(problems)
	}

	var jobID int64
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var cv int64
		if err := tx.QueryRow(ctx, `INSERT INTO config_version (posted_by, document) VALUES ($1, $2) RETURNING id`,
			requestedBy, string(d.Raw)).Scan(&cv); err != nil {
			return err
		}
		perimeterIDs := map[string]int64{}
		for _, p := range d.Perimeters {
			var id int64
			if err := tx.QueryRow(ctx, `INSERT INTO perimeter (config_version, name, include, exclude) VALUES ($1, $2, $3, $4) RETURNING id`,
				cv, p.Name, p.Include, append([]netip.Prefix{}, p.Exclude...)).Scan(&id); err != nil {
				return err
			}
			perimeterIDs[p.Name] = id
		}
		for i, c := range d.CredentialSets {
			var ids []int64
			for _, p := range c.Perimeters {
				ids = append(ids, perimeterIDs[p])
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO credential_set (config_version, name, position, kind, username, secret_ref, max_attempts_per_device, perimeter_ids)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				cv, c.Name, i, c.Kind, nullable(c.Username), c.SecretRef, c.MaxAttemptsPerDevice, ids); err != nil {
				return err
			}
		}
		for _, s := range d.SeedSets {
			if _, err := tx.Exec(ctx, `INSERT INTO seed_set (config_version, name, targets) VALUES ($1, $2, $3)`,
				cv, s.Name, append([]string{}, s.Targets...)); err != nil {
				return err
			}
		}
		params := Params{PerimeterID: perimeterIDs[perimeterName], SeedSet: seedSetName, Discovery: d.Discovery}
		if err := tx.QueryRow(ctx, `
			INSERT INTO job (state, config_version, parameters, requested_by, started_at)
			VALUES ('running', $1, $2, $3, now()) RETURNING id`, cv, params, requestedBy).Scan(&jobID); err != nil {
			return err
		}
		var snap int64
		if err := tx.QueryRow(ctx, `INSERT INTO snapshot (job_id) VALUES ($1) RETURNING id`, jobID).Scan(&snap); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE job SET snapshot_id = $1 WHERE id = $2`, snap, jobID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO parse_generation (snapshot_id, active) VALUES ($1, true)`, snap); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT create_snapshot_partitions($1), create_task_partition($2)`, snap, jobID); err != nil {
			return err
		}
		for _, s := range seeds {
			if err := frontier.Enqueue(ctx, tx, jobID, "find", s.addr, s.name, "", 0, nil); err != nil {
				return err
			}
		}
		return nil
	})
	return jobID, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
