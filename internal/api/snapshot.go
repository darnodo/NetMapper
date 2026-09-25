package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// SnapshotRef is the envelope every answer carries: which snapshot it was built from and what the
// coverage gate said about it, so a caller never needs a second request to learn that the graph they
// just read is quarantined (FR-017, FR-020, SC-009).
//
// verdict and coverage are always present, as null for a snapshot never judged, rather than omitted.
type SnapshotRef struct {
	ID       int64      `json:"id"`
	ClosedAt *time.Time `json:"closed_at"`
	State    string     `json:"state"`
	Verdict  *string    `json:"verdict"`
	Coverage *float64   `json:"coverage"`
	// A projection exists and was built from the entity set the snapshot carries now. A projection
	// whose resolution_at is older answers from a set that no longer exists (research R9).
	HasGraph bool `json:"has_graph"`
}

var errNoSnapshot = errors.New("no such snapshot")

const snapshotColumns = `
	SELECT s.id, s.closed_at, s.state, j.classification, j.coverage::float8,
	       EXISTS (SELECT 1 FROM projection p JOIN resolution r ON r.snapshot_id = p.snapshot_id
	               WHERE p.snapshot_id = s.id AND p.resolution_at = r.computed_at) AS has_graph
	FROM snapshot s
	LEFT JOIN snapshot_judgement j ON j.snapshot_id = s.id AND j.active`

func scanSnapshot(row pgx.Row) (SnapshotRef, error) {
	var s SnapshotRef
	err := row.Scan(&s.ID, &s.ClosedAt, &s.State, &s.Verdict, &s.Coverage, &s.HasGraph)
	return s, err
}

func loadSnapshot(ctx context.Context, tx pgx.Tx, id int64) (SnapshotRef, error) {
	s, err := scanSnapshot(tx.QueryRow(ctx, snapshotColumns+` WHERE s.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return s, errNoSnapshot
	}
	return s, err
}

// defaultSnapshot is the most recently closed snapshot carrying a current graph, whatever its verdict
// (FR-019). Choosing on the verdict would hide a quarantine behind an older answer; not choosing on the
// graph would answer "no graph" for the sweep that separates closing from projection.
func defaultSnapshot(ctx context.Context, tx pgx.Tx) (SnapshotRef, error) {
	s, err := scanSnapshot(tx.QueryRow(ctx, `SELECT * FROM (`+snapshotColumns+`
		WHERE s.state = 'closed') x
		WHERE x.has_graph ORDER BY x.closed_at DESC, x.id DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return s, errNoSnapshot
	}
	return s, err
}

// pickSnapshot is the snapshot a request names in ?snapshot=, or the default. A named snapshot is
// answered from exactly that one, whatever state it is in (FR-018). ok is false when the response has
// already been written.
func pickSnapshot(w http.ResponseWriter, r *http.Request, tx pgx.Tx) (s SnapshotRef, ok bool, err error) {
	q := r.URL.Query().Get("snapshot")
	if q == "" {
		s, err = defaultSnapshot(r.Context(), tx)
		if errors.Is(err, errNoSnapshot) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "not_projected", "snapshot": nil})
			return s, false, nil
		}
		return s, err == nil, err
	}
	id, perr := strconv.ParseInt(q, 10, 64)
	if perr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_snapshot"})
		return s, false, nil
	}
	s, err = loadSnapshot(r.Context(), tx, id)
	if errors.Is(err, errNoSnapshot) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_such_snapshot"})
		return s, false, nil
	}
	return s, err == nil, err
}

// requireGraph refuses a snapshot that carries no current graph: still open, closed but not yet
// projected, or projected from an entity set since replaced. Absent and empty are different answers
// (FR-009).
func requireGraph(w http.ResponseWriter, s SnapshotRef) bool {
	if s.State == "closed" && s.HasGraph {
		return true
	}
	writeJSON(w, http.StatusConflict, map[string]any{"error": "not_projected", "snapshot": s})
	return false
}

// GET /v1/snapshots: every snapshot, newest first. It lists snapshots rather than answering from one,
// so it carries no envelope; each entry is one.
func (s *Server) snapshots(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	rows, err := tx.Query(r.Context(), snapshotColumns+` ORDER BY s.id DESC`)
	if err != nil {
		return err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SnapshotRef, error) { return scanSnapshot(row) })
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": nonNil(out)})
	return nil
}

// judgement is what the coverage gate recorded: the verdict, the figure and what it was compared
// against (FR-007).
type judgement struct {
	Classification     string          `json:"classification"`
	Coverage           *float64        `json:"coverage"`
	BaselineSnapshotID *int64          `json:"baseline_snapshot_id"`
	BaselineDevices    int             `json:"baseline_devices"`
	CarriedOver        int             `json:"carried_over"`
	Reached            int             `json:"reached"`
	Thresholds         json.RawMessage `json:"thresholds"`
	Breakdown          json.RawMessage `json:"breakdown"`
	GateVersion        int             `json:"gate_version"`
	ComputedAt         time.Time       `json:"computed_at"`
}

// GET /v1/snapshots/{id}: one snapshot with its judgement. Served whatever its state, since a verdict
// needs no graph.
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil
	}
	ref, err := loadSnapshot(r.Context(), tx, id)
	if errors.Is(err, errNoSnapshot) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_such_snapshot"})
		return nil
	} else if err != nil {
		return err
	}
	var j judgement
	err = tx.QueryRow(r.Context(), `
		SELECT classification, coverage::float8, baseline_snapshot_id, baseline_devices, carried_over,
		       reached, thresholds, breakdown, gate_version, computed_at
		FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, id).Scan(
		&j.Classification, &j.Coverage, &j.BaselineSnapshotID, &j.BaselineDevices, &j.CarriedOver,
		&j.Reached, &j.Thresholds, &j.Breakdown, &j.GateVersion, &j.ComputedAt)
	var jp *judgement
	switch {
	case err == nil:
		jp = &j
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": ref, "judgement": jp})
	return nil
}

// nonNil turns a nil slice into an empty one, so an answer with nothing in it reads [] and not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
