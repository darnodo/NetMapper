package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"
)

// confidences is the closed set of FR-002a, per kind of element.
var confidences = map[string][]string{
	"device":    {"strong", "weak"},
	"interface": {"described", "revealed"},
	"edge":      {"both_ends", "one_end", "direct"},
	"finding":   {"direct", "derived"},
}

// kindOf maps the key an element sits under to its kind.
var kindOf = map[string]string{
	"devices": "device", "interfaces": "interface", "interface": "interface",
	"edges": "edge", "findings": "finding",
}

// T015, SC-002, FR-002, FR-002a, FR-020: every element of an answer carries its evidence, the time of
// each observation, and a confidence from its kind's set, and the answer carries its snapshot and that
// snapshot's verdict. Checked over the whole body rather than sampled.
func checkEvidence(t testing.TB, path string, body []byte) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	// /v1/snapshots lists snapshots rather than answering from one; each entry is its own envelope.
	if _, ok := root["snapshots"]; !ok {
		snap, ok := root["snapshot"].(map[string]any)
		if !ok {
			t.Errorf("%s: no snapshot envelope", path)
		} else if _, ok := snap["verdict"]; !ok {
			t.Errorf("%s: snapshot envelope has no verdict key", path)
		}
	}
	// The device endpoint answers with the device itself at the top level.
	if _, ok := root["device_key"]; ok {
		if _, isIface := root["interface"]; !isIface {
			checkElement(t, path, "device", root)
		}
	}
	for field, kind := range kindOf {
		switch v := root[field].(type) {
		case []any:
			for _, x := range v {
				checkElement(t, path, kind, x.(map[string]any))
			}
		case map[string]any:
			checkElement(t, path, kind, v)
		}
	}
	// Edges inside an interface or a device are listed at the top level, not nested; nothing else
	// nests elements.
}

func checkElement(t testing.TB, path, kind string, e map[string]any) {
	t.Helper()
	name := fmt.Sprintf("%s: %s %v", path, kind, e["device_key"])
	if n, ok := e["canonical_name"]; ok {
		name = fmt.Sprintf("%s: %s %v", path, kind, n)
	} else if n, ok := e["name"]; ok {
		name = fmt.Sprintf("%s: %s %v", path, kind, n)
	}
	c, _ := e["confidence"].(string)
	if !slices.Contains(confidences[kind], c) {
		t.Errorf("%s: confidence %q, want one of %v", name, c, confidences[kind])
	}
	ev, _ := e["evidence"].([]any)
	if len(ev) == 0 {
		t.Errorf("%s: no evidence", name)
		return
	}
	for _, x := range ev {
		o := x.(map[string]any)
		if id, _ := o["observation_id"].(float64); id == 0 {
			t.Errorf("%s: evidence without an observation id: %v", name, o)
		}
		at, _ := o["collected_at"].(string)
		if ts, err := time.Parse(time.RFC3339Nano, at); err != nil || ts.IsZero() {
			t.Errorf("%s: evidence without a collection time: %v", name, o)
		}
	}
}

// recorder is a testing.TB that records a failure instead of failing, so the checker itself can be
// shown to fail.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }

// T015, SC-002: an answer with one element lacking evidence, or lacking a confidence, or carrying
// one outside its kind's set, fails the suite. Without this the checker could pass everything.
func TestCheckEvidenceCatchesMissing(t *testing.T) {
	env := `"snapshot": {"id": 1, "verdict": null}`
	ev := `"evidence": [{"observation_id": 3, "collected_at": "2026-09-25T10:44:58Z"}]`
	for name, body := range map[string]string{
		"bare interface":       `{` + env + `, "interfaces": [{"canonical_name": "port1", "confidence": "described"}]}`,
		"no confidence":        `{` + env + `, "interfaces": [{"canonical_name": "port1", ` + ev + `}]}`,
		"undefined confidence": `{` + env + `, "edges": [{"name": "e", "confidence": "maybe", ` + ev + `}]}`,
		"no collection time":   `{` + env + `, "findings": [{"id": 1, "confidence": "direct", "evidence": [{"observation_id": 3}]}]}`,
		"no envelope":          `{"devices": []}`,
	} {
		r := &recorder{TB: t}
		checkEvidence(r, name, []byte(body))
		if !r.failed {
			t.Errorf("%s: passed the checker", name)
		}
	}
	r := &recorder{TB: t}
	checkEvidence(r, "good", []byte(`{`+env+`, "interfaces": [{"canonical_name": "port1", "confidence": "described", `+ev+`}]}`))
	if r.failed {
		t.Error("a well-formed answer failed the checker")
	}
}
