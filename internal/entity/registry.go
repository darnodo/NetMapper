package entity

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/darnodo/NetMapper/internal/store"
)

// registry is what device and device_identifier say about the identifiers this snapshot observed,
// after the perimeter's decisions have been applied to them, plus what this resolution will add.
// It is the reason a device key means the same thing in two runs (research R5).
type registry struct {
	perimeter string
	// keyOf maps `<kind>:<value>` to the device it belongs to.
	keyOf map[string]string
	// weak records which known devices were minted without a strong identifier.
	weak map[string]bool
	// taken records the keys already used inside the snapshot being resolved. Two entities of one
	// snapshot can never carry the same key, so a key already used is not offered again (FR-024).
	taken map[string]bool
	// What the write will store: one row per device this snapshot assigned, whether it was minted
	// here or matched, and the identifiers attributed here.
	devices []device
	attribs []attrib
}

type device struct {
	key         string
	weak        bool
	first, last time.Time
}

type attrib struct {
	kind, value, key string
	first            time.Time
}

// loadRegistry reads what the perimeter already knows about the identifiers this snapshot observed.
// A merge makes the second key's identifiers read as the first's; a split takes an identifier away
// from a device before any lookup sees it (FR-009, FR-012, research R7).
func loadRegistry(ctx context.Context, db store.DB, perimeter string, groups []ClaimGroup, dec decisions) (*registry, error) {
	r := &registry{perimeter: perimeter, keyOf: map[string]string{}, weak: map[string]bool{}, taken: map[string]bool{}}

	var tokens []string
	for _, g := range groups {
		for _, c := range g.Claims {
			if c.strong() && !slices.Contains(tokens, c.token()) {
				tokens = append(tokens, c.token())
			}
		}
	}
	if len(tokens) == 0 {
		return r, nil
	}

	rows, err := db.Query(ctx, `
		SELECT di.kind, di.value, di.key, d.weak
		FROM device_identifier di
		JOIN device d ON d.perimeter_name = di.perimeter_name AND d.key = di.key
		WHERE di.perimeter_name = $1 AND di.kind || ':' || di.value = ANY($2)`, perimeter, tokens)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, value, key string
		var weak bool
		if err := rows.Scan(&kind, &value, &key, &weak); err != nil {
			return nil, err
		}
		token := kind + ":" + value
		if dec.detaches(key, token) {
			continue
		}
		key = dec.resolve(key)
		r.keyOf[token] = key
		r.weak[key] = weak
	}
	return r, rows.Err()
}

// assignKeys gives every component the key that names its device, matching it on any of its strong
// identifiers or minting one from its anchor. Components are taken in the order group left them, so
// the same snapshot always mints in the same sequence (FR-021, FR-013, research R5).
func assignKeys(s snapshot, comps []component, reg *registry) ([]Entity, []pending) {
	var entities []Entity
	var conflicts []pending

	for _, c := range comps {
		first, last := c.seen()
		tokens := c.strongTokens()

		// Every device the registry already attributes one of these identifiers to.
		var cands []string
		bridging := map[string][]string{}
		for _, t := range tokens {
			if k, ok := reg.keyOf[t]; ok {
				bridging[k] = append(bridging[k], t)
				if !slices.Contains(cands, k) {
					cands = append(cands, k)
				}
			}
		}
		slices.Sort(cands)
		free := slices.DeleteFunc(slices.Clone(cands), func(k string) bool { return reg.taken[k] })

		var key string
		var weak bool
		switch {
		case len(free) > 0:
			// The lowest key wins every run, so a component matching several known devices does not
			// flap. Only an operator merge makes them one device (FR-023, research R6).
			key, weak = free[0], reg.weak[free[0]]
			if len(free) > 1 {
				p := pending{sort: "across_devices", keys: free, evidence: c.observations(),
					identifiers: map[string][]string{}}
				for _, k := range free {
					p.identifiers[k] = bridging[k]
				}
				conflicts = append(conflicts, p)
			}
		default:
			// Minted from the anchor: the lowest `<kind>:<value>` the component carries that does not
			// already name another device. Derived from an observed identifier rather than from a
			// sequence, so wiping every computed row and replaying mints the same key again (SC-008).
			key = mintable(tokens, reg)
			if key == "" {
				// Nothing strong left to name it with, either because it carries no strong identifier
				// (FR-022) or because every one it carries already names another device of this
				// snapshot. A hostname would collide, so the address it answered on is the handle.
				key, weak = "addr:"+c.winner().Target, true
			}
		}
		reg.taken[key] = true
		reg.devices = append(reg.devices, device{key: key, weak: weak, first: first, last: last})

		// Attribute the identifiers this device carries, leaving alone any that already name another
		// device: one identifier belongs to one device, and moving it is an operator's call.
		for _, t := range tokens {
			if k, ok := reg.keyOf[t]; ok && k != key {
				continue
			}
			kind, value, _ := strings.Cut(t, ":")
			reg.attribs = append(reg.attribs, attrib{kind: kind, value: value, key: key, first: first})
			reg.keyOf[t] = key
		}

		entities = append(entities, Entity{
			SnapshotID: s.id, DeviceKey: key, Weak: weak, Attributes: c.attributes(),
			FirstSeen: first, LastSeen: last, Claims: c.claims(),
		})
	}
	return entities, conflicts
}

// mintable is the lowest identifier a component can be named after: one no other device already
// holds and no other entity of this snapshot has taken. Two entities of one snapshot never carry the
// same key, so a key already in use is not offered a second time (FR-024).
func mintable(tokens []string, reg *registry) string {
	for _, t := range tokens {
		if _, attributed := reg.keyOf[t]; !attributed && !reg.taken[t] {
			return t
		}
	}
	return ""
}

// materialise turns the collisions found while grouping and keying into findings, now that every
// component has a key to be named by (FR-007, research R11).
func materialise(entities []Entity, pendings []pending) []Conflict {
	var out []Conflict
	for _, p := range pendings {
		keys := slices.Clone(p.keys)
		for _, m := range p.members {
			keys = append(keys, entities[m].DeviceKey)
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)

		detail := map[string]any{"conflict": p.sort, "keys": keys}
		if p.kind != "" {
			detail["kind"] = p.kind
		}
		if len(p.values) > 0 {
			detail["values"] = p.values
		}
		if len(p.identifiers) > 0 {
			detail["identifiers"] = p.identifiers
		}
		out = append(out, Conflict{Subject: strings.Join(keys, " "), Detail: detail, Evidence: p.evidence})
	}
	return out
}

// Conflict is one identity collision, ready to be written as a finding with the observations behind
// each side as its evidence.
type Conflict struct {
	Subject  string
	Detail   map[string]any
	Evidence []int64
}
