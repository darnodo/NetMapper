package snmp

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/transport"
)

// The agent is deploy/snmpd (research R7): one SNMPv3 user per protocol combination, passphrases
// auth-<user> and priv-<user>, sysName nm-snmpd.
func agent(t *testing.T) (*Transport, netip.Addr) {
	t.Helper()
	v := os.Getenv("NETMAPPER_TEST_SNMP")
	if v == "" {
		t.Skip("NETMAPPER_TEST_SNMP not set")
	}
	ap, err := netip.ParseAddrPort(v)
	if err != nil {
		t.Fatalf("NETMAPPER_TEST_SNMP=%q: %v", v, err)
	}
	tr := New()
	tr.Port, tr.Timeout = ap.Port(), time.Second
	return tr, ap.Addr()
}

// get reads sysName.0 as user, with the secret holding fields (resolved through env:, as the
// collector does).
func get(t *testing.T, user, auth, priv string, fields map[string]string) (transport.RawOutput, error) {
	t.Helper()
	tr, addr := agent(t)
	b, _ := json.Marshal(fields)
	t.Setenv("NM_TEST_SNMP_V3", string(b))
	sec, err := secret.NewResolver().Resolve(context.Background(), "env:NM_TEST_SNMP_V3")
	if err != nil {
		t.Fatal(err)
	}
	s, err := tr.Open(context.Background(), transport.Target{Addr: addr},
		transport.Credential{Set: "v3", Kind: "snmp_v3", Username: user, AuthProtocol: auth, PrivProtocol: priv, Secret: sec})
	if err != nil {
		return transport.RawOutput{}, err
	}
	defer s.Close()
	return s.Run(context.Background(), transport.Step{OIDs: []string{"1.3.6.1.2.1.1.5.0"}})
}

func both(user string) map[string]string {
	return map[string]string{"auth": "auth-" + user, "priv": "priv-" + user}
}

// FR-001, FR-002, FR-007, FR-009: the protocols named on the set are the ones used on the wire, and
// empty protocols mean SHA-1 / AES-128, which is what the transport sent before they were chosen.
func TestV3Protocols(t *testing.T) {
	for _, c := range []struct{ name, user, auth, priv string }{
		{"defaults", "nm-sha-aes", "", ""},
		{"sha/aes", "nm-sha-aes", "sha", "aes"},
		{"sha256/aes", "nm-sha256-aes", "sha256", "aes"},
		{"sha512/aes256", "nm-sha512-aes256", "sha512", "aes256"},
		{"sha/aes256c", "nm-sha-aes256c", "sha", "aes256c"},
		{"sha256/none", "nm-sha256-nopriv", "sha256", "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := get(t, c.user, c.auth, c.priv, both(c.user))
			if err != nil {
				t.Fatal(err)
			}
			vb, err := transport.DecodeVarbinds(out.Bytes)
			if err != nil || len(vb) != 1 || vb[0].Value != "nm-snmpd" {
				t.Errorf("got %s, %v", out.Bytes, err)
			}
		})
	}
}

// 006 US2 and edge cases: authNoPriv needs only auth; a priv field on an authNoPriv set, or fields
// beyond auth and priv, are ignored.
func TestV3SecretFields(t *testing.T) {
	for _, c := range []struct {
		name, user, priv string
		fields           map[string]string
	}{
		{"authNoPriv, auth only", "nm-sha256-nopriv", "none", map[string]string{"auth": "auth-nm-sha256-nopriv"}},
		{"authNoPriv, priv ignored", "nm-sha256-nopriv", "none", map[string]string{"auth": "auth-nm-sha256-nopriv", "priv": "unused"}},
		{"extra field ignored", "nm-sha256-aes", "aes", map[string]string{"auth": "auth-nm-sha256-aes", "priv": "priv-nm-sha256-aes", "note": "x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := get(t, c.user, "sha256", c.priv, c.fields); err != nil {
				t.Error(err)
			}
		})
	}
}

// 006 FR-008: a device that answers with an SNMPv3 error report refuses the credential. gosnmp
// cannot read the report's reason (research R6), so both cases carry the same evidence.
func TestV3Refused(t *testing.T) {
	for _, c := range []struct{ name, user, auth, priv string }{
		{"protocol mismatch", "nm-sha256-aes", "sha", "aes"},
		{"unknown user", "nm-nobody", "sha", "aes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := get(t, c.user, c.auth, c.priv, both("nm-sha256-aes"))
			var ae *transport.AuthError
			if !errors.As(err, &ae) || len(ae.Evidence) == 0 {
				t.Fatalf("got %v, want an AuthError with evidence", err)
			}
		})
	}
}

func TestV3UnknownProtocol(t *testing.T) {
	if _, err := New().Open(context.Background(), transport.Target{Addr: netip.MustParseAddr("127.0.0.1")},
		transport.Credential{Kind: "snmp_v3", Username: "u", AuthProtocol: "sha3"}); err == nil {
		t.Error("unknown auth protocol accepted")
	}
}
