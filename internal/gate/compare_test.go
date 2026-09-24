package gate_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/gate"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

func missing(t *testing.T, j gate.Judgement, reason string) []string {
	t.Helper()
	a, ok := j.Breakdown.Missing[reason]
	if !ok {
		t.Fatalf("breakdown reports no %q at all: %+v", reason, j.Breakdown)
	}
	if a.Count != len(a.Targets) {
		t.Errorf("%s count %d but %d targets", reason, a.Count, len(a.Targets))
	}
	return a.Targets
}

// T029: a device that stops being reported by its neighbour leaves discovery without ever being
// tried, so nothing in the newer snapshot's own outcomes says anything is wrong. Only the
// comparison against the baseline catches it, and it must be called not_attempted, not unreachable.
// This is the shape of the LLDP parsing bug found while validating 001.
func TestDeviceThatLeftDiscovery(t *testing.T) {
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}

	// sw2 is still up and would answer; sw1 simply stops reporting it, and only sw1 is a seed.
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001")

	job := l.Crawl(Doc)
	if got := l.Outcomes(job); len(got) != 3 {
		t.Fatalf("newer snapshot should hold sw1 only, got %v", got)
	}
	for _, o := range l.Outcomes(job) {
		if o[:8] == "10.0.0.2" {
			t.Fatalf("sw2 was attempted after all: %s", o)
		}
	}

	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, job))
	if err != nil {
		t.Fatal(err)
	}
	if j.CarriedOver != 1 || j.BaselineCount != 2 {
		t.Errorf("carried over %d/%d, want 1/2", j.CarriedOver, j.BaselineCount)
	}
	if j.Coverage == nil || *j.Coverage != 0.5 {
		t.Errorf("coverage %v, want 0.5", j.Coverage)
	}
	if j.Classification != gate.Quarantined {
		t.Errorf("classification %s, want quarantined", j.Classification)
	}
	if got := missing(t, j, "not_attempted"); len(got) != 1 || got[0] != "10.0.0.2" {
		t.Errorf("not_attempted %v, want [10.0.0.2]", got)
	}
	if got := missing(t, j, "unreachable"); len(got) != 0 {
		t.Errorf("unreachable %v, want none: the device was never tried", got)
	}
}

// The other half of the distinction: a device still reported by its neighbour but no longer
// answering is unreachable, not not_attempted.
func TestDeviceStillTriedButSilent(t *testing.T) {
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}

	// sw1 keeps reporting sw2, so sw2 is queued and tried; sw2 no longer answers.
	delete(net.Devices, Addr("10.0.0.2"))

	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(Doc)))
	if err != nil {
		t.Fatal(err)
	}
	if got := missing(t, j, "unreachable"); len(got) != 1 || got[0] != "10.0.0.2" {
		t.Errorf("unreachable %v, want [10.0.0.2]", got)
	}
	if got := missing(t, j, "not_attempted"); len(got) != 0 {
		t.Errorf("not_attempted %v, want none: the device was tried and stayed silent", got)
	}
}

// A device whose address changed but whose strong identifier did not is the same device, not one
// loss plus one arrival (research R2).
func TestRenumberedDeviceIsNotALoss(t *testing.T) {
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}

	// Same sw2, same serial, new address.
	delete(net.Devices, Addr("10.0.0.2"))
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001", "p2 sw2 10.0.0.7")
	net.Devices[Addr("10.0.0.7")] = FakeOS("sw2", "S002", "p1 sw1 10.0.0.1")

	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(Doc)))
	if err != nil {
		t.Fatal(err)
	}
	if j.CarriedOver != 2 || j.Classification != gate.Published {
		t.Errorf("got %s %d/%d, want published 2/2: the renumbered device is the same device",
			j.Classification, j.CarriedOver, j.BaselineCount)
	}
}

// T015: narrowing a perimeter on purpose is a scope change, not a coverage collapse. The device the
// new ranges no longer cover leaves the denominator instead of counting as a loss.
func TestNarrowedPerimeterIsNotALoss(t *testing.T) {
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.9"),
		Addr("10.0.0.9"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}

	// Same network, but the operator narrows the perimeter to .0-.7, leaving 10.0.0.9 out of scope.
	narrowed := strings.Replace(Doc, "include: [10.0.0.0/24]", "include: [10.0.0.0/29]", 1)
	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(narrowed)))
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineCount != 1 || j.CarriedOver != 1 {
		t.Errorf("carried over %d/%d, want 1/1: the excluded device should leave the denominator",
			j.CarriedOver, j.BaselineCount)
	}
	if j.Classification != gate.Published {
		t.Errorf("classification %s, want published", j.Classification)
	}
	if got := missing(t, j, "not_attempted"); len(got) != 0 {
		t.Errorf("not_attempted %v, want none: the device was deliberately put out of scope", got)
	}
	if f := j.Breakdown.PerimeterFiltered; f.Count != 1 || len(f.Targets) != 1 || f.Targets[0] != "10.0.0.9" {
		t.Errorf("perimeter_filtered %+v, want one entry for 10.0.0.9", f)
	}
}

// T030, research R3: a device reached on two addresses is one device, not two, and it keeps both
// addresses as handles. When one of them is retried and fails in the newer snapshot, that failure
// is the device's reason: it was tried, so it is not not_attempted.
func TestDeviceKnownByTwoAddresses(t *testing.T) {
	env(t)
	sw2 := FakeOS("sw2", "S002", "p1 sw1 10.0.0.1")
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2", "p3 sw2 10.0.0.3"),
		Addr("10.0.0.2"): sw2,
		Addr("10.0.0.3"): sw2, // same device, second address
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if n := l.Int(`SELECT count(*) FROM observation
	               WHERE snapshot_id = $1 AND fact_family = 'identity' AND status = 'collected'`, baseline); n != 3 {
		t.Fatalf("%d identity observations in the baseline, want 3 (sw1, sw2, sw2's duplicate)", n)
	}
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}
	if n := l.Int(`SELECT baseline_devices FROM snapshot_judgement
	               WHERE snapshot_id = $1 AND active`, baseline); n != 0 {
		t.Fatalf("first snapshot should have no baseline devices, got %d", n)
	}

	// sw2 is gone from both addresses; sw1 still reports it, so it is tried and stays silent.
	delete(net.Devices, Addr("10.0.0.2"))
	delete(net.Devices, Addr("10.0.0.3"))

	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(Doc)))
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineCount != 2 {
		t.Errorf("baseline holds %d devices, want 2: sw2 on two addresses is one device", j.BaselineCount)
	}
	if got := missing(t, j, "unreachable"); len(got) != 1 {
		t.Errorf("unreachable %v, want one entry for sw2", got)
	}
	if got := missing(t, j, "not_attempted"); len(got) != 0 {
		t.Errorf("not_attempted %v, want none: sw2 was tried on both of its addresses", got)
	}
}

// T028, FR-006: the figures behind a verdict. Three baseline devices leave for three different
// reasons, and the breakdown names each one with the addresses behind it, sums to the devices that
// did not carry over, and reaches the database in the shape contracts and quickstart queries
// expect.
func TestBreakdownNamesEveryReason(t *testing.T) {
	env(t)
	denied := FakeOS("sw4", "S004", "p1 sw1 10.0.0.1")
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2", "p3 sw3 10.0.0.3", "p4 sw4 10.0.0.4"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
		Addr("10.0.0.3"): FakeOS("sw3", "S003", "p1 sw1 10.0.0.1"),
		Addr("10.0.0.4"): denied,
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	baseline := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, baseline); err != nil {
		t.Fatal(err)
	}

	// sw2 stops answering but is still reported; sw3 stops being reported; sw4 answers but now
	// rejects every credential set.
	delete(net.Devices, Addr("10.0.0.2"))
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001", "p2 sw2 10.0.0.2", "p4 sw4 10.0.0.4")
	denied.Reject = []string{"snmp-a", "ssh-a"}
	denied.Evidence = "permission denied"

	snap := snapshotOf(l, l.Crawl(Doc))
	j, err := gate.Judge(ctx, l.Engine, snap)
	if err != nil {
		t.Fatal(err)
	}

	for reason, want := range map[string]string{
		"unreachable":   "10.0.0.2",
		"not_attempted": "10.0.0.3",
		"denied":        "10.0.0.4",
	} {
		if got := missing(t, j, reason); len(got) != 1 || got[0] != want {
			t.Errorf("%s %v, want [%s]", reason, got, want)
		}
	}
	for _, empty := range []string{"unsupported", "parse_failed"} {
		if got := missing(t, j, empty); len(got) != 0 {
			t.Errorf("%s %v, want none", empty, got)
		}
	}

	var sum int
	for _, r := range []string{"unreachable", "denied", "unsupported", "parse_failed", "not_attempted"} {
		sum += len(missing(t, j, r))
	}
	if sum != j.BaselineCount-j.CarriedOver {
		t.Errorf("missing sums to %d, want %d (baseline %d, carried over %d)",
			sum, j.BaselineCount-j.CarriedOver, j.BaselineCount, j.CarriedOver)
	}
	if j.Reached != 1 {
		t.Errorf("reached %d, want 1: only sw1 came back", j.Reached)
	}

	// The shape a quickstart query relies on: every reason readable, counts present, never null.
	stored := l.Strings(`
		SELECT (breakdown->'missing'->'not_attempted'->>'count') || ' ' ||
		       (breakdown->'missing'->'not_attempted'->'targets'->>0) || ' ' ||
		       (breakdown->'missing'->'unsupported'->>'count') || ' ' ||
		       jsonb_array_length(breakdown->'perimeter_filtered'->'targets') || ' ' ||
		       gate_version || ' ' || (computed_at IS NOT NULL)
		FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, snap)
	if len(stored) != 1 || stored[0] != "1 10.0.0.3 0 0 2 true" {
		t.Errorf("stored breakdown reads %q, want \"1 10.0.0.3 0 0 2 true\"", stored)
	}
}
