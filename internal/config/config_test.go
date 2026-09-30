package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

const valid = `
perimeters:
  - name: lab
    include: [172.20.20.0/24]
    exclude: [172.20.20.1/32]
credential_sets:
  - name: ro-snmp
    kind: snmp_v2c
    secret_ref: env:LAB_SNMP_COMMUNITY
    max_attempts_per_device: 1
    perimeters: [lab]
  - name: ro-ssh
    kind: ssh
    username: netmapper
    secret_ref: vault:kv/data/netmapper/lab#password
    max_attempts_per_device: 2
    perimeters: [lab]
seed_sets:
  - name: lab-seeds
    targets: [172.20.20.2]
future_key: kept but ignored
`

func TestValid(t *testing.T) {
	d, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Raw) != valid {
		t.Error("raw document changed")
	}
	if d.Discovery != Defaults {
		t.Errorf("defaults: got %+v", d.Discovery)
	}
	want := Discovery{3, 60 * time.Second, 30 * time.Minute, 5 * time.Minute, 16, time.Second}
	if Defaults != want {
		t.Errorf("Defaults = %+v, research R16 says %+v", Defaults, want)
	}
	if d.CredentialSets[1].Name != "ro-ssh" || d.CredentialSets[1].MaxAttemptsPerDevice != 2 {
		t.Errorf("credential sets out of order: %+v", d.CredentialSets)
	}
}

func TestDiscoveryOverrides(t *testing.T) {
	d, err := Parse([]byte(valid + "discovery:\n  lease: 30s\n  claim_batch: 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Discovery.Lease != 30*time.Second || d.Discovery.ClaimBatch != 4 || d.Discovery.StepDeadline != 30*time.Minute {
		t.Errorf("got %+v", d.Discovery)
	}
}

func TestInvalid(t *testing.T) {
	for _, c := range []struct{ name, old, new, want string }{
		{"empty include", "include: [172.20.20.0/24]", "include: []", `perimeter "lab" has no include range`},
		{"literal secret", "secret_ref: env:LAB_SNMP_COMMUNITY", "secret_ref: public", "secret_ref must be a reference, not a value"},
		{"unknown perimeter", "    perimeters: [lab]\n  - name: ro-ssh", "    perimeters: [nope]\n  - name: ro-ssh", `perimeter "nope" not found`},
		{"ssh without username", "    username: netmapper\n", "", "kind ssh requires username"},
		{"v3 without username", "kind: snmp_v2c", "kind: snmp_v3", "kind snmp_v3 requires username"},
		{"bad kind", "kind: snmp_v2c", "kind: telnet", "kind must be"},
		{"missing budget", "    max_attempts_per_device: 1\n", "", "max_attempts_per_device is required"},
		{"zero budget", "max_attempts_per_device: 1", "max_attempts_per_device: 0", "max_attempts_per_device is required"},
		{"duplicate set", "name: ro-ssh", "name: ro-snmp", `credential set "ro-snmp" is declared twice`},
		{"duplicate seed set", "seed_sets:\n  - name: lab-seeds\n", "seed_sets:\n  - name: lab-seeds\n  - name: lab-seeds\n", `seed set "lab-seeds" is declared twice`},
		{"negative lease", "future_key", "discovery: {lease: -1s}\nfuture_key", "must be positive"},
		{"auth_protocol spelling", "kind: snmp_v2c", "kind: snmp_v3\n    username: nm\n    auth_protocol: SHA256",
			`credential set "ro-snmp": auth_protocol must be one of md5, sha, sha224, sha256, sha384, sha512`},
		{"priv_protocol spelling", "kind: snmp_v2c", "kind: snmp_v3\n    username: nm\n    priv_protocol: aes-256",
			`credential set "ro-snmp": priv_protocol must be one of none, des, aes, aes192, aes256, aes192c, aes256c`},
		{"auth_protocol on v2c", "kind: snmp_v2c", "kind: snmp_v2c\n    auth_protocol: sha",
			`credential set "ro-snmp": auth_protocol applies to snmp_v3 only`},
		{"priv_protocol on ssh", "kind: ssh", "kind: ssh\n    priv_protocol: aes",
			`credential set "ro-ssh": priv_protocol applies to snmp_v3 only`},
		{"v3 vault field", "kind: snmp_v2c\n    secret_ref: env:LAB_SNMP_COMMUNITY", "kind: snmp_v3\n    username: nm\n    secret_ref: vault:kv/data/x#auth",
			`credential set "ro-snmp": a snmp_v3 secret_ref must reference the whole secret, without #field`},
		{"degraded_at above 1", "exclude: [172.20.20.1/32]", "exclude: [172.20.20.1/32]\n    degraded_at: 1.5",
			`perimeter "lab": degraded_at must be greater than 0 and at most 1`},
		{"degraded_at zero", "exclude: [172.20.20.1/32]", "exclude: [172.20.20.1/32]\n    degraded_at: 0",
			`perimeter "lab": degraded_at must be greater than 0 and at most 1`},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := strings.Replace(valid, c.old, c.new, 1)
			if doc == valid {
				t.Fatal("test did not change the document")
			}
			_, err := Parse([]byte(doc))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
	whole := strings.Replace(valid, "kind: snmp_v2c\n    secret_ref: env:LAB_SNMP_COMMUNITY", "kind: snmp_v3\n    username: nm\n    secret_ref: vault:kv/data/x", 1)
	if _, err := Parse([]byte(whole)); err != nil {
		t.Errorf("v3 set referencing a whole vault secret: %v", err)
	}
	if _, err := Parse([]byte(strings.Replace(valid, "172.20.20.0/24", "172.20.20.0/33", 1))); err == nil {
		t.Error("invalid CIDR accepted")
	}
}

// 006 FR-001, FR-002, FR-009: snmp_v3 sets get sha/aes when they name nothing, keep what they name,
// and other kinds carry no protocol.
func TestSNMPv3Protocols(t *testing.T) {
	v3 := func(extra string) *Document {
		t.Helper()
		doc := strings.Replace(valid, "    kind: snmp_v2c\n", "    kind: snmp_v3\n    username: nm\n"+extra, 1)
		d, err := Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if c := v3("").CredentialSets[0]; c.AuthProtocol != "sha" || c.PrivProtocol != "aes" {
		t.Errorf("defaults: got %q/%q, want sha/aes", c.AuthProtocol, c.PrivProtocol)
	}
	for _, a := range []string{"md5", "sha", "sha224", "sha256", "sha384", "sha512"} {
		if c := v3("    auth_protocol: " + a + "\n").CredentialSets[0]; c.AuthProtocol != a {
			t.Errorf("auth_protocol %s read as %q", a, c.AuthProtocol)
		}
	}
	for _, p := range []string{"none", "des", "aes", "aes192", "aes256", "aes192c", "aes256c"} {
		if c := v3("    priv_protocol: " + p + "\n").CredentialSets[0]; c.PrivProtocol != p {
			t.Errorf("priv_protocol %s read as %q", p, c.PrivProtocol)
		}
	}
	d, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range d.CredentialSets {
		if c.AuthProtocol != "" || c.PrivProtocol != "" {
			t.Errorf("%s set %q carries protocols %q/%q", c.Kind, c.Name, c.AuthProtocol, c.PrivProtocol)
		}
	}
}

// 006 SC-002: the configuration documents shipped in the repository load, and so do the complete
// examples in the documentation (every yaml block with credential_sets, so fragments are left out).
func TestRepositoryDocuments(t *testing.T) {
	for _, p := range []string{"../../test/lab/netmapper.yaml"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(b); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"../../README.md", "../../docs/how-to/deploy.md"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for i, block := range strings.Split(string(b), "```yaml\n")[1:] {
			block, _, _ = strings.Cut(block, "```")
			if !strings.Contains(block, "credential_sets:") {
				continue
			}
			found++
			if _, err := Parse([]byte(block)); err != nil {
				t.Errorf("%s, yaml block %d: %v", p, i+1, err)
			}
		}
		if found == 0 {
			t.Errorf("%s: no configuration example found", p)
		}
	}
}

// T035, FR-005: the threshold is optional, and a perimeter that declares none is judged by the
// documented default.
func TestThresholdIsOptional(t *testing.T) {
	d, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if d.Perimeters[0].DegradedAt != nil {
		t.Errorf("undeclared threshold reads as %v, want unset", d.Perimeters[0].DegradedAt)
	}

	one := strings.Replace(valid, "exclude: [172.20.20.1/32]", "exclude: [172.20.20.1/32]\n    degraded_at: 0.5", 1)
	d, err = Parse([]byte(one))
	if err != nil {
		t.Fatal(err)
	}
	if d.Perimeters[0].DegradedAt == nil || *d.Perimeters[0].DegradedAt != 0.5 {
		t.Errorf("degraded_at %v, want 0.5", d.Perimeters[0].DegradedAt)
	}
}
