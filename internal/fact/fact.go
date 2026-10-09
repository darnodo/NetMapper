// Package fact holds the platform-neutral fact family schemas (contracts/fact-families.md).
package fact

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	// VLANList: a strings field holding a set of VLAN IDs as sorted, merged ranges, each item N or
	// lo-hi, 1 to 4094 (feature 008). The parser normalises it; Validate rejects any other form.
	VLANList bool
	// VLANID: an int field holding one VLAN ID, 1 to 4094 (feature 008).
	VLANID bool
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
	// The layer 2 families (feature 008). The engine reads neither yet; the l2domain projection
	// (#37) will.
	"vlans": {
		{Name: "vlan_id", Type: Int, Required: true, VLANID: true},
		{Name: "name", Type: String},
		{Name: "status", Type: String, Required: true, Enum: []string{"active", "suspended", "shutdown", "other"}},
	},
	"interface_vlans": {
		{Name: "interface", Type: String, Required: true, Canonical: true},
		{Name: "mode", Type: String}, // access, trunk, or as the device names it; absent on a member row
		{Name: "access_vlan", Type: Int, VLANID: true},
		{Name: "native_vlan", Type: Int, VLANID: true},
		{Name: "allowed_vlans", Type: Strings, VLANList: true},
		{Name: "active_vlans", Type: Strings, VLANList: true},
		{Name: "channel", Type: String, Canonical: true}, // member rows only: the port-channel
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
		var n int64
		switch x := v.(type) {
		case int:
			n, ok = int64(x), true
		case int64:
			n, ok = x, true
		}
		if ok && f.VLANID && (n < 1 || n > 4094) {
			return fmt.Errorf("field %q: %d is not a VLAN ID (1 to 4094)", f.Name, n)
		}
	case Strings:
		var l []string
		l, ok = v.([]string)
		if ok && f.VLANList && !isVLANList(l) {
			return fmt.Errorf("field %q: %v is not sorted, merged VLAN ranges (1 to 4094)", f.Name, l)
		}
	}
	if !ok {
		return fmt.Errorf("field %q: %v (%T) is not a valid %s", f.Name, v, v, f.Type)
	}
	return nil
}

// isVLANList reports whether items are VLAN ranges in their one normal form: each N or lo-hi with
// 1 <= N, lo < hi <= 4094, ascending, no two overlapping or touching.
func isVLANList(items []string) bool {
	prev := 0
	for _, it := range items {
		lo, hi, err := ParseVLANRange(it)
		if err != nil || lo <= prev+1 && prev > 0 || it != FormatVLANRange(lo, hi) {
			return false
		}
		prev = hi
	}
	return true
}

// ParseVLANRange reads one item, N or lo-hi, with 1 <= lo <= hi <= 4094.
func ParseVLANRange(s string) (lo, hi int, err error) {
	a, b, isRange := strings.Cut(s, "-")
	if lo, err = strconv.Atoi(a); err != nil {
		return 0, 0, fmt.Errorf("%q is not a VLAN ID or range", s)
	}
	hi = lo
	if isRange {
		if hi, err = strconv.Atoi(b); err != nil {
			return 0, 0, fmt.Errorf("%q is not a VLAN ID or range", s)
		}
	}
	if lo < 1 || hi > 4094 || lo > hi {
		return 0, 0, fmt.Errorf("%q is not a VLAN ID or range (1 to 4094)", s)
	}
	return lo, hi, nil
}

// FormatVLANRange prints a range in normal form: N when lo == hi, lo-hi otherwise.
func FormatVLANRange(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}
