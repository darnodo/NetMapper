// Package fact holds the platform-neutral fact family schemas (contracts/fact-families.md).
package fact

import (
	"fmt"
	"slices"
)

const (
	String  = "string"
	Int     = "int"
	Strings = "strings"
)

type Field struct {
	Name      string
	Type      string
	Required  bool
	Canonical bool // an interface name, canonicalised by the pack's naming rules
	MAC       bool // normalised to lowercase colon form
	Enum      []string
}

var Families = map[string][]Field{
	"identity": {
		{Name: "platform", Type: String},
		{Name: "os_version", Type: String},
		{Name: "sys_object_id", Type: String},
		{Name: "sys_descr", Type: String},
		{Name: "transports_answered", Type: Strings},
		{Name: "duplicate_of_task", Type: Int},
	},
	"neighbours": {
		{Name: "protocol", Type: String, Required: true},
		{Name: "local_interface", Type: String, Required: true, Canonical: true},
		{Name: "remote_chassis_id", Type: String},
		{Name: "remote_system_name", Type: String},
		{Name: "remote_interface", Type: String},
		{Name: "remote_mgmt_address", Type: String},
	},
	"interfaces": {
		{Name: "name", Type: String, Required: true, Canonical: true},
		{Name: "description", Type: String},
		{Name: "admin_state", Type: String, Enum: []string{"up", "down"}},
		{Name: "oper_state", Type: String, Enum: []string{"up", "down", "other"}},
		{Name: "speed_bps", Type: Int},
		{Name: "mtu", Type: Int},
		{Name: "mac", Type: String, MAC: true},
	},
}

// Lookup returns the schema of one field.
func Lookup(family, name string) (Field, bool) {
	for _, f := range Families[family] {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Validate rejects unknown families, unlisted fields, missing required fields and wrong types.
func Validate(family string, rows []map[string]any) error {
	if _, ok := Families[family]; !ok {
		return fmt.Errorf("unknown fact family %q", family)
	}
	for i, row := range rows {
		for k, v := range row {
			f, ok := Lookup(family, k)
			if !ok {
				return fmt.Errorf("%s row %d: field %q is not in the schema", family, i, k)
			}
			if err := check(f, v); err != nil {
				return fmt.Errorf("%s row %d: %w", family, i, err)
			}
		}
		for _, f := range Families[family] {
			if _, ok := row[f.Name]; f.Required && !ok {
				return fmt.Errorf("%s row %d: required field %q missing", family, i, f.Name)
			}
		}
	}
	return nil
}

func check(f Field, v any) error {
	ok := false
	switch f.Type {
	case String:
		s, isStr := v.(string)
		ok = isStr && (f.Enum == nil || slices.Contains(f.Enum, s))
	case Int:
		switch v.(type) {
		case int, int64:
			ok = true
		}
	case Strings:
		_, ok = v.([]string)
	}
	if !ok {
		return fmt.Errorf("field %q: %v (%T) is not a valid %s", f.Name, v, v, f.Type)
	}
	return nil
}
