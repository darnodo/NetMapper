package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// device is one entity as a caller sees it. confidence restates weak in the vocabulary every element
// shares, so a consumer reads one field whatever it is looking at (FR-002a).
type device struct {
	id         int64
	DeviceKey  string     `json:"device_key"`
	Hostname   string     `json:"hostname"`
	Platform   string     `json:"platform"`
	Targets    []string   `json:"targets"`
	Weak       bool       `json:"weak"`
	Confidence string     `json:"confidence"`
	FirstSeen  time.Time  `json:"first_seen"`
	LastSeen   time.Time  `json:"last_seen"`
	Evidence   []Evidence `json:"evidence"`
}

type alias struct {
	Spelling string `json:"spelling"`
	Source   string `json:"source"`
}

type iface struct {
	id            int64
	CanonicalName string     `json:"canonical_name"`
	Source        string     `json:"source"`
	Confidence    string     `json:"confidence"`
	Description   *string    `json:"description"`
	AdminState    *string    `json:"admin_state"`
	OperState     *string    `json:"oper_state"`
	SpeedBps      *int64     `json:"speed_bps"`
	MTU           *int32     `json:"mtu"`
	MAC           *string    `json:"mac"`
	FirstSeen     time.Time  `json:"first_seen"`
	LastSeen      time.Time  `json:"last_seen"`
	Aliases       []alias    `json:"aliases"`
	Evidence      []Evidence `json:"evidence"`
}

type edge struct {
	id         int64
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	FromRef    string          `json:"from_ref"`
	ToRef      string          `json:"to_ref"`
	Confidence string          `json:"confidence"`
	Attributes json.RawMessage `json:"attributes"`
	FirstSeen  time.Time       `json:"first_seen"`
	LastSeen   time.Time       `json:"last_seen"`
	Evidence   []Evidence      `json:"evidence"`
}

// GET /v1/devices: every device of a snapshot, without ports or edges (FR-001c). No filter and no
// search, by decision.
func (s *Server) devices(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	snap, ok, err := pickSnapshot(w, r, tx)
	if !ok || err != nil || !requireGraph(w, snap) {
		return err
	}
	devs, err := loadDevices(r.Context(), tx, snap.ID, nil)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snap, "devices": devs})
	return nil
}

// GET /v1/devices/{name}: one device with its interfaces and every edge touching it, uncapped
// (FR-001d). The answer names the device key it resolved to, whatever was asked for (FR-001b).
func (s *Server) device(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	snap, d, ok, err := s.pickDevice(w, r, tx, r.PathValue("name"))
	if !ok || err != nil {
		return err
	}
	ctx := r.Context()
	ifs, err := loadInterfaces(ctx, tx, `i.entity_id = $1`, d.id)
	if err != nil {
		return err
	}
	edges, err := loadEdges(ctx, tx, `g.from_entity_id = $1 OR g.to_entity_id = $1`, d.id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		Snapshot SnapshotRef `json:"snapshot"`
		device
		Interfaces []iface `json:"interfaces"`
		Edges      []edge  `json:"edges"`
	}{snap, d, ifs, edges})
	return nil
}

// GET /v1/interfaces/{device}/{name}: one port of one device, with the edges touching it. A
// convenience over the device endpoint, built from the same loaders.
func (s *Server) iface(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
	snap, d, ok, err := s.pickDevice(w, r, tx, r.PathValue("device"))
	if !ok || err != nil {
		return err
	}
	ctx := r.Context()
	ifs, err := loadInterfaces(ctx, tx, `i.entity_id = $1 AND i.canonical_name = $2`, d.id, r.PathValue("name"))
	if err != nil {
		return err
	}
	if len(ifs) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no_such_interface", "snapshot": snap})
		return nil
	}
	edges, err := loadEdges(ctx, tx, `g.from_interface_id = $1 OR g.to_interface_id = $1`, ifs[0].id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot": snap, "device_key": d.DeviceKey, "interface": ifs[0], "edges": edges,
	})
	return nil
}

// pickDevice resolves a name within the requested snapshot. Absent is 404 and ambiguous is 409, and
// both say which snapshot was searched, since a caller who named none cannot otherwise tell. This is
// the one place "more than one match" is decided, for the hostname and the address alike.
func (s *Server) pickDevice(w http.ResponseWriter, r *http.Request, tx pgx.Tx, name string) (SnapshotRef, device, bool, error) {
	snap, ok, err := pickSnapshot(w, r, tx)
	if !ok || err != nil || !requireGraph(w, snap) {
		return snap, device{}, false, err
	}
	ids, keys, err := resolveDevice(r.Context(), tx, snap.ID, name)
	if err != nil {
		return snap, device{}, false, err
	}
	switch len(ids) {
	case 0:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no_such_device", "snapshot": snap})
		return snap, device{}, false, nil
	case 1:
	default:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ambiguous", "candidates": keys, "snapshot": snap})
		return snap, device{}, false, nil
	}
	devs, err := loadDevices(r.Context(), tx, snap.ID, ids)
	if err != nil {
		return snap, device{}, false, err
	}
	if len(devs) != 1 {
		return snap, device{}, false, fmt.Errorf("device %d resolved but %d rows loaded", ids[0], len(devs))
	}
	return snap, devs[0], true, nil
}

// resolveDevice tries the device key, then the hostname, then an address the device answered on, and
// returns every match of the first tier that matches anything, keys sorted (FR-001a, research R10).
// The key is the only form guaranteed unique, so a caller who used it never meets an ambiguity. The
// match is exact: what a device reports is not guessed at.
func resolveDevice(ctx context.Context, tx pgx.Tx, snapshotID int64, name string) ([]int64, []string, error) {
	rows, err := tx.Query(ctx, `
		WITH m AS (
			SELECT id, device_key,
			       CASE WHEN device_key = $2 THEN 1
			            WHEN attributes->>'hostname' = $2 THEN 2
			            WHEN attributes->'targets' ? $2 THEN 3 END AS tier
			FROM entity WHERE snapshot_id = $1
		)
		SELECT id, device_key FROM m WHERE tier = (SELECT min(tier) FROM m)
		ORDER BY device_key`, snapshotID, name)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []int64
	var keys []string
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, nil, err
		}
		ids, keys = append(ids, id), append(keys, key)
	}
	return ids, keys, rows.Err()
}

// loadDevices reads the devices of a snapshot, all of them when ids is nil, with their evidence.
func loadDevices(ctx context.Context, tx pgx.Tx, snapshotID int64, ids []int64) ([]device, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, device_key, coalesce(attributes->>'hostname', ''), coalesce(attributes->>'platform', ''),
		       coalesce(attributes->'targets', '[]'::jsonb), weak, first_seen, last_seen
		FROM entity WHERE snapshot_id = $1 AND ($2::bigint[] IS NULL OR id = ANY($2))
		ORDER BY device_key`, snapshotID, ids)
	if err != nil {
		return nil, err
	}
	devs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (device, error) {
		var d device
		err := row.Scan(&d.id, &d.DeviceKey, &d.Hostname, &d.Platform, &d.Targets, &d.Weak, &d.FirstSeen, &d.LastSeen)
		d.Confidence = map[bool]string{true: "weak", false: "strong"}[d.Weak]
		d.Targets = nonNil(d.Targets)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	ev, err := entityEvidence(ctx, tx, idsOf(devs, func(d device) int64 { return d.id }))
	if err != nil {
		return nil, err
	}
	for i := range devs {
		devs[i].Evidence = ev[devs[i].id]
		if err := mustEvidence("device", devs[i].id, devs[i].Evidence); err != nil {
			return nil, err
		}
	}
	return nonNil(devs), nil
}

// loadInterfaces reads the ports matching where, with their spellings and evidence. confidence says
// whether the device described the port or a neighbour revealed it (FR-005, FR-002a).
func loadInterfaces(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]iface, error) {
	rows, err := tx.Query(ctx, `
		SELECT i.id, i.canonical_name, i.source, i.description, i.admin_state, i.oper_state, i.speed_bps,
		       i.mtu, i.mac, i.first_seen, i.last_seen
		FROM interface i WHERE `+where+` ORDER BY i.canonical_name`, args...)
	if err != nil {
		return nil, err
	}
	ifs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (iface, error) {
		var i iface
		err := row.Scan(&i.id, &i.CanonicalName, &i.Source, &i.Description, &i.AdminState, &i.OperState,
			&i.SpeedBps, &i.MTU, &i.MAC, &i.FirstSeen, &i.LastSeen)
		i.Confidence = map[string]string{"device": "described", "neighbour": "revealed"}[i.Source]
		return i, err
	})
	if err != nil {
		return nil, err
	}
	ids := idsOf(ifs, func(i iface) int64 { return i.id })
	ev, err := interfaceEvidence(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	aliases := map[int64][]alias{}
	arows, err := tx.Query(ctx, `
		SELECT interface_id, spelling, source FROM interface_alias
		WHERE interface_id = ANY($1) ORDER BY 1, 2, 3`, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var id int64
		var a alias
		if err := arows.Scan(&id, &a.Spelling, &a.Source); err != nil {
			return nil, err
		}
		aliases[id] = append(aliases[id], a)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}
	for k := range ifs {
		ifs[k].Evidence = ev[ifs[k].id]
		ifs[k].Aliases = nonNil(aliases[ifs[k].id])
		if err := mustEvidence("interface", ifs[k].id, ifs[k].Evidence); err != nil {
			return nil, err
		}
	}
	return nonNil(ifs), nil
}

// loadEdges reads every edge matching where, with no limit: a flat segment puts one edge per device on
// a single port, and a cap would be the interface choosing which cables matter (FR-001d).
func loadEdges(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]edge, error) {
	rows, err := tx.Query(ctx, `
		SELECT g.id, g.name, g.type, g.from_ref, g.to_ref, g.confidence, g.attributes, g.first_seen, g.last_seen
		FROM edge g WHERE `+where+` ORDER BY g.name`, args...)
	if err != nil {
		return nil, err
	}
	edges, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (edge, error) {
		var g edge
		err := row.Scan(&g.id, &g.Name, &g.Type, &g.FromRef, &g.ToRef, &g.Confidence, &g.Attributes,
			&g.FirstSeen, &g.LastSeen)
		return g, err
	})
	if err != nil {
		return nil, err
	}
	ev, err := edgeEvidence(ctx, tx, idsOf(edges, func(g edge) int64 { return g.id }))
	if err != nil {
		return nil, err
	}
	for k := range edges {
		edges[k].Evidence = ev[edges[k].id]
		if err := mustEvidence("edge", edges[k].id, edges[k].Evidence); err != nil {
			return nil, err
		}
	}
	return nonNil(edges), nil
}

func idsOf[T any](xs []T, id func(T) int64) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = id(x)
	}
	return out
}
