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
	m, ok := j.Breakdown["missing"].(map[string][]string)
	if !ok {
		t.Fatalf("breakdown has no missing map: %v", j.Breakdown)
	}
	return m[reason]
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
	filtered, _ := j.Breakdown["perimeter_filtered"].([]string)
	if len(filtered) != 1 || filtered[0] != "10.0.0.9" {
		t.Errorf("perimeter_filtered %v, want [10.0.0.9]", filtered)
	}
}
