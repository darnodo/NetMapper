package entity_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/darnodo/NetMapper/internal/entity"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// T025, FR-015: a re-resolution replaces the set rather than adding to it. One resolution row, one
// entity per device, and no entity_claim left pointing at an entity that no longer exists.
func TestReResolvingReplacesTheSet(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("sw1", "S001"),
		"10.0.0.2": FakeOS("sw2", "S002"),
	})
	snap := snapshotOf(l, l.Crawl(doc))

	links := l.Int(`SELECT count(*) FROM entity_claim WHERE snapshot_id = $1`, snap)
	for range 3 {
		resolve(l, snap)
	}

	if n := l.Int(`SELECT count(*) FROM resolution WHERE snapshot_id = $1`, snap); n != 1 {
		t.Errorf("%d resolution rows, want exactly one current set", n)
	}
	if n := l.Int(`SELECT count(*) FROM entity WHERE snapshot_id = $1`, snap); n != 2 {
		t.Errorf("%d entities after three resolutions, want the set replaced and not stacked", n)
	}
	if n := l.Int(`SELECT count(*) FROM entity_claim WHERE snapshot_id = $1`, snap); n != links {
		t.Errorf("%d claim links, want the same %d: a replaced set leaves no orphan", n, links)
	}
	if n := l.Int(`
		SELECT count(*) FROM entity_claim ec
		WHERE NOT EXISTS (SELECT 1 FROM entity e WHERE e.id = ec.entity_id)`); n != 0 {
		t.Errorf("%d orphan claim links", n)
	}
}

// T026, research R8 and the concurrency edge case: a sweep and an explicit request landing together
// leave one complete set, never two half-written ones. The loser waits on the advisory lock.
func TestConcurrentResolutionsLeaveOneSet(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{
		"10.0.0.1": FakeOS("sw1", "S001"),
		"10.0.0.2": FakeOS("sw2", "S002"),
	})
	snap := snapshotOf(l, l.Crawl(doc))
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = entity.Resolve(ctx, l.Engine, snap)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("resolution %d: %v", i, err)
		}
	}

	if n := l.Int(`SELECT count(*) FROM resolution WHERE snapshot_id = $1`, snap); n != 1 {
		t.Errorf("%d resolution rows, want one", n)
	}
	if keys := keysOf(l, snap); len(keys) != 2 {
		t.Errorf("entities %v, want the two devices once each and no interleaving", keys)
	}
}

// The registry is what two resolutions of one perimeter share, so the lock is on the perimeter and not
// on the snapshot: without that, two snapshots reading the identifiers they each happened to collect can
// mint a different key for one device. Asserting the outcome of that race is unreliable, since two
// goroutines usually serialise on their own, so this asserts the mechanism: a resolution waits while
// anything else holds the perimeter's lock, and proceeds once it is released.
func TestResolutionSerialisesOnThePerimeter(t *testing.T) {
	l, doc := lab(t, map[string]*fake.Device{"10.0.0.1": FakeOS("sw1", "S001")})
	snap := snapshotOf(l, l.Crawl(doc))
	ctx := context.Background()

	// Hold the perimeter's lock on a connection of its own.
	conn, err := l.DB.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "lab"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := entity.Resolve(ctx, l.Engine, snap)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("resolution finished while the perimeter was locked (err %v): the lock is not on the perimeter", err)
	case <-time.After(300 * time.Millisecond):
		// Waiting, which is what it should do.
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("resolution after the lock was released: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("resolution never finished after the lock was released")
	}

	if keys := keysOf(l, snap); len(keys) != 1 {
		t.Errorf("entities %v, want the one device resolved once the lock was free", keys)
	}
}
