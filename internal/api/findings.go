package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// findingConfidence places every finding category the schema admits (FR-002a). A category raised from
// what one observation recorded is direct; one raised by comparing several is derived. A category
// missing from this map is an error, not a guess, so a sixth one cannot ship without a decision here.
var findingConfidence = map[string]string{
	"unknown_platform":  "direct",
	"parse_failed":      "direct",
	"credential_denied": "direct",
	"identity_conflict": "derived",
	"link_disagreement": "derived",
}

type finding struct {
	ID         int64           `json:"id"`
	Domain     string          `json:"domain"`
	Category   string          `json:"category"`
	Severity   string          `json:"severity"`
	SubjectRef string          `json:"subject_ref"`
	State      string          `json:"state"`
	Detail     json.RawMessage `json:"detail"`
	Confidence string          `json:"confidence"`
	Evidence   []Evidence      `json:"evidence"`
}

// GET /v1/findings: what the crawl and the computations reported against a snapshot (FR-008).
// Findings exist before projection, so a snapshot without a graph is still answered.
func (s *Server) findings(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	snap, ok, err := pickSnapshot(w, r, tx)
	if !ok || err != nil {
		return err
	}
	rows, err := tx.Query(r.Context(), `
		SELECT id, domain, category, severity, subject_ref, state, detail
		FROM finding WHERE snapshot_id = $1 ORDER BY id`, snap.ID)
	if err != nil {
		return err
	}
	fs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (finding, error) {
		var f finding
		err := row.Scan(&f.ID, &f.Domain, &f.Category, &f.Severity, &f.SubjectRef, &f.State, &f.Detail)
		return f, err
	})
	if err != nil {
		return err
	}
	ev, err := findingEvidence(r.Context(), tx, idsOf(fs, func(f finding) int64 { return f.ID }))
	if err != nil {
		return err
	}
	for i := range fs {
		c, ok := findingConfidence[fs[i].Category]
		if !ok {
			return fmt.Errorf("finding %d: category %q has no confidence", fs[i].ID, fs[i].Category)
		}
		fs[i].Confidence = c
		fs[i].Evidence = ev[fs[i].ID]
		if err := mustEvidence("finding", fs[i].ID, fs[i].Evidence); err != nil {
			return err
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snap, "findings": nonNil(fs)})
	return nil
}
