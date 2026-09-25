package graph

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/darnodo/NetMapper/internal/pack"
)

// report is one `neighbours` row read as a statement about a cable: who is reporting, on which port,
// and what it says is at the far end.
type report struct {
	from         *entity
	fromPort     string // canonical, applied by the parser at collection time
	fromSpelling string
	fromIface    *iface

	to         *entity // nil when no entity accounts for the far end
	toPort     string  // canonical, when the far end resolved and the report named a port
	toSpelling string
	toIface    *iface

	protocol string
	remote   map[string]string // what the report said about the far end, as reported
	obs      int64
	at       time.Time
}

// nearRef names the reporting end, which is always a port of a resolved device.
func (r *report) nearRef() string { return "if:" + r.from.key + "/" + r.fromPort }

// farRef names the far end. A resolved port is an `if:` reference like any other. A device that
// resolved but named no port is the device itself, because FR-011 identifies a link by its
// interfaces and there is no interface to name. A far end no entity accounts for is named by the
// best identifier the report gave, with the port spelling when there is one, so two cables to the
// same unmanaged box stay two edges (FR-010, FR-011, research R6).
func (r *report) farRef() string {
	switch {
	case r.to != nil && r.toPort != "":
		return "if:" + r.to.key + "/" + r.toPort
	case r.to != nil:
		return r.to.ref()
	}
	for _, kind := range []string{"remote_chassis_id", "remote_mgmt_address", "remote_system_name"} {
		if v := r.remote[kind]; v != "" {
			ref := "unknown:" + strings.TrimPrefix(kind, "remote_") + "=" + v
			if r.toSpelling != "" {
				ref += "/" + r.toSpelling
			}
			return ref
		}
	}
	return "" // the report says nothing about the far end: there is no endpoint to connect to
}

// refs are the report's two endpoint references, lower first. Byte order, which is what the
// edge_l1_link_is_ordered constraint compares under the C collation (research R7).
func (r *report) refs() (string, string) {
	near, far := r.nearRef(), r.farRef()
	if far < near {
		return far, near
	}
	return near, far
}

// readReports turns the `neighbours` rows into reports, resolving each far end to an entity and
// canonicalising the port it names with that entity's own platform rules. This is the only place a
// pack is read: the parser canonicalised the local port at collection time, when it knew the
// platform, and could not do the same for the far end (FR-003, research R3).
func (p *projection) readReports(reg *pack.Registry, rows []famRow) []*report {
	byIdentifier, byAddress := p.index()
	var out []*report
	for _, row := range rows {
		local := str(row.row, "local_interface")
		if local == "" {
			continue
		}
		r := &report{
			from: row.owner, fromPort: local, fromSpelling: local,
			protocol: str(row.row, "protocol"), obs: row.obs, at: row.at,
			remote: map[string]string{},
		}
		for _, k := range []string{"remote_chassis_id", "remote_system_name",
			"remote_mgmt_address", "remote_mgmt_address_type"} {
			if v := str(row.row, k); v != "" {
				r.remote[k] = v
			}
		}
		r.toSpelling = str(row.row, "remote_interface")
		r.to = resolveFar(byIdentifier, byAddress, r.remote)
		if r.to != nil && r.toSpelling != "" {
			r.toPort = reg.Normalise(r.to.platform, r.toSpelling)
		}
		out = append(out, r)
	}
	return out
}

// index maps every strong identifier value and every address of the entity set to the entity holding
// it. A value held by more than one entity maps to nothing: resolution produces that when it refuses
// to merge a component contradicting itself, and attributing a cable to one of several candidates is
// a guess the result could not be told apart from a fact (FR-012, clarified 2026-09-25).
func (p *projection) index() (byIdentifier, byAddress map[string]*entity) {
	byIdentifier, byAddress = map[string]*entity{}, map[string]*entity{}
	add := func(m map[string]*entity, k string, e *entity) {
		if k == "" {
			return
		}
		seen, ok := m[k]
		if !ok {
			m[k] = e
			return
		}
		if seen != e {
			m[k] = nil // ambiguous from here on, whatever arrives later
		}
	}
	for _, e := range p.entities {
		for _, v := range slices.Sorted(maps.Values(e.identifiers)) {
			add(byIdentifier, v, e)
			if n := pack.NormaliseMAC(v); n != v {
				add(byIdentifier, n, e)
			}
		}
		for _, t := range e.targets {
			add(byAddress, t, e)
		}
	}
	return byIdentifier, byAddress
}

// resolveFar finds the entity a report's far end names: the chassis identifier first, the management
// address second. A remote system name is recorded but never resolves an endpoint, because a
// hostname is weak in every pack and two devices may share one (FR-012, research R8).
func resolveFar(byIdentifier, byAddress map[string]*entity, remote map[string]string) *entity {
	if v := remote["remote_chassis_id"]; v != "" {
		if e := byIdentifier[v]; e != nil {
			return e
		}
		if e := byIdentifier[pack.NormaliseMAC(v)]; e != nil {
			return e
		}
	}
	addr, typ := remote["remote_mgmt_address"], remote["remote_mgmt_address_type"]
	switch {
	case addr == "":
	case typ == "ipv4" || typ == "ipv6":
		if e := byAddress[addr]; e != nil {
			return e
		}
	case typ == "mac":
		if e := byIdentifier[pack.NormaliseMAC(addr)]; e != nil {
			return e
		}
	}
	return nil
}

// linkGroup is every report describing one cable: same pair of endpoint references, whichever end
// reported and whatever protocol carried it.
type linkGroup struct {
	from, to  string
	reporters map[string]bool
	members   []*report
}

func (g *linkGroup) oneSided() bool { return len(g.reporters) == 1 }

// buildLinks turns the reports into l1_link edges. The group key is the pair of endpoint references,
// so FR-009's "one link, not two" and FR-011's "one link per cable" both fall out of it, and the
// protocol stays evidence rather than identity (clarified 2026-09-25).
func (p *projection) buildLinks(reports []*report) {
	groups := map[string]*linkGroup{}
	for _, r := range reports {
		// Nothing at the far end, or a port claiming to see itself. Neither is a cable, and the second
		// would break the ordering the edge name rests on (research R6). Both are tested before the
		// two references are sorted: an empty far end sorts first, so afterwards it is indistinguishable
		// from a near end that happens to sort low.
		far := r.farRef()
		if far == "" || far == r.nearRef() {
			continue
		}
		from, to := r.refs()
		key := from + "|" + to
		g := groups[key]
		if g == nil {
			g = &linkGroup{from: from, to: to, reporters: map[string]bool{}}
			groups[key] = g
		}
		g.reporters[r.from.key] = true
		g.members = append(g.members, r)
	}

	for _, key := range slices.Sorted(maps.Keys(groups)) {
		p.edges = append(p.edges, p.link(groups[key]))
	}
	p.findings = append(p.findings, disagreements(groups)...)
}

// link builds the one edge a group of reports describes.
func (p *projection) link(g *linkGroup) *edge {
	e := &edge{typ: "l1_link", fromRef: g.from, toRef: g.to,
		confidence: "both_ends", attributes: map[string]any{}}
	if g.oneSided() {
		e.confidence = "one_end"
	}
	protocols := []string{}
	for _, r := range g.members {
		side := "from"
		if r.nearRef() == g.to {
			side = "to"
		}
		e.evidence = append(e.evidence, edgeEvidence{obs: r.obs, side: side})
		if r.protocol != "" && !slices.Contains(protocols, r.protocol) {
			protocols = append(protocols, r.protocol)
		}
		if e.first.IsZero() || r.at.Before(e.first) {
			e.first = r.at
		}
		if r.at.After(e.last) {
			e.last = r.at
		}
		attach(e, r)
	}
	slices.Sort(protocols)
	e.attributes["protocols"] = protocols
	// A one-sided link carries what its one report said about the far end: on an agreed link the two
	// endpoints already say it, and repeating it would be one source of truth too many (FR-010).
	if g.oneSided() {
		r := g.members[0]
		e.attributes["from_spelling"] = r.fromSpelling
		if r.toSpelling != "" {
			e.attributes["to_spelling"] = r.toSpelling
		}
		if r.to == nil {
			for k, v := range r.remote {
				e.attributes[k] = v
			}
		}
	}
	return e
}

// attach fills in the entity and the interface each of the edge's two references points at, from
// whichever report knows it.
func attach(e *edge, r *report) {
	set := func(ref string, ent **entity, port **iface) {
		switch ref {
		case r.nearRef():
			*ent, *port = r.from, r.fromIface
		case r.farRef():
			if r.to != nil {
				*ent, *port = r.to, r.toIface
			}
		}
	}
	set(e.fromRef, &e.fromEntity, &e.fromPort)
	set(e.toRef, &e.toEntity, &e.toPort)
}

// disagreements finds the pairs of devices that contradict each other about which ports are cabled.
// Two reports contradict when they name one port in common and a different port opposite it, because
// one port cannot face two different far ends. Reports that share no port are two separate cables,
// each known from one side, and requiring the shared port is also what keeps a shared medium out of
// this: three ports on one segment produce pairs that each agree with themselves (FR-014, clarified
// 2026-09-25, research R9, R10).
func disagreements(groups map[string]*linkGroup) []disagreement {
	// Only cables whose two ends both resolved can contradict each other: a far end nothing accounts
	// for has no second report to disagree with.
	var resolved []*linkGroup
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		g := groups[key]
		if g.oneSided() && strings.HasPrefix(g.from, "if:") && strings.HasPrefix(g.to, "if:") {
			resolved = append(resolved, g)
		}
	}
	seen := map[string]bool{}
	var out []disagreement
	for i, a := range resolved {
		for _, b := range resolved[i+1:] {
			ra, rb := a.members[0], b.members[0]
			// Reported by two different devices, about the same pair of devices.
			if ra.from == rb.from || ra.to == nil || rb.to == nil {
				continue
			}
			if !(ra.from == rb.to && ra.to == rb.from) {
				continue
			}
			if shared(a, b) != 1 {
				continue
			}
			keys := []string{ra.from.key, rb.from.key}
			slices.Sort(keys)
			pair := keys[0] + "|" + keys[1]
			if seen[pair] {
				continue
			}
			seen[pair] = true
			out = append(out, disagreement{
				subject: "device:" + keys[0],
				detail: map[string]any{
					"devices": keys,
					"reports": []map[string]string{
						{"from": ra.nearRef(), "says": ra.farRef()},
						{"from": rb.nearRef(), "says": rb.farRef()},
					},
				},
				evidence: []int64{min(ra.obs, rb.obs), max(ra.obs, rb.obs)},
			})
		}
	}
	return out
}

// shared counts the endpoint references two groups have in common.
func shared(a, b *linkGroup) int {
	n := 0
	for _, ref := range []string{a.from, a.to} {
		if ref == b.from || ref == b.to {
			n++
		}
	}
	return n
}
