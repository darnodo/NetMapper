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
	// fakeos also has no recipe for the six management families (feature 007), the two layer 2
	// families (feature 008), bgp_peers (feature 010) and the two OSPF families (feature 011): one
	// line each.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 12 || !strings.Contains(buf.String(), "family=ospf_neighbours effect=\"no OSPF data") || !strings.Contains(buf.String(), "family=vlans effect=\"no VLAN data") || !strings.Contains(buf.String(), "pack=fakeos family=neighbours") {
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

// bgpRecipe is a bgp_peers recipe for fakeos, written only into a test's own copy of the pack so
// the collector tests that crawl fakeos never run it.
const bgpRecipe = `family: bgp_peers
implementations:
  - id: bgp-cli
    versions: '>=1.0'
    transport: ssh
    steps:
      - command: display peers
        template: display_peers.textfsm
        map: { vrf: VRF, peer_address: PEER, peer_as: AS, state: STATE, peer_router_id: PEER_ID, negotiated_address_families: NEGOTIATED }
      - command: display family ipv4
        template: display_family.textfsm
        map: { vrf: VRF, peer_address: PEER, configured_address_families: '=ipv4 unicast' }
      - command: display family evpn
        template: display_family.textfsm
        map: { vrf: VRF, peer_address: PEER, configured_address_families: '=l2vpn evpn' }
    merge_on: [vrf, peer_address]
    covers:
      configured_address_families: [ipv4 unicast, l2vpn evpn]
`

// Feature 010, FR-005c: covers must equal the literal values the steps map into the field, both
// ways, and name a list field.
func TestCovers(t *testing.T) {
	for _, c := range []struct{ name, old, new, want string }{
		{"matching", "", "", ""},
		{"covers a value no step maps", "[ipv4 unicast, l2vpn evpn]", "[ipv4 unicast, l2vpn evpn, ipv6 unicast]", "covers configured_address_families [ipv4 unicast ipv6 unicast l2vpn evpn], but the steps map [ipv4 unicast l2vpn evpn]"},
		{"a step maps a value covers lacks", "[ipv4 unicast, l2vpn evpn]", "[ipv4 unicast]", "but the steps map [ipv4 unicast l2vpn evpn]"},
		{"covers a field that is not a list", "configured_address_families: [ipv4", "peer_group: [ipv4", `covers field "peer_group" is not a list field`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := copyPack(t, "", "", "")
			os.WriteFile(filepath.Join(dir, "recipes", "bgp_peers.yaml"), []byte(strings.Replace(bgpRecipe, c.old, c.new, 1)), 0o644)
			_, err := Load(base, dir)
			if c.want == "" {
				if err != nil {
					t.Errorf("matching covers refused: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "bgp_peers/bgp-cli") {
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
}

// ospfRecipe is an ospf_neighbours recipe for fakeos with a guard and a join step, written only
// into a test's own copy of the pack (feature 011).
const ospfRecipe = `family: ospf_neighbours
implementations:
  - id: ospf-cli
    versions: '>=1.0'
    transport: ssh
    steps:
      - command: display ospf json
        template: display_peers.textfsm
        guard: true
        nothing_pattern: '^\{"vrfs":\{\}\}$'
      - command: display ospf neighbours
        template: display_peers.textfsm
        map: { vrf: VRF, interface: PEER, neighbour_router_id: PEER_ID, state: STATE }
      - command: display ospf
        template: display_family.textfsm
        join_on: [vrf]
        unmatched: ignore
        map: { vrf: VRF, router_id: PEER }
    merge_on: [vrf, interface, neighbour_router_id]
`

// Feature 011: pack_schema 2 enables guard, nothing_pattern, join_on and unmatched; a pack without
// pack_schema is version 1 and keeps loading, covers included.
func TestPackSchema(t *testing.T) {
	for _, c := range []struct{ name, schema, old, new, want string }{
		{"version 2 loads", "2", "", "", ""},
		{"version 1 refuses the new keys", "", "", "", "need pack_schema 2"},
		{"unknown version", "3", "", "", "pack_schema 3 is not a known pack format"},
		{"guard not first", "2", "      - command: display ospf neighbours\n        template: display_peers.textfsm\n", "      - command: display ospf neighbours\n        template: display_peers.textfsm\n        guard: true\n        nothing_pattern: x\n", "a guard must be the first step"},
		{"guard with a map", "2", "        guard: true\n", "        guard: true\n        map: { vrf: VRF }\n", "a guard has no map and no empty_lines"},
		{"guard with empty_lines", "2", "        guard: true\n", "        guard: true\n        empty_lines: ['^$']\n", "a guard has no map and no empty_lines"},
		{"guard without nothing_pattern", "2", "        nothing_pattern: '^\\{\"vrfs\":\\{\\}\\}$'\n", "", "a guard needs a nothing_pattern"},
		{"nothing_pattern that does not compile", "2", `'^\{"vrfs":\{\}\}$'`, `'(('`, "nothing_pattern"},
		{"nothing_pattern on a step that is not a guard", "2", "        join_on: [vrf]\n", "        join_on: [vrf]\n        nothing_pattern: x\n", "nothing_pattern is only for a guard"},
		{"join_on with no row step before it", "2", "      - command: display ospf neighbours\n        template: display_peers.textfsm\n        map: { vrf: VRF, interface: PEER, neighbour_router_id: PEER_ID, state: STATE }\n", "", "join_on needs a step that creates rows before it"},
		{"join_on without unmatched", "2", "        unmatched: ignore\n", "", "join_on needs unmatched: fail or ignore"},
		{"unmatched with another value", "2", "unmatched: ignore", "unmatched: drop", "join_on needs unmatched: fail or ignore"},
		{"unmatched without join_on", "2", "        join_on: [vrf]\n", "", "unmatched is only for a join_on step"},
		{"join_on field not in the schema", "2", "join_on: [vrf]", "join_on: [colour]", `join_on field "colour" is not a field of ospf_neighbours`},
		{"expect_rows on a step that is not a guard", "2", "        join_on: [vrf]\n", "        join_on: [vrf]\n        expect_rows: PEER\n", "expect_rows is only for a guard"},
		{"expect_rows on a guard loads", "2", "        guard: true\n", "        guard: true\n        expect_rows: PEER\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := copyPack(t, "", "", "")
			if c.schema != "" {
				b, _ := os.ReadFile(filepath.Join(dir, "pack.yaml"))
				os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(strings.Replace(string(b), "version: 1\n", "version: 1\npack_schema: "+c.schema+"\n", 1)), 0o644)
			}
			os.WriteFile(filepath.Join(dir, "recipes", "ospf_neighbours.yaml"), []byte(strings.Replace(ospfRecipe, c.old, c.new, 1)), 0o644)
			_, err := Load(base, dir)
			if c.want == "" {
				if err != nil {
					t.Errorf("refused: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
	// covers of feature 010 stays valid in both versions.
	for _, schema := range []string{"", "pack_schema: 2\n"} {
		dir := copyPack(t, "", "", "")
		b, _ := os.ReadFile(filepath.Join(dir, "pack.yaml"))
		os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(strings.Replace(string(b), "version: 1\n", "version: 1\n"+schema, 1)), 0o644)
		os.WriteFile(filepath.Join(dir, "recipes", "bgp_peers.yaml"), []byte(bgpRecipe), 0o644)
		if _, err := Load(base, dir); err != nil {
			t.Errorf("covers with %q: %v", schema, err)
		}
	}
}

// Feature 011, FR-004: a field that takes a value when missing (auth_type: unknown) cannot have a
// recipe default, so no pack turns a missing value into none.
func TestNoDefaultOnMissing(t *testing.T) {
	dir := copyPack(t, "", "", "")
	recipe := `family: ospf_interfaces
implementations:
  - id: ospf-if-cli
    versions: '>=1.0'
    transport: ssh
    steps:
      - command: display ospf interfaces
        template: display_peers.textfsm
        map: { vrf: VRF, interface: PEER }
    defaults: { auth_type: none }
`
	os.WriteFile(filepath.Join(dir, "recipes", "ospf_interfaces.yaml"), []byte(recipe), 0o644)
	if _, err := Load(base, dir); err == nil || !strings.Contains(err.Error(), `"auth_type" takes "unknown" when missing and cannot have a default`) {
		t.Errorf("got %v", err)
	}
}
