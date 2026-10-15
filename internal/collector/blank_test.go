package collector_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// Feature 011, FR-009, research R7: a step that fails at the transport (stall, deadline, dropped
// session, no prompt after login) is never read as a device with nothing to report, whatever
// bytes came back. The fake returns no output in every case, the shape that would read as empty.
func TestBlankOutputWithErrorIsNeverEmpty(t *testing.T) {
	for _, c := range []struct {
		name   string
		break_ func(d *fake.Device)
		want   string // the interfaces outcome expected, "" when the family has no observation
	}{
		{"stall", func(d *fake.Device) { d.Stall = map[string]bool{"display interfaces": true} }, "10.0.0.1 interfaces unreachable timeout"},
		{"deadline", func(d *fake.Device) { d.Overrun = map[string]bool{"display interfaces": true} }, ""},
		{"dropped session", func(d *fake.Device) { d.Drop = map[string]bool{"display interfaces": true} }, ""},
		// The device is still identified over SNMP; the SSH session never opens, the task fails.
		{"no prompt after login", func(d *fake.Device) { d.NoPrompt = true }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			env(t)
			sw := FakeOS("sw1", "S001")
			sw.CLI["display interfaces"] = ""
			c.break_(sw)
			l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw}})
			got := l.AllOutcomes(l.Crawl(Doc))
			if slices.Contains(got, "10.0.0.1 interfaces empty") {
				t.Errorf("a failed read became empty: %v", got)
			}
			var iface []string
			for _, o := range got {
				if strings.HasPrefix(o, "10.0.0.1 interfaces ") {
					iface = append(iface, o)
				}
			}
			switch c.want {
			case "":
				if len(iface) != 0 {
					t.Errorf("interfaces observation written after a failed task: %v", iface)
				}
			default:
				if !slices.Equal(iface, []string{c.want}) {
					t.Errorf("interfaces %v, want %q", iface, c.want)
				}
			}
		})
	}
}
