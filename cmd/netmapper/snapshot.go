package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/store"
)

// cmdSnapshot evicts, pins and unpins snapshots (012 contracts/cli.md). It runs as
// netmapper_operator. Everything that touches the database goes through owner functions
// (evict_snapshot, finish_snapshot_eviction, set_snapshot_pinned): the operator role may not delete
// the collected zone itself (research R1).
func cmdSnapshot(ctx context.Context, args []string) int {
	usage := "usage: netmapper snapshot evict --id N [--yes] | pin --id N | unpin --id N"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return exitInvalid
	}
	switch args[0] {
	case "evict":
		return snapshotEvict(ctx, args[1:])
	case "pin":
		return snapshotPin(ctx, args[1:], true)
	case "unpin":
		return snapshotPin(ctx, args[1:], false)
	}
	fmt.Fprintln(os.Stderr, usage)
	return exitInvalid
}

// eviction is the row evict_snapshot returns (data-model.md).
type eviction struct {
	outcome          string
	reason           *string
	blockingSnapshot *int64
	blockingJob      *int64
	observations     int64
	objectsDeleted   int64
	bytesDeleted     int64
	objectsKept      int64
	devicesDeleted   int64
}

// snapshotEvict previews without --yes and evicts with it. The database part runs in one call of
// evict_snapshot; the object store part deletes the outputs it listed in eviction_object, one at a
// time, so an interrupted run is finished by running the same command again (research R5).
func snapshotEvict(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("snapshot evict", flag.ContinueOnError)
	id := fs.Int64("id", 0, "snapshot id")
	yes := fs.Bool("yes", false, "evict; without it, only show what would be removed")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *id <= 0 {
		fmt.Fprintln(os.Stderr, "snapshot evict needs --id")
		return exitInvalid
	}
	// The object store is checked first, so missing settings change nothing (contracts/cli.md).
	var raw *store.RawStore
	if *yes {
		var err error
		if raw, err = rawStore(); err != nil {
			fmt.Fprintf(os.Stderr, "object store: %v\n", err)
			return exitInvalid
		}
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()

	var e eviction
	err = db.QueryRow(ctx, `SELECT * FROM evict_snapshot($1, $2)`, *id, *yes).Scan(&e.outcome, &e.reason,
		&e.blockingSnapshot, &e.blockingJob, &e.observations, &e.objectsDeleted, &e.bytesDeleted,
		&e.objectsKept, &e.devicesDeleted)
	if err != nil {
		return fail("evict", err)
	}

	switch e.outcome {
	case "unknown":
		// Invalid input: not audited (FR-008).
		fmt.Fprintf(os.Stderr, "snapshot %d does not exist\n", *id)
		return exitInvalid
	case "refused":
		fmt.Fprintln(os.Stderr, refusal(ctx, db, *id, e))
		if *yes {
			if err := auditSnapshot(ctx, db, "snapshot.evict", *id, "refused: "+deref(e.reason)); err != nil {
				return fail("audit", err)
			}
		}
		return exitInvalid
	case "noop":
		if err := printEviction(ctx, db, *id, e, "nothing to do"); err != nil {
			return fail("evict", err)
		}
		if *yes {
			if err := auditSnapshot(ctx, db, "snapshot.evict", *id, "noop"); err != nil {
				return fail("audit", err)
			}
		}
		return exitOK
	case "preview":
		if err := printEviction(ctx, db, *id, e, "preview"); err != nil {
			return fail("evict", err)
		}
		return exitPreview
	case "resume":
		if !*yes {
			if err := printEviction(ctx, db, *id, e, "resume"); err != nil {
				return fail("evict", err)
			}
			hashes, err := pending(ctx, db, *id)
			if err != nil {
				return fail("evict", err)
			}
			for _, h := range hashes {
				fmt.Printf("pending: %s\n", hex.EncodeToString(h))
			}
			return exitPreview
		}
	case "evicting":
	default:
		return fail("evict", fmt.Errorf("unexpected outcome %q", e.outcome))
	}

	// The object store part, for a fresh eviction or a resumed one.
	hashes, err := pending(ctx, db, *id)
	if err != nil {
		return fail("evict", err)
	}
	for i, h := range hashes {
		err := raw.Delete(ctx, h)
		if err == nil {
			_, err = db.Exec(ctx, `DELETE FROM eviction_object WHERE snapshot_id = $1 AND hash = $2`, *id, h)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "snapshot %d eviction incomplete: %d stored outputs left; run the same command again\n",
				*id, len(hashes)-i)
			// The database part is committed: record it even when an interrupt is what stopped the
			// deletes, so the cancelled ctx is not used for the audit row.
			if aerr := auditSnapshot(context.WithoutCancel(ctx), db, "snapshot.evict", *id, "incomplete: "+err.Error()); aerr != nil {
				fmt.Fprintf(os.Stderr, "audit: %v\n", aerr)
			}
			return fail("evict", err)
		}
	}
	if _, err := db.Exec(ctx, `SELECT finish_snapshot_eviction($1)`, *id); err != nil {
		return fail("evict", err)
	}
	if err := auditSnapshot(ctx, db, "snapshot.evict", *id, "ok"); err != nil {
		return fail("audit", err)
	}
	result := "evicted"
	if e.outcome == "resume" {
		result = "resumed"
	}
	if err := printEviction(ctx, db, *id, e, result); err != nil {
		return fail("evict", err)
	}
	return exitOK
}

// printEviction prints the block of contracts/cli.md, the same keys for a preview and an eviction.
func printEviction(ctx context.Context, db *pgxpool.Pool, id int64, e eviction, result string) error {
	var closedAt *time.Time
	var verdict *string
	var pinned bool
	err := db.QueryRow(ctx, `
		SELECT s.closed_at, j.classification, s.pinned
		FROM snapshot s LEFT JOIN snapshot_judgement j ON j.snapshot_id = s.id AND j.active
		WHERE s.id = $1`, id).Scan(&closedAt, &verdict, &pinned)
	if err != nil {
		return err
	}
	closed := "-"
	if closedAt != nil {
		closed = closedAt.UTC().Format(time.RFC3339)
	}
	v := "-"
	if verdict != nil {
		v = *verdict
	}
	fmt.Printf("snapshot: %d\nclosed_at: %s\nverdict: %s\npinned: %t\n", id, closed, v, pinned)
	fmt.Printf("observations: %d\nobjects_deleted: %d\nbytes_deleted: %d\nobjects_kept: %d\ndevices_deleted: %d\n",
		e.observations, e.objectsDeleted, e.bytesDeleted, e.objectsKept, e.devicesDeleted)
	fmt.Printf("result: %s\n", result)
	return nil
}

// refusal is the message of contracts/cli.md for a refused eviction.
func refusal(ctx context.Context, db *pgxpool.Pool, id int64, e eviction) string {
	switch deref(e.reason) {
	case "open":
		return fmt.Sprintf("snapshot %d is open: cancel or wait for its crawl first", id)
	case "pinned":
		return fmt.Sprintf("snapshot %d is pinned: unpin it first", id)
	case "crawl_running":
		if e.blockingJob != nil && e.blockingSnapshot != nil {
			return fmt.Sprintf("crawl %d (snapshot %d) is running: retry when it ends", *e.blockingJob, *e.blockingSnapshot)
		}
	case "unresolved_sibling":
		if e.blockingSnapshot == nil {
			break
		}
		perimeter := "?"
		if err := db.QueryRow(ctx, `
			SELECT p.name FROM snapshot s JOIN job j ON j.id = s.job_id
			JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
			WHERE s.id = $1`, *e.blockingSnapshot).Scan(&perimeter); err != nil {
			perimeter = "?"
		}
		return fmt.Sprintf("snapshot %d of perimeter %s is not resolved yet: retry when it is", *e.blockingSnapshot, perimeter)
	}
	return fmt.Sprintf("snapshot %d: eviction refused (%s)", id, deref(e.reason))
}

// pending lists the stored outputs an eviction still has to delete.
func pending(ctx context.Context, db *pgxpool.Pool, id int64) ([][]byte, error) {
	rows, err := db.Query(ctx, `SELECT hash FROM eviction_object WHERE snapshot_id = $1 ORDER BY hash`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// snapshotPin sets or clears the pin through set_snapshot_pinned. Pinning only protects from
// eviction.
func snapshotPin(ctx context.Context, args []string, pin bool) int {
	verb, action, done := "pin", "snapshot.pin", "pinned"
	if !pin {
		verb, action, done = "unpin", "snapshot.unpin", "unpinned"
	}
	fs := flag.NewFlagSet("snapshot "+verb, flag.ContinueOnError)
	id := fs.Int64("id", 0, "snapshot id")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *id <= 0 {
		fmt.Fprintf(os.Stderr, "snapshot %s needs --id\n", verb)
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(ctx, `SELECT set_snapshot_pinned($1, $2)`, *id, pin).Scan(&result); err != nil {
		return fail(verb, err)
	}
	switch result {
	case "unknown":
		fmt.Fprintf(os.Stderr, "snapshot %d does not exist\n", *id)
		return exitInvalid
	case "refused":
		var state string
		if err := db.QueryRow(ctx, `SELECT state FROM snapshot WHERE id = $1`, *id).Scan(&state); err != nil {
			return fail(verb, err)
		}
		fmt.Fprintf(os.Stderr, "snapshot %d is %s: nothing to %s\n", *id, state, verb)
		if err := auditSnapshot(ctx, db, action, *id, "refused: "+state); err != nil {
			return fail("audit", err)
		}
		return exitInvalid
	case "noop":
		fmt.Println("nothing to do")
	default:
		fmt.Println(done)
	}
	if err := auditSnapshot(ctx, db, action, *id, result); err != nil {
		return fail("audit", err)
	}
	return exitOK
}

// auditSnapshot writes the one audit_log row of an operator action on a snapshot (research R11). It
// acts on no device, so it has no target; the snapshot is named in command.
func auditSnapshot(ctx context.Context, db *pgxpool.Pool, action string, id int64, result string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO audit_log (ref, actor, action, target, command, result)
		VALUES (gen_random_uuid(), $1, $2, NULL, $3, $4)`, osUser(), action, fmt.Sprintf("snapshot %d", id), result)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
