// Package perimeter decides which addresses a run may touch (FR-002). Resolve is the only way
// the collector obtains an address to dial (research R8).
package perimeter

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

var ErrOutOfPerimeter = errors.New("out of perimeter")

type Perimeter struct {
	Include []netip.Prefix
	Exclude []netip.Prefix
	// LookupNetIP resolves host names; nil means net.DefaultResolver.
	LookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Allowed applies include then exclude.
func (p Perimeter) Allowed(a netip.Addr) bool {
	a = a.Unmap()
	in := false
	for _, pr := range p.Include {
		if pr.Contains(a) {
			in = true
			break
		}
	}
	if !in {
		return false
	}
	for _, pr := range p.Exclude {
		if pr.Contains(a) {
			return false
		}
	}
	return true
}

// Resolve turns an address or host name into the address to dial, or ErrOutOfPerimeter.
// A name is refused when its first address is outside the perimeter.
func (p Perimeter) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	a, err := netip.ParseAddr(host)
	if err != nil {
		lookup := p.LookupNetIP
		if lookup == nil {
			lookup = net.DefaultResolver.LookupNetIP
		}
		addrs, lerr := lookup(ctx, "ip", host)
		if lerr != nil {
			return netip.Addr{}, lerr
		}
		if len(addrs) == 0 {
			return netip.Addr{}, &net.DNSError{Err: "no address", Name: host, IsNotFound: true}
		}
		a = addrs[0]
	}
	a = a.Unmap()
	if !p.Allowed(a) {
		return a, ErrOutOfPerimeter
	}
	return a, nil
}
