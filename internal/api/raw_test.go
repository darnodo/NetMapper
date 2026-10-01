package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
)

// T021, FR-004, FR-004a, SC-006a, US1-3: from a port's evidence to the observation, to the bytes the
// device printed, without leaving the interface.
func TestEvidenceLeadsToTheBytes(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)

	port := findPort(t, c.json("/v1/devices/sw1", 200), "port1")
	obs := int(port["evidence"].([]any)[0].(map[string]any)["observation_id"].(float64))

	status, raw := c.get(fmt.Sprintf("/v1/observations/%d", obs))
	if status != 200 {
		t.Fatalf("observation %d: %d %s", obs, status, raw)
	}
	if strings.Contains(string(raw), "raw/sha256/") || strings.Contains(string(raw), l.Raw.Bucket) {
		t.Errorf("the observation answer names the object store: %s", raw)
	}
	body := c.json(fmt.Sprintf("/v1/observations/%d", obs), 200)
	cmds := list(body["commands"])
	if len(cmds) == 0 {
		t.Fatalf("observation %d lists no commands: %v", obs, body)
	}
	step, hexHash := cmds[0]["step"].(string), cmds[0]["hash"].(string)

	req, _ := http.NewRequest("GET", c.srv.URL+fmt.Sprintf("/v1/observations/%d/raw/%s", obs, step), nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got strings.Builder
	fmt.Fprint(&got, readAll(t, resp))
	hash, _ := hex.DecodeString(hexHash)
	want, err := l.Raw.Get(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/octet-stream" || got.String() != string(want) {
		t.Errorf("raw: %d %s %q, want the stored bytes %q", resp.StatusCode, resp.Header.Get("Content-Type"), got.String(), want)
	}
	if !strings.Contains(string(want), "p1") {
		t.Errorf("stored bytes %q do not look like the interface listing", want)
	}

	if body := c.json("/v1/observations/999999", 404); body["error"] != "no_such_observation" {
		t.Errorf("unknown observation: %v", body)
	}
	if body := c.json(fmt.Sprintf("/v1/observations/%d/raw/nonesuch", obs), 404); body["error"] != "no_such_step" {
		t.Errorf("unknown step: %v", body)
	}
	if body := c.json("/v1/observations/999999/raw/x", 404); body["error"] != "no_such_observation" {
		t.Errorf("unknown observation's step: %v", body)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}

// Issue #6: every evidence item names its snapshot, and ?snapshot= pins the lookup of both
// observation endpoints to it. Without it the lookup still finds the observation in any snapshot.
func TestObservationPinnedToSnapshot(t *testing.T) {
	l := standard(t)
	c := newClient(t, l)

	ev := findPort(t, c.json("/v1/devices/sw1", 200), "port1")["evidence"].([]any)[0].(map[string]any)
	obs, snap := int(ev["observation_id"].(float64)), int(ev["snapshot_id"].(float64))
	if body := c.json(fmt.Sprintf("/v1/observations/%d", obs), 200); int(body["observation"].(map[string]any)["snapshot_id"].(float64)) != snap {
		t.Fatalf("evidence names snapshot %d, the observation says %v", snap, body["observation"])
	}
	l.Crawl(Doc)
	other := l.Int(`SELECT max(id) FROM snapshot`)
	if other == snap {
		t.Fatal("the second crawl made no new snapshot")
	}

	body := c.json(fmt.Sprintf("/v1/observations/%d?snapshot=%d", obs, snap), 200)
	step := list(body["commands"])[0]["step"].(string)
	for _, c2 := range []struct {
		path   string
		status int
		err    string
	}{
		{fmt.Sprintf("/v1/observations/%d/raw/%s?snapshot=%d", obs, step, snap), 200, ""},
		{fmt.Sprintf("/v1/observations/%d/raw/%s", obs, step), 200, ""},
		{fmt.Sprintf("/v1/observations/%d?snapshot=%d", obs, other), 404, "no_such_observation"},
		{fmt.Sprintf("/v1/observations/%d/raw/%s?snapshot=%d", obs, step, other), 404, "no_such_observation"},
		{fmt.Sprintf("/v1/observations/%d/raw/nonesuch?snapshot=%d", obs, snap), 404, "no_such_step"},
		{fmt.Sprintf("/v1/observations/%d?snapshot=999999", obs), 404, "no_such_snapshot"},
		{fmt.Sprintf("/v1/observations/%d/raw/%s?snapshot=999999", obs, step), 404, "no_such_snapshot"},
		{fmt.Sprintf("/v1/observations/%d?snapshot=latest", obs), 400, "bad_snapshot"},
		{fmt.Sprintf("/v1/observations/%d/raw/%s?snapshot=x", obs, step), 400, "bad_snapshot"},
	} {
		status, raw := c.get(c2.path)
		if status != c2.status || (c2.err != "" && !strings.Contains(string(raw), `"`+c2.err+`"`)) {
			t.Errorf("%s: %d %s, want %d %s", c2.path, status, raw, c2.status, c2.err)
		}
	}
}
