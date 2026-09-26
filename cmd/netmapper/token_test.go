package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/api"
	. "github.com/darnodo/NetMapper/internal/testutil"
)

// asOperator points NETMAPPER_DSN at db's schema as netmapper_operator, the role research R8 assigns
// the token subcommands, and returns the superuser pool for assertions.
func asOperator(t *testing.T) *pgxpool.Pool {
	db := DB(t)
	schema := db.Config().ConnConfig.RuntimeParams["search_path"]
	dsn := os.Getenv("NETMAPPER_TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	t.Setenv("NETMAPPER_DSN", dsn+sep+"options=-c%20search_path%3D"+schema+"%20-c%20role%3Dnetmapper_operator")
	return db
}

// T030, FR-013, research R4: create prints the value once and stores only its hash; list never
// prints a value; and the token works against the interface served as netmapper_api.
func TestTokenCreateAndList(t *testing.T) {
	db := asOperator(t)
	ctx := context.Background()

	var code int
	value := strings.TrimSpace(stdout(t, func() {
		code = cmdToken(ctx, []string{"create", "--name", "grafana-reader", "--scope", "read"})
	}))
	if code != exitOK || !strings.HasPrefix(value, "nm_") || strings.Contains(value, "\n") {
		t.Fatalf("create: exit %d, printed %q, want one nm_ value", code, value)
	}
	var hash []byte
	var row string
	if err := db.QueryRow(ctx, `SELECT hash, t::text FROM api_token t WHERE name = 'grafana-reader'`).Scan(&hash, &row); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(value))
	if !bytes.Equal(hash, sum[:]) {
		t.Error("the stored hash is not the SHA-256 of the printed value")
	}
	if strings.Contains(row, value) || strings.Contains(row, value[3:]) {
		t.Errorf("the row holds the value: %s", row)
	}

	out := stdout(t, func() { code = cmdToken(ctx, []string{"list"}) })
	if code != exitOK || !strings.Contains(out, "grafana-reader\tread\t") || strings.Contains(out, "nm_") {
		t.Errorf("list: exit %d, printed %q", code, out)
	}

	srv := httptest.NewServer(api.New(As(t, db, "netmapper_api"), nil).Handler())
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/v1/snapshots", nil)
	req.Header.Set("Authorization", "Bearer "+value)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("the issued token gets %d from the interface, want 200", resp.StatusCode)
	}

	if code := cmdToken(ctx, []string{"revoke", "--name", "grafana-reader"}); code != exitOK {
		t.Fatalf("revoke: exit %d", code)
	}
	resp, err = srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("the revoked token gets %d, want 401", resp.StatusCode)
	}
}

// T030, contracts/rest.md: each of these is invalid input, exit 2.
func TestTokenRefusals(t *testing.T) {
	asOperator(t)
	ctx := context.Background()
	stdout(t, func() { cmdToken(ctx, []string{"create", "--name", "a", "--scope", "read"}) })
	for _, c := range []struct {
		why  string
		args []string
	}{
		{"a duplicate name", []string{"create", "--name", "a", "--scope", "read"}},
		{"an undefined scope", []string{"create", "--name", "b", "--scope", "admin"}},
		{"no scope", []string{"create", "--name", "c"}},
		{"no name", []string{"create", "--scope", "read"}},
		{"an unknown name", []string{"revoke", "--name", "nonesuch"}},
		{"no subcommand", nil},
		{"an unknown subcommand", []string{"delete"}},
	} {
		var code int
		out := stdout(t, func() { code = cmdToken(ctx, c.args) })
		if code != exitInvalid || strings.Contains(out, "nm_") {
			t.Errorf("%s: exit %d, printed %q, want %d and no value", c.why, code, out, exitInvalid)
		}
	}
	if code := cmdToken(ctx, []string{"revoke", "--name", "a"}); code != exitOK {
		t.Fatalf("first revoke: exit %d", code)
	}
	if code := cmdToken(ctx, []string{"revoke", "--name", "a"}); code != exitInvalid {
		t.Errorf("second revoke: exit %d, want %d rather than a moved timestamp", code, exitInvalid)
	}
}
