package jobrunner_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T027, FR-016 and SC-001: a closed snapshot ends up resolved without anyone asking, and one that
// closed while the resolving side was down is resolved once it comes back, with no recovery path of
// its own.
func TestSweepResolvesClosedSnapshots(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001"),
	}})
	ctx := context.Background()

	snap := int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, l.Crawl(Doc)))
	if n := l.Int(`SELECT count(*) FROM resolution WHERE snapshot_id = $1`, snap); n != 1 {
		t.Fatalf("%d resolutions after a normal run, want the sweep to have done it", n)
	}

	// The case the sweep exists for: closed, with no entity set, as it would be had the engine died
	// between closing the snapshot and resolving it.
	for _, q := range []string{"DELETE FROM entity_claim", "DELETE FROM entity", "DELETE FROM resolution"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := jobrunner.Tick(ctx, l.Engine, l.Registry); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT count(*) FROM resolution WHERE snapshot_id = $1`, snap); n != 1 {
		t.Errorf("%d resolutions after the sweep, want exactly one", n)
	}
	if n := l.Int(`SELECT count(*) FROM entity WHERE snapshot_id = $1`, snap); n != 1 {
		t.Errorf("%d entities after the sweep, want the set rebuilt", n)
	}

	// And it does not redo one it has already done, whatever changed since (FR-017).
	before := l.Strings(`SELECT computed_at::text FROM resolution WHERE snapshot_id = $1`, snap)
	if err := jobrunner.Tick(ctx, l.Engine, l.Registry); err != nil {
		t.Fatal(err)
	}
	if after := l.Strings(`SELECT computed_at::text FROM resolution WHERE snapshot_id = $1`, snap); after[0] != before[0] {
		t.Errorf("computed_at moved from %v to %v: the sweep must leave a resolved snapshot alone", before, after)
	}
}
