package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const refused = "{\"error\":\"unauthenticated\"}\n"

// surface is one real path per endpoint of the lab: the eight the contract defines.
func surface(t *testing.T, c *client) []string {
	t.Helper()
	var obs int64
	var step string
	if err := c.l.DB.QueryRow(context.Background(),
		`SELECT observation_id, step_id FROM observation_raw ORDER BY 1, 2 LIMIT 1`).Scan(&obs, &step); err != nil {
		t.Fatal(err)
	}
	snap := c.l.Int(`SELECT max(id) FROM snapshot`)
	return []string{
		"/v1/snapshots",
		fmt.Sprintf("/v1/snapshots/%d", snap),
		"/v1/devices",
		"/v1/devices/sw1",
		"/v1/interfaces/sw1/port1",
		"/v1/findings",
		fmt.Sprintf("/v1/observations/%d", obs),
		fmt.Sprintf("/v1/observations/%d/raw/%s", obs, step),
	}
}

// T031, FR-010, FR-011, SC-004, SC-005, research R12, R13: every endpoint under every token state.
// The four ways of not holding a live token are one refusal, byte for byte; a token whose scopes do
// not cover the call is a different one, including a token that carries read and something undefined.
func TestEveryEndpointUnderEveryTokenState(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)
	revokedTok := mint(t, l.DB, "gone", "read")
	revoke(t, l.DB, "gone")
	admin := mint(t, l.DB, "admin", "admin")
	mixed := mint(t, l.DB, "mixed", "read", "admin")

	for _, path := range surface(t, c) {
		for name, auth := range map[string]string{
			"no header":     "",
			"not bearer":    "Basic x",
			"no prefix":     "Bearer " + c.token[len(tokenPrefix):],
			"unknown token": "Bearer nm_unknown",
			"revoked token": "Bearer " + revokedTok,
		} {
			if status, body := get(t, c.srv, auth, path); status != 401 || string(body) != refused {
				t.Errorf("%s, %s: %d %q, want the one 401", path, name, status, body)
			}
		}
		for name, tok := range map[string]string{"undefined scope": admin, "read plus undefined": mixed} {
			status, body := get(t, c.srv, "Bearer "+tok, path)
			if status != 403 || string(body) != "{\"error\":\"forbidden\",\"need\":\"read\"}\n" {
				t.Errorf("%s, %s: %d %q, want 403 naming the scope", path, name, status, body)
			}
		}
		if status, body := c.get(path); status != 200 {
			t.Errorf("%s, valid token: %d %s", path, status, body)
		}
	}
}

// T032, SC-004, contracts/rest.md: a refusal says nothing about what was asked for, and nothing
// answers without a token, including paths that do not exist.
func TestRefusalRevealsNothing(t *testing.T) {
	c := newClient(t, standard(t))
	for _, path := range append(surface(t, c),
		"/v1/devices/nonesuch", "/v1/observations/999999", "/v1/observations/999999/raw/x",
		"/healthz", "/version", "/", "/v1", "/v2/devices") {
		if status, body := get(t, c.srv, "", path); status != 401 || string(body) != refused {
			t.Errorf("%s without a token: %d %q, want the one 401", path, status, body)
		}
	}
}

// T032, FR-021, research R5, US2-4: revoking a token refuses it on the very next request, and only a
// successful call records last use.
func TestRevocationIsImmediate(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)
	lastUsed := func() *time.Time {
		var at *time.Time
		if err := l.DB.QueryRow(context.Background(),
			`SELECT last_used_at FROM api_token WHERE name = 'reader'`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if lastUsed() != nil {
		t.Fatal("a token never used has a last use")
	}
	if status, _ := c.get("/v1/devices"); status != 200 {
		t.Fatalf("first call: %d", status)
	}
	used := lastUsed()
	if used == nil {
		t.Fatal("a successful call did not record last use")
	}
	revoke(t, l.DB, "reader")
	if status, body := get(t, c.srv, "Bearer "+c.token, "/v1/devices"); status != 401 || string(body) != refused {
		t.Errorf("after revoke: %d %q, want the one 401", status, body)
	}
	if after := lastUsed(); !after.Equal(*used) {
		t.Errorf("a refused call moved last use from %v to %v", used, after)
	}
}

// Code review, FR-012: nothing but GET is served, even to a valid token.
func TestOnlyReadsAreServed(t *testing.T) {
	c := newClient(t, standard(t))
	for _, path := range surface(t, c) {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			req, _ := http.NewRequest(method, c.srv.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+c.token)
			resp, err := c.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want 405", method, path, resp.StatusCode)
			}
		}
	}
}
