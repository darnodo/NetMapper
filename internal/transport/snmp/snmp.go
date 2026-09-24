// Package snmp is the SNMP transport: v2c and v3, GET and GETBULK walks. There is no SET here and
// there must never be one (principle IV). OIDs come only from the pack registry.
package snmp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"

	"github.com/darnodo/NetMapper/internal/transport"
)

// Transport settings for silence detection, not a device bound: a request with no reply after
// Timeout and Retries means the device is silent on SNMP.
type Transport struct {
	Port    uint16
	Timeout time.Duration
	Retries int
}

func New() *Transport { return &Transport{Port: 161, Timeout: 2 * time.Second, Retries: 1} }

func (t *Transport) Open(ctx context.Context, target transport.Target, cred transport.Credential) (transport.Session, error) {
	g := &gosnmp.GoSNMP{
		Target:             target.Addr.String(),
		Port:               t.Port,
		Transport:          "udp",
		Timeout:            t.Timeout,
		Retries:            t.Retries,
		MaxRepetitions:     20,
		ExponentialTimeout: false,
	}
	switch cred.Kind {
	case "snmp_v2c":
		g.Version = gosnmp.Version2c
		g.Community = cred.Secret.Value()
	case "snmp_v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cred.Username,
			AuthenticationProtocol:   gosnmp.SHA,
			AuthenticationPassphrase: cred.Secret.Field("auth"),
			PrivacyProtocol:          gosnmp.AES,
			PrivacyPassphrase:        cred.Secret.Field("priv"),
		}
	default:
		return nil, fmt.Errorf("snmp transport cannot use a %s credential", cred.Kind)
	}
	if err := g.Connect(); err != nil { // UDP: no packet leaves here
		return nil, err
	}
	return &session{g}, nil
}

type session struct{ g *gosnmp.GoSNMP }

func (s *session) Run(ctx context.Context, step transport.Step) (transport.RawOutput, error) {
	if step.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.Deadline)
		defer cancel()
	}
	s.g.Context = ctx
	var pdus []gosnmp.SnmpPDU
	var err error
	if step.Walk != "" {
		err = s.g.BulkWalk(step.Walk, func(p gosnmp.SnmpPDU) error {
			pdus = append(pdus, p)
			return nil
		})
	} else {
		var pkt *gosnmp.SnmpPacket
		if pkt, err = s.g.Get(step.OIDs); err == nil {
			pdus = pkt.Variables
		}
	}
	if err != nil {
		return transport.RawOutput{}, classify(ctx, err, len(pdus) > 0)
	}
	vb := make([]transport.Varbind, 0, len(pdus))
	for _, p := range pdus {
		vb = append(vb, varbind(p))
	}
	return transport.RawOutput{Bytes: transport.EncodeVarbinds(vb)}, nil
}

func (s *session) Close() error {
	if s.g.Conn == nil {
		return nil
	}
	return s.g.Conn.Close()
}

func classify(ctx context.Context, err error, partial bool) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return transport.ErrDeadline
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, gosnmp.ErrUnknownUsername), errors.Is(err, gosnmp.ErrWrongDigest),
		errors.Is(err, gosnmp.ErrDecryption), errors.Is(err, gosnmp.ErrUnknownSecurityLevel):
		return &transport.AuthError{Evidence: []byte(err.Error())}
	case transport.NoAnswer(err):
		if partial {
			return transport.ErrIdleTimeout
		}
		return transport.ErrSilent
	}
	return err
}

func varbind(p gosnmp.SnmpPDU) transport.Varbind {
	v := transport.Varbind{OID: strings.TrimPrefix(p.Name, "."), Type: p.Type.String()}
	switch x := p.Value.(type) {
	case []byte:
		if utf8.Valid(x) && strings.IndexFunc(string(x), func(r rune) bool { return !unicode.IsPrint(r) && !unicode.IsSpace(r) }) < 0 {
			v.Value = string(x)
		} else {
			v.Value = hex.EncodeToString(x)
		}
	case string:
		v.Value = strings.TrimPrefix(x, ".")
	case nil:
	default:
		v.Value = fmt.Sprint(x)
	}
	return v
}
