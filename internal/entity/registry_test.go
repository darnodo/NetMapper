package entity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T016, FR-004/FR-022 and US1-5: a device whose pack yielded no strong identifier is still an
// entity, keyed on the address it answered on and marked weakly identified. Never on its hostname,
// which two chassis can share.
func TestWeakDeviceKeysOnItsAddress(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOSShaped("sw1", "S001", Shape{})})
	snap := snapshotOf(l, l.Crawl(doc))

	rows := l.Strings(`SELECT device_key || ' ' || weak FROM entity WHERE snapshot_id = $1`, snap)
	if len(rows) != 1 || rows[0] != "addr:10.0.0.1 true" {
		t.Fatalf("entity %v, want addr:10.0.0.1 marked weak", rows)
	}
	if n := l.Int(`SELECT count(*) FROM entity WHERE snapshot_id = $1 AND device_key LIKE 'hostname:%'`, snap); n != 0 {
		t.Errorf("%d entities keyed on a hostname, want none", n)
	}
	if got := l.Strings(`SELECT attributes->>'hostname' FROM entity WHERE snapshot_id = $1`, snap); got[0] != "sw1" {
		t.Errorf("hostname %v, want it kept as an attribute even though it keys nothing", got)
	}
}

// T018, US1-7, SC-007 and FR-021: the same device in two runs of one perimeter carries one key, so a
// consumer follows it across runs without regrouping its claims.
func TestKeyIsStableAcrossRuns(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	first := snapshotOf(l, l.Crawl(doc))
	second := snapshotOf(l, l.Crawl(doc))

	a, b := keysOf(l, first), keysOf(l, second)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("keys %v then %v, want the same device to carry one key", a, b)
	}
	if n := l.Int(`SELECT count(*) FROM device`); n != 1 {
		t.Errorf("%d devices in the registry, want one box to be one row", n)
	}
}

// T019, research R5: a device that answered on a new address but kept its identifiers is the same
// device. Its key follows the identifiers, never the address.
func TestRenumberingKeepsTheKey(t *testing.T) {
	sw := FakeOS("sw1", "S001")
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": sw, "10.0.0.2": nil})
	delete(l.Net.Devices, Addr("10.0.0.2"))
	first := snapshotOf(l, l.Crawl(doc))

	// The same chassis, now answering on the other address.
	delete(l.Net.Devices, Addr("10.0.0.1"))
	l.Net.Devices[Addr("10.0.0.2")] = sw
	second := snapshotOf(l, l.Crawl(doc))

	a, b := keysOf(l, first), keysOf(l, second)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("keys %v then %v, want the key to follow the chassis and not the address", a, b)
	}
	if n := l.Int(`SELECT count(*) FROM device`); n != 1 {
		t.Errorf("%d devices, want the renumbered box to stay one", n)
	}
	targets := l.Strings(`SELECT attributes->>'targets' FROM entity WHERE snapshot_id = $1`, second)
	if len(targets) != 1 || !strings.Contains(targets[0], "10.0.0.2") {
		t.Errorf("targets %v, want the new address on the entity", targets)
	}
}

// T020, research R5: a run that reads only some of a device's identifiers still finds it. This is
// the case a key hashed from the identifier set fails, and it is the common one with SNMP.
func TestPartialIdentifiersKeepTheKey(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	first := snapshotOf(l, l.Crawl(doc))

	// The next run reads the serial and no chassis MAC, so the key it was minted from is missing.
	l.Net.Devices[Addr("10.0.0.1")] = FakeOSShaped("sw1", "S001", Shape{Serial: true})
	second := snapshotOf(l, l.Crawl(doc))

	a, b := keysOf(l, first), keysOf(l, second)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("keys %v then %v, want a device matched on any identifier it still carries", a, b)
	}
	if !strings.HasPrefix(a[0], "chassis_mac:") {
		t.Fatalf("key %q, want the anchor of the first run, which the second no longer reads", a[0])
	}
}

// T020a, FR-023: a device that changed every strong identifier is not guessed onto its nearest
// match. A new key is minted and it reads as a device gone and a device appeared, until an operator
// records a merge.
func TestChangedIdentifiersMintANewKey(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	first := snapshotOf(l, l.Crawl(doc))

	l.Net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S002")
	second := snapshotOf(l, l.Crawl(doc))

	a, b := keysOf(l, first), keysOf(l, second)
	if len(a) != 1 || len(b) != 1 || a[0] == b[0] {
		t.Fatalf("keys %v then %v, want a new key rather than a guess onto the old one", a, b)
	}
	if n := l.Int(`SELECT count(*) FROM device`); n != 2 {
		t.Errorf("%d devices, want the old one kept and a new one minted", n)
	}
}

// T021, FR-021 and the clarification of 2026-09-24: a device key is scoped to a perimeter name. Two
// perimeters never combine a device, whatever identifier they share, so a lab clone carrying a
// factory serial cannot reach into another perimeter's history.
func TestPerimetersDoNotShareDevices(t *testing.T) {
	l, _ := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	doc := strings.Replace(Doc, "perimeters:\n  - name: lab",
		"perimeters:\n  - name: lab2\n    include: [10.0.0.0/24]\n  - name: lab", 1)
	doc = strings.Replace(doc, "perimeters: [lab]}", "perimeters: [lab, lab2]}", 2)

	crawlAs := func(perimeter string) int64 {
		job, err := jobrunner.Start(context.Background(), l.Operator, []byte(doc), perimeter, "seeds", "test")
		if err != nil {
			t.Fatal(err)
		}
		stopC := l.RunCollector(l.Collector("c1"))
		stopE := l.RunEngine()
		l.Wait(job, "succeeded", "failed", "cancelled")
		stopE()
		stopC()
		return snapshotOf(l, job)
	}
	one, two := crawlAs("lab"), crawlAs("lab2")

	a, b := keysOf(l, one), keysOf(l, two)
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("keys %v and %v, want the same identifier to give the same key in each perimeter", a, b)
	}
	// Ordered by the column, not by the concatenation: the database's collation ignores the space
	// and would sort lab2 before lab.
	rows := l.Strings(`SELECT perimeter_name || ' ' || key FROM device ORDER BY perimeter_name, key`)
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "lab ") || !strings.HasPrefix(rows[1], "lab2 ") {
		t.Errorf("devices %v, want one row per perimeter and no reach between them", rows)
	}
	if n := l.Int(`SELECT count(DISTINCT perimeter_name) FROM device_identifier`); n != 2 {
		t.Errorf("%d perimeters in device_identifier, want each to keep its own attribution", n)
	}
}
