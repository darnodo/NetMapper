package graph

// This file builds the layer 2 domains of a snapshot: per VLAN, the devices joined by cables whose
// two ends carry that VLAN the same way, tagged or untagged (feature 009). It runs inside the
// projection, after the l1_link edges exist, and reads only the platform-neutral vlans and
// interface_vlans families (research R1).

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/darnodo/NetMapper/internal/store"
)

// l2State is what the projector knows about one entity's layer 2 configuration (research R2).
type l2State struct {
	// declared: a vlans observation was collected, so the entity is a member of each VLAN in it.
	declared bool
	// portsKnown: an interface_vlans observation was collected or empty, so what each of its ports
	// carries is known, including "nothing".
	portsKnown bool
	// portsObs is the lowest interface_vlans observation, the member row's port evidence.
	portsObs int64
	vlans    map[int]vlanRow
	ports    map[string]portVLANs
}

// vlanRow is one declared VLAN of one device, with the observation that declared it.
type vlanRow struct {
	name string
	obs  int64
}

// portVLANs is one interface_vlans row: what a switched port is configured to carry.
type portVLANs struct {
	mode    string
	access  int
	native  int
	active  []string
	channel string
	obs     int64
}

// readVLANs fills each entity's l2State from the active parse generation. The status query tells
// "nothing to report" (empty) from "not known" (unreachable, parse_failed, unsupported, missing),
// which readFamily cannot, since it reads collected observations only.
//
// A VLAN ID or a port seen in two observations of one entity keeps the row of the lowest
// observation id, the same tie rule as interface aliases (research R2). readFamily orders by
// observation id, so the first row seen is that one.
func (p *projection) readVLANs(ctx context.Context, db store.DB) error {
	for _, e := range p.entities {
		e.l2 = &l2State{vlans: map[int]vlanRow{}, ports: map[string]portVLANs{}}
	}
	rows, err := db.Query(ctx, `
		SELECT coalesce(t.parent_task_id, o.task_id), o.task_id, o.fact_family, o.status, o.id
		FROM observation o
		JOIN parse_generation pg
		  ON pg.snapshot_id = o.snapshot_id AND pg.id = o.parse_generation_id AND pg.active
		LEFT JOIN task t ON t.job_id = $2 AND t.id = o.task_id
		WHERE o.snapshot_id = $1 AND o.fact_family IN ('vlans', 'interface_vlans')
		  AND o.status IN ('collected', 'empty')
		ORDER BY o.id`, p.snap.id, p.snap.jobID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			parent, task, obs int64
			family, status    string
		)
		if err := rows.Scan(&parent, &task, &family, &status, &obs); err != nil {
			return err
		}
		owner := p.byTask[task]
		if owner == nil {
			owner = p.byTask[parent]
		}
		if owner == nil {
			continue
		}
		switch {
		case family == "vlans" && status == "collected":
			owner.l2.declared = true
		case family == "interface_vlans" && !owner.l2.portsKnown:
			owner.l2.portsKnown, owner.l2.portsObs = true, obs
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	vlanRows, err := p.readFamily(ctx, db, "vlans")
	if err != nil {
		return err
	}
	for _, r := range vlanRows {
		id := num(r.row, "vlan_id")
		if id == nil {
			continue
		}
		if _, seen := r.owner.l2.vlans[int(*id)]; !seen {
			r.owner.l2.vlans[int(*id)] = vlanRow{name: str(r.row, "name"), obs: r.obs}
		}
	}
	portRows, err := p.readFamily(ctx, db, "interface_vlans")
	if err != nil {
		return err
	}
	for _, r := range portRows {
		name := str(r.row, "interface")
		if _, seen := r.owner.l2.ports[name]; name == "" || seen {
			continue
		}
		r.owner.l2.ports[name] = portVLANs{
			mode: str(r.row, "mode"), access: vlanNum(r.row, "access_vlan"),
			native: vlanNum(r.row, "native_vlan"), active: strs(r.row, "active_vlans"),
			channel: str(r.row, "channel"), obs: r.obs,
		}
	}
	return nil
}

// able says whether an entity can join anything over a cable: what it declares and what its ports
// carry are both known (FR-003).
func (s *l2State) able() bool { return s.declared && s.portsKnown }

// effective is the row that decides what a port carries: its own, or its channel's when it is a
// member of one (feature 008 records VLAN settings on the channel).
func (s *l2State) effective(port string) (portVLANs, bool) {
	pv, ok := s.ports[port]
	if ok && pv.channel != "" {
		pv, ok = s.ports[pv.channel]
		if ok && pv.channel != "" {
			return portVLANs{}, false // a channel that is itself a member: nothing sensible to read
		}
	}
	return pv, ok
}

// carries says what a port puts on its cable: one VLAN untagged (0 for none) and the declared VLANs
// it tags, sorted (research R3). An access port carries its access VLAN untagged. A trunk carries
// its native VLAN untagged when the device reports it active, and every other declared VLAN in its
// active list tagged. Any other mode, or no row, carries nothing.
func (s *l2State) carries(port string) (untagged int, tagged []int) {
	pv, ok := s.effective(port)
	if !ok {
		return 0, nil
	}
	switch pv.mode {
	case "access":
		return pv.access, nil
	case "trunk":
		for _, v := range slices.Sorted(maps.Keys(s.vlans)) {
			if v != pv.native && inRanges(v, pv.active) {
				tagged = append(tagged, v)
			}
		}
		if pv.native != 0 && inRanges(pv.native, pv.active) {
			untagged = pv.native
		}
		return untagged, tagged
	}
	return 0, nil
}

// inRanges reports whether a VLAN ID is in a normalised range list ("10", "30-40"). The parser
// already validated the list (feature 008, FR-006), so an item that does not parse is skipped
// rather than reported.
func inRanges(v int, ranges []string) bool {
	for _, item := range ranges {
		lo, hi, isRange := strings.Cut(item, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		if v >= a && v <= b {
			return true
		}
	}
	return false
}

// domain is one broadcast domain being built.
type domain struct {
	id         int64
	key        int
	label      string
	confidence string
	members    []node
	links      map[*edge]string // joined or unchecked
}

// node is one declared VLAN of one device: the unit domains are made of.
type node struct {
	e    *entity
	vlan int
}

// joint is one cable joining two nodes, kept until the domains exist to say which domain it belongs
// to. Both nodes end up in the same domain, so the first one is enough to find it.
type joint struct {
	at   int // node index
	edge *edge
}

// buildDomains computes the snapshot's domains (research R4). A node is one declared VLAN of one
// device. Each l1_link whose two ends can join unions the nodes of the VLANs both ends carry the same
// way, tagged or untagged; each connected component is one domain. Union-find's choice of root never
// reaches the output: domains are ordered by key, then by their first member.
func (p *projection) buildDomains() {
	var nodes []node
	index := map[node]int{}
	for _, e := range p.entities { // device key order
		if !e.l2.declared {
			continue
		}
		for _, v := range slices.Sorted(maps.Keys(e.l2.vlans)) {
			n := node{e: e, vlan: v}
			index[n] = len(nodes)
			nodes = append(nodes, n)
		}
	}
	parent := make([]int, len(nodes))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]] // path halving
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	var joints, unchecked []joint
	mismatches := map[string]*mismatch{}
	for _, g := range p.edges {
		if g.typ != "l1_link" || g.fromEntity == nil || g.toEntity == nil {
			continue // a far end no entity accounts for adds no member (FR-005)
		}
		a, b := g.fromEntity, g.toEntity
		if g.fromPort == nil || g.toPort == nil || !a.l2.able() || !b.l2.able() {
			unchecked = append(unchecked, uncheckedEnd(index, g, a, g.fromPort, b, g.toPort)...)
			unchecked = append(unchecked, uncheckedEnd(index, g, b, g.toPort, a, g.fromPort)...)
			continue
		}
		ua, ta := a.l2.carries(g.fromPort.name)
		ub, tb := b.l2.carries(g.toPort.name)
		// Tagged: the same VLAN at both ends. carries only lists declared VLANs, so both nodes exist.
		for _, v := range ta {
			if slices.Contains(tb, v) {
				x, y := index[node{a, v}], index[node{b, v}]
				union(x, y)
				joints = append(joints, joint{at: x, edge: g})
			}
		}
		// Untagged: both ends carry one, and each is declared on its own device. Frames cross whatever
		// the two IDs are, so different IDs are joined too, and reported (research R7).
		x, okA := index[node{a, ua}]
		y, okB := index[node{b, ub}]
		if ua != 0 && ub != 0 && okA && okB {
			union(x, y)
			joints = append(joints, joint{at: x, edge: g})
			if ua != ub {
				addMismatch(mismatches, g, a, g.fromPort.name, ua, b, g.toPort.name, ub)
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(mismatches)) {
		p.mismatches = append(p.mismatches, mismatches[k].finding())
	}

	byRoot := map[int]*domain{}
	for i, n := range nodes {
		d := byRoot[find(i)]
		if d == nil {
			d = &domain{key: n.vlan, links: map[*edge]string{}}
			byRoot[find(i)] = d
			p.domains = append(p.domains, d)
		}
		d.members = append(d.members, n) // node order: device key, then VLAN
		d.key = min(d.key, n.vlan)
	}
	for _, j := range joints {
		byRoot[find(j.at)].links[j.edge] = "joined"
	}
	for _, j := range unchecked {
		if d := byRoot[find(j.at)]; d.links[j.edge] == "" {
			d.links[j.edge] = "unchecked"
		}
	}
	for _, d := range p.domains {
		d.confidence = "complete"
		if slices.Contains(slices.Collect(maps.Values(d.links)), "unchecked") {
			d.confidence = "partial"
		}
		d.label = label(d.members)
	}
	slices.SortStableFunc(p.domains, func(x, y *domain) int {
		if x.key != y.key {
			return x.key - y.key
		}
		if c := strings.Compare(x.members[0].e.key, y.members[0].e.key); c != 0 {
			return c
		}
		return x.members[0].vlan - y.members[0].vlan
	})
}

// uncheckedEnd lists the domains one end of a cable that cannot be checked should be marked partial
// on (research R5), as the nodes those domains hold. The cable joins nothing either way.
//
//   - The end can join but the far end cannot, or the far port is unknown: the VLANs this end carries
//     on that port, tagged or untagged, might reach the far side, so each of their domains is partial.
//   - The end declares VLANs but its own port data is missing: any of its VLANs might be on this
//     cable, so every domain it is in is partial (clarified 2026-10-12).
func uncheckedEnd(index map[node]int, g *edge, e *entity, port *iface, far *entity, farPort *iface) []joint {
	var out []joint
	switch {
	case e == far:
	case e.l2.able() && port != nil && (!far.l2.able() || farPort == nil):
		untagged, tagged := e.l2.carries(port.name)
		for _, v := range append(tagged, untagged) {
			if i, ok := index[node{e, v}]; ok {
				out = append(out, joint{at: i, edge: g})
			}
		}
	case e.l2.declared && !e.l2.portsKnown:
		for _, v := range slices.Sorted(maps.Keys(e.l2.vlans)) {
			out = append(out, joint{at: index[node{e, v}], edge: g})
		}
	}
	return out
}

// mismatch is two facing ports carrying different VLANs untagged, with every cable between them.
type mismatch struct {
	ports [2]string // port references, lower first; a member port is replaced by its channel
	vlans [2]int    // the untagged VLAN at each port, same order
	edges []*edge
	obs   map[int64]bool
}

// addMismatch records one cable of a mismatch. The key is the pair of facing ports, so the member
// cables of one port-channel pair are one finding, since the setting to fix is on the channel
// (clarified 2026-10-12).
func addMismatch(all map[string]*mismatch, g *edge, a *entity, portA string, va int, b *entity, portB string, vb int) {
	ra, rb := "if:"+a.key+"/"+a.l2.facing(portA), "if:"+b.key+"/"+b.l2.facing(portB)
	if rb < ra {
		ra, rb, va, vb = rb, ra, vb, va
	}
	m := all[ra+"|"+rb]
	if m == nil {
		m = &mismatch{ports: [2]string{ra, rb}, vlans: [2]int{va, vb}, obs: map[int64]bool{}}
		all[ra+"|"+rb] = m
	}
	m.edges = append(m.edges, g)
	for _, side := range []struct {
		s    *l2State
		port string
	}{{a.l2, portA}, {b.l2, portB}} {
		if pv, ok := side.s.effective(side.port); ok {
			m.obs[pv.obs] = true
		}
	}
	for _, ev := range g.evidence {
		m.obs[ev.obs] = true
	}
}

// finding turns a mismatch into the vlan_mismatch finding it raises.
func (m *mismatch) finding() disagreement {
	var edges []string
	for _, g := range m.edges {
		edges = append(edges, g.name())
	}
	slices.Sort(edges)
	return disagreement{
		subject: m.ports[0],
		detail: map[string]any{
			"ports": m.ports[:],
			"vlans": m.vlans[:],
			"edges": edges,
		},
		evidence: slices.Sorted(maps.Keys(m.obs)),
	}
}

// facing is the name a port is configured under: its channel when it is a member of one.
func (s *l2State) facing(port string) string {
	if pv, ok := s.ports[port]; ok && pv.channel != "" {
		return pv.channel
	}
	return port
}

// label is the VLAN name every member that has one agrees on, or nothing (research R6). A majority
// is not agreement: picking one of several names would present a guess as a fact.
func label(members []node) string {
	name := ""
	for _, m := range members {
		switch n := m.e.l2.vlans[m.vlan].name; {
		case n == "":
		case name == "":
			name = n
		case n != name:
			return ""
		}
	}
	return name
}

func vlanNum(row map[string]any, key string) int {
	if v := num(row, key); v != nil {
		return int(*v)
	}
	return 0
}

func strs(row map[string]any, key string) []string {
	list, _ := row[key].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
