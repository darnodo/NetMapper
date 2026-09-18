package secret

import (
	"context"
	"strings"

	vault "github.com/hashicorp/vault/api"
)

// Vault reads KV v2 references `vault:<kv v2 path>#<field>` (Vault or OpenBao), using
// VAULT_ADDR and VAULT_TOKEN. Without #field every string field of the secret is returned.
type Vault struct{ c *vault.Client }

func NewVault() (*Vault, error) {
	c, err := vault.NewClient(vault.DefaultConfig()) // reads VAULT_ADDR, VAULT_TOKEN
	if err != nil {
		return nil, err
	}
	return &Vault{c}, nil
}

func (v *Vault) Resolve(ctx context.Context, ref string) (Secret, error) {
	path, field, _ := strings.Cut(ref, "#")
	s, err := v.c.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return Secret{}, err
	}
	if s == nil {
		return Secret{}, ErrUnresolved
	}
	data, _ := s.Data["data"].(map[string]any)
	fields := map[string]string{}
	for k, x := range data {
		if str, ok := x.(string); ok {
			fields[k] = str
		}
	}
	if field != "" {
		if fields[field] == "" {
			return Secret{}, ErrUnresolved
		}
		fields = map[string]string{"": fields[field]}
	}
	if len(fields) == 0 {
		return Secret{}, ErrUnresolved
	}
	return Secret{fields}, nil
}
