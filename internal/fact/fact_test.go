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

func TestLayer2Families(t *testing.T) {
	for _, c := range []struct {
		family string
		row    map[string]any
		ok     bool
	}{
		{"vlans", map[string]any{"vlan_id": int64(10), "name": "USERS", "status": "active"}, true},
		{"vlans", map[string]any{"vlan_id": int64(4094), "status": "suspended"}, true},
		{"vlans", map[string]any{"vlan_id": int64(10), "status": "act/lshut"}, false},
		{"vlans", map[string]any{"vlan_id": int64(0), "status": "active"}, false},
		{"interface_vlans", map[string]any{"interface": "Ethernet4", "mode": "access", "access_vlan": int64(10)}, true},
		{"interface_vlans", map[string]any{"interface": "Ethernet4", "mode": "access", "access_vlan": int64(4095)}, false},
		{"interface_vlans", map[string]any{"interface": "Ethernet1", "mode": "trunk", "native_vlan": int64(-1)}, false},
		{"interface_vlans", map[string]any{"interface": "Port-Channel10", "mode": "trunk", "allowed_vlans": []string{"1-4094"}}, true},
		{"interface_vlans", map[string]any{"interface": "Ethernet2", "channel": "Port-Channel10"}, true},
	} {
		err := Validate(c.family, []map[string]any{c.row})
		if (err == nil) != c.ok {
			t.Errorf("%s %v: err %v, want ok %v", c.family, c.row, err, c.ok)
		}
	}
}

// A VLAN list has one form only (feature 008, FR-006): anything else is refused.
func TestVLANListForm(t *testing.T) {
	for _, c := range []struct {
		list []string
		ok   bool
	}{
		{[]string{"1-4094"}, true},
		{[]string{"10", "20", "30-32"}, true},
		{[]string{"0"}, false},
		{[]string{"4095"}, false},
		{[]string{"20", "10"}, false},
		{[]string{"10-12", "12-14"}, false},
		{[]string{"10-11", "12"}, false}, // touching: the form is 10-12
		{[]string{"12-10"}, false},
		{[]string{"10-10"}, false}, // the form is 10
		{[]string{"x"}, false},
	} {
		err := Validate("interface_vlans", []map[string]any{{"interface": "Ethernet1", "allowed_vlans": c.list}})
		if (err == nil) != c.ok {
			t.Errorf("%v: err %v, want ok %v", c.list, err, c.ok)
		}
	}
}
