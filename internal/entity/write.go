package entity

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/store"
)

// write replaces the snapshot's entity set, and the collision findings that describe it, inside the
// transaction that already holds the snapshot's advisory lock. Nothing here is a patch of what was
// there: a resolution result is derived data, replaced wholesale (FR-015, research R8).
func write(ctx context.Context, tx pgx.Tx, s snapshot, entities []Entity, conflicts []Conflict, reg *registry, highest int64) error {
	if err := writeRegistry(ctx, tx, s, reg); err != nil {
		return err
	}
	// The findings go before the entities for one reason: both are this resolution's output, so a
	// failure anywhere leaves neither the report nor the set it describes (FR-015, research R11).
	if err := replaceFindings(ctx, tx, s, conflicts); err != nil {
		return err
	}
	if err := writeEntities(ctx, tx, s, entities); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO resolution (snapshot_id, resolver_version, decisions_applied, entities)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (snapshot_id) DO UPDATE SET
			resolver_version  = EXCLUDED.resolver_version,
			decisions_applied = EXCLUDED.decisions_applied,
			entities          = EXCLUDED.entities,
			computed_at       = now()`,
		s.id, resolverVersion, highest, len(entities))
	return err
}

// writeRegistry stores the devices this snapshot assigned and the identifiers it attributed. The
// timestamps only ever widen, so resolving an older snapshot after a newer one does not move them
// backwards, and an identifier already attributed elsewhere is left where it is: one identifier
// belongs to one device, and moving it is an operator's call (research R5, R6).
func writeRegistry(ctx context.Context, tx pgx.Tx, s snapshot, reg *registry) error {
	b := &pgx.Batch{}
	for _, d := range reg.devices {
		b.Queue(`
			INSERT INTO device (perimeter_name, key, weak, first_seen, last_seen, minted_from)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (perimeter_name, key) DO UPDATE SET
				first_seen = LEAST(device.first_seen, EXCLUDED.first_seen),
				last_seen  = GREATEST(device.last_seen, EXCLUDED.last_seen)`,
			reg.perimeter, d.key, d.weak, d.first, d.last, s.id)
	}
	for _, a := range reg.attribs {
		b.Queue(`
			INSERT INTO device_identifier (perimeter_name, kind, value, key, first_seen)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (perimeter_name, kind, value) DO NOTHING`,
			reg.perimeter, a.kind, a.value, a.key, a.first)
	}
	return tx.SendBatch(ctx, b).Close()
}

// replaceFindings removes the identity collisions the previous resolution of this snapshot raised and
// raises the ones that still hold. It is restricted to this category and this snapshot: a finding the
// collector raised is not this resolution's to remove (FR-015, research R11).
func replaceFindings(ctx context.Context, tx pgx.Tx, s snapshot, conflicts []Conflict) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM finding_evidence
		WHERE finding_id IN (
			SELECT id FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict')`, s.id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM finding WHERE snapshot_id = $1 AND category = 'identity_conflict'`, s.id); err != nil {
		return err
	}
	for _, c := range conflicts {
		if err := store.RaiseFinding(ctx, tx, s.id, "data_quality", "identity_conflict", "warning",
			c.Subject, c.Detail, c.Evidence...); err != nil {
			return err
		}
	}
	return nil
}

// writeEntities replaces the snapshot's entities. entity_claim follows the entity it hangs off, so
// the delete takes the links with it and no orphan survives a re-resolution.
func writeEntities(ctx context.Context, tx pgx.Tx, s snapshot, entities []Entity) error {
	if _, err := tx.Exec(ctx, `DELETE FROM entity WHERE snapshot_id = $1`, s.id); err != nil {
		return err
	}
	for _, e := range entities {
		attrs, err := json.Marshal(e.Attributes)
		if err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO entity (snapshot_id, kind, device_key, weak, attributes, first_seen, last_seen)
			VALUES ($1, 'device', $2, $3, $4, $5, $6) RETURNING id`,
			s.id, e.DeviceKey, e.Weak, attrs, e.FirstSeen, e.LastSeen).Scan(&id); err != nil {
			return err
		}
		b := &pgx.Batch{}
		for _, claim := range e.Claims {
			b.Queue(`INSERT INTO entity_claim (entity_id, snapshot_id, identifier_claim_id) VALUES ($1, $2, $3)`,
				id, s.id, claim)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return err
		}
	}
	return nil
}
