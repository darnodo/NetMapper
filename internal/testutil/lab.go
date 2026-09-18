package testutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/collector"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/transport"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// Lab is a run environment: fresh schema and bucket, the fakeos test pack, a fake network, and
// one pool per role so tests exercise the grants.
type Lab struct {
	T        testing.TB
	DB       *pgxpool.Pool // superuser, for assertions
	Operator *pgxpool.Pool
	Engine   *pgxpool.Pool
	Col      *pgxpool.Pool
	Raw      *store.RawStore
	Registry *pack.Registry
	Net      *fake.Network
	Log      io.Writer
}

// Root is the repository root.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func NewLab(t testing.TB, net *fake.Network) *Lab {
	db := DB(t)
	raw := S3(t)
	reg, err := pack.Load(filepath.Join(Root(), "packs", "_base"), filepath.Join(Root(), "internal", "pack", "testdata", "fakeos"))
	if err != nil {
		t.Fatal(err)
	}
	return &Lab{T: t, DB: db, Operator: As(t, db, "netmapper_operator"), Engine: As(t, db, "netmapper_engine"),
		Col: As(t, db, "netmapper_collector"), Raw: raw, Registry: reg, Net: net, Log: io.Discard}
}

// Doc is a configuration for the lab network 10.0.0.0/24 (10.0.0.254 excluded) with one SNMP
// and one SSH set, whose secrets come from NM_SNMP and NM_SSH. Short bounds keep tests fast.
const Doc = `
perimeters:
  - name: lab
    include: [10.0.0.0/24]
    exclude: [10.0.0.254/32]
credential_sets:
  - {name: snmp-a, kind: snmp_v2c, secret_ref: env:NM_SNMP, max_attempts_per_device: 1, perimeters: [lab]}
  - {name: ssh-a, kind: ssh, username: nm, secret_ref: env:NM_SSH, max_attempts_per_device: 2, perimeters: [lab]}
seed_sets:
  - {name: seeds, targets: [10.0.0.1]}
discovery: {lease: 2s, poll_interval: 20ms, step_idle_timeout: 1s, step_deadline: 5s}
`

func (l *Lab) Start(doc string) int64 {
	l.T.Helper()
	id, err := jobrunner.Start(context.Background(), l.Operator, []byte(doc), "lab", "seeds", "test")
	if err != nil {
		l.T.Fatal(err)
	}
	return id
}

func (l *Lab) Collector(id string) *collector.Collector {
	return collector.New(l.Col, id, l.Registry, l.Raw, secret.NewResolver(),
		map[string]transport.Transport{"ssh": l.Net.Transport("ssh"), "snmp": l.Net.Transport("snmp")},
		slog.New(slog.NewJSONHandler(l.Log, nil)))
}

// Go runs f until the returned stop is called, which waits for it to return.
func Go(f func(ctx context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); f(ctx) }()
	return func() { cancel(); wg.Wait() }
}

// RunCollector runs a collector until stop.
func (l *Lab) RunCollector(c *collector.Collector) (stop func()) {
	return Go(func(ctx context.Context) { c.Run(ctx) })
}

// RunEngine runs the job runner until stop.
func (l *Lab) RunEngine() (stop func()) {
	return Go(func(ctx context.Context) {
		jobrunner.Run(ctx, l.Engine, 20*time.Millisecond, slog.New(slog.NewJSONHandler(l.Log, nil)))
	})
}

// Wait polls the job until its state is one of states.
func (l *Lab) Wait(job int64, states ...string) string {
	l.T.Helper()
	var s string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		l.DB.QueryRow(context.Background(), `SELECT state FROM job WHERE id = $1`, job).Scan(&s)
		if slices.Contains(states, s) {
			return s
		}
	}
	l.T.Fatalf("job %d still %s", job, s)
	return s
}

// Crawl starts doc, runs one collector and the engine until the job ends, and returns the job.
func (l *Lab) Crawl(doc string) int64 {
	l.T.Helper()
	job := l.Start(doc)
	stopC := l.RunCollector(l.Collector("c1"))
	stopE := l.RunEngine()
	l.Wait(job, "succeeded", "failed", "cancelled")
	stopE()
	stopC()
	return job
}

// Strings runs a query returning one text column per row.
func (l *Lab) Strings(sql string, args ...any) []string {
	l.T.Helper()
	rows, err := l.DB.Query(context.Background(), sql, args...)
	if err != nil {
		l.T.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			l.T.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// Int runs a query returning one integer.
func (l *Lab) Int(sql string, args ...any) int {
	l.T.Helper()
	var n int
	if err := l.DB.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		l.T.Fatal(err)
	}
	return n
}

// Outcomes lists "<target> <family> <status>[ <detail>]" for every observation of job, sorted.
func (l *Lab) Outcomes(job int64) []string {
	return l.Strings(`
		SELECT host(o.target) || ' ' || o.fact_family || ' ' || o.status || coalesce(' ' || o.detail, '')
		FROM observation o JOIN job j ON j.snapshot_id = o.snapshot_id WHERE j.id = $1 ORDER BY 1`, job)
}

// FakeOS is a device of the fakeos test pack. Neighbours are "<local port> <name> <address>".
func FakeOS(name, serial string, neighbours ...string) *fake.Device {
	mac := fmt.Sprintf("aa:bb:cc:00:00:%s", serial[len(serial)-2:])
	var nb strings.Builder
	for _, n := range neighbours {
		f := strings.Fields(n)
		fmt.Fprintf(&nb, "%s %s %s %s\n", f[0], f[1], f[2], "aa:bb:cc:ff:ff:ff")
	}
	return &fake.Device{
		CLI: map[string]string{
			"display version":    fmt.Sprintf("FakeOS 1.2\nHostname: %s\nSerial: %s\nMAC: %s\n", name, serial, mac),
			"display neighbours": nb.String(),
			"display interfaces": "p1 up up 1500 " + mac + " uplink\np2 down down 1500 " + mac + "\n",
		},
		SNMP: map[string]string{
			"1.3.6.1.2.1.1.2.0":       "1.3.6.1.4.1.99999.1",
			"1.3.6.1.2.1.1.1.0":       "FakeOS 1.2 on " + name,
			"1.3.6.1.2.1.1.5.0":       name,
			"1.3.6.1.4.1.99999.2.1.0": serial,
		},
	}
}

// Addr parses an address.
func Addr(s string) netip.Addr { return netip.MustParseAddr(s) }
