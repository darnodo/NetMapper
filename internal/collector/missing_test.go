package collector_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/pack"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// Issue #14: a family the device's pack has no recipe for is recorded unsupported/no_recipe, and
// one whose implementations all need another version is unsupported/no_matching_version, so the
// gap is in the snapshot instead of missing from it.
func TestMissingFamilies(t *testing.T) {
	env(t)

	// fakeos without its interfaces recipe.
	dir := filepath.Join(t.TempDir(), "fakeos")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(Root(), "internal", "pack", "testdata", "fakeos"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "recipes", "interfaces.yaml")); err != nil {
		t.Fatal(err)
	}
	reg, err := pack.Load(filepath.Join(Root(), "packs", "_base"), dir)
	if err != nil {
		t.Fatal(err)
	}

	// A device on a release older than every fakeos recipe (>=1.0).
	old := FakeOS("sw2", "S002")
	for k, v := range old.CLI {
		old.CLI[k] = strings.ReplaceAll(v, "FakeOS 1.2", "FakeOS 0.9")
	}
	for k, v := range old.SNMP {
		old.SNMP[k] = strings.ReplaceAll(v, "FakeOS 1.2", "FakeOS 0.9")
	}

	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{
		Addr("10.0.0.1"): FakeOS("sw1", "S001", "p1 sw2 10.0.0.2"),
		Addr("10.0.0.2"): old,
	}})
	l.Registry = reg
	got := l.AllOutcomes(l.Crawl(Doc))
	for _, want := range []string{
		"10.0.0.1 neighbours collected",
		"10.0.0.1 interfaces unsupported no_recipe",
		"10.0.0.2 identity collected",
		"10.0.0.2 neighbours unsupported no_matching_version",
		"10.0.0.2 interfaces unsupported no_recipe",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	// Feature 007: fakeos has no recipe for the management families, so each is recorded
	// unsupported/no_recipe on every identified device (SC-004).
	for _, family := range ManagementFamilies {
		for _, target := range []string{"10.0.0.1", "10.0.0.2"} {
			if want := target + " " + family + " unsupported no_recipe"; !slices.Contains(got, want) {
				t.Errorf("missing %q in %v", want, got)
			}
		}
	}
}
