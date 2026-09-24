package gate

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// device is one device the baseline reached, and what became of it in the newer snapshot.
type device struct {
	target   string
	carried  bool
	reason   string // empty when carried
	filtered bool   // the newer snapshot's perimeter no longer covers it
}

// compare matches every device the baseline reached against the newer snapshot.
//
// Devices are matched on a shared strong identifier claim, so a device that changed address is
// still the same device. Only a baseline device that reported no strong identifier at all falls
// back to matching on its address. A device that did not carry over takes the status of the newer
// snapshot's identity observation at its address as its reason, and `not_attempted` when the newer
// snapshot holds no identity observation for it at all: that is a device that silently left
// discovery, which no outcome count in the snapshot itself can reveal.
//
// A baseline device the newer snapshot's perimeter no longer covers is marked filtered rather than
// missing: deliberately narrowing a perimeter is a scope change, not a coverage collapse. The
// include-then-exclude test is the same one perimeter.Allowed applies, expressed in SQL so the
// whole comparison stays one statement.
func compare(ctx context.Context, db *pgxpool.Pool, baseline, current, perimeterID int64) ([]device, error) {
	rows, err := db.Query(ctx, `
		WITH per AS (
		    SELECT include, exclude FROM perimeter WHERE id = $3
		),
		base AS (
		    SELECT o.id, o.target
		    FROM observation o
		    WHERE o.snapshot_id = $1 AND o.fact_family = 'identity' AND o.status = 'collected'
		),
		matched_strong AS (
		    SELECT DISTINCT b.id
		    FROM base b
		    JOIN identifier_claim bc
		      ON bc.snapshot_id = $1 AND bc.observation_id = b.id AND bc.strength = 'strong'
		    JOIN identifier_claim cc
		      ON cc.snapshot_id = $2 AND cc.strength = 'strong'
		     AND cc.kind = bc.kind AND cc.value = bc.value
		    JOIN observation co
		      ON co.snapshot_id = $2 AND co.id = cc.observation_id
		     AND co.fact_family = 'identity' AND co.status = 'collected'
		),
		no_strong AS (
		    SELECT b.id
		    FROM base b
		    WHERE NOT EXISTS (
		        SELECT 1 FROM identifier_claim bc
		        WHERE bc.snapshot_id = $1 AND bc.observation_id = b.id AND bc.strength = 'strong')
		),
		matched_address AS (
		    SELECT DISTINCT b.id
		    FROM base b
		    JOIN no_strong n ON n.id = b.id
		    JOIN observation co
		      ON co.snapshot_id = $2 AND co.target = b.target
		     AND co.fact_family = 'identity' AND co.status = 'collected'
		)
		SELECT host(b.target),
		       b.id IN (SELECT id FROM matched_strong) OR b.id IN (SELECT id FROM matched_address),
		       coalesce((SELECT co.status FROM observation co
		                 WHERE co.snapshot_id = $2 AND co.target = b.target
		                   AND co.fact_family = 'identity'
		                 ORDER BY co.id LIMIT 1), 'not_attempted'),
		       NOT (b.target <<= ANY (per.include) AND NOT (b.target <<= ANY (per.exclude)))
		FROM base b CROSS JOIN per
		ORDER BY b.target`, baseline, current, perimeterID)
	if err != nil {
		return nil, err
	}
	devices, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (device, error) {
		var d device
		err := r.Scan(&d.target, &d.carried, &d.reason, &d.filtered)
		if d.carried {
			d.reason = ""
		}
		return d, err
	})
	return devices, err
}
