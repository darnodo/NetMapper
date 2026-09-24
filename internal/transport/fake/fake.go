// Package fake is a Transport replaying recorded output per address, for tests.
package fake

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/darnodo/NetMapper/internal/transport"
)

type Device struct {
	Transports []string          // transports that answer ("ssh", "snmp"); empty means both
	Reject     []string          // credential set names the device rejects
	Evidence   string            // what the device returns when it rejects a credential
	CLI        map[string]string // command -> output
	SNMP       map[string]string // OID -> value
	Block      map[string]bool   // command blocks until its context ends
	Stall      map[string]bool   // command stops sending: ErrIdleTimeout
	Overrun    map[string]bool   // command outlives its deadline: ErrDeadline
}

type Open struct {
	Addr      netip.Addr
	Transport string
	Set       string
}

type Network struct {
	Devices map[netip.Addr]*Device
	// OnRun, when set, is called as a step reaches the device, before it answers.
	OnRun func(addr netip.Addr, step transport.Step)

	mu       sync.Mutex
	opens    []Open
	commands []string
}

// Transport returns this network seen through one transport, "ssh" or "snmp".
func (n *Network) Transport(name string) transport.Transport { return &tr{n, name} }

func (n *Network) Opens() []Open {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.opens)
}

// Commands lists "<addr> <step>" for every step that reached a device.
func (n *Network) Commands() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.commands)
}

type tr struct {
	n    *Network
	name string
}

func (t *tr) Open(ctx context.Context, target transport.Target, cred transport.Credential) (transport.Session, error) {
	t.n.mu.Lock()
	t.n.opens = append(t.n.opens, Open{target.Addr, t.name, cred.Set})
	d := t.n.Devices[target.Addr]
	t.n.mu.Unlock()
	if d == nil || (len(d.Transports) > 0 && !slices.Contains(d.Transports, t.name)) {
		return nil, transport.ErrSilent
	}
	if slices.Contains(d.Reject, cred.Set) {
		return nil, &transport.AuthError{Evidence: []byte(d.Evidence)}
	}
	return &session{t, target.Addr, d}, nil
}

type session struct {
	t    *tr
	addr netip.Addr
	d    *Device
}

func (s *session) Run(ctx context.Context, step transport.Step) (transport.RawOutput, error) {
	n := s.t.n
	n.mu.Lock()
	n.commands = append(n.commands, s.addr.String()+" "+step.String())
	n.mu.Unlock()
	if n.OnRun != nil {
		n.OnRun(s.addr, step)
	}
	if s.t.name == "snmp" {
		return s.snmp(step), nil
	}
	switch c := step.Command; {
	case s.d.Block[c]:
		<-ctx.Done()
		return transport.RawOutput{}, ctx.Err()
	case s.d.Stall[c]:
		return transport.RawOutput{}, transport.ErrIdleTimeout
	case s.d.Overrun[c]:
		return transport.RawOutput{}, transport.ErrDeadline
	}
	return transport.RawOutput{Bytes: []byte(s.d.CLI[step.Command])}, nil
}

func (s *session) snmp(step transport.Step) transport.RawOutput {
	var vb []transport.Varbind
	if step.Walk != "" {
		var oids []string
		for oid := range s.d.SNMP {
			if strings.HasPrefix(oid, step.Walk+".") {
				oids = append(oids, oid)
			}
		}
		slices.Sort(oids)
		for _, oid := range oids {
			vb = append(vb, transport.Varbind{OID: oid, Type: "OctetString", Value: s.d.SNMP[oid]})
		}
	}
	for _, oid := range step.OIDs {
		if v, ok := s.d.SNMP[oid]; ok {
			vb = append(vb, transport.Varbind{OID: oid, Type: "OctetString", Value: v})
		} else {
			vb = append(vb, transport.Varbind{OID: oid, Type: "NoSuchObject"})
		}
	}
	return transport.RawOutput{Bytes: transport.EncodeVarbinds(vb)}
}

func (s *session) Close() error { return nil }
