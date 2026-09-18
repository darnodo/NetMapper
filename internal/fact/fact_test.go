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
