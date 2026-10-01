package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type observation struct {
	ID          int64     `json:"id"`
	SnapshotID  int64     `json:"snapshot_id"`
	CollectedAt time.Time `json:"collected_at"`
	Target      string    `json:"target"`
	Transport   *string   `json:"transport"`
	Platform    *string   `json:"platform"`
	RecipeID    *string   `json:"recipe_id"`
	Family      string    `json:"family"`
	Status      string    `json:"status"`
	Detail      *string   `json:"detail"`
}

type command struct {
	Step    string `json:"step"`
	Command string `json:"command"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
}

// observationScope reads the optional ?snapshot= of the observation endpoints. With it, a lookup is
// pinned to that snapshot's partition; without it, every partition is searched (issue #6). Unlike the
// graph endpoints there is no default snapshot: an observation id belongs to exactly one snapshot, so
// a default would answer 404 for evidence from any older crawl. ok is false when the response has
// already been written.
func observationScope(w http.ResponseWriter, r *http.Request) (snapshot *int64, ok bool) {
	q := r.URL.Query().Get("snapshot")
	if q == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_snapshot"})
		return nil, false
	}
	return &id, true
}

// pin narrows a query on observation or observation_raw to one snapshot when the request named one,
// adding the condition as the next placeholder.
func pin(q string, args []any, snapshot *int64) (string, []any) {
	if snapshot == nil {
		return q, args
	}
	return q + fmt.Sprintf(" AND snapshot_id = $%d", len(args)+1), append(args, *snapshot)
}

// notFound answers the 404 for an observation lookup that found nothing: no_such_snapshot when the
// request named a snapshot that does not exist, otherwise the error given.
func notFound(ctx context.Context, w http.ResponseWriter, tx pgx.Tx, snapshot *int64, body string) error {
	if snapshot != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM snapshot WHERE id = $1)`, *snapshot).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			body = "no_such_snapshot"
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": body})
	return nil
}

// GET /v1/observations/{id}: one observation and the commands behind it, each with the hash of what it
// printed. The parsed facts are left out: they are not evidence the contract names and can be large.
func (s *Server) observation(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil
	}
	snapshot, ok := observationScope(w, r)
	if !ok {
		return nil
	}
	ctx := r.Context()
	q, args := pin(`
		SELECT id, snapshot_id, collected_at, host(target), transport, platform, recipe_id, fact_family,
		       status, detail
		FROM observation WHERE id = $1`, []any{id}, snapshot)
	var o observation
	err := tx.QueryRow(ctx, q, args...).Scan(&o.ID, &o.SnapshotID, &o.CollectedAt, &o.Target,
		&o.Transport, &o.Platform, &o.RecipeID, &o.Family, &o.Status, &o.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(ctx, w, tx, snapshot, "no_such_observation")
	} else if err != nil {
		return err
	}
	snap, err := loadSnapshot(ctx, tx, o.SnapshotID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT r.step_id, r.command, r.hash, o.size
		FROM observation_raw r JOIN raw_object o ON o.hash = r.hash
		WHERE r.snapshot_id = $1 AND r.observation_id = $2 ORDER BY length(r.step_id), r.step_id`, o.SnapshotID, o.ID)
	if err != nil {
		return err
	}
	cmds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (command, error) {
		var c command
		var hash []byte
		err := row.Scan(&c.Step, &c.Command, &hash, &c.Size)
		c.Hash = hex.EncodeToString(hash)
		return c, err
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snap, "observation": o, "commands": nonNil(cmds)})
	return nil
}

// GET /v1/observations/{id}/raw/{step}: the bytes one command printed, served by the interface itself
// so the evidence chain ends at the thing it names (FR-004). The object store is only read here; the
// store's key and bucket stay in the log when a read fails, never in the response (FR-004a, FR-014).
func (s *Server) rawOutput(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil
	}
	snapshot, ok := observationScope(w, r)
	if !ok {
		return nil
	}
	ctx := r.Context()
	q, args := pin(`SELECT hash FROM observation_raw WHERE observation_id = $1 AND step_id = $2`,
		[]any{id, r.PathValue("step")}, snapshot)
	var hash []byte
	err := tx.QueryRow(ctx, q, args...).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		// No such step, or no such observation: tell them apart.
		q, args := pin(`SELECT 1 FROM observation WHERE id = $1`, []any{id}, snapshot)
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (`+q+`)`, args...).Scan(&found); err != nil {
			return err
		}
		if found {
			return notFound(ctx, w, tx, nil, "no_such_step")
		}
		return notFound(ctx, w, tx, snapshot, "no_such_observation")
	} else if err != nil {
		return err
	}
	b, err := s.raw.Get(ctx, hash)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
	return nil
}
