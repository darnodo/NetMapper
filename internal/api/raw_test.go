package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
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
