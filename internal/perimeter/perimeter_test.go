package perimeter

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func pfx(s ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, x := range s {
		out = append(out, netip.MustParsePrefix(x))
	}
	return out
}

func TestAllowed(t *testing.T) {
	p := Perimeter{Include: pfx("10.0.0.0/24", "2001:db8::/64"), Exclude: pfx("10.0.0.1/32", "2001:db8::1/128")}
	for addr, want := range map[string]bool{
		"10.0.0.2":         true,
		"10.0.0.1":         false, // excluded
		"10.0.1.2":         false, // not included
		"::ffff:10.0.0.2":  true,  // mapped v4
		"2001:db8::2":      true,
		"2001:db8::1":      false,
		"2001:db8:0:1::10": false,
	} {
		if got := p.Allowed(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Allowed(%s) = %v", addr, got)
		}
	}
	if (Perimeter{}).Allowed(netip.MustParseAddr("10.0.0.2")) {
		t.Error("empty include allowed an address")
	}
}

func TestResolve(t *testing.T) {
	p := Perimeter{Include: pfx("10.0.0.0/24"), LookupNetIP: func(_ context.Context, _, host string) ([]netip.Addr, error) {
		return map[string][]netip.Addr{
			"inside":  {netip.MustParseAddr("10.0.0.5")},
			"outside": {netip.MustParseAddr("192.0.2.1")},
		}[host], nil
	}}
	ctx := context.Background()
	if a, err := p.Resolve(ctx, "inside"); err != nil || a != netip.MustParseAddr("10.0.0.5") {
		t.Errorf("inside: %v %v", a, err)
	}
	if _, err := p.Resolve(ctx, "outside"); !errors.Is(err, ErrOutOfPerimeter) {
		t.Errorf("outside: %v", err)
	}
	if _, err := p.Resolve(ctx, "10.0.1.1"); !errors.Is(err, ErrOutOfPerimeter) {
		t.Errorf("literal outside: %v", err)
	}
	if _, err := p.Resolve(ctx, "nowhere"); err == nil || errors.Is(err, ErrOutOfPerimeter) {
		t.Errorf("unresolvable: %v", err)
	}
}
