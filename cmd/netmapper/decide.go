package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cmdDecide records one operator decision and nothing else. No entity changes until the snapshots
// that carry its subjects are resolved again, which is what "replayed, never applied in place"
// means (FR-009).
func cmdDecide(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: netmapper decide merge|split|never-merge [flags]")
		return exitInvalid
	}
	kind, args := args[0], args[1:]
	stored, ok := map[string]string{"merge": "merge", "never-merge": "never_merge", "split": "split"}[kind]
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: netmapper decide merge|split|never-merge [flags]")
		return exitInvalid
	}

	fs := flag.NewFlagSet("decide "+kind, flag.ContinueOnError)
	perimeter := fs.String("perimeter", "", "perimeter name a device key is scoped to")
	keys := fs.String("keys", "", "the two device keys of a merge or a never-merge, comma separated")
	key := fs.String("key", "", "the device key a split names")
	identifier := fs.String("identifier", "", "kind=value, the identifier a split detaches")
	note := fs.String("note", "", "why, in your own words")
	actor := fs.String("actor", osUser(), "who recorded it")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *perimeter == "" {
		fmt.Fprintln(os.Stderr, "--perimeter is required")
		return exitInvalid
	}

	var subjects []string
	var idKind, idValue string
	if stored == "split" {
		if *key == "" || *identifier == "" {
			fmt.Fprintln(os.Stderr, "split takes --key and --identifier kind=value")
			return exitInvalid
		}
		var found bool
		idKind, idValue, found = strings.Cut(*identifier, "=")
		if !found || idKind == "" || idValue == "" {
			fmt.Fprintln(os.Stderr, "--identifier must be kind=value")
			return exitInvalid
		}
		subjects = []string{*key}
	} else {
		subjects = strings.Split(*keys, ",")
		if len(subjects) != 2 || subjects[0] == "" || subjects[1] == "" {
			fmt.Fprintf(os.Stderr, "%s takes --keys <a>,<b>\n", kind)
			return exitInvalid
		}
		if subjects[0] == subjects[1] {
			fmt.Fprintf(os.Stderr, "%s names the same key twice: %s\n", kind, subjects[0])
			return exitInvalid
		}
	}

	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()

	// Recording a decision about something that does not exist is a typo, not an intention.
	for _, s := range subjects {
		var known bool
		if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM device WHERE perimeter_name = $1 AND key = $2)`,
			*perimeter, s).Scan(&known); err != nil {
			return fail("decide", err)
		}
		if !known {
			fmt.Fprintf(os.Stderr, "device %s is unknown in perimeter %s\n", s, *perimeter)
			return exitInvalid
		}
	}
	if stored == "split" {
		var attributed bool
		if err := db.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM device_identifier
			               WHERE perimeter_name = $1 AND kind = $2 AND value = $3 AND key = $4)`,
			*perimeter, idKind, idValue, subjects[0]).Scan(&attributed); err != nil {
			return fail("decide", err)
		}
		if !attributed {
			fmt.Fprintf(os.Stderr, "%s=%s is not attributed to %s\n", idKind, idValue, subjects[0])
			return exitInvalid
		}
	}

	var payload any
	if stored == "split" {
		payload = map[string]string{"kind": idKind, "value": idValue}
	}
	var id int64
	if err := db.QueryRow(ctx, `
		INSERT INTO entity_decision (perimeter_name, kind, subjects, identifier, actor, note)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		*perimeter, stored, subjects, payload, *actor, null(*note)).Scan(&id); err != nil {
		return fail("decide", err)
	}
	fmt.Printf("decision %d recorded\n", id)

	stale, err := staleSnapshots(ctx, db, *perimeter, subjects)
	switch {
	case err != nil:
		// The decision is committed, so this is not a failure. Say so rather than leave the operator
		// thinking there is nothing to resolve.
		fmt.Fprintf(os.Stderr, "cannot list the snapshots to resolve: %v\n", err)
	case len(stale) > 0:
		fmt.Printf("resolve %s to apply\n", join(stale))
	}
	return exitOK
}

// staleSnapshots lists the resolved snapshots carrying one of the subjects, which are the ones whose
// entity set no longer reflects every recorded decision.
func staleSnapshots(ctx context.Context, db *pgxpool.Pool, perimeter string, subjects []string) ([]int64, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT e.snapshot_id
		FROM entity e
		JOIN job j ON j.snapshot_id = e.snapshot_id
		JOIN perimeter p ON p.id = (j.parameters->>'perimeter_id')::bigint
		WHERE p.name = $1 AND e.device_key = ANY($2)
		ORDER BY 1`, perimeter, subjects)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func osUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func join(ids []int64) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = fmt.Sprint(id)
	}
	return strings.Join(out, ", ")
}
