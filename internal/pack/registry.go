// Package pack loads platform packs (contracts/pack-format.md). With the parser, it is the only
// code aware of a vendor, and it learns everything it knows from pack data (principle V).
package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sirikothe/gotextfsm"
	"go.yaml.in/yaml/v3"

	"github.com/darnodo/NetMapper/internal/fact"
)

type Pack struct {
	Name     string   `yaml:"name"`
	Version  int      `yaml:"version"`
	ReadOnly []string `yaml:"read_only"`
	Probe    *struct {
		SNMP []ProbeOID `yaml:"snmp"`
	} `yaml:"probe"`
	Fingerprint struct {
		SNMP []struct {
			SysObjectIDPrefix string `yaml:"sys_object_id_prefix"`
			VersionRegex      string `yaml:"version_regex"`
		} `yaml:"snmp"`
		SSH []struct {
			Command      string `yaml:"command"`
			Match        string `yaml:"match"`
			VersionRegex string `yaml:"version_regex"`
		} `yaml:"ssh"`
	} `yaml:"fingerprint"`
	Identifiers    []Identifier `yaml:"identifiers"`
	InterfaceNames []struct {
		Match   string `yaml:"match"`
		Replace string `yaml:"replace"`
	} `yaml:"interface_names"`

	Hash      string             // version hash of every file in the pack, for recipe_id (FR-011)
	Recipes   map[string]*Recipe // by family
	Templates map[string]string  // file name -> TextFSM source
	Scrapli   []byte             // scrapligo platform definition, nil if the pack has none
}

type ProbeOID struct {
	Name string `yaml:"name"`
	OID  string `yaml:"oid"`
}

type Identifier struct {
	Kind      string `yaml:"kind"`
	Strength  string `yaml:"strength"`
	Normalise string `yaml:"normalise"`
	SSH       *struct {
		Command string `yaml:"command"`
		Regex   string `yaml:"regex"`
	} `yaml:"ssh"`
	SNMP *struct {
		OID string `yaml:"oid"`
	} `yaml:"snmp"`
}

type Recipe struct {
	Family          string `yaml:"family"`
	Implementations []Impl `yaml:"implementations"`
}

type Impl struct {
	ID        string            `yaml:"id"`
	Versions  string            `yaml:"versions"`
	Transport string            `yaml:"transport"`
	Steps     []Step            `yaml:"steps"`
	Map       map[string]string `yaml:"map"`
	// Values translates a device spelling into the schema's, per field; "*" is the fallback.
	Values  map[string]map[string]string `yaml:"values"`
	MergeOn []string                     `yaml:"merge_on"`
}

type Step struct {
	Command  string `yaml:"command"`
	Template string `yaml:"template"`
	Walk     string `yaml:"walk"`
	// EmptyLines are the lines this command prints when it has nothing to report. Output whose
	// every non-blank line matches one of them is empty, not a parse failure (research R9).
	EmptyLines []string `yaml:"empty_lines"`
}

// Registry is the set of loaded packs, in load order.
type Registry struct {
	packs []*Pack
}

// LoadRoot loads every pack directory under root.
func LoadRoot(root string) (*Registry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	return Load(dirs...)
}

// missingCost says what a platform gives up without a recipe for a family, for the load warning.
var missingCost = map[string]string{
	"neighbours": "the crawl will not expand from these devices and they report no link",
	"interfaces": "only ports named by a neighbour will exist, with no description, state, speed or MTU",
}

// Load loads the given pack directories and refuses the whole set if any check fails. A platform
// pack missing a family's recipe still loads, with one warning per missing family.
func Load(dirs ...string) (*Registry, error) {
	r := &Registry{}
	var errs []error
	names := map[string]bool{}
	probes := 0
	for _, dir := range dirs {
		p, err := loadPack(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("pack %s: %w", dir, err))
			continue
		}
		if names[p.Name] {
			errs = append(errs, fmt.Errorf("pack %s: name %q is already loaded", dir, p.Name))
		}
		names[p.Name] = true
		if p.Probe != nil {
			probes++
			if p.Name != "_base" {
				errs = append(errs, fmt.Errorf("pack %s: only _base may declare probe", p.Name))
			}
		}
		r.packs = append(r.packs, p)
	}
	if probes != 1 || !names["_base"] {
		errs = append(errs, errors.New("exactly one pack named _base must declare probe"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, p := range r.packs {
		if len(p.Fingerprint.SNMP)+len(p.Fingerprint.SSH) == 0 {
			continue // matches no device, so it collects nothing
		}
		for _, f := range slices.Sorted(maps.Keys(fact.Families)) {
			if f != "identity" && p.Recipes[f] == nil {
				slog.Warn("pack has no recipe for a fact family", "pack", p.Name, "family", f, "effect", missingCost[f])
			}
		}
	}
	return r, nil
}

func loadPack(dir string) (*Pack, error) {
	p := &Pack{Recipes: map[string]*Recipe{}, Templates: map[string]string{}}
	if err := readYAML(filepath.Join(dir, "pack.yaml"), p); err != nil {
		return nil, err
	}
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if p.Name == "" {
		fail("name is required")
	}

	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
		switch {
		case strings.HasPrefix(rel, "templates"+string(filepath.Separator)):
			p.Templates[filepath.Base(rel)] = string(b)
		case strings.HasPrefix(rel, "recipes"+string(filepath.Separator)) && strings.HasSuffix(rel, ".yaml"):
			rc := &Recipe{}
			if err := yaml.Unmarshal(b, rc); err != nil {
				fail("%s: %v", rel, err)
				return nil
			}
			p.Recipes[rc.Family] = rc
		case rel == "scrapli.yaml":
			p.Scrapli = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.Hash = hex.EncodeToString(h.Sum(nil))[:12]

	readOnly := func(where, cmd string) {
		for _, prefix := range p.ReadOnly {
			if strings.HasPrefix(cmd, prefix) {
				return
			}
		}
		fail("%s: command %q does not start with an entry of read_only", where, cmd)
	}
	compiles := func(where, re string) {
		if _, err := regexp.Compile(re); err != nil {
			fail("%s: %v", where, err)
		}
	}
	for _, f := range p.Fingerprint.SSH {
		readOnly("fingerprint", f.Command)
		compiles("fingerprint", f.Match)
		compiles("fingerprint", f.VersionRegex)
	}
	for _, f := range p.Fingerprint.SNMP {
		compiles("fingerprint", f.VersionRegex)
	}
	for _, id := range p.Identifiers {
		if id.Strength != "strong" && id.Strength != "weak" {
			fail("identifier %s: strength must be strong or weak", id.Kind)
		}
		if id.SSH != nil {
			readOnly("identifier "+id.Kind, id.SSH.Command)
			compiles("identifier "+id.Kind, id.SSH.Regex)
		}
	}
	for _, n := range p.InterfaceNames {
		compiles("interface_names", n.Match)
	}
	for family, rc := range p.Recipes {
		if _, ok := fact.Families[family]; !ok {
			fail("recipe for unknown fact family %q", family)
			continue
		}
		for _, im := range rc.Implementations {
			where := family + "/" + im.ID
			if im.Transport != "ssh" && im.Transport != "snmp" {
				fail("%s: transport must be ssh or snmp", where)
			}
			if len(im.Steps) == 0 {
				fail("%s: no steps", where)
			}
			for _, s := range im.Steps {
				for _, re := range s.EmptyLines {
					compiles(where+" empty_lines", re)
				}
				if s.Command != "" {
					readOnly(where, s.Command)
					src, ok := p.Templates[s.Template]
					if !ok {
						fail("%s: template %q not found", where, s.Template)
						continue
					}
					fsm := gotextfsm.TextFSM{}
					if err := fsm.ParseString(src); err != nil {
						fail("%s: template %s does not compile: %v", where, s.Template, err)
					}
				}
			}
			for field := range im.Map {
				if _, ok := fact.Lookup(family, field); !ok {
					fail("%s: map target %q is not a field of %s", where, field, family)
				}
			}
			if _, err := parseConstraint(im.Versions); err != nil {
				fail("%s: %v", where, err)
			}
		}
	}
	return p, errors.Join(errs...)
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, v)
}

// Probe is what the collector asks every target before its platform is known.
func (r *Registry) Probe() []ProbeOID {
	for _, p := range r.packs {
		if p.Probe != nil {
			return p.Probe.SNMP
		}
	}
	return nil
}

func (r *Registry) Pack(name string) *Pack {
	for _, p := range r.packs {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// Scrapli returns the scrapligo platform definition of every pack that has one.
func (r *Registry) Scrapli() map[string][]byte {
	m := map[string][]byte{}
	for _, p := range r.packs {
		if p.Scrapli != nil {
			m[p.Name] = p.Scrapli
		}
	}
	return m
}

// Evidence is what fingerprinting has learnt so far about a target.
type Evidence struct {
	SysObjectID string
	SysDescr    string
	CLI         map[string]string // command -> output
}

// FingerprintCommands lists the SSH fingerprint commands of every pack, in pack order, once each.
func (r *Registry) FingerprintCommands() []string {
	var out []string
	for _, p := range r.packs {
		for _, f := range p.Fingerprint.SSH {
			if !slices.Contains(out, f.Command) {
				out = append(out, f.Command)
			}
		}
	}
	return out
}

// Fingerprint returns the first pack whose rules match the evidence, and the OS version.
func (r *Registry) Fingerprint(ev Evidence) (platform, version string, ok bool) {
	for _, p := range r.packs {
		for _, f := range p.Fingerprint.SNMP {
			if ev.SysObjectID != "" && (ev.SysObjectID == f.SysObjectIDPrefix || strings.HasPrefix(ev.SysObjectID, f.SysObjectIDPrefix+".")) {
				return p.Name, capture(f.VersionRegex, ev.SysDescr, "version"), true
			}
		}
		for _, f := range p.Fingerprint.SSH {
			if out, ran := ev.CLI[f.Command]; ran && regexp.MustCompile(f.Match).MatchString(out) {
				return p.Name, capture(f.VersionRegex, out, "version"), true
			}
		}
	}
	return "", "", false
}

func capture(re, s, group string) string {
	if re == "" {
		return ""
	}
	x := regexp.MustCompile(re)
	m := x.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	if i := x.SubexpIndex(group); i > 0 {
		return m[i]
	}
	return ""
}

// ExtractSSH applies an identifier's regex to its command output.
func (id Identifier) ExtractSSH(out string) string {
	return id.normalise(capture(id.SSH.Regex, out, "value"))
}

func (id Identifier) normalise(v string) string {
	if id.Normalise == "mac" {
		return NormaliseMAC(v)
	}
	return v
}

// ExtractSNMP normalises a value read from the identifier's OID.
func (id Identifier) ExtractSNMP(v string) string { return id.normalise(v) }

// Implementations returns the implementations of family on platform whose version constraint fits
// version, in pack order.
func (r *Registry) Implementations(platform, family, version string) []Impl {
	p := r.Pack(platform)
	if p == nil || p.Recipes[family] == nil {
		return nil
	}
	var out []Impl
	for _, im := range p.Recipes[family].Implementations {
		if fits(im.Versions, version) {
			out = append(out, im)
		}
	}
	return out
}

// Families lists the fact families platform has recipes for, sorted.
func (r *Registry) Families(platform string) []string {
	p := r.Pack(platform)
	if p == nil {
		return nil
	}
	var out []string
	for f := range p.Recipes {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// RecipeID is observation.recipe_id: <pack>/<recipe id>@<pack version hash>.
func (r *Registry) RecipeID(platform string, im Impl) string {
	return platform + "/" + im.ID + "@" + r.Pack(platform).Hash
}

// Normalise returns the canonical interface name for a spelling on platform.
func (r *Registry) Normalise(platform, spelling string) string {
	if p := r.Pack(platform); p != nil {
		for _, n := range p.InterfaceNames {
			if re := regexp.MustCompile(n.Match); re.MatchString(spelling) {
				return re.ReplaceAllString(spelling, n.Replace)
			}
		}
	}
	return spelling
}

var macSeparators = strings.NewReplacer(".", "", ":", "", "-", "")

// NormaliseMAC returns the lowercase colon form, or the input unchanged if it is not a MAC.
func NormaliseMAC(s string) string {
	h := strings.ToLower(macSeparators.Replace(s))
	if len(h) != 12 {
		return s
	}
	if _, err := hex.DecodeString(h); err != nil {
		return s
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

type clause struct {
	op  string
	ver []int
}

// parseConstraint reads "", ">=4.20", or clauses joined by commas ("<5,>=4.20").
func parseConstraint(s string) ([]clause, error) {
	var out []clause
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		op := part[:len(part)-len(strings.TrimLeft(part, "<>=!"))]
		v := versionParts(strings.TrimSpace(part[len(op):]))
		if op == "" {
			op = "="
		}
		if v == nil || !slices.Contains([]string{"=", ">", "<", ">=", "<=", "!="}, op) {
			return nil, fmt.Errorf("bad version constraint %q", s)
		}
		out = append(out, clause{op, v})
	}
	return out, nil
}

// versionParts reads the leading numeric components: "4.28.3M" -> [4 28 3].
func versionParts(s string) []int {
	var out []int
	for _, f := range strings.Split(s, ".") {
		end := 0
		for end < len(f) && f[end] >= '0' && f[end] <= '9' {
			end++
		}
		n, err := strconv.Atoi(f[:end])
		if err != nil {
			break
		}
		out = append(out, n)
		if end < len(f) {
			break
		}
	}
	return out
}

func fits(constraint, version string) bool {
	cs, _ := parseConstraint(constraint)
	if len(cs) == 0 {
		return true
	}
	v := versionParts(version)
	if v == nil {
		return false
	}
	for _, c := range cs {
		cmp := slices.Compare(v, c.ver)
		ok := map[string]bool{"=": cmp == 0, "!=": cmp != 0, ">": cmp > 0, "<": cmp < 0, ">=": cmp >= 0, "<=": cmp <= 0}[c.op]
		if !ok {
			return false
		}
	}
	return true
}
