package entity

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/darnodo/NetMapper/internal/store"
)

// component is one set of claim groups that becomes one entity.
type component struct {
	groups []ClaimGroup
}

// pending is a collision found before the device keys are known. It is materialised into a finding
// once assignKeys has named the components involved (research R4, R6).
type pending struct {
	sort    string // "within_snapshot" or "across_devices"
	kind    string
	values  []string
	members []int    // component indices, whose keys the finding names
	keys    []string // keys already known, for an across_devices collision
	// identifiers records which of them bridged which key, so a reader sees what linked the two.
	identifiers map[string][]string
	evidence    []int64 // observation ids behind each side
}

// readGroups reads every identity observation of the snapshot's active parse generation with the
// claims it produced. Ordered by observation id, so the grouping never depends on the order rows
// happen to come back in (FR-019, research R2, R10).
func readGroups(ctx context.Context, db store.DB, snapshotID int64) ([]ClaimGroup, error) {
	rows, err := db.Query(ctx, `
		SELECT o.id, o.task_id, host(o.target), o.collected_at,
		       coalesce(o.parsed -> 0 ->> 'platform', ''),
		       (o.parsed -> 0 ? 'duplicate_of_task'),
		       coalesce(c.id, 0), coalesce(c.kind, ''), coalesce(c.value, ''), coalesce(c.strength, '')
		FROM observation o
		JOIN parse_generation pg
		  ON pg.snapshot_id = o.snapshot_id AND pg.id = o.parse_generation_id AND pg.active
		LEFT JOIN identifier_claim c
		  ON c.snapshot_id = o.snapshot_id AND c.observation_id = o.id
		WHERE o.snapshot_id = $1 AND o.fact_family = 'identity' AND o.status = 'collected'
		ORDER BY o.id, c.kind, c.value, c.id`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ClaimGroup
	for rows.Next() {
		var (
			obs, task, claimID int64
			target, platform   string
			duplicate          bool
			collected          time.Time
			kind, value, str   string
		)
		if err := rows.Scan(&obs, &task, &target, &collected, &platform, &duplicate,
			&claimID, &kind, &value, &str); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ObservationID != obs {
			out = append(out, ClaimGroup{ObservationID: obs, TaskID: task, Target: target,
				Platform: platform, Duplicate: duplicate, CollectedAt: collected})
		}
		g := &out[len(out)-1]
		if claimID != 0 {
			g.Claims = append(g.Claims, Claim{ID: claimID, Kind: kind, Value: value, Strength: str})
			// A hostname is weak, so it never groups anything; it is what the entity is called.
			if kind == "hostname" && str == "weak" && g.Hostname == "" {
				g.Hostname = value
			}
		}
	}
	return out, rows.Err()
}

// group links claim groups that share a strong identifier, transitively, and returns the components
// that each become one entity. A weak identifier never links anything, however unique it looks
// (FR-002, FR-003, research R3).
//
// A component that disagrees with itself on a strong kind is not merged at all: every claim group in
// it becomes its own entity and one finding names the collision. Refusing the whole component keeps
// the rule explainable in one sentence and reproducible, which cutting the minimum set of links
// would not be (FR-007, research R4).
func group(groups []ClaimGroup, reg *registry, dec decisions) ([]component, []pending) {
	u := newUnionFind(len(groups))

	// The keys the registry already attributes to each component's identifiers. A never-merge is a
	// pair of keys that must never end up in one component, so the link that would bridge them is
	// dropped rather than reported (FR-009, research R7).
	keys := make([]map[string]bool, len(groups))
	for i, g := range groups {
		keys[i] = map[string]bool{}
		for _, c := range g.Claims {
			if c.strong() {
				if k, ok := reg.keyOf[c.token()]; ok {
					keys[i][k] = true
				}
			}
		}
	}

	// Two claim groups are linked by a strong identifier they share, and by a device key they both
	// already resolve to: a merge decision names keys, not identifiers, and it has to bring together
	// claim groups that share nothing observable (FR-009, research R7).
	for _, t := range append(sortedTokens(groups), sortedKeys(keys)...) {
		members := t.members
		for _, j := range members[1:] {
			a, b := u.find(members[0]), u.find(j)
			if a == b || dec.forbids(keys[a], keys[b]) {
				continue
			}
			if !t.byKey && dec.silences(t.token, keys[a], keys[b]) {
				continue
			}
			u.union(a, b)
			root := u.find(a)
			for _, k := range []int{a, b} {
				for key := range keys[k] {
					keys[root][key] = true
				}
			}
		}
	}

	// Components in a deterministic order: by the lowest token of each, so the same snapshot always
	// mints and takes keys in the same sequence (FR-013). Stable, because two components share their
	// lowest token whenever the link between them was dropped by a never-merge or silenced by a split,
	// and an unstable sort would then let them swap which one keeps the matched key.
	roots := map[int][]ClaimGroup{}
	for i, g := range groups {
		r := u.find(i)
		roots[r] = append(roots[r], g)
	}
	var comps []component
	for _, r := range slices.Sorted(maps.Keys(roots)) {
		comps = append(comps, component{groups: roots[r]})
	}
	slices.SortStableFunc(comps, func(a, b component) int {
		return cmp.Compare(anchorOf(a.groups), anchorOf(b.groups))
	})

	// Split the components that disagree with themselves, keeping the pieces in the same order.
	var out []component
	var conflicts []pending
	for _, c := range comps {
		kind, values := "", []string(nil)
		if !settled(c.groups, keys, groups) {
			kind, values = contradicts(c.groups)
		}
		if kind == "" {
			out = append(out, c)
			continue
		}
		pieces := make([]component, 0, len(c.groups))
		for _, g := range c.groups {
			pieces = append(pieces, component{groups: []ClaimGroup{g}})
		}
		slices.SortStableFunc(pieces, func(a, b component) int {
			return cmp.Compare(anchorOf(a.groups), anchorOf(b.groups))
		})
		p := pending{sort: "within_snapshot", kind: kind, values: values}
		for _, piece := range pieces {
			p.members = append(p.members, len(out))
			out = append(out, piece)
			for _, g := range piece.groups {
				p.evidence = append(p.evidence, g.ObservationID)
			}
		}
		conflicts = append(conflicts, p)
	}
	return out, conflicts
}

// settled reports whether the registry already says these claim groups are one device: every one of
// them resolves to the same single key. That is either what a previous resolution concluded or what
// an operator merged, and neither is an automatic merge for the contradiction rule to refuse. Two
// devices an operator has declared to be one box do disagree on their serials, and saying so again
// on every resolution would make the decision impossible to act on (FR-009, research R4, R7).
func settled(comp []ClaimGroup, keys []map[string]bool, all []ClaimGroup) bool {
	var only string
	for _, g := range comp {
		set := keys[indexOf(all, g)]
		if len(set) != 1 {
			return false
		}
		for k := range set {
			if only == "" {
				only = k
			} else if only != k {
				return false
			}
		}
	}
	return only != ""
}

func indexOf(all []ClaimGroup, g ClaimGroup) int {
	for i := range all {
		if all[i].ObservationID == g.ObservationID {
			return i
		}
	}
	return 0
}

// contradicts reports the first strong kind the groups disagree on, and the values they disagree
// with, or "" when they agree on every kind they share.
func contradicts(groups []ClaimGroup) (string, []string) {
	seen := map[string]map[string]bool{}
	for _, g := range groups {
		for _, c := range g.Claims {
			if !c.strong() {
				continue
			}
			if seen[c.Kind] == nil {
				seen[c.Kind] = map[string]bool{}
			}
			seen[c.Kind][c.Value] = true
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(seen)) {
		if len(seen[kind]) > 1 {
			return kind, slices.Sorted(maps.Keys(seen[kind]))
		}
	}
	return "", nil
}

// attributes builds what an entity carries beyond its key. The weak values come from the winning
// observation, the one the crawl did not mark a duplicate, so two recomputations of one snapshot
// never disagree (FR-003, FR-008, research R15).
func (c component) attributes() Attributes {
	a := Attributes{Targets: []string{}, Identifiers: map[string]string{}}
	w := c.winner()
	a.Hostname, a.Platform = w.Hostname, w.Platform
	a.Targets = append(a.Targets, w.Target)
	for _, g := range c.ordered() {
		if g.ObservationID != w.ObservationID && !slices.Contains(a.Targets, g.Target) {
			a.Targets = append(a.Targets, g.Target)
		}
		for _, cl := range g.Claims {
			// A kind seen twice inside one entity keeps its lowest value: the contradicting case is
			// already refused, so this only settles a repeat.
			if cl.strong() {
				if v, ok := a.Identifiers[cl.Kind]; !ok || cl.Value < v {
					a.Identifiers[cl.Kind] = cl.Value
				}
			}
		}
	}
	return a
}

// winner is the observation that stands for the device itself rather than one of its addresses. The
// lowest observation id breaks a tie, so a component of duplicates alone still resolves the same way
// every time.
func (c component) winner() ClaimGroup {
	ordered := c.ordered()
	for _, g := range ordered {
		if !g.Duplicate {
			return g
		}
	}
	return ordered[0]
}

func (c component) ordered() []ClaimGroup {
	out := slices.Clone(c.groups)
	slices.SortFunc(out, func(a, b ClaimGroup) int { return int(a.ObservationID - b.ObservationID) })
	return out
}

// seen is the range of collection times the entity's own evidence covers, which is what a consumer
// downstream reads as freshness rather than the resolution time (FR-006, Principle I).
func (c component) seen() (first, last time.Time) {
	for i, g := range c.groups {
		if i == 0 || g.CollectedAt.Before(first) {
			first = g.CollectedAt
		}
		if i == 0 || g.CollectedAt.After(last) {
			last = g.CollectedAt
		}
	}
	return first, last
}

// claims lists the identifier claims the entity was built from, in a stable order.
func (c component) claims() []int64 {
	var out []int64
	for _, g := range c.ordered() {
		for _, cl := range g.Claims {
			out = append(out, cl.ID)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// strongTokens lists the component's strong identifiers as `<kind>:<value>`, lowest first. The
// lowest is the anchor a minted key is named after (research R5).
func (c component) strongTokens() []string {
	var out []string
	for _, g := range c.groups {
		for _, cl := range g.Claims {
			if cl.strong() && !slices.Contains(out, cl.token()) {
				out = append(out, cl.token())
			}
		}
	}
	slices.Sort(out)
	return out
}

// claimOf finds a strong claim by token, for a finding that has to name the kind and the value.
func (c component) claimOf(token string) (Claim, bool) {
	for _, g := range c.ordered() {
		for _, cl := range g.Claims {
			if cl.strong() && cl.token() == token {
				return cl, true
			}
		}
	}
	return Claim{}, false
}

func (c component) observations() []int64 {
	var out []int64
	for _, g := range c.ordered() {
		out = append(out, g.ObservationID)
	}
	return out
}

// anchorOf is the token a component is ordered and named by: its lowest strong identifier, or the
// address its winning observation answered on when it has none (FR-022).
func anchorOf(groups []ClaimGroup) string {
	c := component{groups: groups}
	if t := c.strongTokens(); len(t) > 0 {
		return t[0]
	}
	return "addr:" + c.winner().Target
}

type tokenMembers struct {
	token   string
	members []int
	// byKey is true when the link is a device key both sides already resolve to rather than an
	// identifier they both carry. A split detaches an identifier, so it silences only the latter: a
	// device key can be spelled like an identifier and must not be silenced by accident.
	byKey bool
}

// sortedKeys lists every device key the registry already resolves a claim group to, with the groups
// resolving to it, in key order.
func sortedKeys(keys []map[string]bool) []tokenMembers {
	byKey := map[string][]int{}
	for i, set := range keys {
		for k := range set {
			byKey[k] = append(byKey[k], i)
		}
	}
	var out []tokenMembers
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		members := byKey[k]
		slices.Sort(members)
		out = append(out, tokenMembers{token: k, members: members, byKey: true})
	}
	return out
}

// sortedTokens lists every strong identifier with the claim groups carrying it, token order, so the
// links are followed in the same order on every run.
func sortedTokens(groups []ClaimGroup) []tokenMembers {
	byToken := map[string][]int{}
	for i, g := range groups {
		for _, c := range g.Claims {
			if c.strong() && !slices.Contains(byToken[c.token()], i) {
				byToken[c.token()] = append(byToken[c.token()], i)
			}
		}
	}
	var out []tokenMembers
	for _, t := range slices.Sorted(maps.Keys(byToken)) {
		out = append(out, tokenMembers{token: t, members: byToken[t]})
	}
	return out
}

type unionFind struct{ parent []int }

func newUnionFind(n int) *unionFind {
	u := &unionFind{parent: make([]int, n)}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u
}

func (u *unionFind) find(i int) int {
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]]
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(i, j int) {
	a, b := u.find(i), u.find(j)
	if a == b {
		return
	}
	if a > b {
		a, b = b, a
	}
	u.parent[b] = a
}
