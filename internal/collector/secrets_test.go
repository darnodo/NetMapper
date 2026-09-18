package collector_test

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// FR-017, SC-007: after a run that logged in, was refused and resolved references, no secret
// value is in any text or jsonb column of any table, nor in the logs.
func TestNoSecretStored(t *testing.T) {
	secrets := []string{"snmp-c0mmunity-X7", "ssh-pa55word-Y9"}
	t.Setenv("NM_SNMP", secrets[0])
	t.Setenv("NM_SSH", secrets[1])
	denied := FakeOS("sw2", "S002")
	denied.Transports, denied.Reject, denied.Evidence = []string{"ssh"}, []string{"ssh-a"}, "Permission denied"
	var logs bytes.Buffer
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2"),
		Addr("10.0.0.2"): denied,
	}})
	l.Log = &logs
	job := l.Crawl(Doc)
	if got := l.Outcomes(job); len(got) != 4 {
		t.Fatalf("outcomes %v", got)
	}

	ctx := context.Background()
	rows, err := l.DB.Query(ctx, `
		SELECT c.table_name, c.column_name FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE'
		  AND c.data_type IN ('text', 'jsonb', 'ARRAY', 'USER-DEFINED', 'character varying')`)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct{ Table, Column string }])
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) < 20 {
		t.Fatalf("only %d columns searched", len(cols))
	}
	for _, c := range cols {
		for _, s := range secrets {
			q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s::text LIKE '%%' || $1 || '%%'`, pgx.Identifier{c.Table}.Sanitize(), pgx.Identifier{c.Column}.Sanitize())
			if n := l.Int(q, s); n != 0 {
				t.Errorf("%s.%s holds a secret value", c.Table, c.Column)
			}
		}
	}
	for _, s := range secrets {
		if strings.Contains(logs.String(), s) {
			t.Error("a secret value reached the logs")
		}
	}
}
