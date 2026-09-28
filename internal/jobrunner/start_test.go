package jobrunner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/testutil"
)

const doc = `
perimeters:
  - name: lab
    include: [10.0.0.0/24]
    exclude: [10.0.0.1/32]
  - name: bare
    include: [10.9.0.0/24]
credential_sets:
  - name: ro-ssh
    kind: ssh
    username: netmapper
    secret_ref: env:LAB_SSH_PASSWORD
    max_attempts_per_device: 2
    perimeters: [lab]
seed_sets:
  - name: good
    targets: [10.0.0.2, 10.0.0.3]
  - name: outside
    targets: [10.99.0.1]
  - name: excluded
    targets: [10.0.0.1]
  - name: unresolvable
    targets: [does-not-resolve.invalid]
  - name: bare-seed
    targets: [10.9.0.2]
`

func TestStart(t *testing.T) {
	db := testutil.DB(t)
	op := testutil.As(t, db, "netmapper_operator")
	ctx := context.Background()

	for _, c := range []struct{ name, doc, perimeter, seeds, want string }{
		{"missing perimeter", doc, "nope", "good", `perimeter "nope" not found`},
		{"empty include", strings.Replace(doc, "include: [10.0.0.0/24]", "include: []", 1), "lab", "good", `perimeter "lab" has no include range`},
		{"seed outside", doc, "lab", "outside", `seed "10.99.0.1" is outside perimeter "lab"`},
		{"seed excluded", doc, "lab", "excluded", `seed "10.0.0.1" is outside perimeter "lab"`},
		{"unresolvable seed", doc, "lab", "unresolvable", `seed "does-not-resolve.invalid" does not resolve`},
		{"no covering set", doc, "bare", "bare-seed", `no credential set covers perimeter "bare"`},
		{"literal secret", strings.Replace(doc, "env:LAB_SSH_PASSWORD", "hunter2", 1), "lab", "good", "secret_ref must be a reference, not a value"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := jobrunner.Start(ctx, op, []byte(c.doc), c.perimeter, c.seeds, "test")
			inv, ok := err.(jobrunner.Invalid)
			if !ok || !strings.Contains(inv.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			for _, line := range inv {
				if strings.Contains(line, "\n") {
					t.Errorf("problem spans lines: %q", line)
				}
			}
		})
	}
	var jobs int
	db.QueryRow(ctx, `SELECT count(*) FROM job`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("%d jobs inserted by refused runs", jobs)
	}

	id, err := jobrunner.Start(ctx, op, []byte(doc), "lab", "good", "test")
	if err != nil {
		t.Fatal(err)
	}
	var state, stored, snapState string
	var finds int
	db.QueryRow(ctx, `SELECT j.state, c.document, s.state FROM job j JOIN config_version c ON c.id = j.config_version
		JOIN snapshot s ON s.id = j.snapshot_id WHERE j.id = $1`, id).Scan(&state, &stored, &snapState)
	db.QueryRow(ctx, `SELECT count(*) FROM task WHERE job_id = $1 AND kind = 'find' AND state = 'pending'`, id).Scan(&finds)
	if state != "running" || stored != doc || snapState != "open" || finds != 2 {
		t.Errorf("job %s, snapshot %s, %d finds, document kept: %v", state, snapState, finds, stored == doc)
	}

	if err := jobrunner.Cancel(ctx, op, id); err != nil {
		t.Fatal(err)
	}
	if err := jobrunner.Cancel(ctx, op, id); err != jobrunner.ErrNotRunning {
		t.Errorf("second cancel: %v", err)
	}
}

// 006 research R3: the protocols are stored with the set, defaults applied, and only for snmp_v3.
func TestStartStoresSNMPv3Protocols(t *testing.T) {
	db := testutil.DB(t)
	op := testutil.As(t, db, "netmapper_operator")
	ctx := context.Background()

	v3 := strings.Replace(doc, "credential_sets:\n", `credential_sets:
  - {name: v3-named, kind: snmp_v3, username: nm, auth_protocol: sha256, priv_protocol: aes256, secret_ref: env:V3, max_attempts_per_device: 1, perimeters: [lab]}
  - {name: v3-default, kind: snmp_v3, username: nm, secret_ref: env:V3, max_attempts_per_device: 1, perimeters: [lab]}
`, 1)
	if _, err := jobrunner.Start(ctx, op, []byte(v3), "lab", "good", "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT name, coalesce(auth_protocol, 'NULL'), coalesce(priv_protocol, 'NULL') FROM credential_set ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var n, a, p string
		rows.Scan(&n, &a, &p)
		got = append(got, n+" "+a+"/"+p)
	}
	want := "v3-named sha256/aes256, v3-default sha/aes, ro-ssh NULL/NULL"
	if strings.Join(got, ", ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, ", "), want)
	}
}
