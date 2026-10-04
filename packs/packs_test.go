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
	fsm := gotextfsm.TextFSM{}
	if err := fsm.ParseString(p.Templates[tmpl]); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(raw)
	po := gotextfsm.ParserOutput{}
	if err := po.ParseTextString(string(out), fsm, true); err != nil {
		t.Fatal(err)
	}
	// A recording named _empty is a device with nothing to report (feature 007): no row is right.
	if empty := strings.Contains(filepath.Base(raw), "_empty"); empty != (len(po.Dict) == 0) {
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
	}
	raws, _ := filepath.Glob(filepath.Join("*", "testdata", "lab", "*.raw"))
	if len(raws) == 0 {
		t.Fatal("no recorded lab output found")
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
