// Lint every shipped pack with the collector's own loader, and parse every recorded output with
// its template (FR-024, constitution: parser changes are verified over stored output).
package packs_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sirikothe/gotextfsm"
	"go.yaml.in/yaml/v3"

	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/parse"
)

// An EOS switch with LLDP on and no neighbour is empty, not a parse failure. The sample is
// written by hand until a cEOS recording replaces it (T045).
func TestNoLLDPNeighbourIsEmpty(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "neighbours", "4.30.1F")[0]
	none := "Interface Ethernet1 detected 0 LLDP neighbors:\n\nInterface Ethernet2 detected 0 LLDP neighbors:\n\n"
	some, _ := os.ReadFile("arista_eos/testdata/ntc/show_lldp_neighbors_detail.raw")
	for _, c := range []struct{ name, out, want string }{
		{"no neighbour", none, parse.Empty},
		{"some interfaces without a neighbour", none + string(some), parse.Collected},
		{"reworded", "Interface Ethernet1 has no LLDP neighbor\n", parse.ParseFailed},
	} {
		if got, _, err := parse.Parse(reg, "arista_eos", "neighbours", im, [][]byte{[]byte(c.out)}); got != c.want {
			t.Errorf("%s: %s (%v), want %s", c.name, got, err, c.want)
		}
	}
}

func TestPacks(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ := os.ReadDir(".")
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := reg.Pack(d.Name())
		if p == nil {
			t.Errorf("pack directory %s is not named after its pack", d.Name())
			continue
		}
		raws, _ := filepath.Glob(filepath.Join(d.Name(), "testdata", "*", "*.raw"))
		for _, raw := range raws {
			t.Run(raw, func(t *testing.T) { parseRecorded(t, p, raw) })
		}
	}
}

// parseRecorded parses a recorded output with the template whose name it contains, and compares
// with the .yml next to it when there is one (ntc-templates fixture format).
//
// A recording named _empty is a device with nothing to report (feature 007). When the empty_lines
// of a step using the template cover all of it, the pipeline never runs the template, so neither
// does this test; a recording not named _empty must never be covered that way, or a too-broad
// empty_lines pattern would hide its rows. A recording also named _invalid_ or _denied_ is the
// device refusing the command: the template may raise an error on it instead of giving no row.
func parseRecorded(t *testing.T, p *pack.Pack, raw string) {
	var tmpl string
	for name := range p.Templates {
		if base := strings.TrimSuffix(name, ".textfsm"); strings.Contains(filepath.Base(raw), base) && len(name) > len(tmpl) {
			tmpl = name
		}
	}
	if tmpl == "" {
		t.Fatalf("no template for %s", raw)
	}
	out, _ := os.ReadFile(raw)
	empty := strings.Contains(filepath.Base(raw), "_empty")
	for _, r := range p.Recipes {
		for _, im := range r.Implementations {
			for _, st := range im.Steps {
				if st.Template != tmpl || !parse.Nothing(st.EmptyLines, out) {
					continue
				}
				if !empty {
					t.Fatalf("the empty_lines of %q cover all of %s, which is not named _empty", st.Command, raw)
				}
				return
			}
		}
	}
	fsm := gotextfsm.TextFSM{}
	if err := fsm.ParseString(p.Templates[tmpl]); err != nil {
		t.Fatal(err)
	}
	po := gotextfsm.ParserOutput{}
	if err := po.ParseTextString(string(out), fsm, true); err != nil {
		refused := strings.Contains(raw, "_invalid_") || strings.Contains(raw, "_denied_")
		if !empty || !refused {
			t.Fatal(err)
		}
		return
	}
	if empty != (len(po.Dict) == 0) {
		t.Fatalf("%s yielded %d rows (recording named _empty: %v)", tmpl, len(po.Dict), empty)
	}
	b, err := os.ReadFile(strings.TrimSuffix(raw, ".raw") + ".yml")
	if err != nil {
		return
	}
	var want struct {
		Parsed []map[string]any `yaml:"parsed_sample"`
	}
	if err := yaml.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	for _, row := range po.Dict {
		m := map[string]any{}
		for k, v := range row {
			if l, ok := v.([]string); ok {
				anys := make([]any, len(l))
				for i := range l {
					anys[i] = l[i]
				}
				v = anys
			}
			m[strings.ToLower(k)] = v
		}
		got = append(got, m)
	}
	if !reflect.DeepEqual(got, want.Parsed) {
		t.Errorf("%s:\n got %v\nwant %v", raw, got, want.Parsed)
	}
}

// A management output the template does not understand is parse_failed, never empty: an auditor
// reads empty as "nothing configured" (feature 007, FR-012).
func TestDriftIsNotEmpty(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "aaa_servers", "4.36.0F")[0]
	tacacs, _ := os.ReadFile("arista_eos/testdata/lab/sw2_show_tacacs.raw")
	radius, _ := os.ReadFile("arista_eos/testdata/lab/sw1_show_radius_empty.raw")
	reworded := strings.ReplaceAll(string(tacacs), "TACACS+ server ", "TACACS+ host ")
	reworded = strings.ReplaceAll(reworded, "TACACS+ server-group", "TACACS+ host-group")
	for _, c := range []struct{ name, out, want string }{
		{"recorded", string(tacacs), parse.Collected},
		{"nothing configured", "Last time counters were cleared: never\n", parse.Empty},
		{"reworded", reworded, parse.ParseFailed},
	} {
		got, _, err := parse.Parse(reg, "arista_eos", "aaa_servers", im, [][]byte{[]byte(c.out), radius})
		if got != c.want {
			t.Errorf("%s: %s (%v), want %s", c.name, got, err, c.want)
		}
	}
}

// The same for interface_vlans (feature 008, US3): a status table with a Vlan word the pack has
// never recorded, a reworded trunk message and a spine table with one switched port are not empty.
func TestVLANDriftIsNotEmpty(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "interface_vlans", "4.36.0F")[0]
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("arista_eos", "testdata", "lab", name+".raw"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	status, trunk := read("sw1_show_interfaces_status_vlan"), read("sw1_show_interfaces_trunk")
	spine, noTrunk := read("dc-spine1_show_interfaces_status_vlan_empty"), read("dc-spine1_show_interfaces_trunk_empty")
	for _, c := range []struct{ name, status, trunk, want string }{
		{"recorded", status, trunk, parse.Collected},
		{"spine", spine, noTrunk, parse.Empty},
		{"unknown Vlan word", status + "Et9               connected    foo      full   1G     EbraTestPhyPort\n", trunk, parse.ParseFailed},
		{"reworded trunk message", spine, "There are no trunk ports\n", parse.ParseFailed},
		{"spine with one access port", spine + "Et11              connected    10       full   1G     EbraTestPhyPort\n", noTrunk, parse.Collected},
	} {
		got, _, err := parse.Parse(reg, "arista_eos", "interface_vlans", im, [][]byte{[]byte(c.status), []byte(c.trunk)})
		if got != c.want {
			t.Errorf("%s: %s (%v), want %s", c.name, got, err, c.want)
		}
	}
}

// An EOS switch with no SNMP community and no user still prints "SNMP agent enabled in VRFs:
// default", then "SNMP agent disabled" (recorded on test/lab sw1, cEOS 4.36). The agent row must say
// enabled: no and list no VRF.
func TestSNMPAgentDisabled(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "management_apis", "4.36.0F")[0]
	var outputs [][]byte
	for _, st := range im.Steps {
		name := "sw1_" + strings.TrimSuffix(st.Template, ".textfsm") + ".raw"
		if st.Template == "show_snmp.textfsm" {
			name = "sw1nosnmp_show_snmp.raw"
		}
		b, err := os.ReadFile(filepath.Join("arista_eos/testdata/lab", name))
		if os.IsNotExist(err) {
			b, err = os.ReadFile(filepath.Join("arista_eos/testdata/lab", strings.TrimSuffix(name, ".raw")+"_empty.raw"))
		}
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, b)
	}
	status, rows, err := parse.Parse(reg, "arista_eos", "management_apis", im, outputs)
	if status != parse.Collected {
		t.Fatalf("%s %v", status, err)
	}
	for _, r := range rows {
		if r["api"] == "snmp" {
			if r["enabled"] != "no" || r["vrfs"] != nil {
				t.Errorf("snmp row %v, want enabled no and no vrfs", r)
			}
			return
		}
	}
	t.Error("no snmp row")
}

// eAPI with `protocol http` (recorded on the EVPN lab's dc-leaf1, cEOS 4.36, then reverted): a
// second eapi row says cleartext HTTP is served, which is what an audit looks for (issue #25).
func TestEAPIOverHTTP(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "management_apis", "4.36.0F")[0]
	var outputs [][]byte
	for _, st := range im.Steps {
		base := strings.TrimSuffix(st.Template, ".textfsm")
		sw := "dc-leaf1"
		if strings.HasPrefix(base, "show_management_api_http") {
			sw = "dc-leaf1http"
		}
		b, err := os.ReadFile(filepath.Join("arista_eos/testdata/lab", sw+"_"+base+".raw"))
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, b)
	}
	status, rows, err := parse.Parse(reg, "arista_eos", "management_apis", im, outputs)
	if status != parse.Collected {
		t.Fatalf("%s %v", status, err)
	}
	var eapi []string
	for _, r := range rows {
		if r["api"] == "eapi" {
			eapi = append(eapi, fmt.Sprint(r["transport"], " ", r["port"], " ", r["enabled"]))
		}
	}
	slices.Sort(eapi)
	if want := []string{"http 80 yes", "https 443 yes"}; !slices.Equal(eapi, want) {
		t.Errorf("eapi rows %v, want %v", eapi, want)
	}
}

// The management address is stored with its LLDP subtype, so an IP and a MAC are never confused.
func TestLLDPAddressKeepsItsType(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "neighbours", "4.30.1F")[0]
	out, _ := os.ReadFile("arista_eos/testdata/ntc/show_lldp_neighbors_detail.raw")
	status, rows, err := parse.Parse(reg, "arista_eos", "neighbours", im, [][]byte{out})
	if status != parse.Collected {
		t.Fatalf("%s %v", status, err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[fmt.Sprint(r["remote_mgmt_address_type"], " ", r["remote_mgmt_address"])] = true
	}
	for _, want := range []string{"mac 2c:c2:60:81:ea:f9", "ipv4 10.0.0.31", "ipv6 fe80::250:56ff:feac:4cd9"} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, seen)
		}
	}
}

// No recorded lab output holds a secret (feature 007, FR-014). Every output under testdata/lab is
// what a recipe would store as evidence and the API would serve, so a secret here is a secret in
// the database. The lab secrets are listed by their distinctive values; `admin` and `public`, the
// older test/lab values, are left out because they are ordinary words in legitimate output
// (`role network-admin`, `ssh public key`), and they cannot reach the new outputs anyway. The
// patterns catch a hash or a type 7 key nobody thought to list.
func TestNoSecretInRecordedOutput(t *testing.T) {
	literals := []string{"nm-lab-secret-", "lab-auth-sw2", "lab-priv-sw2", "evpnlab-"}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\$[156]\$`),
		regexp.MustCompile(`sha512 \$`),
		regexp.MustCompile(`key 7 [0-9A-Fa-f]{6,}`),
		regexp.MustCompile(`password 7 \S+`), // a BGP neighbour password as EOS stores it (feature 010)
		// OSPF keys as EOS stores them, base64 after 7, which the hex pattern above misses (feature 011).
		regexp.MustCompile(`(md5|sha\d*) 7 \S+`),
		regexp.MustCompile(`authentication-key 7 \S+`),
	}
	// Every recorded output: the lab recordings and the ntc-templates samples (testdata/ntc).
	raws, _ := filepath.Glob(filepath.Join("*", "testdata", "*", "*.raw"))
	if len(raws) == 0 {
		t.Fatal("no recorded output found")
	}
	if ntc, _ := filepath.Glob(filepath.Join("*", "testdata", "ntc", "*.raw")); len(ntc) == 0 {
		t.Fatal("no ntc sample found: the scan no longer covers testdata/ntc")
	}
	for _, raw := range raws {
		b, err := os.ReadFile(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range literals {
			if strings.Contains(string(b), s) {
				t.Errorf("%s holds %q", raw, s)
			}
		}
		for _, re := range patterns {
			if m := re.Find(b); m != nil {
				t.Errorf("%s holds %q (%s)", raw, m, re)
			}
		}
	}
}

// Each <switch>_<family>.facts.yml under testdata/lab is what the family's recipe must produce from
// that switch's recordings, one per step, through the whole parse: step maps, values, split,
// defaults, merge and schema validation (feature 007, research R11). No lab needed.
func TestFamiliesFromLab(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join("*", "testdata", "lab", "*.facts.yml"))
	if len(files) == 0 {
		t.Fatal("no .facts.yml found")
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			platform := strings.Split(f, string(filepath.Separator))[0]
			sw, family, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".facts.yml"), "_")
			im := reg.Implementations(platform, family, "4.36.0F")
			if len(im) == 0 {
				t.Fatalf("no %s implementation for 4.36.0F", family)
			}
			var outputs [][]byte
			for _, st := range im[0].Steps {
				base := filepath.Join(filepath.Dir(f), sw+"_"+strings.TrimSuffix(st.Template, ".textfsm"))
				b, err := os.ReadFile(base + ".raw")
				if os.IsNotExist(err) {
					b, err = os.ReadFile(base + "_empty.raw")
				}
				if err != nil {
					t.Fatalf("step %q: %v", st.Command, err)
				}
				outputs = append(outputs, b)
			}
			status, rows, err := parse.Parse(reg, platform, family, im[0], outputs)
			b, _ := os.ReadFile(f)
			var want struct {
				Status string           `yaml:"status"`
				Rows   []map[string]any `yaml:"rows"`
			}
			if err := yaml.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			if status != want.Status {
				t.Fatalf("status %s (%v), want %s", status, err, want.Status)
			}
			// Rows compare as a multiset: order is not part of the contract, a duplicate row is.
			got, exp := canonical(t, rows), canonical(t, want.Rows)
			if !slices.Equal(got, exp) {
				t.Errorf("rows\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(exp, "\n     "))
			}
		})
	}
}

// canonical renders each row as JSON (sorted keys, numbers and lists as YAML reads them) and sorts.
func canonical(t *testing.T, rows []map[string]any) []string {
	out := []string{}
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

// Internal VLANs a switch allocates for its routed ports are not declared VLANs (feature 008,
// FR-003). show vlan internal usage is recorded beside show vlan as evidence: none of the IDs it
// lists may be a vlans row.
func TestInternalVLANsAreNotDeclared(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "vlans", "4.36.0F")[0]
	for _, sw := range []string{"sw2", "dc-leaf1"} {
		base := filepath.Join("arista_eos", "testdata", "lab", sw)
		usage, _ := os.ReadFile(base + "_show_vlan_internal_usage.raw")
		fsm := gotextfsm.TextFSM{}
		if err := fsm.ParseString(reg.Pack("arista_eos").Templates["show_vlan_internal_usage.textfsm"]); err != nil {
			t.Fatal(err)
		}
		po := gotextfsm.ParserOutput{}
		if err := po.ParseTextString(string(usage), fsm, true); err != nil {
			t.Fatal(err)
		}
		if len(po.Dict) == 0 {
			t.Fatalf("%s: the recording lists no internal VLAN, so this test proves nothing", sw)
		}
		out, _ := os.ReadFile(base + "_show_vlan.raw")
		_, rows, err := parse.Parse(reg, "arista_eos", "vlans", im, [][]byte{out})
		if err != nil {
			t.Fatal(err)
		}
		for _, internal := range po.Dict {
			for _, r := range rows {
				if fmt.Sprint(r["vlan_id"]) == internal["VLAN_ID"] {
					t.Errorf("%s: internal VLAN %v (%v) is a vlans row", sw, internal["VLAN_ID"], internal["INTERFACE"])
				}
			}
		}
	}
}

// The trunk and port variants recorded on sw1 by changing one setting at a time (feature 008,
// tasks T006): each is parsed with the interface_vlans recipe and checked for the port it changed.
func TestTrunkVariants(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "interface_vlans", "4.36.0F")[0]
	dir := filepath.Join("arista_eos", "testdata", "lab")
	evens := []string{}
	for v := 2; v <= 200; v += 2 {
		evens = append(evens, fmt.Sprint(v))
	}
	for _, c := range []struct {
		status, trunk, port string
		want                map[string]any
	}{
		// allowed vlan none: the trunk is still listed, with None in both lists.
		{"sw1_show_interfaces_status_vlan", "sw1_show_interfaces_trunk_none", "Ethernet1",
			map[string]any{"interface": "Ethernet1", "mode": "trunk", "native_vlan": int64(99)}},
		// Every even VLAN from 2 to 200, wrapped by EOS over eight lines (research R5).
		{"sw1_show_interfaces_status_vlan", "sw1_show_interfaces_trunk_wrapped", "Ethernet1",
			map[string]any{"interface": "Ethernet1", "mode": "trunk", "native_vlan": int64(99), "allowed_vlans": evens,
				"active_vlans": []string{"10", "20", "30", "32", "40", "42", "44", "46", "48"}}},
		// A shut trunk is absent from the trunk view: mode only (research R2).
		{"sw1_show_interfaces_status_vlan_po10_down", "sw1_show_interfaces_trunk_po10_down", "Port-Channel10",
			map[string]any{"interface": "Port-Channel10", "mode": "trunk"}},
		{"sw1_show_interfaces_status_vlan_po10_down", "sw1_show_interfaces_trunk_po10_down", "Ethernet2",
			map[string]any{"interface": "Ethernet2", "channel": "Port-Channel10"}},
		// A shut access port keeps its VLAN.
		{"sw1_show_interfaces_status_vlan_access_down", "sw1_show_interfaces_trunk", "Ethernet4",
			map[string]any{"interface": "Ethernet4", "mode": "access", "access_vlan": int64(10)}},
		// dot1q-tunnel is printed like an access port in its outer VLAN, and read so (research R3).
		{"sw1_show_interfaces_status_vlan_tunnel", "sw1_show_interfaces_trunk", "Ethernet4",
			map[string]any{"interface": "Ethernet4", "mode": "access", "access_vlan": int64(10)}},
	} {
		var outputs [][]byte
		for _, f := range []string{c.status, c.trunk} {
			b, err := os.ReadFile(filepath.Join(dir, f+".raw"))
			if err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, b)
		}
		_, rows, err := parse.Parse(reg, "arista_eos", "interface_vlans", im, outputs)
		if err != nil {
			t.Fatalf("%s + %s: %v", c.status, c.trunk, err)
		}
		var got map[string]any
		for _, r := range rows {
			if r["interface"] == c.port {
				got = r
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s + %s, %s:\n got %v\nwant %v", c.status, c.trunk, c.port, got, c.want)
		}
	}
}

// A description can hold a status word and a token that looks like a Vlan column ("connected in
// rack3", "connected 10"): the Vlan column is the one after the last status word, as in the
// interfaces template (feature 008, found in review). Inputs that tie on purpose.
func TestDescriptionIsNotTheVlanColumn(t *testing.T) {
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", "interface_vlans", "4.36.0F")[0]
	status := `Port       Name                Status       Vlan     Duplex Speed  Type            Flags Encapsulation
Et2        to sw2 connected 10 connected    in Po10  full   1G     EbraTestPhyPort
Et4        connected in rack3  connected    10       full   1G     EbraTestPhyPort
Et5        connected 20        connected    routed   full   1G     EbraTestPhyPort
Ma0                            connected    routed   a-full a-1G   10/100/1000
`
	_, rows, err := parse.Parse(reg, "arista_eos", "interface_vlans", im, [][]byte{[]byte(status), []byte("There are no active trunk ports\n")})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"interface": "Ethernet2", "channel": "Port-Channel10"},
		{"interface": "Ethernet4", "mode": "access", "access_vlan": int64(10)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v\nwant %v", rows, want)
	}
}

// bgpOutputs reads one recording per step of the bgp_peers recipe for sw, with the files named in
// override replacing the step whose template they name (feature 010).
func bgpOutputs(t *testing.T, im pack.Impl, sw string, override ...string) [][]byte {
	t.Helper()
	var outs [][]byte
	for _, st := range im.Steps {
		tmpl := strings.TrimSuffix(st.Template, ".textfsm")
		base := filepath.Join("arista_eos", "testdata", "lab", sw+"_"+tmpl)
		paths := []string{base + ".raw", base + "_empty.raw"}
		for _, o := range override {
			if strings.Contains(o, tmpl+"_") || strings.HasSuffix(o, tmpl+".raw") {
				paths = []string{filepath.Join("arista_eos", "testdata", "lab", o)}
			}
		}
		var b []byte
		var err error
		for _, p := range paths {
			if b, err = os.ReadFile(p); err == nil {
				break
			}
		}
		if err != nil {
			t.Fatalf("%s step %q: %v", sw, st.Command, err)
		}
		outs = append(outs, b)
	}
	return outs
}

func bgpImpl(t *testing.T) (*pack.Registry, pack.Impl) {
	t.Helper()
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	return reg, reg.Implementations("arista_eos", "bgp_peers", "4.36.0F")[0]
}

// FR-005d: the summary steps in another order give the same rows (feature 010).
func TestBGPStepOrder(t *testing.T) {
	reg, im := bgpImpl(t)
	outs := bgpOutputs(t, im, "dc-leaf1")
	_, want, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, outs)
	if err != nil {
		t.Fatal(err)
	}
	rev := im
	rev.Steps = slices.Clone(im.Steps)
	revOuts := slices.Clone(outs)
	slices.Reverse(rev.Steps[1:4])
	slices.Reverse(revOuts[1:4])
	_, got, err := parse.Parse(reg, "arista_eos", "bgp_peers", rev, revOuts)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := canonical(t, got), canonical(t, want); !slices.Equal(g, w) {
		t.Errorf("reversed summaries\n got %s\nwant %s", strings.Join(g, "\n     "), strings.Join(w, "\n     "))
	}
}

// Research R4: sw1 with its password session shut after it was up. EOS still prints the last
// remote router ID; the row keeps neither it nor a negotiated list, and keeps its configured list.
func TestBGPSessionThatWentDown(t *testing.T) {
	reg, im := bgpImpl(t)
	b, _ := os.ReadFile(filepath.Join("arista_eos", "testdata", "lab", "sw1_show_bgp_neighbors_vrf_all_down.raw"))
	if !strings.Contains(string(b), "remote router ID 10.255.0.2") {
		t.Fatal("the recording no longer shows the stale router ID, so this test proves nothing")
	}
	s, rows, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, "sw1", "sw1_show_bgp_neighbors_vrf_all_down.raw"))
	if s != parse.Collected {
		t.Fatalf("%s %v", s, err)
	}
	for _, r := range rows {
		if r["peer_address"] != "10.0.10.2" {
			continue
		}
		_, neg := r["negotiated_address_families"]
		_, id := r["peer_router_id"]
		if r["shutdown"] != "yes" || r["state"] == "established" || neg || id ||
			!reflect.DeepEqual(r["configured_address_families"], []string{"ipv4 unicast", "ipv6 unicast"}) {
			t.Errorf("row %v", r)
		}
		return
	}
	t.Error("no row for 10.0.10.2")
}

// FR-009: only the exact "nothing here" answers are empty. A refusal, an invalid command or
// "% BGP inactive" next to anything else is parse_failed, so a diff never reads an error as
// sessions that disappeared (feature 010, research R8).
func TestBGPErrorsAreNotEmpty(t *testing.T) {
	reg, im := bgpImpl(t)
	lab := filepath.Join("arista_eos", "testdata", "lab")
	denied, _ := os.ReadFile(filepath.Join(lab, "sw2_show_bgp_neighbors_vrf_all_denied_empty.raw"))
	invalid, _ := os.ReadFile(filepath.Join(lab, "sw1_show_bgp_evpn_summary_invalid_empty.raw"))
	for _, bad := range [][]byte{denied, invalid, []byte("% BGP inactive\nsomething else\n")} {
		for i := range im.Steps {
			outs := bgpOutputs(t, im, "sw1")
			outs[i] = bad
			if s, rows, _ := parse.Parse(reg, "arista_eos", "bgp_peers", im, outs); s != parse.ParseFailed || rows != nil {
				t.Errorf("step %d given %q: %s with %d rows, want parse_failed", i+1, bad, s, len(rows))
			}
		}
	}
	if s, rows, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, "sw4")); s != parse.Empty || rows != nil {
		t.Errorf("sw4: %s %v %v, want empty", s, err, rows)
	}
	// sw2 after its dynamic peer left: the peer group lists no member, which is nothing to report,
	// not an error. Paired with sw2's neighbour view, which still lists the peer in group DYN, this
	// is the leave case of #44: the peer's row is now dropped and reported as a disagreement
	// (lists_every: peer_group, research R14). Before, it was kept with the default dynamic: no.
	s, rows, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, "sw2", "sw2_show_bgp_peer_group_nomembers_empty.raw"))
	if s != parse.Collected {
		t.Fatalf("sw2 with no member: %s %v", s, err)
	}
	for _, r := range rows {
		if r["dynamic"] == "yes" {
			t.Errorf("dynamic row with no member listed: %v", r)
		}
	}
}

// Issue #45: a summary line the template cannot read, next to peers it can, is parse_failed in every
// family summary. Before, the line was skipped, the step still gave rows, and the peer came out with
// that family missing from its configured list, as the link-local peer of #31 did. A "%" line
// between readable peers, such as a warning, fails the same way.
func TestBGPUnreadableSummaryLine(t *testing.T) {
	reg, im := bgpImpl(t)
	ipv4, err := os.ReadFile(filepath.Join("arista_eos", "testdata", "lab", "sw1_show_ip_bgp_summary_vrf_all.raw"))
	if err != nil {
		t.Fatal(err)
	}
	peer := "                           10.255.0.2  4 65199"
	if !strings.Contains(string(ipv4), peer) {
		t.Fatal("the recording no longer lists 10.255.0.2, so the warning is not between two peers")
	}
	bad := map[string][]byte{
		"an unreadable peer line": append(slices.Clone(ipv4), "                           10.0.10.98  4 1.10                 0         0    0    0 00:01:26 Connect\n"...),
		"a % line between peers":  []byte(strings.Replace(string(ipv4), peer, "% Some warning\n"+peer, 1)),
	}
	for i, st := range im.Steps {
		if !strings.Contains(st.Template, "summary") {
			continue
		}
		outs := bgpOutputs(t, im, "sw1")
		outs[i] = ipv4
		if s, _, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, outs); s != parse.Collected {
			t.Fatalf("%s given the readable summary: %s %v", st.Command, s, err)
		}
		for what, b := range bad {
			outs[i] = b
			if s, rows, _ := parse.Parse(reg, "arista_eos", "bgp_peers", im, outs); s != parse.ParseFailed || rows != nil {
				t.Errorf("%s given %s: %s with %d rows, want parse_failed", st.Command, what, s, len(rows))
			}
		}
	}
}

// Issue #52: a peer whose "BGP neighbor is" line the template cannot read, here a remote AS in
// asdot, is parse_failed. Before, the peer got no row, the step was still collected, and the family
// summaries that list it raised it as a peer that connected during the collection (#44).
func TestBGPUnreadableNeighbor(t *testing.T) {
	reg, im := bgpImpl(t)
	outs := bgpOutputs(t, im, "sw1")
	header := "BGP neighbor is 10.255.0.2, remote AS 65199, external link"
	if !strings.Contains(string(outs[0]), header) {
		t.Fatal("the recording no longer lists 10.255.0.2, so the summaries do not list the peer either")
	}
	outs[0] = []byte(strings.Replace(string(outs[0]), header, "BGP neighbor is 10.255.0.2, remote AS 1.10, external link", 1))
	s, rows, _, disagree, _ := parse.ParseDetail(reg, "arista_eos", "bgp_peers", im, outs)
	if s != parse.ParseFailed || rows != nil || len(disagree) > 0 {
		t.Errorf("%s with %d rows and disagreements %v, want parse_failed", s, len(rows), disagree)
	}
}

// Edge case "a peer relying on the device's default" (convergence T039): sw2 with the explicit
// activate removed still lists 10.0.10.1 in its ipv4 summary, so the configured list holds ipv4
// unicast.
func TestBGPDefaultActivation(t *testing.T) {
	reg, im := bgpImpl(t)
	s, rows, err := parse.Parse(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, "sw2", "sw2_show_ip_bgp_summary_vrf_all_defaultactive.raw"))
	if s != parse.Collected {
		t.Fatalf("%s %v", s, err)
	}
	for _, r := range rows {
		if r["peer_address"] == "10.0.10.1" {
			if !reflect.DeepEqual(r["configured_address_families"], []string{"ipv4 unicast"}) {
				t.Errorf("row %v", r)
			}
			return
		}
	}
	t.Error("no row for 10.0.10.1")
}

// Issue #44, research R14: sw2 recorded while its dynamic peer connected, then while it left, during
// the collection. The peer gets no row and is reported once, under the first command that
// disagreed with the neighbour view; the other session is kept (sw2connect and sw2leave facts.yml).
func TestBGPPeerChurn(t *testing.T) {
	reg, im := bgpImpl(t)
	for sw, cmd := range map[string]string{
		"sw2connect": "show ip bgp summary vrf all | no-more",
		"sw2leave":   "show bgp peer-group | no-more",
	} {
		s, rows, _, disagree, err := parse.ParseDetail(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, sw))
		if s != parse.Collected || len(rows) != 1 {
			t.Errorf("%s: %s with %d rows (%v), want collected with 1", sw, s, len(rows), err)
		}
		want := []parse.Disagreement{{Command: cmd, Row: "vrf=default, peer_address=10.255.1.1"}}
		if !reflect.DeepEqual(disagree, want) {
			t.Errorf("%s: disagreements %v, want %v", sw, disagree, want)
		}
	}
	// The steady recordings disagree about nothing.
	for _, sw := range []string{"sw1", "sw2", "dc-leaf1", "core1"} {
		if _, _, _, disagree, _ := parse.ParseDetail(reg, "arista_eos", "bgp_peers", im, bgpOutputs(t, im, sw)); len(disagree) > 0 {
			t.Errorf("%s: disagreements %v, want none", sw, disagree)
		}
	}
}

func ospfImpl(t *testing.T, family string) (*pack.Registry, pack.Impl) {
	t.Helper()
	reg, err := pack.LoadRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	im := reg.Implementations("arista_eos", family, "4.36.0F")
	if len(im) != 1 {
		t.Fatalf("%s: %d implementations", family, len(im))
	}
	return reg, im[0]
}

var ospfFamilies = []string{"ospf_interfaces", "ospf_neighbours"}

// SC-005 (offline part): every OSPF row names its interface in canonical form, the form the
// interfaces family uses. The interfaces recordings of 007 predate the subinterface, so the join
// itself is checked live (quickstart section 4).
func TestOSPFInterfacesJoinInterfaces(t *testing.T) {
	for _, family := range ospfFamilies {
		reg, im := ospfImpl(t, family)
		for _, sw := range []string{"core1", "sw1"} {
			_, rows, err := parse.Parse(reg, "arista_eos", family, im, bgpOutputs(t, im, sw))
			if err != nil || len(rows) == 0 {
				t.Fatalf("%s %s: %v %v", sw, family, rows, err)
			}
			for _, r := range rows {
				name := r["interface"].(string)
				if reg.Normalise("arista_eos", name) != name || strings.HasPrefix(name, "Et") && !strings.HasPrefix(name, "Ethernet") {
					t.Errorf("%s %s: interface %q is not canonical", sw, family, name)
				}
			}
		}
	}
}

// Spec story 2 scenario 2, FR-007: the same recordings give the same rows, and the adjacency held
// by the MTU mismatch has a fixture that is not full.
func TestOSPFSteadyState(t *testing.T) {
	for _, family := range ospfFamilies {
		reg, im := ospfImpl(t, family)
		_, first, err := parse.Parse(reg, "arista_eos", family, im, bgpOutputs(t, im, "sw1"))
		_, second, _ := parse.Parse(reg, "arista_eos", family, im, bgpOutputs(t, im, "sw1"))
		if err != nil || !reflect.DeepEqual(canonical(t, first), canonical(t, second)) {
			t.Errorf("%s: two parses differ or fail: %v", family, err)
		}
		if family != "ospf_neighbours" {
			continue
		}
		for _, r := range first {
			if r["interface"] == "Vlan44" && r["state"] == "full" {
				t.Errorf("the MTU mismatch adjacency reads full: %v", r)
			}
		}
	}
}

// FR-009, research R7 and R14: an error is never empty, a blank guard is a failure, the guard's
// "none" decides empty, and a blank interface view while the guard counts interfaces is a failure.
func TestOSPFErrorsAreNotEmpty(t *testing.T) {
	lab := filepath.Join("arista_eos", "testdata", "lab")
	denied, _ := os.ReadFile(filepath.Join(lab, "sw2_show_ip_ospf_interface_vrf_all_denied_empty.raw"))
	invalid, _ := os.ReadFile(filepath.Join(lab, "sw1_show_ip_ospf_neighbor_detail_vrf_all_invalid_empty.raw"))
	if len(denied) == 0 || len(invalid) == 0 {
		t.Fatal("error recordings missing")
	}
	for _, family := range ospfFamilies {
		reg, im := ospfImpl(t, family)
		for _, bad := range [][]byte{denied, invalid} {
			for i := range im.Steps {
				outs := bgpOutputs(t, im, "sw1")
				outs[i] = bad
				if s, rows, _ := parse.Parse(reg, "arista_eos", family, im, outs); s != parse.ParseFailed || rows != nil {
					t.Errorf("%s step %d given %q: %s with %d rows, want parse_failed", family, i+1, bad, s, len(rows))
				}
			}
		}
		outs := bgpOutputs(t, im, "sw1")
		outs[0] = []byte("\n")
		if s, _, _ := parse.Parse(reg, "arista_eos", family, im, outs); s != parse.ParseFailed {
			t.Errorf("%s: blank guard gave %s, want parse_failed", family, s)
		}
		if s, rows, err := parse.Parse(reg, "arista_eos", family, im, bgpOutputs(t, im, "sw4")); s != parse.Empty || rows != nil {
			t.Errorf("%s sw4: %s %v %v, want empty", family, s, err, rows)
		}
		outs = bgpOutputs(t, im, "sw1")
		outs[0] = bgpOutputs(t, im, "sw4")[0] // the guard says no OSPF, the other steps disagree
		if s, _, _ := parse.Parse(reg, "arista_eos", family, im, outs); s != parse.ParseFailed {
			t.Errorf("%s: sw4's guard with sw1's views gave %s, want parse_failed", family, s)
		}
	}
	// sw1's guard counts interfaces: a blank interface view is a broken read (T031). A blank
	// neighbour view stays "no adjacency".
	reg, im := ospfImpl(t, "ospf_interfaces")
	outs := bgpOutputs(t, im, "sw1")
	outs[1] = nil
	if s, _, err := parse.Parse(reg, "arista_eos", "ospf_interfaces", im, outs); s != parse.ParseFailed || !strings.Contains(err.Error(), "the guard counts 5") {
		t.Errorf("blank interface view with a count: %s %v", s, err)
	}
	reg, im = ospfImpl(t, "ospf_neighbours")
	outs = bgpOutputs(t, im, "sw1")
	outs[1] = nil
	if s, _, err := parse.Parse(reg, "arista_eos", "ospf_neighbours", im, outs); s != parse.Empty {
		t.Errorf("blank neighbour view: %s %v, want empty", s, err)
	}
}
