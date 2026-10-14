// Package parse turns raw device output into fact family rows: TextFSM templates for CLI output,
// column mapping for SNMP walks, then the pack's field mapping. It reads only stored bytes, so a
// replay can run it again without a device (FR-011).
package parse

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sirikothe/gotextfsm"

	"github.com/darnodo/NetMapper/internal/fact"
	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/transport"
)

const (
	Collected   = "collected"
	Empty       = "empty"
	ParseFailed = "parse_failed"
)

// Parse maps outputs (one per step of im) into rows of family. Empty output is Empty; a step that
// yields no row from non-empty output is ParseFailed (research R9), with err saying why.
func Parse(reg *pack.Registry, platform, family string, im pack.Impl, outputs [][]byte) (status string, rows []map[string]any, err error) {
	p := reg.Pack(platform)
	empty := true
	for i, st := range im.Steps {
		fields := im.Map
		if len(st.Map) > 0 {
			fields = map[string]string{} // im.Map may be nil: a recipe can map per step only
			maps.Copy(fields, im.Map)
			maps.Copy(fields, st.Map)
		}
		var raw []map[string]any
		switch {
		case st.Walk != "":
			raw, err = walkRows(outputs[i], fields)
		case nothing(st.EmptyLines, outputs[i]):
		default:
			raw, err = textFSM(p.Templates[st.Template], outputs[i])
			if err == nil && len(raw) == 0 {
				err = fmt.Errorf("%s: template %s yielded no row", st.Command, st.Template)
			}
		}
		if err != nil {
			return ParseFailed, nil, err
		}
		if len(raw) == 0 {
			continue
		}
		empty = false
		var mapped []map[string]any
		for _, r := range raw {
			m, err := mapRow(reg, platform, family, im, fields, r)
			if err != nil {
				return ParseFailed, nil, err
			}
			mapped = append(mapped, m)
		}
		rows = merge(rows, mapped, im.MergeOn)
	}
	if empty {
		return Empty, nil, nil
	}
	for _, row := range rows {
		for field, v := range im.Defaults {
			if _, set := row[field]; !set {
				row[field] = v
			}
		}
	}
	settle(family, rows)
	if err := fact.Validate(family, rows); err != nil {
		return ParseFailed, nil, err
	}
	return Collected, rows, nil
}

// settle applies the schema's row rules once every step is merged (feature 010): an OnlyWith field
// is dropped where its condition does not hold (a device may still print the last session's
// values), an EmptyList field with no item becomes [], and a Set field is sorted and deduplicated.
// It removes nothing else: a row still breaking a rule fails Validate, and the whole observation
// is parse_failed.
func settle(family string, rows []map[string]any) {
	for _, row := range rows {
		for _, f := range fact.Families[family] {
			holds := f.OnlyWith[0] == "" || row[f.OnlyWith[0]] == f.OnlyWith[1]
			if !holds {
				delete(row, f.Name)
				continue
			}
			if _, ok := row[f.Name]; !ok && f.EmptyList {
				row[f.Name] = []string{}
			}
			if l, ok := row[f.Name].([]string); ok && f.Set {
				l = slices.Clone(l)
				slices.Sort(l)
				row[f.Name] = slices.Compact(l)
			}
		}
	}
}

// nothing reports whether out says there is nothing to report: it is blank, or every non-blank
// line is one of the step's empty lines. Every line must match, so drift, such as a new header or
// a reworded message, still reaches the template and fails there.
func nothing(emptyLines []string, out []byte) bool {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !slices.ContainsFunc(emptyLines, func(re string) bool { return regexp.MustCompile(re).MatchString(line) }) {
			return false
		}
	}
	return true
}

func textFSM(template string, out []byte) ([]map[string]any, error) {
	fsm := gotextfsm.TextFSM{}
	if err := fsm.ParseString(template); err != nil {
		return nil, err
	}
	po := gotextfsm.ParserOutput{}
	if err := po.ParseTextString(string(out), fsm, true); err != nil {
		return nil, err
	}
	return po.Dict, nil
}

// walkRows groups a walk by row index: the varbind OID is <column OID>.<index>, and the columns
// are the non-literal sources of the map.
func walkRows(out []byte, m map[string]string) ([]map[string]any, error) {
	vbs, err := transport.DecodeVarbinds(out)
	if err != nil {
		return nil, err
	}
	var order []string
	rows := map[string]map[string]any{}
	for _, vb := range vbs {
		for _, col := range m {
			idx, ok := strings.CutPrefix(vb.OID, col+".")
			if strings.HasPrefix(col, "=") || !ok {
				continue
			}
			if rows[idx] == nil {
				rows[idx] = map[string]any{}
				order = append(order, idx)
			}
			rows[idx][col] = vb.Value
		}
	}
	out2 := make([]map[string]any, 0, len(order))
	for _, idx := range order {
		out2 = append(out2, rows[idx])
	}
	return out2, nil
}

// mapRow builds one fact row from one template row, with m the step's effective map. A list field
// keeps a template List as a list and cuts a string on its split separator; values translate each
// item, and a VLAN list is then normalised (feature 008). A default on a merge_on field fills the
// row here, so rows join on it; every other default fills the merged row in Parse, so a later step's
// value is not shadowed by an earlier row's default (feature 010).
func mapRow(reg *pack.Registry, platform, family string, im pack.Impl, m map[string]string, raw map[string]any) (map[string]any, error) {
	row := map[string]any{}
	for field, src := range m {
		f, _ := fact.Lookup(family, field)
		var s string
		var items []string
		if lit, ok := strings.CutPrefix(src, "="); ok {
			s = lit
		} else {
			switch v := raw[src].(type) {
			case string:
				s = v
			case []string:
				if f.Type == fact.Strings {
					items = v
				} else {
					s = strings.Join(v, " ")
				}
			}
		}
		if f.Type == fact.Strings {
			if items == nil {
				if sep := im.Split[field]; sep != "" {
					items = strings.Split(s, sep)
				} else {
					items = []string{s}
				}
			}
			var list []string
			for _, it := range items {
				if it, ok := translate(im.Values[field], strings.TrimSpace(it)); ok {
					list = append(list, it)
				}
			}
			if f.VLANList && len(list) > 0 {
				var err error
				if list, err = pack.NormaliseVLANs(list); err != nil {
					return nil, fmt.Errorf("field %s: %w", field, err)
				}
			}
			if len(list) > 0 {
				row[field] = list
			}
			continue
		}
		s, ok := translate(im.Values[field], strings.TrimSpace(s))
		if !ok {
			continue
		}
		if f.Canonical {
			s = reg.Normalise(platform, s)
		}
		if f.MAC {
			s = pack.NormaliseMAC(s)
		}
		if f.Type == fact.Int {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("field %s: %q is not an integer", field, s)
			}
			row[field] = n
			continue
		}
		row[field] = s
	}
	for field := range row {
		if f, _ := fact.Lookup(family, field); f.TypedBy != "" && row[f.TypedBy] == "mac" {
			row[field] = pack.NormaliseMAC(row[field].(string))
		}
	}
	for field, v := range im.Defaults {
		if _, set := row[field]; !set && slices.Contains(im.MergeOn, field) {
			row[field] = v
		}
	}
	return row, nil
}

// translate applies a field's values table to one value. ok is false when there is nothing to
// keep: the value is blank, or the table maps it to "" (the device's way of saying "none").
func translate(tr map[string]string, s string) (string, bool) {
	if s == "" {
		return "", false
	}
	if tr != nil {
		if t, ok := tr[s]; ok {
			s = t
		} else if t, ok := tr["*"]; ok {
			s = t
		}
	}
	return s, s != ""
}

// merge adds rows to the rows so far, joining on the merge_on fields: rows of a later step, and
// rows of the same step that share a key. A scalar keeps its first value; a list adds up the items
// it does not hold yet (feature 010: one family per summary step).
func merge(rows, more []map[string]any, on []string) []map[string]any {
	if len(on) == 0 {
		return append(rows, more...)
	}
	key := func(r map[string]any) string {
		var b strings.Builder
		for _, k := range on {
			fmt.Fprintf(&b, "%v\x00", r[k])
		}
		return b.String()
	}
	index := map[string]map[string]any{}
	for _, r := range rows {
		index[key(r)] = r
	}
	for _, r := range more {
		if into, ok := index[key(r)]; ok {
			for k, v := range r {
				old, set := into[k]
				if !set {
					into[k] = v
					continue
				}
				if l, ok := old.([]string); ok {
					if more, ok := v.([]string); ok {
						for _, it := range more {
							if !slices.Contains(l, it) {
								l = append(l, it)
							}
						}
						into[k] = l
					}
				}
			}
			continue
		}
		rows = append(rows, r)
		index[key(r)] = r // rows of one step merge too: a server line and its group line
	}
	return rows
}
