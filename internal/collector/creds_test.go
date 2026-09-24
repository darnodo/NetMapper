package collector_test

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// Three covering sets, in this order.
const credDoc = `
perimeters: [{name: lab, include: [10.0.0.0/24]}]
credential_sets:
  - {name: snmp-a, kind: snmp_v2c, secret_ref: env:NM_SNMP, max_attempts_per_device: 1, perimeters: [lab]}
  - {name: ssh-a, kind: ssh, username: nm, secret_ref: env:NM_SSH, max_attempts_per_device: 2, perimeters: [lab]}
  - {name: ssh-b, kind: ssh, username: nm2, secret_ref: env:NM_SSH_B, max_attempts_per_device: 1, perimeters: [lab]}
seed_sets: [{name: seeds, targets: [10.0.0.1]}]
discovery: {lease: 2s, poll_interval: 20ms, step_idle_timeout: 1s, step_deadline: 5s}
`

// credLab runs one device at 10.0.0.1 that answers SSH only, after preset writes the seed
// task's cred_attempts (a JSON object keyed by set name, rewritten to set ids).
func credLab(t *testing.T, dev *fake.Device, preset string) (*Lab, int64) {
	dev.Transports = []string{"ssh"}
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): dev}})
	job := l.Start(credDoc)
	if preset != "" {
		for _, name := range []string{"snmp-a", "ssh-a", "ssh-b"} {
			id := l.Strings(`SELECT id::text FROM credential_set WHERE name = $1`, name)[0]
			preset = strings.ReplaceAll(preset, `"`+name+`"`, `"`+id+`"`)
		}
		if _, err := l.DB.Exec(context.Background(), `UPDATE task SET cred_attempts = $1 WHERE job_id = $2`, preset, job); err != nil {
			t.Fatal(err)
		}
	}
	stopC, stopE := l.RunCollector(l.Collector("c1")), l.RunEngine()
	l.Wait(job, "succeeded")
	stopE()
	stopC()
	return l, job
}

func sshOpens(l *Lab) string {
	var sets []string
	for _, o := range l.Net.Opens() {
		if o.Transport == "ssh" {
			sets = append(sets, o.Set)
		}
	}
	return strings.Join(sets, ",")
}

func lastError(l *Lab, job int64) string {
	return l.Strings(`SELECT coalesce(last_error, '') FROM task WHERE job_id = $1 AND kind = 'find'`, job)[0]
}

func TestCredentials(t *testing.T) {
	t.Run("document order", func(t *testing.T) {
		env(t)
		t.Setenv("NM_SSH_B", "b-secret")
		dev := FakeOS("sw1", "S001")
		dev.Reject = []string{"ssh-a"}
		l, job := credLab(t, dev, "")
		// The find tries ssh-a then ssh-b; the scrape goes straight to the set that worked.
		if got := sshOpens(l); got != "ssh-a,ssh-b,ssh-b" {
			t.Errorf("ssh sets tried: %s", got)
		}
		if got := l.Outcomes(job); got[0] != "10.0.0.1 identity collected" {
			t.Errorf("%v", got)
		}
	})

	t.Run("budget kept across a reclaim", func(t *testing.T) {
		env(t)
		t.Setenv("NM_SSH_B", "b-secret")
		dev := FakeOS("sw1", "S001")
		l, _ := credLab(t, dev, `{"ssh-a": {"n": 2, "ok": false}}`)
		if got := sshOpens(l); got != "ssh-b,ssh-b" {
			t.Errorf("a set past its budget was tried again: %s", got)
		}
		// ssh-b has a budget of 1 on this device: the scrape reuses it without spending more.
		if got := l.Strings(`SELECT max((cred_attempts -> (SELECT id::text FROM credential_set WHERE name = 'ssh-b') ->> 'n')::int)::text FROM task`)[0]; got != "1" {
			t.Errorf("ssh-b attempts %s", got)
		}
	})

	t.Run("working set retried after a crash", func(t *testing.T) {
		env(t)
		t.Setenv("NM_SSH_B", "b-secret")
		l, job := credLab(t, FakeOS("sw1", "S001"), `{"ssh-a": {"n": 2, "ok": true}}`)
		if got := sshOpens(l); !strings.HasPrefix(got, "ssh-a") || strings.Contains(got, "ssh-b") {
			t.Errorf("ssh sets tried: %s", got)
		}
		if got := l.Outcomes(job); got[0] != "10.0.0.1 identity collected" {
			t.Errorf("%v", got)
		}
		if n := l.Int(`SELECT (cred_attempts -> (SELECT id::text FROM credential_set WHERE name = 'ssh-a') ->> 'n')::int FROM task WHERE kind = 'find'`); n != 2 {
			t.Errorf("working set spent budget: n = %d", n)
		}
	})

	t.Run("every set rejected is denied", func(t *testing.T) {
		env(t)
		t.Setenv("NM_SSH_B", "b-secret")
		dev := FakeOS("sw1", "S001")
		dev.Reject, dev.Evidence = []string{"ssh-a", "ssh-b"}, "Permission denied"
		l, job := credLab(t, dev, "")
		if got := l.Outcomes(job); len(got) != 1 || got[0] != "10.0.0.1 identity denied" {
			t.Errorf("%v", got)
		}
		// ssh-a has 2 attempts; a denied device is done, so it is shown once per run anyway.
		if got := sshOpens(l); got != "ssh-a,ssh-b" {
			t.Errorf("ssh sets tried: %s", got)
		}
	})

	t.Run("no set resolved", func(t *testing.T) {
		t.Setenv("NM_SNMP", "")
		t.Setenv("NM_SSH", "")
		t.Setenv("NM_SSH_B", "")
		l, job := credLab(t, FakeOS("sw1", "S001"), "")
		if le := lastError(l, job); le != "credential_unresolved: snmp-a, ssh-a, ssh-b" {
			t.Errorf("last_error %q", le)
		}
		if n := len(l.Outcomes(job)); n != 0 {
			t.Errorf("%d observations", n)
		}
		if n := len(l.Net.Opens()); n != 0 {
			t.Errorf("%d sessions opened with nothing to present", n)
		}
	})

	t.Run("rejected and unresolved is partial", func(t *testing.T) {
		env(t)
		t.Setenv("NM_SSH_B", "")
		dev := FakeOS("sw1", "S001")
		dev.Reject = []string{"ssh-a"}
		l, job := credLab(t, dev, "")
		if le := lastError(l, job); le != "credential_partial: rejected ssh-a; unresolved ssh-b" {
			t.Errorf("last_error %q", le)
		}
		if n := len(l.Outcomes(job)); n != 0 {
			t.Errorf("%d observations", n)
		}
		if n := l.Int(`SELECT count(*) FROM finding`); n != 0 {
			t.Errorf("%d findings", n)
		}
		if n := l.Int(`SELECT count(*) FROM audit_log WHERE action = 'ssh.auth' AND command = 'nm' AND result = 'auth_failed' AND host(target) = '10.0.0.1'`); n == 0 {
			t.Error("rejection of ssh-a not in the audit log")
		}
		// First attempt plus the final pass: ssh-a is shown twice, its budget, and never more.
		if got := sshOpens(l); got != "ssh-a,ssh-a" {
			t.Errorf("ssh sets tried: %s", got)
		}
		if got := l.Strings(`SELECT cred_attempts::text FROM task WHERE kind = 'find'`)[0]; strings.Contains(got, fmt.Sprintf(`"%s"`, l.Strings(`SELECT id::text FROM credential_set WHERE name = 'ssh-b'`)[0])) {
			t.Errorf("unresolved set counted an attempt: %s", got)
		}
	})
}
