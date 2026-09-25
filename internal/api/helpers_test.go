package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// lab crawls a fake network and returns the settled lab: every test here reads a snapshot a real
// crawl, resolution and projection produced.
func lab(t *testing.T, devices map[netip.Addr]*fake.Device) *Lab {
	t.Helper()
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: devices})
	l.Crawl(Doc)
	return l
}

// standard is the lab most tests read:
//   - sw1 and sw2 report each other on p1: one cable both ends agree on, and sw1 spells sw2's port
//     "p1" where sw2 calls it port1, so the port carries an alias;
//   - sw1 reports sw3 on its p2, naming sw3's port p5, which sw3 never describes: a one-sided cable
//     to a port only a neighbour revealed;
//   - sw2 reports a far end on p2 that no device accounts for;
//   - sw4 prints no strong identifier, so resolution marks it weak.
func standard(t *testing.T) *Lab {
	return lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"),
			"p2 sw3 10.0.0.3 p5 "+MAC("S003"),
			"p3 sw4 10.0.0.4"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002",
			"p1 sw1 10.0.0.1 p1 "+MAC("S001"),
			"p2 ghost - - aa:bb:cc:00:00:99"),
		Addr("10.0.0.3"): FakeOS("sw3", "S003"),
		Addr("10.0.0.4"): FakeOSShaped("sw4", "S004", Shape{}),
	})
}

// key is the device key resolution mints for a fakeos device, its chassis MAC.
func key(serial string) string { return "chassis_mac:" + MAC(serial) }

// server serves the lab's schema as netmapper_api, never as the owner: the grants are part of what
// is under test (Constitution, Development Workflow).
func server(t *testing.T, l *Lab) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(As(t, l.DB, "netmapper_api"), l.Raw).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// mint issues a token straight into the table, as `netmapper token create` would.
func mint(t *testing.T, db *pgxpool.Pool, name string, scopes ...string) string {
	t.Helper()
	value, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(),
		`INSERT INTO api_token (name, hash, scopes) VALUES ($1, $2, $3)`, name, hash, scopes); err != nil {
		t.Fatal(err)
	}
	return value
}

func revoke(t *testing.T, db *pgxpool.Pool, name string) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`UPDATE api_token SET revoked_at = now() WHERE name = $1`, name); err != nil {
		t.Fatal(err)
	}
}

// get calls path with the Authorization header set to auth verbatim, or none when auth is empty.
func get(t *testing.T, srv *httptest.Server, auth, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

// client is a server and a valid read token on one lab.
type client struct {
	t     *testing.T
	l     *Lab
	srv   *httptest.Server
	token string
}

func newClient(t *testing.T, l *Lab) *client {
	return &client{t: t, l: l, srv: server(t, l), token: mint(t, l.DB, "reader", "read")}
}

// get calls path with the client's token and, on a 200 answer other than raw bytes, checks the
// evidence contract over the whole body, so every test that reads anything also proves SC-002 on what
// it read. Raw output is whatever the device printed, JSON included, and is not an answer.
func (c *client) get(path string) (int, []byte) {
	c.t.Helper()
	status, body := get(c.t, c.srv, "Bearer "+c.token, path)
	if status == http.StatusOK && !strings.Contains(path, "/raw/") {
		checkEvidence(c.t, path, body)
	}
	return status, body
}

// json calls path, requires want, and decodes the body.
func (c *client) json(path string, want int) map[string]any {
	c.t.Helper()
	status, body := c.get(path)
	if status != want {
		c.t.Fatalf("GET %s: %d %s, want %d", path, status, body, want)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

// list reads a JSON array field as objects.
func list(v any) []map[string]any {
	var out []map[string]any
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}
