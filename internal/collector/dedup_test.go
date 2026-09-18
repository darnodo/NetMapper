package collector_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport"
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

// A duplicate's claims do not make it the device's holder. Here the find on .1 holds the device,
// the find on .11 ends duplicate, then .1's worker dies after its claim step. When .1 is
// reclaimed it must find its own claims and carry on, not become a duplicate of its duplicate,
// which would leave the device collected by nobody.
func TestHolderSurvivesRestartAfterDuplicate(t *testing.T) {
	env(t)
	sw := FakeOS("sw1", "S001")
	sw.Block = map[string]bool{"display neighbours": true} // .1 stops in find step 3
	n := &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw, Addr("10.0.0.11"): sw}}
	n.OnRun = func(a netip.Addr, s transport.Step) {
		// .11 identifies only once .1 holds the device.
		for a == Addr("10.0.0.11") && s.Command == "display version" && !slices.Contains(n.Commands(), "10.0.0.1 display neighbours") {
			time.Sleep(5 * time.Millisecond)
		}
	}
	l := NewLab(t, n)
	job := l.Start(replace(t, shortLease, "targets: [10.0.0.1]", "targets: [10.0.0.1, 10.0.0.11]"))
	stopE := l.RunEngine()
	defer stopE()
	stop1 := l.RunCollector(l.Collector("c1"))
	for deadline := time.Now().Add(10 * time.Second); l.Int(`SELECT count(*) FROM task WHERE state = 'duplicate'`) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(".11 never ended duplicate")
		}
	}
	stop1()
	sw.Block = nil
	stop2 := l.RunCollector(l.Collector("c2"))
	defer stop2()
	l.Wait(job, "succeeded")
	got := l.Strings(`SELECT host(target) || ' ' || state FROM task WHERE job_id = $1 AND kind = 'find' ORDER BY target`, job)
	if len(got) != 2 || got[0] != "10.0.0.1 done" || got[1] != "10.0.0.11 duplicate" {
		t.Errorf("finds %v", got)
	}
	if n := l.Int(`SELECT count(*) FROM observation WHERE fact_family = 'interfaces' AND status = 'collected'`); n != 1 {
		t.Errorf("device collected %d times, want once", n)
	}
}
