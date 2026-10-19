package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"

	"github.com/darnodo/NetMapper/internal/gate"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/pack"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// evictLab is two cabled switches on the lab network, sw1 printing VLAN data so a projection writes
// an L2 domain, with the packs of projectLab. The engine is not running: each test crawls what it
// needs with l.Crawl. NETMAPPER_DSN points at the schema as netmapper_operator, the role
// contracts/cli.md assigns `netmapper snapshot` (constitution: a path is tested under its role), and
// NETMAPPER_S3_* at the lab's bucket.
func evictLab(t *testing.T) (*Lab, *fake.Network) {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	net := &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): vlanSwitch(FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002"))),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}}
	l := NewLab(t, net)
	reg, err := pack.LoadRoot(packDir(t))
	if err != nil {
		t.Fatal(err)
	}
	l.Registry = reg

	schema := l.DB.Config().ConnConfig.RuntimeParams["search_path"]
	dsn := os.Getenv("NETMAPPER_TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	t.Setenv("NETMAPPER_DSN", dsn+sep+"options=-c%20search_path%3D"+schema+"%20-c%20role%3Dnetmapper_operator")
	t.Setenv("NETMAPPER_S3_ENDPOINT", os.Getenv("NETMAPPER_TEST_S3_ENDPOINT"))
	t.Setenv("NETMAPPER_S3_BUCKET", l.Raw.Bucket)
	t.Setenv("NETMAPPER_S3_ACCESS_KEY", envOr("NETMAPPER_TEST_S3_ACCESS_KEY", "GK0123456789abcdef01234567"))
	t.Setenv("NETMAPPER_S3_SECRET_KEY", envOr("NETMAPPER_TEST_S3_SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	t.Setenv("NETMAPPER_S3_REGION", envOr("NETMAPPER_TEST_S3_REGION", "garage"))
	t.Setenv("NETMAPPER_S3_INSECURE", "1")
	return l, net
}

func vlanSwitch(d *fake.Device) *fake.Device {
	d.CLI["display vlans"] = "10 active\n"
	d.CLI["display switchport"] = "p1 trunk 1 10\n"
	return d
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// snapshotCmd runs `netmapper snapshot args...` and returns its exit code and what it printed.
func snapshotCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var code int
	out := stdout(t, func() { code = cmdSnapshot(context.Background(), args) })
	return code, out
}

// crawl runs one crawl and returns the id of the snapshot it closed.
func crawl(l *Lab) int {
	job := l.Crawl(Doc)
	return l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job)
}

// perSnapshot is every table holding rows of one snapshot (data-model.md, "Rows removed by an
// eviction"); l2domain_link has no snapshot column and goes with its domain.
var perSnapshot = []string{"observation", "observation_raw", "identifier_claim", "parse_generation",
	"finding", "finding_evidence", "resolution", "entity", "entity_claim", "projection", "interface",
	"interface_alias", "interface_evidence", "edge", "edge_evidence", "l2domain", "l2domain_member"}

func rows(l *Lab, snap int) map[string]int {
	out := map[string]int{}
	for _, table := range perSnapshot {
		out[table] = l.Int(fmt.Sprintf(`SELECT count(*) FROM %s WHERE snapshot_id = $1`, table), snap)
	}
	out["task"] = l.Int(`SELECT count(*) FROM task t JOIN snapshot s ON s.job_id = t.job_id WHERE s.id = $1`, snap)
	return out
}

func audit(l *Lab) []string {
	return l.Strings(`SELECT action || ' ' || command || ' ' || result FROM audit_log WHERE action LIKE 'snapshot.%' ORDER BY id`)
}

func objects(t *testing.T, l *Lab) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for o := range l.Raw.Client.ListObjects(context.Background(), l.Raw.Bucket, minio.ListObjectsOptions{Recursive: true}) {
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		out[o.Key] = true
	}
	return out
}

func hashes(l *Lab, query string, args ...any) []string {
	return l.Strings(query, args...)
}

func value(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, key+": "); ok {
			return v
		}
	}
	return ""
}

// T009, SC-001, FR-002, FR-003, FR-014: the preview changes nothing; the eviction removes every row
// of the snapshot, its crawl's task list and its partitions, and keeps the tombstone and its verdict.
func TestEvictProjectedSnapshot(t *testing.T) {
	l, net := evictLab(t)
	denied := FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001"))
	denied.Transports = []string{"ssh"}
	denied.Reject = []string{"ssh-a"}
	denied.Evidence = "ssh: unable to authenticate, attempted methods [none password]"
	net.Devices[Addr("10.0.0.2")] = denied
	snap := crawl(l)

	before := rows(l, snap)
	for _, table := range []string{"observation", "observation_raw", "finding", "entity", "edge", "l2domain", "task"} {
		if before[table] == 0 {
			t.Fatalf("no %s row before eviction, the test proves nothing: %v", table, before)
		}
	}

	code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(snap))
	if code != exitPreview || value(out, "result") != "preview" || value(out, "observations") != fmt.Sprint(before["observation"]) {
		t.Fatalf("preview: exit %d, printed\n%s", code, out)
	}
	if after := rows(l, snap); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("the preview changed rows: %v, was %v", after, before)
	}

	code, out = snapshotCmd(t, "evict", "--id", fmt.Sprint(snap), "--yes")
	if code != exitOK || value(out, "result") != "evicted" {
		t.Fatalf("evict: exit %d, printed\n%s", code, out)
	}
	for table, n := range rows(l, snap) {
		if n != 0 {
			t.Errorf("%s still holds %d rows of snapshot %d", table, n, snap)
		}
	}
	if left := l.Strings(`SELECT c FROM unnest(ARRAY['observation_raw_' || $1, 'identifier_claim_' || $1, 'observation_' || $1]) c
		WHERE to_regclass(c) IS NOT NULL`, fmt.Sprint(snap)); len(left) != 0 {
		t.Errorf("partitions left: %v", left)
	}
	if got := l.Strings(`SELECT state || ' ' || (evicted_at IS NOT NULL) || ' ' || (closed_at IS NOT NULL) FROM snapshot WHERE id = $1`, snap); len(got) != 1 || got[0] != "evicted true true" {
		t.Errorf("tombstone %v, want evicted with evicted_at and closed_at", got)
	}
	if n := l.Int(`SELECT count(*) FROM snapshot_judgement WHERE snapshot_id = $1`, snap); n == 0 {
		t.Error("the verdict went with the data")
	}
	if got := audit(l); fmt.Sprint(got) != fmt.Sprintf("[snapshot.evict snapshot %d ok]", snap) {
		t.Errorf("audit %v", got)
	}
}

// T010, SC-002, FR-004, FR-015: outputs another snapshot uses stay readable, outputs only the evicted
// one used go from the bucket and from raw_object, and an output orphaned before is left alone.
func TestEvictSharedOutputs(t *testing.T) {
	l, net := evictLab(t)
	first := crawl(l)
	net.Devices[Addr("10.0.0.2")] = FakeOS("sw2", "S002")
	second := crawl(l)

	orphan, err := l.Raw.Put(context.Background(), []byte("output nobody recorded"))
	if err != nil {
		t.Fatal(err)
	}
	only := hashes(l, `SELECT DISTINCT encode(hash, 'hex') FROM observation_raw WHERE snapshot_id = $1
		AND hash NOT IN (SELECT hash FROM observation_raw WHERE snapshot_id = $2)`, first, second)
	shared := hashes(l, `SELECT DISTINCT encode(hash, 'hex') FROM observation_raw WHERE snapshot_id = $1
		AND hash IN (SELECT hash FROM observation_raw WHERE snapshot_id = $2)`, first, second)
	if len(only) == 0 || len(shared) == 0 {
		t.Fatalf("%d outputs only the first used, %d shared: the test needs both", len(only), len(shared))
	}
	size := l.Int(`SELECT coalesce(sum(size), 0)::int FROM raw_object WHERE encode(hash, 'hex') = ANY($1)`, only)

	code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(first), "--yes")
	if code != exitOK {
		t.Fatalf("evict: exit %d\n%s", code, out)
	}
	if value(out, "objects_deleted") != fmt.Sprint(len(only)) || value(out, "objects_kept") != fmt.Sprint(len(shared)) ||
		value(out, "bytes_deleted") != fmt.Sprint(size) {
		t.Errorf("printed\n%s\nwant %d deleted (%d bytes), %d kept", out, len(only), size, len(shared))
	}
	ctx := context.Background()
	for _, h := range hashes(l, `SELECT DISTINCT encode(hash, 'hex') FROM observation_raw WHERE snapshot_id = $1`, second) {
		b, _ := hex.DecodeString(h)
		if _, err := l.Raw.Get(ctx, b); err != nil {
			t.Errorf("output %s of the remaining snapshot: %v", h, err)
		}
	}
	stored := objects(t, l)
	for _, h := range only {
		b, _ := hex.DecodeString(h)
		if stored[rawKey(b)] {
			t.Errorf("output %s only the evicted snapshot used is still stored", h)
		}
	}
	if n := l.Int(`SELECT count(*) FROM raw_object WHERE encode(hash, 'hex') = ANY($1)`, only); n != 0 {
		t.Errorf("%d raw_object rows left for deleted outputs", n)
	}
	if !stored[rawKey(orphan)] {
		t.Error("an output orphaned before the eviction was deleted")
	}
}

func rawKey(h []byte) string { return "raw/sha256/" + hex.EncodeToString(h) }

// other is Doc with a second perimeter whose crawl never reaches anything, so a job can be left
// running in it.
const other = `
perimeters:
  - name: lab
    include: [10.0.0.0/24]
  - name: other
    include: [10.0.1.0/24]
credential_sets:
  - {name: snmp-a, kind: snmp_v2c, secret_ref: env:NM_SNMP, max_attempts_per_device: 1, perimeters: [lab, other]}
seed_sets:
  - {name: seeds, targets: [10.0.0.1]}
  - {name: far, targets: [10.0.1.1]}
discovery: {lease: 2s, poll_interval: 20ms, step_idle_timeout: 1s, step_deadline: 5s}
`

// closeAsOwner ends a job and closes its snapshot without the engine, so nothing judges or resolves
// it.
func closeAsOwner(t *testing.T, l *Lab, job int64) int {
	t.Helper()
	ctx := context.Background()
	if _, err := l.DB.Exec(ctx, `UPDATE job SET state = 'succeeded', ended_at = now() WHERE id = $1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err := l.DB.Exec(ctx, `UPDATE snapshot SET state = 'closed', closed_at = now() WHERE job_id = $1`, job); err != nil {
		t.Fatal(err)
	}
	return l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job)
}

// T011, SC-005, FR-005, FR-008: each refusal exits 2 and changes nothing; a refusal of an existing
// snapshot with --yes is audited, invalid input and previews are not.
func TestEvictRefusals(t *testing.T) {
	l, _ := evictLab(t)
	ctx := context.Background()
	snap := crawl(l)
	before, bucket := rows(l, snap), objects(t, l)

	job, err := jobrunner.Start(ctx, l.Operator, []byte(other), "other", "far", "test")
	if err != nil {
		t.Fatal(err)
	}
	open := l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, job)
	refuse := func(want string, args ...string) {
		t.Helper()
		var code int
		var errOut string
		errOut = stderr(t, func() { code, _ = snapshotCmd(t, args...) })
		if code != exitInvalid || !strings.Contains(errOut, want) {
			t.Errorf("%v: exit %d, stderr %q, want 2 and %q", args, code, errOut, want)
		}
	}
	refuse(fmt.Sprintf("crawl %d (snapshot %d) is running", job, open), "evict", "--id", fmt.Sprint(snap), "--yes")
	refuse(fmt.Sprintf("crawl %d (snapshot %d) is running", job, open), "evict", "--id", fmt.Sprint(snap))
	refuse(fmt.Sprintf("snapshot %d is open", open), "evict", "--id", fmt.Sprint(open), "--yes")

	// The other perimeter's snapshot closes without a resolution: it does not block this one.
	closeAsOwner(t, l, job)
	sibling := closeAsOwner(t, l, l.Start(Doc))
	refuse(fmt.Sprintf("snapshot %d of perimeter lab is not resolved yet", sibling), "evict", "--id", fmt.Sprint(snap), "--yes")

	refuse("snapshot 9999 does not exist", "evict", "--id", "9999", "--yes")
	t.Setenv("NETMAPPER_S3_BUCKET", "")
	refuse("NETMAPPER_S3_BUCKET", "evict", "--id", fmt.Sprint(snap), "--yes")

	if after := rows(l, snap); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("rows changed: %v, was %v", after, before)
	}
	if after := objects(t, l); len(after) != len(bucket) {
		t.Errorf("bucket holds %d objects, was %d", len(after), len(bucket))
	}
	want := []string{
		fmt.Sprintf("snapshot.evict snapshot %d refused: crawl_running", snap),
		fmt.Sprintf("snapshot.evict snapshot %d refused: open", open),
		fmt.Sprintf("snapshot.evict snapshot %d refused: unresolved_sibling", snap),
	}
	if got := audit(l); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit\n%v\nwant\n%v", got, want)
	}
}

// stderr captures what a subcommand printed on stderr.
func stderr(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	run()
	w.Close()
	os.Stderr = old
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

// T012, SC-004, FR-006: an object store failure after the database part leaves the snapshot
// evicting, blocks crawls, and the same command finishes it.
func TestEvictResume(t *testing.T) {
	l, _ := evictLab(t)
	ctx := context.Background()
	snap := crawl(l)
	endpoint := os.Getenv("NETMAPPER_S3_ENDPOINT")
	t.Setenv("NETMAPPER_S3_ENDPOINT", "127.0.0.1:1")
	if code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(snap), "--yes"); code != exitError {
		t.Fatalf("store down: exit %d, want 1\n%s", code, out)
	}
	if got := l.Strings(`SELECT state FROM snapshot WHERE id = $1`, snap); got[0] != "evicting" {
		t.Fatalf("state %v, want evicting", got)
	}
	left := l.Int(`SELECT count(*) FROM eviction_object WHERE snapshot_id = $1`, snap)
	if left == 0 {
		t.Fatal("no stored output listed to delete")
	}
	var invalid jobrunner.Invalid
	if _, err := jobrunner.Start(ctx, l.Operator, []byte(Doc), "lab", "seeds", "test"); !errors.As(err, &invalid) ||
		!strings.Contains(err.Error(), fmt.Sprintf("snapshot %d is being evicted", snap)) {
		t.Errorf("crawl start during the eviction: %v", err)
	}

	code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(snap))
	if code != exitPreview || value(out, "result") != "resume" || strings.Count(out, "pending: ") != left {
		t.Errorf("preview of an evicting snapshot: exit %d, want 3, %d pending lines\n%s", code, left, out)
	}

	t.Setenv("NETMAPPER_S3_ENDPOINT", endpoint)
	code, out = snapshotCmd(t, "evict", "--id", fmt.Sprint(snap), "--yes")
	if code != exitOK || value(out, "result") != "resumed" {
		t.Fatalf("resume: exit %d\n%s", code, out)
	}
	if got := l.Strings(`SELECT state FROM snapshot WHERE id = $1`, snap); got[0] != "evicted" {
		t.Errorf("state %v, want evicted", got)
	}
	if n := l.Int(`SELECT count(*) FROM eviction_object`); n != 0 {
		t.Errorf("%d outputs still listed", n)
	}
	if n := len(objects(t, l)); n != 0 {
		t.Errorf("%d objects left in the bucket of the only snapshot", n)
	}
	got := audit(l)
	if len(got) != 2 || !strings.HasPrefix(got[0], fmt.Sprintf("snapshot.evict snapshot %d incomplete: ", snap)) ||
		got[1] != fmt.Sprintf("snapshot.evict snapshot %d ok", snap) {
		t.Errorf("audit %v, want incomplete then ok", got)
	}
}

// T013, FR-006: a second eviction and a preview of an evicted snapshot have nothing to do.
func TestEvictTwice(t *testing.T) {
	l, _ := evictLab(t)
	snap := fmt.Sprint(crawl(l))
	if code, out := snapshotCmd(t, "evict", "--id", snap, "--yes"); code != exitOK {
		t.Fatalf("evict: exit %d\n%s", code, out)
	}
	for _, args := range [][]string{{"evict", "--id", snap, "--yes"}, {"evict", "--id", snap}} {
		if code, out := snapshotCmd(t, args...); code != exitOK || value(out, "result") != "nothing to do" {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	want := []string{"snapshot.evict snapshot " + snap + " ok", "snapshot.evict snapshot " + snap + " noop"}
	if got := audit(l); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit %v, want %v", got, want)
	}
}

// T014, SC-007, FR-012: registry entries no remaining snapshot carries go with their identifiers;
// those still carried keep their key and history; a leftover with no entity anywhere goes too.
func TestEvictRegistry(t *testing.T) {
	l, net := evictLab(t)
	ctx := context.Background()
	a := crawl(l)
	net.Devices[Addr("10.0.0.1")] = vlanSwitch(FakeOS("sw1", "S001"))
	delete(net.Devices, Addr("10.0.0.2"))
	b := crawl(l)

	sw1, sw2 := "chassis_mac:"+MAC("S001"), "chassis_mac:"+MAC("S002")
	history := func(key string) []string {
		return l.Strings(`SELECT key || ' ' || first_seen || ' ' || last_seen || ' ' || minted_from FROM device WHERE key = $1`, key)
	}
	kept := history(sw1)
	if len(kept) != 1 || len(history(sw2)) != 1 {
		t.Fatalf("registry %v / %v, want one entry each", kept, history(sw2))
	}

	code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(a), "--yes")
	if code != exitOK || value(out, "devices_deleted") != "1" {
		t.Fatalf("evict: exit %d\n%s", code, out)
	}
	if got := history(sw2); len(got) != 0 {
		t.Errorf("sw2, seen only by the evicted snapshot, is still registered: %v", got)
	}
	if n := l.Int(`SELECT count(*) FROM device_identifier WHERE key = $1`, sw2); n != 0 {
		t.Errorf("%d identifiers left for sw2", n)
	}
	if got := history(sw1); fmt.Sprint(got) != fmt.Sprint(kept) {
		t.Errorf("sw1 changed: %v, was %v", got, kept)
	}

	if _, err := l.DB.Exec(ctx, `INSERT INTO device (perimeter_name, key, first_seen, last_seen, minted_from)
		VALUES ('lab', 'leftover', now(), now(), $1)`, b); err != nil {
		t.Fatal(err)
	}
	crawl(l)
	if code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(b), "--yes"); code != exitOK || value(out, "devices_deleted") != "1" {
		t.Errorf("second eviction: exit %d\n%s", code, out)
	}
	if got := l.Strings(`SELECT key FROM device ORDER BY key`); fmt.Sprint(got) != fmt.Sprint([]string{sw1}) {
		t.Errorf("registry %v, want only sw1", got)
	}
}

// T030, FR-007: pin and unpin are idempotent and audited; a pinned snapshot is not evicted; an
// evicted one cannot be pinned.
func TestPinUnpin(t *testing.T) {
	l, _ := evictLab(t)
	snap := fmt.Sprint(crawl(l))
	steps := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"pin", "--id", snap}, exitOK, "pinned"},
		{[]string{"pin", "--id", snap}, exitOK, "nothing to do"},
		{[]string{"evict", "--id", snap, "--yes"}, exitInvalid, ""},
		{[]string{"unpin", "--id", snap}, exitOK, "unpinned"},
		{[]string{"unpin", "--id", snap}, exitOK, "nothing to do"},
		{[]string{"evict", "--id", snap, "--yes"}, exitOK, ""},
		{[]string{"pin", "--id", snap}, exitInvalid, ""},
	}
	for _, s := range steps {
		code, out := snapshotCmd(t, s.args...)
		if code != s.code || (s.out != "" && strings.TrimSpace(out) != s.out) {
			t.Errorf("%v: exit %d, printed %q, want %d %q", s.args, code, out, s.code, s.out)
		}
	}
	want := []string{
		"snapshot.pin snapshot " + snap + " ok",
		"snapshot.pin snapshot " + snap + " noop",
		"snapshot.evict snapshot " + snap + " refused: pinned",
		"snapshot.unpin snapshot " + snap + " ok",
		"snapshot.unpin snapshot " + snap + " noop",
		"snapshot.evict snapshot " + snap + " ok",
		"snapshot.pin snapshot " + snap + " refused: evicted",
	}
	if got := audit(l); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit\n%v\nwant\n%v", got, want)
	}

	// A crawl can be protected before it ends.
	open := l.Int(`SELECT snapshot_id FROM job WHERE id = $1`, l.Start(Doc))
	if code, _ := snapshotCmd(t, "pin", "--id", fmt.Sprint(open)); code != exitOK {
		t.Errorf("pin an open snapshot: exit %d", code)
	}
}

// T035, FR-011, FR-013: evicting a baseline leaves the later verdict as it was; judging that snapshot
// again measures it against the closest earlier snapshot still there.
func TestEvictBaseline(t *testing.T) {
	l, _ := evictLab(t)
	a, b := crawl(l), crawl(l)
	c := crawl(l)
	verdict := func() string {
		return fmt.Sprint(l.Strings(`SELECT coalesce(baseline_snapshot_id::text, '-') || ' ' || coalesce(coverage::text, '-')
			FROM snapshot_judgement WHERE snapshot_id = $1 AND active`, c))
	}
	before := verdict()
	if !strings.HasPrefix(before, fmt.Sprintf("[%d ", b)) {
		t.Fatalf("verdict of %d is %s, want measured against %d", c, before, b)
	}
	if code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(b), "--yes"); code != exitOK {
		t.Fatalf("evict: exit %d\n%s", code, out)
	}
	if got := verdict(); got != before {
		t.Errorf("verdict changed to %s, was %s", got, before)
	}
	j, err := gate.Judge(context.Background(), l.Engine, int64(c))
	if err != nil {
		t.Fatal(err)
	}
	if j.BaselineID == nil || *j.BaselineID != int64(a) {
		t.Errorf("judged again against %v, want %d", j.BaselineID, a)
	}
}

// T037, FR-008, US3-3: the audit trail tells the story of a snapshot.
func TestEvictAuditTrail(t *testing.T) {
	l, _ := evictLab(t)
	snap := fmt.Sprint(crawl(l))
	for _, args := range [][]string{{"pin", "--id", snap}, {"evict", "--id", snap, "--yes"},
		{"unpin", "--id", snap}, {"evict", "--id", snap, "--yes"}} {
		snapshotCmd(t, args...)
	}
	got := l.Strings(`SELECT action || '|' || command || '|' || result || '|' || (actor <> '') || '|' || (at IS NOT NULL) || '|' || (target IS NULL)
		FROM audit_log WHERE action LIKE 'snapshot.%' ORDER BY id`)
	want := []string{
		"snapshot.pin|snapshot " + snap + "|ok|true|true|true",
		"snapshot.evict|snapshot " + snap + "|refused: pinned|true|true|true",
		"snapshot.unpin|snapshot " + snap + "|ok|true|true|true",
		"snapshot.evict|snapshot " + snap + "|ok|true|true|true",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit\n%v\nwant\n%v", got, want)
	}
}

// 012 research R3: an API request is one read-only transaction that holds its table locks until it
// ends, and the endpoints read observation and observation_raw in opposite orders: an observation,
// then its commands (GET /v1/observations/{id}); a raw output, then its observation on a miss (GET
// /v1/observations/{id}/raw/{step}). An eviction that waits for its table locks one by one holds one
// while it waits for the other, and deadlocks with a reader holding the second and asking for the
// first. A reader of the remaining snapshot reads one table, the eviction starts, the reader reads
// the other: whatever the order, both must succeed.
func TestEvictDoesNotDeadlockWithReaders(t *testing.T) {
	// The last pair stands for every table a foreign key ties to a dropped partition: dropping it
	// locks them too (edge_evidence, interface_alias, entity_claim, snapshot...).
	for _, order := range [][2]string{{"observation", "observation_raw"}, {"observation_raw", "observation"},
		{"edge_evidence", "observation"}} {
		t.Run(order[0]+"_first", func(t *testing.T) {
			l, _ := evictLab(t)
			ctx := context.Background()
			keep := crawl(l)
			gone := crawl(l)
			api := As(t, l.DB, "netmapper_api")

			tx, err := api.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			read := func(table string) error {
				var n int
				return tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE snapshot_id = $1`, keep).Scan(&n)
			}
			if err := read(order[0]); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() {
				_, err := l.Operator.Exec(ctx, `SELECT * FROM evict_snapshot($1, true)`, gone)
				done <- err
			}()
			time.Sleep(500 * time.Millisecond) // the eviction reaches its table locks

			if err := read(order[1]); err != nil {
				t.Errorf("reader: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Errorf("reader commit: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("eviction: %v", err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the eviction never finished")
			}
			if got := l.Strings(`SELECT state FROM snapshot WHERE id = $1`, gone); fmt.Sprint(got) != "[evicting]" {
				t.Errorf("state %v, want evicting", got)
			}
		})
	}
}

// 012 FR-005, FR-012 (review of 2026-10-19): two unresolved snapshots of one perimeter, whose
// resolution may keep failing, do not block each other's eviction. Evicting an unresolved snapshot
// removes no registry reference, so it leaves the registry alone; a resolved snapshot is still
// refused while another of its perimeter is unresolved.
func TestEvictUnresolvedSnapshots(t *testing.T) {
	l, _ := evictLab(t)
	ctx := context.Background()
	resolved := crawl(l)
	if _, err := l.DB.Exec(ctx, `INSERT INTO device (perimeter_name, key, first_seen, last_seen, minted_from)
		VALUES ('lab', 'leftover', now(), now(), $1)`, resolved); err != nil {
		t.Fatal(err)
	}
	registry := l.Strings(`SELECT key FROM device ORDER BY key`)
	first := closeAsOwner(t, l, l.Start(Doc))
	second := closeAsOwner(t, l, l.Start(Doc))

	if code, _ := snapshotCmd(t, "evict", "--id", fmt.Sprint(resolved), "--yes"); code != exitInvalid {
		t.Errorf("resolved snapshot with unresolved ones beside it: exit %d, want 2", code)
	}
	for _, snap := range []int{first, second} {
		code, out := snapshotCmd(t, "evict", "--id", fmt.Sprint(snap), "--yes")
		if code != exitOK || value(out, "devices_deleted") != "0" {
			t.Errorf("unresolved snapshot %d: exit %d\n%s", snap, code, out)
		}
	}
	if got := l.Strings(`SELECT key FROM device ORDER BY key`); fmt.Sprint(got) != fmt.Sprint(registry) {
		t.Errorf("registry %v, was %v", got, registry)
	}
	if got := l.Strings(`SELECT state FROM snapshot WHERE id IN ($1, $2) ORDER BY id`, first, second); fmt.Sprint(got) != "[evicted evicted]" {
		t.Errorf("states %v", got)
	}
}
