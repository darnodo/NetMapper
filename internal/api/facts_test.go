package api

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// Feature 007, contracts/rest-facts.md: one family of one device, every observation with its rows
// and evidence, under netmapper_api.
func TestDeviceFacts(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)

	// A collected family: rows and evidence.
	out := c.json("/v1/devices/sw1/facts/interfaces", http.StatusOK)
	obs := list(out["observations"])
	if out["device_key"] != key("S001") || out["family"] != "interfaces" || len(obs) != 1 ||
		obs[0]["status"] != "collected" || len(obs[0]["rows"].([]any)) == 0 {
		t.Fatalf("interfaces: %v", out)
	}
	if ev := list(obs[0]["evidence"]); len(ev) != 1 || ev[0]["family"] != "interfaces" || ev[0]["target"] != "10.0.0.1" {
		t.Errorf("evidence: %v", obs[0]["evidence"])
	}
	// Empty and unsupported are answers, with no rows: 200, never left out.
	out = c.json("/v1/devices/sw3/facts/neighbours", http.StatusOK)
	if o := list(out["observations"])[0]; o["status"] != "empty" || len(o["rows"].([]any)) != 0 {
		t.Errorf("empty: %v", out)
	}
	out = c.json("/v1/devices/sw1/facts/aaa_servers", http.StatusOK)
	if o := list(out["observations"])[0]; o["status"] != "unsupported" || o["detail"] != "no_recipe" {
		t.Errorf("unsupported: %v", out)
	}
	// A family name not in the schema is not a missing device.
	if out := c.json("/v1/devices/sw1/facts/weather", http.StatusNotFound); out["error"] != "no_such_family" {
		t.Errorf("unknown family: %v", out)
	}
	if out := c.json("/v1/devices/nonesuch/facts/interfaces", http.StatusNotFound); out["error"] != "no_such_device" {
		t.Errorf("unknown device: %v", out)
	}
	// No token is 401, like every endpoint: authentication wraps the whole mux (auth_test.go).
	if status, _ := get(t, c.srv, "", "/v1/devices/sw1/facts/interfaces"); status != http.StatusUnauthorized {
		t.Errorf("no token: %d", status)
	}
	// Observations of an inactive parse generation are not served: with none active, nothing of
	// the device is collected as far as this endpoint can tell. Same answer as a snapshot taken
	// before a family existed.
	exec(t, l.DB, `UPDATE parse_generation SET active = false`)
	if out := c.json("/v1/devices/sw1/facts/interfaces", http.StatusNotFound); out["error"] != "not_collected" {
		t.Errorf("inactive generation: %v", out)
	}
}

// A device that answered on two addresses has an identity observation on each; both are served, by
// collection time, then by id when the times tie (constitution: a tie rule tested with a tie).
func TestDeviceFactsTwoAddresses(t *testing.T) {
	sw := FakeOS("sw1", "S001")
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw, Addr("10.0.0.11"): sw}})
	l.Crawl(strings.Replace(Doc, "targets: [10.0.0.1]", "targets: [10.0.0.1, 10.0.0.11]", 1))
	c := newClient(t, l)
	exec(t, l.DB, `UPDATE observation SET collected_at = '2026-10-04T10:00:00Z' WHERE fact_family = 'identity'`)
	ids := l.Strings(`SELECT id::text FROM observation WHERE fact_family = 'identity' ORDER BY id`)
	out := c.json("/v1/devices/10.0.0.1/facts/identity", http.StatusOK)
	obs := list(out["observations"])
	if len(obs) != 2 || len(ids) != 2 {
		t.Fatalf("identity: %v", out)
	}
	for i, o := range obs {
		if got := fmt.Sprint(int64(list(o["evidence"])[0]["observation_id"].(float64))); got != ids[i] {
			t.Errorf("observation %d is %v, want %s", i, got, ids[i])
		}
	}
	// The scraped family was collected once, from the address that was not a duplicate.
	if n := len(list(c.json("/v1/devices/10.0.0.1/facts/interfaces", http.StatusOK)["observations"])); n != 1 {
		t.Errorf("interfaces observations: %d", n)
	}
}

func exec(t *testing.T, db *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
