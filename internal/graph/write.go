package graph

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/store"
)

// write replaces the snapshot's interfaces, edges and disagreement findings inside the transaction
// that already holds the snapshot's advisory lock. Nothing here is a patch of what was there: a
// projection is derived data, replaced wholesale, and a consumer never sees half of it (FR-018).
func (p *projection) write(ctx context.Context, tx pgx.Tx) error {
	if err := p.clear(ctx, tx); err != nil {
		return err
	}
	if err := p.writePorts(ctx, tx); err != nil {
		return err
	}
	if err := p.writeEdges(ctx, tx); err != nil {
		return err
	}
	if err := p.writeFindings(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO projection (snapshot_id, projector_version, resolution_at, interfaces, edges)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (snapshot_id) DO UPDATE SET
			projector_version = EXCLUDED.projector_version,
			resolution_at     = EXCLUDED.resolution_at,
			interfaces        = EXCLUDED.interfaces,
			edges             = EXCLUDED.edges,
			computed_at       = now()`,
		p.snap.id, projectorVersion, p.snap.resolutionAt, p.countPorts(), len(p.edges))
	return err
}

// clear removes what a previous projection of this snapshot wrote. Aliases, evidence and edges
// cascade from the interfaces they hang off, so deleting the edges first and the interfaces second
// takes everything with it and leaves no orphan. The delete on `finding` is restricted to this
// category and this snapshot: a finding the collector or the resolver raised is not this
// projection's to remove (FR-018, FR-020, research R9).
func (p *projection) clear(ctx context.Context, tx pgx.Tx) error {
	for _, q := range []string{
		`DELETE FROM edge WHERE snapshot_id = $1`,
		`DELETE FROM interface WHERE snapshot_id = $1`,
		`DELETE FROM finding_evidence WHERE finding_id IN (
			SELECT id FROM finding WHERE snapshot_id = $1 AND category = 'link_disagreement')`,
		`DELETE FROM finding WHERE snapshot_id = $1 AND category = 'link_disagreement'`,
	} {
		if _, err := tx.Exec(ctx, q, p.snap.id); err != nil {
			return err
		}
	}
	return nil
}

// writePorts writes the interfaces in (device key, canonical name) order, then their aliases and
// their evidence, keeping the row id on each port so the edge writer can reference it.
func (p *projection) writePorts(ctx context.Context, tx pgx.Tx) error {
	for _, e := range p.entities {
		for _, name := range slices.Sorted(maps.Keys(e.ports)) {
			port := e.ports[name]
			if err := tx.QueryRow(ctx, `
				INSERT INTO interface (entity_id, snapshot_id, canonical_name, source, description,
					admin_state, oper_state, speed_bps, mtu, mac, first_seen, last_seen)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
				e.id, p.snap.id, port.name, port.source, null(port.description),
				null(port.adminState), null(port.operState), port.speedBPS, port.mtu,
				null(port.mac), port.first, port.last).Scan(&port.id); err != nil {
				return err
			}
			b := &pgx.Batch{}
			for _, spelling := range slices.Sorted(maps.Keys(port.aliases)) {
				a := port.aliases[spelling]
				b.Queue(`INSERT INTO interface_alias (interface_id, spelling, source, snapshot_id, observation_id)
					VALUES ($1, $2, $3, $4, $5)`, port.id, a.spelling, a.source, p.snap.id, a.obs)
			}
			for _, obs := range slices.Sorted(maps.Keys(port.evidence)) {
				b.Queue(`INSERT INTO interface_evidence (interface_id, snapshot_id, observation_id)
					VALUES ($1, $2, $3)`, port.id, p.snap.id, obs)
			}
			if err := tx.SendBatch(ctx, b).Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeEdges writes the edges in name order with one evidence row per side. The interface ids come
// from writePorts, which is why the order of the two is not an accident.
func (p *projection) writeEdges(ctx context.Context, tx pgx.Tx) error {
	slices.SortFunc(p.edges, func(a, b *edge) int {
		return cmpString(a.name(), b.name())
	})
	for _, e := range p.edges {
		attrs, err := json.Marshal(e.attributes)
		if err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO edge (snapshot_id, type, from_ref, to_ref, from_entity_id, to_entity_id,
				from_interface_id, to_interface_id, confidence, attributes, first_seen, last_seen)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
			p.snap.id, e.typ, e.fromRef, e.toRef, entityID(e.fromEntity), entityID(e.toEntity),
			ifaceID(e.fromPort), ifaceID(e.toPort), e.confidence, attrs, e.first, e.last).Scan(&id); err != nil {
			return err
		}
		b := &pgx.Batch{}
		slices.SortFunc(e.evidence, func(x, y edgeEvidence) int {
			if x.obs != y.obs {
				return int(x.obs - y.obs)
			}
			return cmpString(x.side, y.side)
		})
		for _, ev := range e.evidence {
			b.Queue(`INSERT INTO edge_evidence (edge_id, snapshot_id, observation_id, side)
				VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, id, p.snap.id, ev.obs, ev.side)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
	}
	return nil
}

// writeFindings raises the disagreements this projection found, on the surface 001 established and
// 003 already writes to (research R9).
func (p *projection) writeFindings(ctx context.Context, tx pgx.Tx) error {
	slices.SortFunc(p.findings, func(a, b disagreement) int { return cmpString(a.subject, b.subject) })
	for _, d := range p.findings {
		if err := store.RaiseFinding(ctx, tx, p.snap.id, "data_quality", "link_disagreement",
			"warning", d.subject, d.detail, d.evidence...); err != nil {
			return err
		}
	}
	return nil
}

func entityID(e *entity) any {
	if e == nil {
		return nil
	}
	return e.id
}

func ifaceID(p *iface) any {
	if p == nil || p.id == 0 {
		return nil
	}
	return p.id
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// cmpString compares by byte, the way the edge_l1_link_is_ordered constraint does under the C
// collation, so the projector and the database never disagree about order (research R7).
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
