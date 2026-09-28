// Package transport defines how the collector talks to a device (docs/c4-model/04-data-model.md).
// Transports only dial addresses obtained from perimeter.Resolve.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/darnodo/NetMapper/internal/secret"
)

var (
	// ErrSilent: nothing answered on this transport.
	ErrSilent = errors.New("no answer")
	// ErrAuth: the device answered and rejected the credential. Match with errors.Is; the
	// concrete *AuthError carries the device's response as evidence.
	ErrAuth = errors.New("credential rejected")
	// ErrIdleTimeout: no byte received for Step.IdleTimeout in the middle of a step.
	ErrIdleTimeout = errors.New("idle timeout")
	// ErrDeadline: the step was still running at Step.Deadline.
	ErrDeadline = errors.New("step deadline")
)

// AuthError is ErrAuth with the bytes that prove the device answered (FR-022).
type AuthError struct{ Evidence []byte }

func (e *AuthError) Error() string        { return ErrAuth.Error() }
func (e *AuthError) Is(target error) bool { return target == ErrAuth }

type Target struct {
	Addr     netip.Addr // from perimeter.Resolve, nothing else
	Name     string
	Platform string // pack name once identified, "" while fingerprinting
}

type Credential struct {
	Set      string // credential set name, never secret
	Kind     string // ssh, snmp_v2c, snmp_v3
	Username string
	Secret   secret.Secret
	// Protocol names as written in the configuration document (sha256, aes, none...), set for
	// snmp_v3 only. Never key material.
	AuthProtocol string
	PrivProtocol string
}

// Step is one command (SSH) or one request (SNMP GET of OIDs, or a walk).
type Step struct {
	Command     string
	OIDs        []string
	Walk        string
	IdleTimeout time.Duration
	Deadline    time.Duration
}

// String is what the audit log and observation_raw.command record.
func (s Step) String() string {
	switch {
	case s.Command != "":
		return s.Command
	case s.Walk != "":
		return s.Walk
	}
	return strings.Join(s.OIDs, " ")
}

type RawOutput struct{ Bytes []byte }

type Transport interface {
	Open(ctx context.Context, target Target, cred Credential) (Session, error)
}

type Session interface {
	Run(ctx context.Context, step Step) (RawOutput, error)
	Close() error
}

// Varbind is one SNMP value. SNMP output is stored as a JSON array of them, which is the raw form
// the parser reads back.
type Varbind struct {
	OID   string `json:"oid"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

func EncodeVarbinds(v []Varbind) []byte {
	b, _ := json.Marshal(v)
	return b
}

func DecodeVarbinds(b []byte) ([]Varbind, error) {
	var v []Varbind
	return v, json.Unmarshal(b, &v)
}

// Audit writes one audit_log row before a command or request leaves (Sent) and one after
// (Result), each in its own statement, never buffered (FR-023, research R6).
type Audit interface {
	Sent(ctx context.Context, action string, target netip.Addr, command string) (Ref, error)
	Result(ctx context.Context, ref Ref, result string) error
}

type Ref struct {
	ID      string
	Action  string
	Target  netip.Addr
	Command string
}

// WithAudit wraps t so that every authentication and every step is audited. If the `sent` row
// cannot be written, nothing is sent.
func WithAudit(t Transport, a Audit) Transport { return audited{t, a} }

type audited struct {
	t Transport
	a Audit
}

func (x audited) Open(ctx context.Context, target Target, cred Credential) (Session, error) {
	// SNMP has no login exchange; its first request carries the credential and is audited there.
	if cred.Kind != "ssh" {
		s, err := x.t.Open(ctx, target, cred)
		if err != nil {
			return nil, err
		}
		return auditedSession{s, x.a, target.Addr}, nil
	}
	ref, err := x.a.Sent(ctx, "ssh.auth", target.Addr, cred.Username)
	if err != nil {
		return nil, err
	}
	s, err := x.t.Open(ctx, target, cred)
	if rerr := x.a.Result(context.WithoutCancel(ctx), ref, Result(err)); rerr != nil && err == nil {
		s.Close()
		return nil, rerr
	}
	if err != nil {
		return nil, err
	}
	return auditedSession{s, x.a, target.Addr}, nil
}

type auditedSession struct {
	s    Session
	a    Audit
	addr netip.Addr
}

func (x auditedSession) Run(ctx context.Context, step Step) (RawOutput, error) {
	action := "snmp.get"
	switch {
	case step.Command != "":
		action = "ssh.command"
	case step.Walk != "":
		action = "snmp.walk"
	}
	ref, err := x.a.Sent(ctx, action, x.addr, step.String())
	if err != nil {
		return RawOutput{}, err
	}
	out, err := x.s.Run(ctx, step)
	if rerr := x.a.Result(context.WithoutCancel(ctx), ref, Result(err)); rerr != nil && err == nil {
		return RawOutput{}, rerr
	}
	return out, err
}

func (x auditedSession) Close() error { return x.s.Close() }

// NoAnswer reports whether a dial or read error means that nothing answered at that address.
func NoAnswer(err error) bool {
	msg := err.Error()
	for _, s := range []string{"connection refused", "i/o timeout", "no route to host", "host is down", "network is unreachable", "request timeout"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Result is the audit_log.result for err. It never carries a credential: transports do not put
// secrets in errors.
func Result(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrAuth):
		return "auth_failed"
	case errors.Is(err, ErrSilent), errors.Is(err, ErrIdleTimeout), errors.Is(err, ErrDeadline):
		return "timeout"
	}
	return "error: " + err.Error()
}
