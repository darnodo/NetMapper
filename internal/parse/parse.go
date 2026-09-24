// Package parse turns raw device output into fact family rows: TextFSM templates for CLI output,
// column mapping for SNMP walks, then the pack's field mapping. It reads only stored bytes, so a
// replay can run it again without a device (FR-011).
package parse

import (
	"fmt"
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
		var raw []map[string]any
		switch {
		case st.Walk != "":
			raw, err = walkRows(outputs[i], im.Map)
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
			m, err := mapRow(reg, platform, family, im, r)
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
	if err := fact.Validate(family, rows); err != nil {
		return ParseFailed, nil, err
	}
	return Collected, rows, nil
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

func mapRow(reg *pack.Registry, platform, family string, im pack.Impl, raw map[string]any) (map[string]any, error) {
	row := map[string]any{}
	for field, src := range im.Map {
		var s string
		if lit, ok := strings.CutPrefix(src, "="); ok {
			s = lit
		} else {
			switch v := raw[src].(type) {
			case string:
				s = v
			case []string:
				s = strings.Join(v, " ")
			}
		}
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if tr := im.Values[field]; tr != nil {
			if t, ok := tr[s]; ok {
				s = t
			} else if t, ok := tr["*"]; ok {
				s = t
			}
			if s == "" { // the device's way of saying "none"
				continue
			}
		}
		f, _ := fact.Lookup(family, field)
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
	return row, nil
}

// merge adds rows from a later step to the rows so far, joining on the merge_on fields.
func merge(rows, more []map[string]any, on []string) []map[string]any {
	if len(on) == 0 || len(rows) == 0 {
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
				if _, set := into[k]; !set {
					into[k] = v
				}
			}
			continue
		}
		rows = append(rows, r)
	}
	return rows
}
