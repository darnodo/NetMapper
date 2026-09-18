package collector

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/perimeter"
	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/transport"
)

// find identifies a target, claims it, reads its neighbours and queues them with its scrape
// (research R4, R5). No transaction is open while the device is being talked to.
func (c *Collector) find(ctx context.Context, j *job, t *frontier.Task) error {
	addr, err := j.resolve(ctx, t.Target)
	if errors.Is(err, perimeter.ErrOutOfPerimeter) {
		return frontier.Skip(ctx, c.DB, *t, "out_of_perimeter")
	} else if err != nil {
		return err
	}
	t.Target = addr
	d := c.device(j, t, "")
	defer d.close()

	// Step 1: network only.
	id, err := c.identify(ctx, d)
	if err != nil {
		return err
	}
	obs := c.observation(j, t, "identity", id.platform)
	switch {
	case len(id.answered) == 0:
		var tried []*opened
		for _, tr := range []string{"snmp", "ssh"} {
			if o := d.tried[tr]; o != nil {
				tried = append(tried, o)
			}
		}
		status, ev, err := verdict(tried...)
		if err != nil {
			return err
		}
		obs.Status = status
		if ev != nil {
			obs.Transport = ev.transport
			if err := c.keep(ctx, &obs.Raw, ev.evidenceBy, ev.evidence); err != nil {
				return err
			}
		}
		_, err = c.write(ctx, t, obs, nil, done(ctx, t))
		return err
	case !id.ok:
		obs.Status, obs.Detail, obs.Transport, obs.Raw = "unsupported", "unknown_platform", id.answered[0], id.raw
		_, err = c.write(ctx, t, obs, nil, done(ctx, t))
		return err
	}
	obs.Status, obs.Transport, obs.Raw, obs.Claims = "collected", id.transport, id.raw, id.claims
	row := map[string]any{"platform": id.platform, "transports_answered": id.answered}
	for k, v := range map[string]string{"os_version": id.version, "sys_object_id": id.sysObjectID, "sys_descr": id.sysDescr} {
		if v != "" {
			row[k] = v
		}
	}
	obs.Parsed = []map[string]any{row}

	// Step 2: a short transaction decides whether another task already holds this device.
	if dup, err := c.claim(ctx, j, t, obs); err != nil || dup {
		return err
	}

	// Step 3: neighbours, then one short transaction queues them and the scrape.
	nb, parseErr, err := c.family(ctx, d, j, t, id.platform, id.version, "neighbours")
	if err != nil {
		return err
	}
	type neighbour struct {
		addr netip.Addr
		name string
	}
	var found []neighbour
	for _, r := range nb.Parsed {
		// A row without a usable address is kept in the observation and queues nothing.
		s, _ := r["remote_mgmt_address"].(string)
		if a, err := netip.ParseAddr(s); err == nil {
			name, _ := r["remote_system_name"].(string)
			found = append(found, neighbour{a.Unmap(), name})
		}
	}
	// Enqueue in address order: two finds enqueueing the same new targets in different orders
	// would deadlock on each other's inserts.
	slices.SortFunc(found, func(a, b neighbour) int { return a.addr.Compare(b.addr) })
	_, err = c.write(ctx, t, nb, parseErr, func(tx pgx.Tx) error {
		for _, n := range found {
			var err error
			if j.Perimeter.Allowed(n.addr) {
				err = frontier.Enqueue(ctx, tx, j.ID, "find", n.addr, n.name, "", t.ID, nil)
			} else {
				err = frontier.InsertSkipped(ctx, tx, j.ID, n.addr, n.name, t.ID, "out_of_perimeter")
			}
			if err != nil {
				return err
			}
		}
		if err := frontier.Enqueue(ctx, tx, j.ID, "scrape", t.Target, t.TargetName, id.platform, t.ID, t.CredAttempts); err != nil {
			return err
		}
		return done(ctx, t)(tx)
	})
	return err
}

type identity struct {
	ok                    bool
	platform, version     string
	transport             string   // the transport that identified the platform
	answered              []string // transports on which a session opened
	sysObjectID, sysDescr string
	raw                   []store.Raw
	claims                []store.Claim
}

// identify probes over SNMP, then runs the packs' SSH fingerprint commands when SNMP did not
// identify the platform, then extracts the pack's identifiers.
func (c *Collector) identify(ctx context.Context, d *device) (*identity, error) {
	id := &identity{}
	probe := c.Registry.Probe()
	step := d.step()
	for _, p := range probe {
		step.OIDs = append(step.OIDs, p.OID)
	}
	snmp, err := d.session(ctx, "snmp", &step)
	if err != nil {
		return nil, err
	}
	ev := pack.Evidence{CLI: map[string]string{}}
	if snmp.s != nil {
		id.answered = append(id.answered, "snmp")
		if err := c.keep(ctx, &id.raw, step.String(), snmp.first.Bytes); err != nil {
			return nil, err
		}
		vals := values(snmp.first.Bytes)
		for _, p := range probe {
			switch p.Name {
			case "sys_object_id":
				id.sysObjectID = vals[p.OID]
			case "sys_descr":
				id.sysDescr = vals[p.OID]
			}
		}
		ev.SysObjectID, ev.SysDescr = id.sysObjectID, id.sysDescr
		if id.platform, id.version, id.ok = c.Registry.Fingerprint(ev); id.ok {
			id.transport = "snmp"
		}
	}
	if !id.ok {
		ssh, err := d.session(ctx, "ssh", nil)
		if err != nil {
			return nil, err
		}
		if ssh.s != nil {
			id.answered = append(id.answered, "ssh")
			for _, cmd := range c.Registry.FingerprintCommands() {
				if _, err := c.cli(ctx, d, cmd, &id.raw, ev.CLI); err != nil {
					return nil, err
				}
				if id.platform, id.version, id.ok = c.Registry.Fingerprint(ev); id.ok {
					id.transport = "ssh"
					break
				}
			}
		}
	}
	if !id.ok {
		return id, nil
	}
	d.target.Platform = id.platform

	rules := c.Registry.Pack(id.platform).Identifiers
	var snmpVals map[string]string
	if snmp.s != nil {
		get := d.step()
		for _, r := range rules {
			if r.SNMP != nil {
				get.OIDs = append(get.OIDs, r.SNMP.OID)
			}
		}
		if len(get.OIDs) > 0 {
			out, err := snmp.s.Run(ctx, get)
			if err != nil {
				return nil, stepErr("identity", d.j, err)
			}
			if err := c.keep(ctx, &id.raw, get.String(), out.Bytes); err != nil {
				return nil, err
			}
			snmpVals = values(out.Bytes)
		}
	}
	for _, r := range rules {
		var v, from string
		if r.SNMP != nil && snmpVals != nil {
			v, from = r.ExtractSNMP(snmpVals[r.SNMP.OID]), "snmp"
		}
		if v == "" && r.SSH != nil {
			out, ran := ev.CLI[r.SSH.Command]
			if !ran {
				ssh, err := d.session(ctx, "ssh", nil)
				if err != nil {
					return nil, err
				}
				if ssh.s == nil {
					continue
				}
				if !slices.Contains(id.answered, "ssh") {
					id.answered = append(id.answered, "ssh")
				}
				if out, err = c.cli(ctx, d, r.SSH.Command, &id.raw, ev.CLI); err != nil {
					return nil, err
				}
			}
			v, from = r.ExtractSSH(out), "ssh"
		}
		if v != "" {
			id.claims = append(id.claims, store.Claim{Kind: r.Kind, Subtype: from, Value: v, Strength: r.Strength})
		}
	}
	return id, nil
}

// cli runs a command on the SSH session, keeps its output as raw evidence and in seen.
func (c *Collector) cli(ctx context.Context, d *device, cmd string, raws *[]store.Raw, seen map[string]string) (string, error) {
	step := d.step()
	step.Command = cmd
	out, err := d.tried["ssh"].s.Run(ctx, step)
	if err != nil {
		return "", stepErr("identity", d.j, err)
	}
	seen[cmd] = string(out.Bytes)
	return seen[cmd], c.keep(ctx, raws, cmd, out.Bytes)
}

func (d *device) step() transport.Step {
	return transport.Step{IdleTimeout: d.j.Discovery.StepIdleTimeout, Deadline: d.j.Discovery.StepDeadline}
}

func stepErr(family string, j *job, err error) error {
	switch {
	case errors.Is(err, transport.ErrDeadline):
		return &failure{frontier.KindDeadline, fmt.Sprintf("%s after %s", family, j.Discovery.StepDeadline)}
	case errors.Is(err, transport.ErrIdleTimeout), errors.Is(err, transport.ErrSilent):
		return fmt.Errorf("%s: device stopped answering: %w", family, err)
	}
	return err
}

// values reads SNMP output into OID -> value, leaving out the OIDs the device does not have.
func values(b []byte) map[string]string {
	vbs, _ := transport.DecodeVarbinds(b)
	m := map[string]string{}
	for _, vb := range vbs {
		switch vb.Type {
		case "NoSuchObject", "NoSuchInstance", "EndOfMibView", "Null":
		default:
			m[vb.OID] = vb.Value
		}
	}
	return m
}

// keep stores output in the object store and links it as the next step of an observation.
func (c *Collector) keep(ctx context.Context, raws *[]store.Raw, command string, b []byte) error {
	h, err := c.Raw.Put(ctx, b)
	if err != nil {
		return err
	}
	*raws = append(*raws, store.Raw{StepID: strconv.Itoa(len(*raws)), Command: command, Hash: h, Size: len(b)})
	return nil
}

func (c *Collector) observation(j *job, t *frontier.Task, family, platform string) store.Observation {
	return store.Observation{SnapshotID: j.SnapshotID, ParseGenerationID: j.GenerationID, TaskID: t.ID,
		CollectorID: c.ID, Target: t.Target, Family: family, Platform: platform}
}

func done(ctx context.Context, t *frontier.Task) func(pgx.Tx) error {
	return func(tx pgx.Tx) error { return frontier.Complete(ctx, tx, *t, "done") }
}
