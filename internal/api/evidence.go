package api

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Evidence is one observation an element rests on: which one, when it was collected, what it was
// about and where it was collected from. The bytes behind it are one request away, at
// /v1/observations/{observation_id}?snapshot={snapshot_id} (FR-002, FR-004, research R11). Naming the
// snapshot pins the lookup to one partition (issue #6).
type Evidence struct {
	ObservationID int64     `json:"observation_id"`
	SnapshotID    int64     `json:"snapshot_id"`
	CollectedAt   time.Time `json:"collected_at"`
	Family        string    `json:"family"`
	Target        string    `json:"target"`
	// For an edge only: which end reported it. A both_ends edge has a row per side.
	Side string `json:"side,omitempty"`
}

// Each loader takes the ids of the elements in one answer and reads all their evidence in one query.
// Ordered by collection time, then observation, so an answer is the same every time it is read.

// entityEvidence walks entity → entity_claim → identifier_claim → observation, and adds the
// observations the device's address edges cite. The second half is what keeps an identity observation
// that produced no claim in the chain: the projector matches those to a device by address (004).
func entityEvidence(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64][]Evidence, error) {
	return collect(ctx, tx, `
		SELECT DISTINCT x.id, o.id, o.collected_at, o.fact_family, host(o.target), '', o.snapshot_id
		FROM (
			SELECT ec.entity_id AS id, c.snapshot_id, c.observation_id
			FROM entity_claim ec
			JOIN identifier_claim c ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
			WHERE ec.entity_id = ANY($1)
			UNION
			SELECT g.from_entity_id, ev.snapshot_id, ev.observation_id
			FROM edge g JOIN edge_evidence ev ON ev.edge_id = g.id
			WHERE g.type = 'has_address' AND g.from_entity_id = ANY($1)
		) x
		JOIN observation o ON o.snapshot_id = x.snapshot_id AND o.id = x.observation_id
		ORDER BY 1, 3, 2`, ids)
}

func interfaceEvidence(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64][]Evidence, error) {
	return collect(ctx, tx, `
		SELECT ie.interface_id, o.id, o.collected_at, o.fact_family, host(o.target), '', o.snapshot_id
		FROM interface_evidence ie
		JOIN observation o ON o.snapshot_id = ie.snapshot_id AND o.id = ie.observation_id
		WHERE ie.interface_id = ANY($1)
		ORDER BY 1, 3, 2`, ids)
}

func edgeEvidence(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64][]Evidence, error) {
	return collect(ctx, tx, `
		SELECT ev.edge_id, o.id, o.collected_at, o.fact_family, host(o.target), ev.side, o.snapshot_id
		FROM edge_evidence ev
		JOIN observation o ON o.snapshot_id = ev.snapshot_id AND o.id = ev.observation_id
		WHERE ev.edge_id = ANY($1)
		ORDER BY 1, 3, 2, 6`, ids)
}

func findingEvidence(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64][]Evidence, error) {
	return collect(ctx, tx, `
		SELECT fe.finding_id, o.id, o.collected_at, o.fact_family, host(o.target), '', o.snapshot_id
		FROM finding_evidence fe
		JOIN observation o ON o.snapshot_id = fe.snapshot_id AND o.id = fe.observation_id
		WHERE fe.finding_id = ANY($1)
		ORDER BY 1, 3, 2`, ids)
}

func collect(ctx context.Context, tx pgx.Tx, sql string, ids []int64) (map[int64][]Evidence, error) {
	out := map[int64][]Evidence{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var e Evidence
		if err := rows.Scan(&id, &e.ObservationID, &e.CollectedAt, &e.Family, &e.Target, &e.Side, &e.SnapshotID); err != nil {
			return nil, err
		}
		out[id] = append(out[id], e)
	}
	return out, rows.Err()
}

// mustEvidence refuses to let an element out without its evidence. An element that cannot carry it is
// a defect to surface, not a sparse answer to serve (FR-002: "MUST NOT be served").
func mustEvidence(kind string, id int64, ev []Evidence) error {
	if len(ev) == 0 {
		return fmt.Errorf("%s %d has no evidence", kind, id)
	}
	return nil
}
