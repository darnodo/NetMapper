package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestEnv(t *testing.T) {
	t.Setenv("NM_TEST_PW", "hunter2")
	t.Setenv("NM_TEST_V3", `{"auth":"a1","priv":"p1"}`)
	r := NewResolver()
	ctx := context.Background()
	s, err := r.Resolve(ctx, "env:NM_TEST_PW")
	if err != nil || s.Value() != "hunter2" {
		t.Fatalf("got %v", err)
	}
	v3, err := r.Resolve(ctx, "env:NM_TEST_V3")
	if err != nil || v3.Field("auth") != "a1" || v3.Field("priv") != "p1" {
		t.Fatalf("v3: %v", err)
	}
	if _, err := r.Resolve(ctx, "env:NM_TEST_UNSET"); !errors.Is(err, ErrUnresolved) {
		t.Errorf("unset: %v", err)
	}
	if _, err := r.Resolve(ctx, "plain"); err == nil {
		t.Error("reference without scheme resolved")
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret{map[string]string{"": "hunter2"}}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "secret", s)
	j, _ := json.Marshal(map[string]any{"s": s})
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%+v %#v %v", s, s, &s), string(j), logs.String()} {
		if strings.Contains(out, "hunter2") {
			t.Errorf("secret leaked: %s", out)
		}
	}
}
