// Package config parses and validates the configuration document (contracts/config.md).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Document struct {
	Perimeters     []Perimeter     `yaml:"perimeters"`
	CredentialSets []CredentialSet `yaml:"credential_sets"`
	SeedSets       []SeedSet       `yaml:"seed_sets"`
	Discovery      Discovery       `yaml:"discovery"`
	// Raw is the document exactly as given, stored as config_version.document.
	Raw []byte `yaml:"-"`
}

type Perimeter struct {
	Name    string         `yaml:"name"`
	Include []netip.Prefix `yaml:"include"`
	Exclude []netip.Prefix `yaml:"exclude"`
	// Coverage thresholds, both optional fractions in (0, 1]. Nil means the documented defaults:
	// published only when every device of the baseline is reached again, degraded from 0.9.
	DegradedAt       *float64 `yaml:"degraded_at"`
	QuarantinedBelow *float64 `yaml:"quarantined_below"`
}

type CredentialSet struct {
	Name                 string   `yaml:"name"`
	Kind                 string   `yaml:"kind"`
	Username             string   `yaml:"username"`
	SecretRef            string   `yaml:"secret_ref"`
	MaxAttemptsPerDevice int      `yaml:"max_attempts_per_device"`
	Perimeters           []string `yaml:"perimeters"`
}

type SeedSet struct {
	Name    string   `yaml:"name"`
	Targets []string `yaml:"targets"`
}

// Discovery bounds a run. Defaults are starting points, not measured values (research R16).
type Discovery struct {
	MaxTaskAttempts int           `yaml:"max_task_attempts" json:"max_task_attempts"`
	StepIdleTimeout time.Duration `yaml:"step_idle_timeout" json:"step_idle_timeout"`
	StepDeadline    time.Duration `yaml:"step_deadline" json:"step_deadline"`
	Lease           time.Duration `yaml:"lease" json:"lease"`
	ClaimBatch      int           `yaml:"claim_batch" json:"claim_batch"`
	PollInterval    time.Duration `yaml:"poll_interval" json:"poll_interval"`
}

var Defaults = Discovery{
	MaxTaskAttempts: 3,
	StepIdleTimeout: 60 * time.Second,
	StepDeadline:    30 * time.Minute,
	Lease:           5 * time.Minute,
	ClaimBatch:      16,
	PollInterval:    time.Second,
}

// Parse decodes and validates raw. The error, if any, joins one error per problem.
func Parse(raw []byte) (*Document, error) {
	d := &Document{Raw: raw}
	if err := yaml.Unmarshal(raw, d); err != nil {
		return nil, err
	}
	d.Discovery.fill()
	return d, d.validate()
}

func (d *Discovery) fill() {
	if d.MaxTaskAttempts == 0 {
		d.MaxTaskAttempts = Defaults.MaxTaskAttempts
	}
	if d.StepIdleTimeout == 0 {
		d.StepIdleTimeout = Defaults.StepIdleTimeout
	}
	if d.StepDeadline == 0 {
		d.StepDeadline = Defaults.StepDeadline
	}
	if d.Lease == 0 {
		d.Lease = Defaults.Lease
	}
	if d.ClaimBatch == 0 {
		d.ClaimBatch = Defaults.ClaimBatch
	}
	if d.PollInterval == 0 {
		d.PollInterval = Defaults.PollInterval
	}
}

func (d *Document) validate() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	perimeters := map[string]bool{}
	for _, p := range d.Perimeters {
		if perimeters[p.Name] {
			fail("perimeter %q is declared twice", p.Name)
		}
		perimeters[p.Name] = true
		if len(p.Include) == 0 {
			fail("perimeter %q has no include range", p.Name)
		}
		for _, t := range []struct {
			name string
			v    *float64
		}{{"degraded_at", p.DegradedAt}, {"quarantined_below", p.QuarantinedBelow}} {
			if t.v != nil && (*t.v <= 0 || *t.v > 1) {
				fail("perimeter %q: %s must be greater than 0 and at most 1", p.Name, t.name)
			}
		}
		if p.DegradedAt != nil && p.QuarantinedBelow != nil && *p.QuarantinedBelow > *p.DegradedAt {
			fail("perimeter %q: quarantined_below must not be greater than degraded_at", p.Name)
		}
	}

	sets := map[string]bool{}
	for _, c := range d.CredentialSets {
		if sets[c.Name] {
			fail("credential set %q is declared twice", c.Name)
		}
		sets[c.Name] = true
		switch c.Kind {
		case "ssh", "snmp_v3":
			if c.Username == "" {
				fail("credential set %q: kind %s requires username", c.Name, c.Kind)
			}
		case "snmp_v2c":
		default:
			fail("credential set %q: kind must be ssh, snmp_v2c or snmp_v3", c.Name)
		}
		if !strings.HasPrefix(c.SecretRef, "env:") && !strings.HasPrefix(c.SecretRef, "vault:") {
			fail("credential set %q: secret_ref must be a reference, not a value", c.Name)
		}
		if c.MaxAttemptsPerDevice < 1 {
			fail("credential set %q: max_attempts_per_device is required and must be at least 1", c.Name)
		}
		if len(c.Perimeters) == 0 {
			fail("credential set %q covers no perimeter", c.Name)
		}
		for _, p := range c.Perimeters {
			if !perimeters[p] {
				fail("credential set %q: perimeter %q not found", c.Name, p)
			}
		}
	}

	seeds := map[string]bool{}
	for _, s := range d.SeedSets {
		if seeds[s.Name] {
			fail("seed set %q is declared twice", s.Name)
		}
		seeds[s.Name] = true
	}

	dd := d.Discovery
	if dd.MaxTaskAttempts < 1 || dd.ClaimBatch < 1 || dd.StepIdleTimeout < 0 || dd.StepDeadline < 0 || dd.Lease < 0 || dd.PollInterval < 0 {
		fail("discovery: values must be positive")
	}
	return errors.Join(errs...)
}
