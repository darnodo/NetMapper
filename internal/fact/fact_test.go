package fact

import "testing"

func TestTypedAddress(t *testing.T) {
	ok := []map[string]any{{"protocol": "lldp", "local_interface": "e1", "remote_mgmt_address": "10.0.0.1", "remote_mgmt_address_type": "ipv4"}}
	if err := Validate("neighbours", ok); err != nil {
		t.Error(err)
	}
	untyped := []map[string]any{{"protocol": "lldp", "local_interface": "e1", "remote_mgmt_address": "10.0.0.1"}}
	if err := Validate("neighbours", untyped); err == nil {
		t.Error("address accepted without its type")
	}
	odd := []map[string]any{{"protocol": "lldp", "local_interface": "e1", "remote_mgmt_address": "x", "remote_mgmt_address_type": "ethernet"}}
	if err := Validate("neighbours", odd); err == nil {
		t.Error("type outside the enum accepted")
	}
}

func TestManagementFamilies(t *testing.T) {
	for _, c := range []struct {
		family string
		row    map[string]any
		ok     bool
	}{
		{"snmp", map[string]any{"version": "v2c", "access": "ro", "acl": "SNMP-RO"}, true},
		{"snmp", map[string]any{"version": "v3", "user": "u", "group": "g", "auth_protocol": "sha256"}, true},
		{"snmp", map[string]any{"version": "v1"}, false},
		{"aaa_servers", map[string]any{"protocol": "tacacs", "address": "192.0.2.10", "port": int64(49), "vrf": "default"}, true},
		{"aaa_servers", map[string]any{"protocol": "tacacs", "address": "192.0.2.10"}, false}, // no vrf
		{"local_users", map[string]any{"name": "admin", "privilege": int64(15), "ssh_key": "no"}, true},
		{"local_users", map[string]any{"name": "admin", "ssh_key": "true"}, false},
		{"management_apis", map[string]any{"api": "eapi", "enabled": "yes", "vrfs": []string{"default", "MGMT"}}, true},
		{"management_apis", map[string]any{"api": "eapi", "enabled": "true"}, false},
		{"aaa_methods", map[string]any{"type": "accounting", "service": "exec", "record": "start-stop", "methods": []string{"group X"}}, true},
		{"aaa_methods", map[string]any{"type": "accounting", "service": "exec", "methods": "group X"}, false}, // not a list
		{"management_servers", map[string]any{"service": "syslog", "address": "192.0.2.41", "port": int64(1514), "vrf": "default"}, true},
		{"management_servers", map[string]any{"service": "syslog", "address": "192.0.2.41"}, false}, // no vrf
	} {
		err := Validate(c.family, []map[string]any{c.row})
		if (err == nil) != c.ok {
			t.Errorf("%s %v: err %v, want ok=%v", c.family, c.row, err, c.ok)
		}
	}
}
