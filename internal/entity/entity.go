// Package entity turns a closed snapshot's identifier claims into device entities: it groups the
// claims that describe one device, gives each device a key that means the same thing in the next
// run, and replays the operator decisions that correct both. It contacts no device, reads no pack
// and resolves no secret.
package entity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/store"
)

// What Resolve refuses to do, for callers that need to tell an operator why.
var (
	ErrNotFound  = errors.New("snapshot not found")
	ErrNotClosed = errors.New("snapshot is not closed")
)

// resolverVersion identifies the grouping that produced an entity set. Bump it by hand when a
// change to the grouping alters results, so the sets the old version wrote can be found and
// replayed instead of being re-crawled (research R9).
const resolverVersion = 1

// Entity is one resolved device in one snapshot, as stored in entity.
type Entity struct {
	SnapshotID int64
	DeviceKey  string
	Weak       bool
	Attributes Attributes
	FirstSeen  time.Time
	LastSeen   time.Time
	// The identifier_claim ids this entity was built from: the first link of the chain an answer
	// follows back to the bytes it rests on (FR-005).
	Claims []int64
}

// Attributes is what an entity carries beyond its key: the weak identifiers that describe it and
// the addresses it answered on. Evidence lives in entity_claim; this is for reading.
type Attributes struct {
	Hostname string `json:"hostname,omitempty"`
	Platform string `json:"platform,omitempty"`
	// Every address the device answered on in this snapshot, the winning observation's first.
	Targets []string `json:"targets"`
	// A flat copy of the strong claims. entity_claim is the authoritative link.
	Identifiers map[string]string `json:"identifiers"`
}

// ClaimGroup is one identity observation with the claims it produced: the unit that gets grouped
// (research R2). A device reached on two addresses has one group per address.
type ClaimGroup struct {
	ObservationID int64
	TaskID        int64
	Target        string
	Hostname      string
	Platform      string
	// True when the crawl ended this task as a duplicate of another. The group that is not a
	// duplicate stands for the device itself, and its values win (research R15).
	Duplicate   bool
	CollectedAt time.Time
	Claims      []Claim
}

// Claim is one identifier claim, read with the strength the pack declared. Nothing here interprets
// a kind or a value (Principle V).
type Claim struct {
	ID       int64
	Kind     string
	Value    string
	Strength string
}

func (c Claim) strong() bool { return c.Strength == "strong" }

// token is the `<kind>:<value>` form a device key and a decision subject are written in.
func (c Claim) token() string { return c.Kind + ":" + c.Value }

// Result is what a resolution produced, for an operator command to print.
type Result struct {
	SnapshotID int64
	Entities   int
	Weak       int
	Conflicts  int
}

// Resolve replaces a closed snapshot's entity set with the one its claims and the recorded
// decisions produce. It is the single entry point the engine's sweep, both operator subcommands and
// the tests go through, which is what makes "same inputs, same grouping" testable (FR-013).
func Resolve(ctx context.Context, db *pgxpool.Pool, snapshotID int64) (Result, error) {
	s, err := describe(ctx, db, snapshotID)
	if err != nil {
		return Result{}, err
	}
	if s.state != "closed" {
		return Result{}, fmt.Errorf("snapshot %d: %w", snapshotID, ErrNotClosed)
	}
	var r Result
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var err error
		r, err = resolveIn(ctx, tx, s)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return r, nil
}

// resolveIn does the whole resolution in one transaction: read, group, key, write. Everything is
// inside it because a consumer must never see a partial set and because the registry a component is
// matched against must not change under it while it is being matched (FR-015, research R8).
func resolveIn(ctx context.Context, tx pgx.Tx, s snapshot) (Result, error) {
	// Two resolutions of one snapshot wait for each other here rather than interleaving. The lock
	// covers this snapshot, not the registry: two snapshots minting the same identifier at once
	// conflict on device_identifier's primary key and the loser is retried (research R8).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, s.id); err != nil {
		return Result{}, err
	}

	groups, err := readGroups(ctx, tx, s.id)
	if err != nil {
		return Result{}, err
	}
	dec, err := readDecisions(ctx, tx, s.perimeter)
	if err != nil {
		return Result{}, err
	}
	reg, err := loadRegistry(ctx, tx, s.perimeter, groups, dec)
	if err != nil {
		return Result{}, err
	}

	components, conflicts := group(groups, reg, dec)
	entities, keyed := assignKeys(s, components, reg)
	conflicts = append(conflicts, keyed...)

	if err := write(ctx, tx, s, entities, materialise(entities, conflicts), reg, dec.highest); err != nil {
		return Result{}, err
	}

	r := Result{SnapshotID: s.id, Entities: len(entities), Conflicts: len(conflicts)}
	for _, e := range entities {
		if e.Weak {
			r.Weak++
		}
	}
	return r, nil
}

type snapshot struct {
	id        int64
	state     string
	perimeter string
}

// describe reads the snapshot and the name of the perimeter its run used. The perimeter is taken by
// name, not by id: every run posts the configuration document whole, so each run inserts a fresh
// perimeter row and an id would scope a device key to one run (002, research R1).
func describe(ctx context.Context, db store.DB, id int64) (snapshot, error) {
	s := snapshot{id: id}
	err := db.QueryRow(ctx, `
		SELECT s.state, p.name
		FROM snapshot s
		JOIN job j ON j.snapshot_id = s.id
		JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
		WHERE s.id = $1`, id).Scan(&s.state, &s.perimeter)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, fmt.Errorf("snapshot %d: %w", id, ErrNotFound)
	}
	return s, err
}
