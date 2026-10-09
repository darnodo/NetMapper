package api

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/fact"
)

// factObservation is one observation of a family, as the facts endpoint serves it: what the device
// answered and the evidence behind it (specs/007-device-management-facts/contracts/rest-facts.md).
type factObservation struct {
	Status   string           `json:"status"`
	Detail   *string          `json:"detail"`
	Rows     []map[string]any `json:"rows"`
	Evidence []Evidence       `json:"evidence"`
}

// GET /v1/devices/{name}/facts/{family}: the observations of one fact family for one device, from
// the snapshot's active parse generation. Any family of the schema can be asked for. The family is
// checked before the device, so a typo in it is not reported as a missing device. A device with no
// observation of the family is not_collected, distinct from an empty or unsupported one, which is a
// 200 with that status: there is nothing to cite, so nothing is served (constitution I).
func (s *Server) deviceFacts(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	snap, ok, err := pickSnapshot(w, r, tx)
	if !ok || err != nil || !requireGraph(w, snap) {
		return err
	}
	family := r.PathValue("family")
	if _, known := fact.Families[family]; !known {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no_such_family", "snapshot": snap})
		return nil
	}
	snap, d, ok, err := s.pickDevice(w, r, tx, r.PathValue("name"))
	if !ok || err != nil {
		return err
	}
	rows, err := tx.Query(r.Context(), `
		SELECT o.status, o.detail, o.parsed,
		       o.id, o.snapshot_id, o.collected_at, o.fact_family, host(o.target)
		FROM observation o
		JOIN parse_generation pg ON pg.snapshot_id = o.snapshot_id AND pg.id = o.parse_generation_id AND pg.active
		WHERE o.snapshot_id = $1 AND o.fact_family = $2 AND host(o.target) = ANY($3)
		ORDER BY o.collected_at, o.id`, snap.ID, family, d.Targets)
	if err != nil {
		return err
	}
	obs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (factObservation, error) {
		var o factObservation
		var parsed []byte
		var e Evidence
		if err := row.Scan(&o.Status, &o.Detail, &parsed, &e.ObservationID, &e.SnapshotID, &e.CollectedAt,
			&e.Family, &e.Target); err != nil {
			return o, err
		}
		if parsed != nil {
			if err := json.Unmarshal(parsed, &o.Rows); err != nil {
				return o, err
			}
		}
		o.Rows, o.Evidence = nonNil(o.Rows), []Evidence{e}
		return o, nil
	})
	if err != nil {
		return err
	}
	if len(obs) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_collected", "snapshot": snap})
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot": snap, "device_key": d.DeviceKey, "family": family, "observations": obs,
	})
	return nil
}
