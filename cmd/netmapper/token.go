package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/darnodo/NetMapper/internal/api"
)

// cmdToken issues, lists and revokes the bearer tokens the read interface accepts. It runs as
// netmapper_operator: the interface exposes no call that changes anything, so it cannot manage its own
// credentials (research R8).
func cmdToken(ctx context.Context, args []string) int {
	usage := "usage: netmapper token create --name N --scope S | list | revoke --name N"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return exitInvalid
	}
	switch args[0] {
	case "create":
		return tokenCreate(ctx, args[1:])
	case "list":
		return tokenList(ctx, args[1:])
	case "revoke":
		return tokenRevoke(ctx, args[1:])
	}
	fmt.Fprintln(os.Stderr, usage)
	return exitInvalid
}

type scopeList []string

func (s *scopeList) String() string     { return strings.Join(*s, ",") }
func (s *scopeList) Set(v string) error { *s = append(*s, v); return nil }

// tokenCreate prints the value on stdout once and stores only its hash (FR-013).
func tokenCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	name := fs.String("name", "", "token name, unique")
	var scopes scopeList
	fs.Var(&scopes, "scope", "scope to grant, repeatable (defined: read)")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *name == "" || len(scopes) == 0 {
		fmt.Fprintln(os.Stderr, "token create needs --name and at least one --scope")
		return exitInvalid
	}
	for _, sc := range scopes {
		if !api.Scopes[sc] {
			fmt.Fprintf(os.Stderr, "scope %q is not defined\n", sc)
			return exitInvalid
		}
	}
	value, hash, err := api.NewToken()
	if err != nil {
		return fail("token", err)
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	_, err = db.Exec(ctx, `INSERT INTO api_token (name, hash, scopes) VALUES ($1, $2, $3)`,
		*name, hash, []string(scopes))
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		fmt.Fprintf(os.Stderr, "a token named %s already exists\n", *name)
		return exitInvalid
	} else if err != nil {
		return fail("token create", err)
	}
	fmt.Println(value)
	return exitOK
}

// tokenList never reads the hash, let alone a value.
func tokenList(ctx context.Context, args []string) int {
	if code, exit := parse(flag.NewFlagSet("token list", flag.ContinueOnError), args); exit {
		return code
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `
		SELECT name, scopes, created_at, last_used_at, revoked_at FROM api_token ORDER BY name`)
	if err != nil {
		return fail("token list", err)
	}
	defer rows.Close()
	stamp := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.UTC().Format(time.RFC3339)
	}
	fmt.Println("NAME\tSCOPES\tCREATED\tLAST_USED\tREVOKED")
	for rows.Next() {
		var name string
		var scopes []string
		var created time.Time
		var used, revoked *time.Time
		if err := rows.Scan(&name, &scopes, &created, &used, &revoked); err != nil {
			return fail("token list", err)
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", name, strings.Join(scopes, ","), stamp(&created), stamp(used), stamp(revoked))
	}
	if err := rows.Err(); err != nil {
		return fail("token list", err)
	}
	return exitOK
}

// tokenRevoke refuses the token from the next request on. Nothing is deleted, so a name is never
// quietly reused, and revoking twice is an error rather than a moved timestamp.
func tokenRevoke(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	name := fs.String("name", "", "token name")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *name == "" {
		fmt.Fprintln(os.Stderr, "token revoke needs --name")
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	tag, err := db.Exec(ctx, `UPDATE api_token SET revoked_at = now() WHERE name = $1 AND revoked_at IS NULL`, *name)
	if err != nil {
		return fail("token revoke", err)
	}
	if tag.RowsAffected() == 0 {
		fmt.Fprintf(os.Stderr, "no active token named %s\n", *name)
		return exitInvalid
	}
	return exitOK
}
