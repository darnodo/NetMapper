package jobrunner_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darnodo/NetMapper/internal/entity"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T034, FR-021 and SC-001: a closed snapshot that carries an entity set ends up projected without
// anyone asking, and one that closed while the projecting side was down is projected once it comes
// back, with no recovery path of its own.
func TestSweepProjectsResolvedSnapshots(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}})
	ctx := context.Background()

	snap := int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, l.Crawl(Doc)))
	if n := l.Int(`SELECT count(*) FROM projection WHERE snapshot_id = $1`, snap); n != 1 {
		t.Fatalf("%d projections after a normal run, want the sweep to have done it", n)
	}
	links := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'`)
	if links == 0 {
		t.Fatal("the fixture produced no link, so this proves nothing")
	}

	// The case the sweep exists for: closed and resolved, with no projection, as it would be had the
	// engine died between resolving the snapshot and projecting it.
	for _, q := range []string{"DELETE FROM edge", "DELETE FROM interface", "DELETE FROM projection"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := jobrunner.Tick(ctx, l.Engine, l.Registry); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT count(*) FROM projection WHERE snapshot_id = $1`, snap); n != 1 {
		t.Errorf("%d projections after the sweep, want exactly one", n)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'`); n != links {
		t.Errorf("%d links after the sweep, want the %d the first projection produced", n, links)
	}
}

// T035, FR-021 and research R12: re-resolving a snapshot cascades its interfaces away and leaves the
// projection row claiming a set that is gone. The sweep takes it again, because the input changed and
// the output is already destroyed. That is self-repair, not the system deciding on its own to redo a
// projection whose inputs are unchanged.
func TestSweepReprojectsAfterReresolution(t *testing.T) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}})
	ctx := context.Background()
	snap := int64(l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, l.Crawl(Doc)))
	before := l.Int(`SELECT count(*) FROM interface`)
	if before == 0 {
		t.Fatal("nothing was projected to begin with")
	}

	if _, err := entity.Resolve(ctx, l.DB, snap); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT count(*) FROM interface`); n != 0 {
		t.Fatalf("%d interfaces survived a re-resolution, want the cascade to have taken them", n)
	}
	if n := l.Int(`SELECT count(*) FROM projection p JOIN resolution r USING (snapshot_id)
		WHERE p.snapshot_id = $1 AND p.resolution_at = r.computed_at`, snap); n != 0 {
		t.Fatal("the projection row still claims to match the entity set it was built from")
	}

	if err := jobrunner.Tick(ctx, l.Engine, l.Registry); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT count(*) FROM interface`); n != before {
		t.Errorf("%d interfaces after the sweep, want the %d it had", n, before)
	}
	if n := l.Int(`SELECT count(*) FROM projection p JOIN resolution r USING (snapshot_id)
		WHERE p.snapshot_id = $1 AND p.resolution_at = r.computed_at`, snap); n != 1 {
		t.Error("the projection does not record the entity set it was built from")
	}

	// A second tick must leave it alone: the inputs have not changed since.
	at := l.Strings(`SELECT computed_at::text FROM projection WHERE snapshot_id = $1`, snap)
	if err := jobrunner.Tick(ctx, l.Engine, l.Registry); err != nil {
		t.Fatal(err)
	}
	if got := l.Strings(`SELECT computed_at::text FROM projection WHERE snapshot_id = $1`, snap); got[0] != at[0] {
		t.Error("the sweep redid a projection whose inputs had not changed (FR-022)")
	}
}
