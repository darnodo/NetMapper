// Lint every shipped pack with the collector's own loader, and parse every recorded output with
// its template (FR-024, constitution: parser changes are verified over stored output).
package packs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	if len(po.Dict) == 0 {
		t.Fatalf("%s yielded no row", tmpl)
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
