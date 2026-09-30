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
		auth, err := authProtocol(cred.AuthProtocol)
		if err != nil {
			return nil, err
		}
		priv, err := privProtocol(cred.PrivProtocol)
		if err != nil {
			return nil, err
		}
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		usm := &gosnmp.UsmSecurityParameters{
			UserName:                 cred.Username,
			AuthenticationProtocol:   auth,
			AuthenticationPassphrase: cred.Secret.Field("auth"),
			PrivacyProtocol:          priv,
		}
		if priv == gosnmp.NoPriv {
			g.MsgFlags = gosnmp.AuthNoPriv // a priv field in the secret is not sent
		} else {
			usm.PrivacyPassphrase = cred.Secret.Field("priv")
		}
		g.SecurityParameters = usm
	default:
		return nil, fmt.Errorf("snmp transport cannot use a %s credential", cred.Kind)
	}
	if err := g.Connect(); err != nil { // UDP: no packet leaves here
		return nil, err
	}
	return &session{g}, nil
}

// authProtocol maps a protocol name from the configuration document (contracts/config.md) to
// gosnmp's constant. Empty means sha, the default, so a transport used without the collector
// behaves as before protocols could be chosen. Only the USM parameters change here: requests stay
// GET and GETBULK (principle IV).
func authProtocol(name string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch name {
	case "", "sha":
		return gosnmp.SHA, nil
	case "md5":
		return gosnmp.MD5, nil
	case "sha224":
		return gosnmp.SHA224, nil
	case "sha256":
		return gosnmp.SHA256, nil
	case "sha384":
		return gosnmp.SHA384, nil
	case "sha512":
		return gosnmp.SHA512, nil
	}
	return 0, fmt.Errorf("unknown snmp_v3 auth protocol %q", name)
}

// privProtocol does the same for privacy; empty means aes (AES-128), none means authNoPriv.
func privProtocol(name string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch name {
	case "", "aes":
		return gosnmp.AES, nil
	case "none":
		return gosnmp.NoPriv, nil
	case "des":
		return gosnmp.DES, nil
	case "aes192":
		return gosnmp.AES192, nil
	case "aes256":
		return gosnmp.AES256, nil
	case "aes192c":
		return gosnmp.AES192C, nil
	case "aes256c":
		return gosnmp.AES256C, nil
	}
	return 0, fmt.Errorf("unknown snmp_v3 privacy protocol %q", name)
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
		errors.Is(err, gosnmp.ErrDecryption), errors.Is(err, gosnmp.ErrUnknownSecurityLevel),
		// An agent reports a wrong digest or an unknown user without authenticating the report, and
		// gosnmp checks every v3 answer against our key before reading it, so both arrive as this
		// message and never as the errors above. It has no sentinel. The device answered, and not
		// to this credential (006 research R6).
		strings.Contains(err.Error(), "incoming packet is not authentic"):
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
