package graph_test

import (
	"net/netip"
	"slices"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// TestEdgeAttributeShapes is the source of truth for what an edge's `attributes` holds. data-model.md
// explains why each field is there and points here for the shapes themselves, so the two cannot
// contradict each other: the section drifted from the code four times while it carried its own copy of
// the answer, and every one of those was the prose being wrong about code that had not moved.
//
// The fixture covers every shape the projector can produce:
//
//   - has_address, which is always just the address;
//   - an agreed link, where both endpoints are resolved ports and say everything themselves;
//   - a one-sided link whose far end resolved and whose report named the port;
//   - a one-sided link whose far end resolved and whose report named no port, which keeps a `dev:`
//     endpoint because no other report named that port for it to fold into;
//   - a one-sided link whose far end no entity accounts for, the only shape carrying `remote_*`.
func TestEdgeAttributeShapes(t *testing.T) {
	l := lab(t, map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001",
			// Agreed with sw2, and the far-end port named.
			"p1 sw2 10.0.0.2 p1 "+MAC("S002"),
			// A box inside the perimeter that answers nothing, so no entity accounts for it.
			"p2 box 10.0.0.9 eth0 aa:bb:cc:99:99:99",
			// sw3 resolves but never reports back, and this names its port.
			"p9 sw3 10.0.0.3 p1 "+MAC("S003")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002",
			"p1 sw1 10.0.0.1 p1 "+MAC("S001"),
			// sw3 again, named with no port at all and with nothing to fold into.
			"p2 sw3 10.0.0.3 - "+MAC("S003")),
		Addr("10.0.0.3"): FakeOS("sw3", "S003"),
	})

	got := l.Strings(`
		SELECT g.type || ' ' || g.confidence || ' {' ||
		       coalesce((SELECT string_agg(k, ',' ORDER BY k) FROM jsonb_object_keys(g.attributes) k), '') || '}'
		FROM edge g GROUP BY 1 ORDER BY 1`)
	want := []string{
		"has_address direct {address}",
		"l1_link both_ends {protocols}",
		"l1_link one_end {protocols}",
		"l1_link one_end {protocols,remote_chassis_id,remote_mgmt_address," +
			"remote_mgmt_address_type,remote_system_name,to_spelling}",
		"l1_link one_end {protocols,to_spelling}",
	}
	if !slices.Equal(got, want) {
		t.Errorf("edge attribute shapes:\n got %v\nwant %v", got, want)
	}

	// The fixture has to actually produce all five, or the assertion above proves less than it reads.
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link'`); n != 4 {
		t.Errorf("%d links, want the four the fixture describes", n)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE from_ref LIKE 'dev:%' AND type = 'l1_link'`); n != 1 {
		t.Error("the portless report folded or vanished, so its shape is untested")
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE to_ref LIKE 'unknown:%'`); n != 1 {
		t.Error("the unresolved far end resolved, so the remote_* shape is untested")
	}
}
