package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/transport"
)

// failure ends a task under FR-014 with a last_error kind other than a plain error.
type failure struct{ kind, msg string }

func (f *failure) Error() string { return f.kind + ": " + f.msg }

type credSet struct {
	ID        int64
	Name      string
	Kind      string
	Username  string
	SecretRef string
	Max       int
	// snmp_v3 only, as stored by jobrunner with defaults applied.
	AuthProtocol string
	PrivProtocol string
}

func (c credSet) transport() string {
	if c.Kind == "ssh" {
		return "ssh"
	}
	return "snmp"
}

// opened is what trying the covering sets of one transport gave.
type opened struct {
	transport  string
	s          transport.Session
	first      transport.RawOutput // output of the first step, for SNMP
	rejected   []string            // sets the device refused
	spent      []string            // sets not shown: their budget on this device is used up
	unresolved []string            // sets whose reference resolved to nothing
	evidence   []byte              // the device's answer to a rejected credential
	evidenceBy string              // what was presented: "<transport>.auth <username>"
	presented  bool                // at least one set was shown to the device
}

// device holds one task's sessions: at most one per transport, opened on first use.
type device struct {
	c      *Collector
	j      *job
	t      *frontier.Task
	target transport.Target
	tried  map[string]*opened
}

func (c *Collector) device(j *job, t *frontier.Task, platform string) *device {
	return &device{c: c, j: j, t: t, target: transport.Target{Addr: t.Target, Name: t.TargetName, Platform: platform}, tried: map[string]*opened{}}
}

func (d *device) close() {
	for _, o := range d.tried {
		if o.s != nil {
			o.s.Close()
		}
	}
}

// drop forgets a session broken mid-step; the next use opens a new one.
func (d *device) drop(tr string) {
	if o := d.tried[tr]; o != nil && o.s != nil {
		o.s.Close()
	}
	delete(d.tried, tr)
}

// session returns the transport's session, trying the covering sets on first use. SNMP only
// answers to a request, so its attempt includes first (the probe) when given.
func (d *device) session(ctx context.Context, tr string, first *transport.Step) (*opened, error) {
	if o := d.tried[tr]; o != nil {
		return o, nil
	}
	o, err := d.open(ctx, tr, first)
	if err == nil {
		d.tried[tr] = o
	}
	return o, err
}

// open tries the covering sets of one transport (research R7): sets that opened a session for
// this task before come first and do not spend budget unless they fail; the others are tried in
// document order, each attempt counted in its own statement before it is made, and a set whose
// budget is spent is skipped. A reference that resolves to nothing moves on without counting.
func (d *device) open(ctx context.Context, tr string, first *transport.Step) (*opened, error) {
	var sets []credSet
	for _, cs := range d.j.Creds {
		if cs.transport() == tr {
			sets = append(sets, cs)
		}
	}
	key := func(cs credSet) string { return strconv.FormatInt(cs.ID, 10) }
	slices.SortStableFunc(sets, func(a, b credSet) int {
		oa, ob := d.t.CredAttempts[key(a)].OK, d.t.CredAttempts[key(b)].OK
		switch {
		case oa && !ob:
			return -1
		case ob && !oa:
			return 1
		}
		return 0
	})

	o := &opened{transport: tr}
	count := func(cs credSet, dn int, ok bool) error {
		if err := frontier.CountCredential(ctx, d.c.DB, *d.t, key(cs), dn, ok); err != nil {
			return err
		}
		n := d.t.CredAttempts[key(cs)]
		d.t.CredAttempts[key(cs)] = frontier.CredCount{N: n.N + dn, OK: ok}
		return nil
	}
	for _, cs := range sets {
		n := d.t.CredAttempts[key(cs)]
		if !n.OK && n.N >= cs.Max {
			o.spent = append(o.spent, cs.Name)
			continue
		}
		sec, err := d.c.Secrets.Resolve(ctx, cs.SecretRef)
		if err != nil {
			d.c.Log.Warn("credential set did not resolve", "set", cs.Name, "target", d.t.Target, "err", err)
			o.unresolved = append(o.unresolved, cs.Name)
			continue
		}
		// A v3 secret missing a field it needs is the same as a reference to nothing: sending an
		// empty passphrase would only earn a denial for the wrong reason (006 research R4).
		if field := missingField(cs, sec); field != "" {
			d.c.Log.Warn("credential set secret has no "+field+" field", "set", cs.Name, "target", d.t.Target)
			o.unresolved = append(o.unresolved, cs.Name+" (missing "+field+")")
			continue
		}
		if !n.OK {
			if err := count(cs, 1, false); err != nil {
				return nil, err
			}
		}
		o.presented = true
		s, err := d.c.Transports[tr].Open(ctx, d.target, transport.Credential{Set: cs.Name, Kind: cs.Kind, Username: cs.Username, Secret: sec,
			AuthProtocol: cs.AuthProtocol, PrivProtocol: cs.PrivProtocol})
		var out transport.RawOutput
		if err == nil && first != nil {
			if out, err = s.Run(ctx, *first); err != nil {
				s.Close()
			}
		}
		var ae *transport.AuthError
		switch {
		case err == nil:
			if !n.OK {
				if err := count(cs, 0, true); err != nil {
					s.Close()
					return nil, err
				}
			}
			o.s, o.first = s, out
			return o, nil
		case errors.As(err, &ae):
			if n.OK {
				if err := count(cs, 1, false); err != nil {
					return nil, err
				}
			}
			o.rejected = append(o.rejected, cs.Name)
			o.evidence, o.evidenceBy = ae.Evidence, fmt.Sprintf("%s.auth %s", tr, cs.Username)
		case errors.Is(err, transport.ErrSilent):
			// No answer from SSH means nothing is listening; the next set will not change that.
			// SNMP v2c answers a wrong community with silence, so the next set may still work.
			if tr == "ssh" {
				return o, nil
			}
		default:
			return nil, err
		}
	}
	return o, nil
}

// missingField names the first field a snmp_v3 secret needs and lacks: auth always, priv unless
// the set is authNoPriv. Other kinds are not checked here.
func missingField(cs credSet, sec secret.Secret) string {
	switch {
	case cs.Kind != "snmp_v3":
		return ""
	case sec.Field("auth") == "":
		return "auth"
	case cs.PrivProtocol != "none" && sec.Field("priv") == "":
		return "priv"
	}
	return ""
}

// verdict states why no session opened on any of os (research R6). unreachable only when nothing
// answered; denied only when something refused every set it was shown and every set was shown;
// when a set never resolved, the task fails instead and nothing is written about the device.
// A set whose budget was spent on earlier attempts is not shown again, and counts for neither.
func verdict(os ...*opened) (status string, evidence *opened, err error) {
	var rejected, unresolved, spent []string
	presented := false
	for _, o := range os {
		rejected = append(rejected, o.rejected...)
		unresolved = append(unresolved, o.unresolved...)
		spent = append(spent, o.spent...)
		presented = presented || o.presented
		if evidence == nil && o.evidence != nil {
			evidence = o
		}
	}
	switch {
	case len(unresolved) > 0 && len(rejected) == 0:
		return "", nil, &failure{frontier.KindCredentialUnresolved, strings.Join(unresolved, ", ")}
	case len(unresolved) > 0:
		return "", nil, &failure{frontier.KindCredentialPartial, fmt.Sprintf("rejected %s; unresolved %s", strings.Join(rejected, ", "), strings.Join(unresolved, ", "))}
	case evidence != nil:
		return "denied", evidence, nil
	case !presented && len(spent) > 0:
		// Nothing left to show: saying unreachable or denied would be a guess.
		return "", nil, &failure{frontier.KindError, "attempt budget spent for " + strings.Join(spent, ", ")}
	}
	return "unreachable", nil, nil
}
