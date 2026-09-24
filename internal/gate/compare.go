package gate

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// device is one device the baseline reached, and what became of it in the newer snapshot.
type device struct {
	target  string
	carried bool
	reason  string // empty when carried
}

// compare matches every device the baseline reached against the newer snapshot.
//
// Devices are matched on a shared strong identifier claim, so a device that changed address is
// still the same device. Only a baseline device that reported no strong identifier at all falls
// back to matching on its address. A device that did not carry over takes the status of the newer
// snapshot's identity observation at its address as its reason, and `not_attempted` when the newer
// snapshot holds no identity observation for it at all: that is a device that silently left
// discovery, which no outcome count in the snapshot itself can reveal.
func compare(ctx context.Context, db *pgxpool.Pool, baseline, current int64) ([]device, error) {
	rows, err := db.Query(ctx, `
		WITH base AS (
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
		                 ORDER BY co.id LIMIT 1), 'not_attempted')
		FROM base b
		ORDER BY b.target`, baseline, current)
	if err != nil {
		return nil, err
	}
	devices, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (device, error) {
		var d device
		err := r.Scan(&d.target, &d.carried, &d.reason)
		if d.carried {
			d.reason = ""
		}
		return d, err
	})
	return devices, err
}
