package collector_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// FR-023: every command a device receives has an audit row written before it arrives, and a
// result row with the same ref after. A worker stopped while a command blocks leaves its `sent`
// row in place.
func TestAuditBeforeSend(t *testing.T) {
	env(t)
	sw2 := FakeOS("sw2", "S002")
	sw2.Block = map[string]bool{"display interfaces": true}
	n := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2"),
		Addr("10.0.0.2"): sw2,
	}}
	l := NewLab(t, n)
	var mu sync.Mutex
	var unaudited []string
	n.OnRun = func(addr netip.Addr, step transport.Step) {
		var sent int
		l.DB.QueryRow(context.Background(), `SELECT count(*) FROM audit_log s WHERE s.target = $1 AND s.command = $2 AND s.result = 'sent'
			AND NOT EXISTS (SELECT 1 FROM audit_log r WHERE r.ref = s.ref AND r.result <> 'sent')`, addr, step.String()).Scan(&sent)
		if sent != 1 {
			mu.Lock()
			unaudited = append(unaudited, addr.String()+" "+step.String())
			mu.Unlock()
		}
	}
	l.Start(shortLease)
	stop := l.RunCollector(l.Collector("c1"))
	waitCommand(t, n, "10.0.0.2 display interfaces")
	stop()

	if len(unaudited) > 0 {
		t.Errorf("reached the device with no pending sent row: %v", unaudited)
	}
	if got, want := l.Int(`SELECT count(*) FROM audit_log WHERE result = 'sent' AND action IN ('ssh.command', 'snmp.get', 'snmp.walk')`), len(n.Commands()); got != want {
		t.Errorf("%d sent rows for %d commands", got, want)
	}
	if n := l.Int(`SELECT count(*) FROM audit_log WHERE result = 'sent' AND host(target) = '10.0.0.2' AND command = 'display interfaces' AND actor = 'collector:c1'`); n != 1 {
		t.Error("the blocked command's sent row is missing")
	}
	// Every other command has its result under the same ref.
	if n := l.Int(`SELECT count(*) FROM audit_log s WHERE s.result = 'sent' AND s.command <> 'display interfaces'
		AND NOT EXISTS (SELECT 1 FROM audit_log r WHERE r.ref = s.ref AND r.result <> 'sent')`); n != 0 {
		t.Errorf("%d commands without a result row", n)
	}
}
