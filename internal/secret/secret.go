// Package secret resolves credential references at collection time (FR-017). Values live in
// memory for the time of a session and are never formatted, logged or serialised.
package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// ErrUnresolved means the reference points at nothing. The caller moves to the next set.
var ErrUnresolved = errors.New("secret reference resolved to nothing")

const redacted = "[redacted]"

// Secret holds named fields. A single-value reference has one field named "".
type Secret struct{ fields map[string]string }

func (s Secret) Value() string            { return s.fields[""] }
func (s Secret) Field(name string) string { return s.fields[name] }

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }

type SecretBackend interface {
	Resolve(ctx context.Context, ref string) (Secret, error)
}

// Resolver dispatches on the reference scheme. The vault backend is built on first use, so a
// collector with env references only needs no VAULT_* variables.
type Resolver struct {
	Env   SecretBackend
	Vault SecretBackend

	once sync.Once
	err  error
}

func NewResolver() *Resolver { return &Resolver{Env: Env{}} }

func (r *Resolver) Resolve(ctx context.Context, ref string) (Secret, error) {
	scheme, rest, _ := strings.Cut(ref, ":")
	switch scheme {
	case "env":
		return r.Env.Resolve(ctx, rest)
	case "vault":
		r.once.Do(func() {
			if r.Vault == nil {
				r.Vault, r.err = NewVault()
			}
		})
		if r.err != nil {
			return Secret{}, r.err
		}
		return r.Vault.Resolve(ctx, rest)
	}
	return Secret{}, fmt.Errorf("unknown secret scheme %q", scheme)
}

// Env reads `env:NAME` from the collector's environment. A value that is a JSON object gives
// named fields, e.g. {"auth": "...", "priv": "..."} for SNMP v3.
type Env struct{}

func (Env) Resolve(_ context.Context, name string) (Secret, error) {
	v := os.Getenv(name)
	if v == "" {
		return Secret{}, ErrUnresolved
	}
	fields := map[string]string{}
	if strings.HasPrefix(v, "{") && json.Unmarshal([]byte(v), &fields) == nil {
		return Secret{fields}, nil
	}
	return Secret{map[string]string{"": v}}, nil
}
