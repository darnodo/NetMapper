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
// A device is its winning identity observation: 001 writes a second, equally collected observation
// for every other address the same device answered on, marked with duplicate_of_task, and counting
// those as devices of their own would inflate the denominator. Those addresses are kept as further
// handles on the device instead.
//
// Devices are matched on a shared strong identifier claim, so a device that changed address is
// still the same device. Only a baseline device that reported no strong identifier at all falls
// back to matching on its addresses. A device that did not carry over takes the status of the newer
// snapshot's identity observation at any of its addresses as its reason, and `not_attempted` when
// the newer snapshot holds no identity observation for any of them: that is a device that silently
// left discovery, which no outcome count in the snapshot itself can reveal.
//
// A baseline device none of whose addresses the newer snapshot's perimeter still covers is marked
// filtered rather than missing: deliberately narrowing a perimeter is a scope change, not a
// coverage collapse. The include-then-exclude test is the one perimeter.Allowed applies, expressed
// in SQL so the whole comparison stays one statement.
func compare(ctx context.Context, db *pgxpool.Pool, baseline, current, perimeterID int64) ([]device, error) {
	rows, err := db.Query(ctx, `
		WITH per AS (
		    SELECT include, exclude FROM perimeter WHERE id = $3
		),
		base AS (
		    SELECT o.id, o.task_id, o.target
		    FROM observation o
		    WHERE o.snapshot_id = $1 AND o.fact_family = 'identity' AND o.status = 'collected'
		      AND NOT (o.parsed -> 0 ? 'duplicate_of_task')
		),
		addr AS (
		    SELECT b.id, b.target FROM base b
		    UNION
		    SELECT b.id, d.target
		    FROM base b
		    JOIN observation d
		      ON d.snapshot_id = $1 AND d.fact_family = 'identity'
		     AND (d.parsed -> 0 ->> 'duplicate_of_task')::bigint = b.task_id
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
		    SELECT DISTINCT a.id
		    FROM addr a
		    JOIN no_strong n ON n.id = a.id
		    JOIN observation co
		      ON co.snapshot_id = $2 AND co.target = a.target
		     AND co.fact_family = 'identity' AND co.status = 'collected'
		)
		SELECT host(b.target),
		       b.id IN (SELECT id FROM matched_strong) OR b.id IN (SELECT id FROM matched_address),
		       coalesce((SELECT co.status
		                 FROM observation co
		                 JOIN addr a ON a.id = b.id AND a.target = co.target
		                 WHERE co.snapshot_id = $2 AND co.fact_family = 'identity'
		                 ORDER BY co.id LIMIT 1), 'not_attempted'),
		       NOT EXISTS (SELECT 1 FROM addr a
		                   WHERE a.id = b.id
		                     AND a.target <<= ANY (per.include)
		                     AND NOT (a.target <<= ANY (per.exclude)))
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
