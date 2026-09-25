package main

import (
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// projectLab crawls two cabled devices and points NETMAPPER_DSN at the schema they live in, as
// netmapper_operator. The role is the point: contracts/cli.md assigns `netmapper project` to the
// operator, and the constitution asks that a path be tested under the role the contracts give it,
// because 003 shipped a command whose own role could not run it.
func projectLab(t *testing.T) *Lab {
	t.Setenv("NM_SNMP", "public")
	t.Setenv("NM_SSH", "lab-ssh-secret")
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2 p1 "+MAC("S002")),
		Addr("10.0.0.2"): FakeOS("sw2", "S002", "p1 sw1 10.0.0.1 p1 "+MAC("S001")),
	}})
	l.Crawl(Doc)

	schema := l.DB.Config().ConnConfig.RuntimeParams["search_path"]
	dsn := os.Getenv("NETMAPPER_TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	t.Setenv("NETMAPPER_DSN", dsn+sep+
		"options=-c%20search_path%3D"+schema+"%20-c%20role%3Dnetmapper_operator")
	return l
}

// packRoot is a directory holding the two packs the lab runs on, _base and the fakeos test platform.
// The subcommand loads a directory rather than a pack list, and pointing it at the repository's own
// packs/ would leave it without fakeos: the far-end spellings would then fall back to themselves,
// which is correct behaviour for an unknown platform (FR-005) and silently the wrong graph for this
// one.
//
// The packs are copied rather than symlinked, because pack.LoadRoot lists the directory with
// os.ReadDir, and a symlink's DirEntry does not report itself as a directory.
func packRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, src := range []string{
		filepath.Join(Root(), "packs", "_base"),
		filepath.Join(Root(), "internal", "pack", "testdata", "fakeos"),
	} {
		if err := os.CopyFS(filepath.Join(dir, filepath.Base(src)), os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
	}
	return "--packs=" + dir
}

// stdout captures what a subcommand printed.
func stdout(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	run()
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// T036, contracts/cli.md, research R15: `netmapper project` replaces the snapshot's set and prints
// what it wrote, running as netmapper_operator throughout.
func TestProjectAsOperator(t *testing.T) {
	l := projectLab(t)
	snap := l.Strings(`SELECT id::text FROM snapshot ORDER BY id LIMIT 1`)[0]
	packs := packRoot(t)

	before := l.Strings(`SELECT computed_at::text FROM projection`)
	var code int
	out := stdout(t, func() { code = cmdProject(context.Background(), []string{packs, snap}) })
	if code != exitOK {
		t.Fatalf("exit %d, want %d", code, exitOK)
	}
	if got := strings.TrimSpace(out); got != "4 interfaces, 3 edges, 0 disagreements" {
		t.Errorf("printed %q, want the contract's line", got)
	}
	after := l.Strings(`SELECT computed_at::text FROM projection`)
	if len(after) != 1 || after[0] == before[0] {
		t.Errorf("projection rows %v, want the one row rewritten", after)
	}
	if n := l.Int(`SELECT count(*) FROM edge WHERE type = 'l1_link' AND confidence = 'both_ends'`); n != 1 {
		t.Error("the operator's own projection did not produce the agreed link")
	}
}

// The other half of the contract: each of these is invalid input, not a runtime error.
func TestProjectRefusesWhatCannotBeProjected(t *testing.T) {
	l := projectLab(t)
	ctx := context.Background()
	snap := l.Strings(`SELECT id::text FROM snapshot ORDER BY id LIMIT 1`)[0]
	packs := packRoot(t)

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"no snapshot at all", []string{packs}},
		{"not a number", []string{packs, "banana"}},
		{"a snapshot that does not exist", []string{packs, "99999"}},
	} {
		if code := cmdProject(ctx, c.args); code != exitInvalid {
			t.Errorf("%s: exit %d, want %d", c.why, code, exitInvalid)
		}
	}

	// An open snapshot: closed is the only state a projection is defined for (FR-001).
	l.Start(Doc)
	open := l.Strings(`SELECT id::text FROM snapshot WHERE state = 'open' ORDER BY id LIMIT 1`)
	if len(open) != 1 {
		t.Fatal("the second job did not leave an open snapshot")
	}
	if code := cmdProject(ctx, []string{packs, open[0]}); code != exitInvalid {
		t.Errorf("an open snapshot: exit %d, want %d", code, exitInvalid)
	}

	// A closed snapshot with no entity set is not projected and is not an error either.
	for _, q := range []string{"DELETE FROM projection", "DELETE FROM entity", "DELETE FROM resolution"} {
		if _, err := l.DB.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if code := cmdProject(ctx, []string{packs, snap}); code != exitInvalid {
		t.Errorf("a snapshot with no entity set: exit %d, want %d", code, exitInvalid)
	}
	if n := l.Int(`SELECT count(*) FROM projection`); n != 0 {
		t.Error("a snapshot with no entity set was projected anyway")
	}
}
