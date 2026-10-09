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
	// TypedBy names the field that says what kind of value this one holds. It is required
	// whenever this field is present, and a value typed mac is normalised like a MAC field.
	TypedBy string
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
		{Name: "remote_mgmt_address", Type: String, TypedBy: "remote_mgmt_address_type"},
		{Name: "remote_mgmt_address_type", Type: String, Enum: []string{"ipv4", "ipv6", "mac", "other"}},
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
	// The management families describe the device, not its links: the engine reads none of them.
	// No field holds a secret (feature 007). yes/no stands for a boolean, like admin_state.
	"snmp": {
		{Name: "version", Type: String, Required: true, Enum: []string{"v2c", "v3"}},
		{Name: "access", Type: String, Enum: []string{"ro", "rw"}}, // v2c
		{Name: "acl", Type: String},                                // v2c
		{Name: "user", Type: String},                               // v3
		{Name: "group", Type: String},                              // v3
		{Name: "auth_protocol", Type: String, Enum: []string{"md5", "sha", "sha224", "sha256", "sha384", "sha512"}},
		// Absent means authNoPriv.
		{Name: "priv_protocol", Type: String, Enum: []string{"des", "3des", "aes", "aes192", "aes256"}},
	},
	"aaa_servers": {
		{Name: "protocol", Type: String, Required: true, Enum: []string{"radius", "tacacs"}},
		{Name: "address", Type: String, Required: true},
		{Name: "port", Type: Int},
		{Name: "vrf", Type: String, Required: true},
		{Name: "group", Type: String},
	},
	"local_users": {
		{Name: "name", Type: String, Required: true},
		{Name: "role", Type: String},
		{Name: "privilege", Type: Int},
		{Name: "ssh_key", Type: String, Required: true, Enum: []string{"yes", "no"}},
	},
	"management_apis": {
		{Name: "api", Type: String, Required: true, Enum: []string{"gnmi", "eapi", "netconf", "ssh", "telnet", "snmp"}},
		{Name: "enabled", Type: String, Required: true, Enum: []string{"yes", "no"}},
		{Name: "transport", Type: String},
		{Name: "port", Type: Int},
		{Name: "vrfs", Type: Strings},
	},
	"aaa_methods": {
		{Name: "type", Type: String, Required: true, Enum: []string{"authentication", "authorization", "accounting"}},
		{Name: "service", Type: String, Required: true},
		{Name: "list", Type: String},                                              // where the device prints a list name
		{Name: "level", Type: String},                                             // commands only, as printed: "0-15"
		{Name: "record", Type: String, Enum: []string{"start-stop", "stop-only"}}, // accounting only
		{Name: "methods", Type: Strings},
	},
	"management_servers": {
		{Name: "service", Type: String, Required: true, Enum: []string{"ntp", "syslog", "dns"}},
		{Name: "address", Type: String, Required: true},
		{Name: "port", Type: Int},
		{Name: "vrf", Type: String, Required: true},
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
			_, ok := row[f.Name]
			if f.Required && !ok {
				return fmt.Errorf("%s row %d: required field %q missing", family, i, f.Name)
			}
			if _, typed := row[f.TypedBy]; ok && f.TypedBy != "" && !typed {
				return fmt.Errorf("%s row %d: %q needs %q beside it", family, i, f.Name, f.TypedBy)
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
