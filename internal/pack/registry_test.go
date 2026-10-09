package pack

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const base = "../../packs/_base"

func TestLoadFakeOS(t *testing.T) {
	r, err := Load(base, "testdata/fakeos")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Probe(); len(got) != 2 || got[0].OID != "1.3.6.1.2.1.1.2.0" {
		t.Errorf("probe = %v", got)
	}
	if p, v, ok := r.Fingerprint(Evidence{SysObjectID: "1.3.6.1.4.1.99999.1", SysDescr: "FakeOS 2.1 on a box"}); !ok || p != "fakeos" || v != "2.1" {
		t.Errorf("snmp fingerprint = %s %s %v", p, v, ok)
	}
	if _, _, ok := r.Fingerprint(Evidence{SysObjectID: "1.3.6.1.4.1.999990.1"}); ok {
		t.Error("prefix matched a longer arc")
	}
	if p, v, ok := r.Fingerprint(Evidence{CLI: map[string]string{"display version": "FakeOS 1.5\nSerial: X"}}); !ok || p != "fakeos" || v != "1.5" {
		t.Errorf("ssh fingerprint = %s %s %v", p, v, ok)
	}
	if got := r.FingerprintCommands(); len(got) != 1 || got[0] != "display version" {
		t.Errorf("fingerprint commands = %v", got)
	}
	if got := r.Families("fakeos"); strings.Join(got, ",") != "interfaces,neighbours" {
		t.Errorf("families = %v", got)
	}
	if n := len(r.Implementations("fakeos", "neighbours", "0.9")); n != 0 {
		t.Errorf("version 0.9 got %d implementations", n)
	}
	im := r.Implementations("fakeos", "neighbours", "1.5")
	if len(im) != 1 || !strings.HasPrefix(r.RecipeID("fakeos", im[0]), "fakeos/nbr-cli@") {
		t.Errorf("implementations = %v", im)
	}
	if got := r.Normalise("fakeos", "p3"); got != "port3" {
		t.Errorf("normalise = %s", got)
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"AABB.CCDD.EEFF": "aa:bb:cc:dd:ee:ff", "aa-bb-cc-dd-ee-ff": "aa:bb:cc:dd:ee:ff", "nope": "nope"} {
		if got := NormaliseMAC(in); got != want {
			t.Errorf("NormaliseMAC(%s) = %s", in, got)
		}
	}
	for _, c := range []struct {
		constraint, version string
		want                bool
	}{
		{"", "", true}, {">=4.20", "4.28.3M", true}, {">=4.20", "4.15.2F", false}, {">=4.20", "", false},
		{"<5,>=4.20", "4.28", true}, {"<5,>=4.20", "5.1", false}, {"4.20", "4.20", true}, {"!=4.20", "4.21", true},
	} {
		if got := fits(c.constraint, c.version); got != c.want {
			t.Errorf("fits(%q, %q) = %v", c.constraint, c.version, got)
		}
	}
}

// copyPack copies testdata/fakeos to a temp dir and applies edit to the file at rel.
func copyPack(t *testing.T, rel, old, new string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "fakeos")
	if err := os.CopyFS(dst, os.DirFS("testdata/fakeos")); err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		path := filepath.Join(dst, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(b), old, new, 1)
		if edited == string(b) {
			t.Fatalf("edit of %s changed nothing", rel)
		}
		os.WriteFile(path, []byte(edited), 0o644)
	}
	return dst
}

func TestRefusals(t *testing.T) {
	for _, c := range []struct {
		name, rel, old, new, want string
	}{
		{"not read only", "recipes/interfaces.yaml", "command: display interfaces", "command: reload now", "does not start with an entry of read_only"},
		{"fingerprint not read only", "pack.yaml", "- command: display version", "- command: erase startup", "does not start with an entry of read_only"},
		{"missing template", "recipes/interfaces.yaml", "template: display_interfaces.textfsm", "template: nope.textfsm", `template "nope.textfsm" not found`},
		{"template does not compile", "templates/display_interfaces.textfsm", "Value NAME (\\S+)", "Value NAME", "does not compile"},
		{"map target not in schema", "recipes/interfaces.yaml", "description: DESC", "colour: DESC", `map target "colour"`},
		{"unknown family", "recipes/interfaces.yaml", "family: interfaces", "family: weather", `unknown fact family "weather"`},
		{"probe outside _base", "pack.yaml", "fingerprint:", "probe: {snmp: []}\nfingerprint:", "only _base may declare probe"},
		{"step map target not in schema", "recipes/interfaces.yaml", "template: display_interfaces.textfsm", "template: display_interfaces.textfsm\n        map: {colour: '=red'}", `step map target "colour"`},
		{"default outside the enum", "recipes/interfaces.yaml", "values:", "defaults: {admin_state: maybe}\n    values:", `default "maybe" is not a valid value of admin_state`},
		{"default not in schema", "recipes/interfaces.yaml", "values:", "defaults: {colour: red}\n    values:", `default for "colour"`},
		{"split on a string field", "recipes/interfaces.yaml", "values:", "split: {description: ', '}\n    values:", `split field "description" is not a list field`},
		{"split with no separator", "recipes/interfaces.yaml", "values:", "split: {description: ''}\n    values:", `split separator for "description" is empty`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(base, copyPack(t, c.rel, c.old, c.new))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
	if _, err := Load(base, "testdata/fakeos", copyPack(t, "", "", "")); err == nil || !strings.Contains(err.Error(), `name "fakeos" is already loaded`) {
		t.Errorf("duplicate name: %v", err)
	}
	if _, err := Load("testdata/fakeos"); err == nil || !strings.Contains(err.Error(), "_base") {
		t.Errorf("no base: %v", err)
	}
}

// Issue #14: a platform pack missing a family's recipe loads, with one warning per missing family;
// _base, which fingerprints nothing, is not warned about.
func TestLoadWarnsMissingFamily(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fakeos")
	if err := os.CopyFS(dir, os.DirFS("testdata/fakeos")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "recipes", "neighbours.yaml")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	if _, err := Load(base, dir); err != nil {
		t.Fatal(err)
	}
	// fakeos also has no recipe for the six management families (feature 007) and the two layer 2
	// families (feature 008): one line each.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 9 || !strings.Contains(buf.String(), "family=vlans effect=\"no VLAN data") || !strings.Contains(buf.String(), "pack=fakeos family=neighbours") {
		t.Errorf("warnings = %q", buf.String())
	}
	for _, l := range lines {
		if !strings.Contains(l, "pack=fakeos") {
			t.Errorf("warning about another pack: %q", l)
		}
	}
}

func TestNormaliseVLANs(t *testing.T) {
	for _, c := range []struct {
		in, want []string
	}{
		{[]string{"1,40,4090-4091"}, []string{"1", "40", "4090-4091"}},
		{[]string{"10-12", "11-20", "21"}, []string{"10-21"}},
		{[]string{"2,4,", "6"}, []string{"2", "4", "6"}},                // a list cut after a comma
		{[]string{"2,4,6", "8,10"}, []string{"2", "4", "6", "8", "10"}}, // cut between items (EOS)
		{[]string{"1-4094"}, []string{"1-4094"}},
		{[]string{"30-32", "10"}, []string{"10", "30-32"}},
	} {
		got, err := NormaliseVLANs(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("NormaliseVLANs(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range [][]string{{"0"}, {"4095"}, {"a-b"}, {"20-10"}} {
		if got, err := NormaliseVLANs(bad); err == nil {
			t.Errorf("NormaliseVLANs(%q) = %q, want an error", bad, got)
		}
	}
}
