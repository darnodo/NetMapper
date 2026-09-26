package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/testutil"
)

// T033, FR-013, FR-014, SC-006: no response anywhere carries a secret value, a secret reference, an
// object-store key, or a token value. The whole surface, every raw step of every observation included,
// because the check is only worth something where nobody expects a secret.
func TestNoSecretInAnyResponse(t *testing.T) {
	l := standard(t)
	t.Setenv("NM_SNMP", "public-community-7f3a")
	t.Setenv("NM_SSH", "lab-ssh-secret-9c1e")
	// The crawl above ran under the ordinary test values; crawl once more under the distinctive ones.
	l.Crawl(testutil.Doc)
	c := newClient(t, l)

	paths := surface(t, c)
	rows, err := l.DB.Query(context.Background(), `SELECT observation_id, step_id FROM observation_raw`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var obs int64
		var step string
		if err := rows.Scan(&obs, &step); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, fmt.Sprintf("/v1/observations/%d", obs), fmt.Sprintf("/v1/observations/%d/raw/%s", obs, step))
	}
	rows.Close()
	for _, snap := range l.Strings(`SELECT id::text FROM snapshot`) {
		paths = append(paths, "/v1/devices?snapshot="+snap, "/v1/findings?snapshot="+snap, "/v1/snapshots/"+snap)
	}

	forbidden := []string{
		"public-community-7f3a", "lab-ssh-secret-9c1e", "lab-ssh-secret",
		"env:NM_SSH", "env:NM_SNMP",
		env("NETMAPPER_TEST_S3_ACCESS_KEY", "GK0123456789abcdef01234567"),
		env("NETMAPPER_TEST_S3_SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		"raw/sha256/", c.token,
	}
	for _, path := range paths {
		_, body := c.get(path)
		for _, f := range forbidden {
			if strings.Contains(string(body), f) {
				t.Errorf("%s contains %q", path, f)
			}
		}
	}
}

// T034, FR-016, SC-008, US2-5: serving every endpoint under every token state leaves every row of the
// collected, computed and reported zones as it was.
func TestServingWritesNothing(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)
	tables := []string{
		"observation", "observation_raw", "raw_object", "identifier_claim",
		"snapshot", "snapshot_judgement", "resolution", "entity", "entity_claim",
		"projection", "interface", "interface_alias", "interface_evidence", "edge", "edge_evidence",
		"finding", "finding_evidence",
	}
	checksum := func() []string {
		var out []string
		for _, table := range tables {
			out = append(out, table+" "+l.Strings(`SELECT coalesce(md5(string_agg(t::text, '|' ORDER BY t::text)), '-') FROM ` + table + ` t`)[0])
		}
		return out
	}
	before := checksum()
	revoked := mint(t, l.DB, "gone", "read")
	revoke(t, l.DB, "gone")
	admin := mint(t, l.DB, "admin", "admin")
	for _, path := range surface(t, c) {
		for _, auth := range []string{"", "Bearer nm_unknown", "Bearer " + revoked, "Bearer " + admin, "Bearer " + c.token} {
			get(t, c.srv, auth, path)
		}
	}
	after := checksum()
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("changed: %s -> %s", before[i], after[i])
		}
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
