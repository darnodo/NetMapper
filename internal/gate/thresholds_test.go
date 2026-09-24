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

// twoDevicesLosingOne builds a baseline of two devices and a successor that reaches one, which is
// 50% coverage: quarantined under the defaults, and the figure US3 tunes.
func twoDevicesLosingOne(t *testing.T, doc string) (*Lab, gate.Judgement) {
	t.Helper()
	env(t)
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p2 sw2 10.0.0.2"),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1"),
	}}
	l := NewLab(t, net)
	ctx := context.Background()

	if _, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(doc))); err != nil {
		t.Fatal(err)
	}
	net.Devices[Addr("10.0.0.1")] = FakeOS("sw1", "S001")

	j, err := gate.Judge(ctx, l.Engine, snapshotOf(l, l.Crawl(doc)))
	if err != nil {
		t.Fatal(err)
	}
	if j.Coverage == nil || *j.Coverage != 0.5 {
		t.Fatalf("coverage %v, want 0.5", j.Coverage)
	}
	return l, j
}

// T034, FR-005: the same coverage lands differently under a perimeter that declares its own
// tolerance, and the verdict says which rules it was judged by.
func TestDeclaredThresholdsChangeTheVerdict(t *testing.T) {
	_, byDefault := twoDevicesLosingOne(t, Doc)
	if byDefault.Classification != gate.Quarantined {
		t.Errorf("classification %s under the defaults, want quarantined", byDefault.Classification)
	}
	if byDefault.Thresholds["source"] != "default" {
		t.Errorf("thresholds %v, want source default", byDefault.Thresholds)
	}

	tolerant := strings.Replace(Doc, "exclude: [10.0.0.254/32]",
		"exclude: [10.0.0.254/32]\n    degraded_at: 0.5\n    quarantined_below: 0.5", 1)
	_, declared := twoDevicesLosingOne(t, tolerant)
	if declared.Classification != gate.Degraded {
		t.Errorf("classification %s under degraded_at 0.5, want degraded", declared.Classification)
	}
	if declared.Thresholds["source"] != "perimeter" {
		t.Errorf("thresholds %v, want source perimeter", declared.Thresholds)
	}
}

// T036, FR-005/SC-005: thresholds are read from the perimeter row of the snapshot's own config
// version, so changing the document leaves earlier verdicts alone, and re-judging an old snapshot
// still applies the rules it ran under.
func TestThresholdsArePinnedToTheirConfigVersion(t *testing.T) {
	tolerant := strings.Replace(Doc, "exclude: [10.0.0.254/32]",
		"exclude: [10.0.0.254/32]\n    degraded_at: 0.5\n    quarantined_below: 0.5", 1)
	l, lenient := twoDevicesLosingOne(t, tolerant)
	ctx := context.Background()

	// A later run of the same perimeter goes back to the defaults.
	strict := snapshotOf(l, l.Crawl(Doc))
	if _, err := gate.Judge(ctx, l.Engine, strict); err != nil {
		t.Fatal(err)
	}

	again, err := gate.Judge(ctx, l.Engine, lenient.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Classification != gate.Degraded || again.Thresholds["source"] != "perimeter" {
		t.Errorf("re-judged as %s under %v, want degraded under its own perimeter's thresholds",
			again.Classification, again.Thresholds)
	}
}
