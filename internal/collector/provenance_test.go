package collector_test

import (
	"context"
	"net/netip"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// US1-4, FR-007, FR-008, SC-005: every collected fact links to the command that produced it and
// to the exact bytes, retrievable from the object store; identical output is stored once.
func TestProvenance(t *testing.T) {
	env(t)
	sw1, sw2 := FakeOS("sw1", "S001", "p1 sw2 10.0.0.2"), FakeOS("sw2", "S002")
	same := "p1 up up 1500 aa:bb:cc:00:00:99 identical on both\n"
	sw1.CLI["display interfaces"], sw2.CLI["display interfaces"] = same, same
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw1, Addr("10.0.0.2"): sw2}})
	l.Crawl(Doc)
	ctx := context.Background()

	if n := l.Int(`SELECT count(*) FROM observation o WHERE status = 'collected'
		AND NOT EXISTS (SELECT 1 FROM observation_raw r WHERE r.snapshot_id = o.snapshot_id AND r.observation_id = o.id)`); n != 0 {
		t.Errorf("%d collected observations without raw output", n)
	}
	rows, err := l.DB.Query(ctx, `SELECT host(o.target), r.command, r.hash FROM observation o
		JOIN observation_raw r ON r.snapshot_id = o.snapshot_id AND r.observation_id = o.id WHERE r.command LIKE 'display %'`)
	if err != nil {
		t.Fatal(err)
	}
	type link struct {
		target, command string
		hash            []byte
	}
	var links []link
	for rows.Next() {
		var k link
		rows.Scan(&k.target, &k.command, &k.hash)
		links = append(links, k)
	}
	if len(links) < 6 {
		t.Fatalf("only %d raw links", len(links))
	}
	for _, k := range links {
		b, err := l.Raw.Get(ctx, k.hash)
		if err != nil {
			t.Fatal(err)
		}
		dev := map[string]*fake.Device{"10.0.0.1": sw1, "10.0.0.2": sw2}[k.target]
		if string(b) != dev.CLI[k.command] {
			t.Errorf("%s %s: stored bytes differ from device output", k.target, k.command)
		}
	}
	if n := l.Int(`SELECT count(*) FROM observation_raw r JOIN raw_object o ON o.hash = r.hash WHERE r.command = 'display interfaces'`); n != 2 {
		t.Errorf("%d interface outputs linked, want 2", n)
	}
	if n := l.Int(`SELECT count(DISTINCT hash) FROM observation_raw WHERE command = 'display interfaces'`); n != 1 {
		t.Errorf("identical output stored %d times", n)
	}
	if n := l.Int(`SELECT count(*) FROM observation WHERE fact_family = 'interfaces' AND (recipe_id NOT LIKE 'fakeos/if-cli@%' OR collected_at IS NULL)`); n != 0 {
		t.Error("interfaces observation without recipe_id or time")
	}
}
