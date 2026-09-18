package collector_test

import (
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US1-2, FR-004: one device on two addresses, both queued at once, is identified and collected
// once. Both finds write their claims; one is done, the other duplicate.
func TestOneDeviceTwoAddresses(t *testing.T) {
	env(t)
	sw := FakeOS("sw1", "S001")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw, Addr("10.0.0.11"): sw}})
	doc := Doc + "\n"
	doc = replace(t, doc, "targets: [10.0.0.1]", "targets: [10.0.0.1, 10.0.0.11]")
	job := l.Crawl(doc)

	states := l.Strings(`SELECT state FROM task WHERE job_id = $1 AND kind = 'find' ORDER BY state`, job)
	if len(states) != 2 || states[0] != "done" || states[1] != "duplicate" {
		t.Errorf("find states %v", states)
	}
	if n := l.Int(`SELECT count(*) FROM task WHERE job_id = $1 AND kind = 'scrape'`, job); n != 1 {
		t.Errorf("%d scrapes", n)
	}
	if n := l.Int(`SELECT count(DISTINCT observation_id) FROM identifier_claim WHERE strength = 'strong'`); n != 2 {
		t.Errorf("claims written by %d finds, want 2", n)
	}
	if n := l.Int(`SELECT count(*) FROM observation WHERE fact_family <> 'identity'`); n != 2 {
		t.Errorf("%d non-identity observations, want neighbours and interfaces once", n)
	}
	dup := l.Strings(`SELECT parsed -> 0 ->> 'duplicate_of_task' FROM observation WHERE parsed -> 0 ? 'duplicate_of_task'`)
	owner := l.Strings(`SELECT id::text FROM task WHERE job_id = $1 AND kind = 'find' AND state = 'done'`, job)
	if len(dup) != 1 || dup[0] != owner[0] {
		t.Errorf("duplicate_of_task %v, owner %v", dup, owner)
	}
}
