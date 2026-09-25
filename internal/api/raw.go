package api

import (
	"encoding/hex"
	"errors"
	"net/http"
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

// GET /v1/observations/{id}: one observation and the commands behind it, each with the hash of what it
// printed. The parsed facts are left out: they are not evidence the contract names and can be large.
func (s *Server) observation(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	id, ok := pathID(w, r, "id")
	if !ok {
		return nil
	}
	ctx := r.Context()
	var o observation
	err := tx.QueryRow(ctx, `
		SELECT id, snapshot_id, collected_at, host(target), transport, platform, recipe_id, fact_family,
		       status, detail
		FROM observation WHERE id = $1`, id).Scan(&o.ID, &o.SnapshotID, &o.CollectedAt, &o.Target,
		&o.Transport, &o.Platform, &o.RecipeID, &o.Family, &o.Status, &o.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_such_observation"})
		return nil
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
	var hash []byte
	err := tx.QueryRow(r.Context(), `
		SELECT hash FROM observation_raw WHERE observation_id = $1 AND step_id = $2`,
		id, r.PathValue("step")).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM observation WHERE id = $1)`, id).Scan(&exists); err != nil {
			return err
		}
		body := map[bool]string{true: "no_such_step", false: "no_such_observation"}[exists]
		writeJSON(w, http.StatusNotFound, map[string]string{"error": body})
		return nil
	} else if err != nil {
		return err
	}
	b, err := s.raw.Get(r.Context(), hash)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
	return nil
}
