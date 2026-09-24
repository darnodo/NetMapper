package entity

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/darnodo/NetMapper/internal/store"
)

// decisions is a perimeter's recorded decisions reduced to what one resolution needs. The table
// itself is append only and is never written here: a decision is replayed, never applied in place
// (FR-009, Principle II).
type decisions struct {
	// canon maps a key that was merged away to the key that now stands for the device.
	canon map[string]string
	// forbidden holds the unordered key pairs a never-merge covers.
	forbidden map[[2]string]bool
	// detached holds the identifiers a split removed from a device, keyed `<key>|<kind>:<value>`.
	detached map[string]bool
	// highest is the highest decision id read for the perimeter, 0 when it has none. It is what
	// explains a result afterwards, whether or not every decision it read applied.
	highest int64
}

type decisionRow struct {
	id         int64
	kind       string
	subjects   []string
	identifier struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
}

// readDecisions reads the perimeter's decisions in id order and reduces them. On the same pair of
// subjects the last one recorded governs, and every earlier row stays readable in the table
// (FR-011, research R7).
func readDecisions(ctx context.Context, db store.DB, perimeter string) (decisions, error) {
	d := decisions{canon: map[string]string{}, forbidden: map[[2]string]bool{}, detached: map[string]bool{}}
	rows, err := db.Query(ctx, `
		SELECT id, kind, subjects, identifier
		FROM entity_decision WHERE perimeter_name = $1 ORDER BY id`, perimeter)
	if err != nil {
		return d, err
	}
	defer rows.Close()

	// The last decision on a pair wins, so the pairs are collected first and read afterwards.
	type winner struct {
		kind     string
		subjects []string
	}
	pairs := map[[2]string]winner{}
	var all []decisionRow
	for rows.Next() {
		var r decisionRow
		var raw []byte
		if err := rows.Scan(&r.id, &r.kind, &r.subjects, &raw); err != nil {
			return d, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &r.identifier); err != nil {
				return d, err
			}
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	for _, r := range all {
		d.highest = max(d.highest, r.id)
		switch r.kind {
		case "split":
			d.detached[r.subjects[0]+"|"+r.identifier.Kind+":"+r.identifier.Value] = true
		case "merge", "never_merge":
			pairs[pairOf(r.subjects[0], r.subjects[1])] = winner{kind: r.kind, subjects: r.subjects}
		}
	}

	merged := map[string]string{}
	for pair, w := range pairs {
		if w.kind == "never_merge" {
			d.forbidden[pair] = true
			continue
		}
		// The first subject is the key the device keeps, which is what the operator named first.
		merged[w.subjects[1]] = w.subjects[0]
	}
	for from := range merged {
		d.canon[from] = follow(merged, from)
	}
	return d, nil
}

// follow walks a chain of merges to the key that stands for the device. A chain that loops back on
// itself, which two contradicting merges can produce, stops at its lowest key so the result is the
// same on every run.
func follow(merged map[string]string, from string) string {
	seen := []string{from}
	for {
		to, ok := merged[seen[len(seen)-1]]
		if !ok {
			return seen[len(seen)-1]
		}
		if slices.Contains(seen, to) {
			return slices.Min(seen)
		}
		seen = append(seen, to)
	}
}

// resolve maps a key through the merges recorded for the perimeter.
func (d decisions) resolve(key string) string {
	if to, ok := d.canon[key]; ok {
		return to
	}
	return key
}

// forbids reports whether two sets of device keys hold a pair a never-merge covers. The link that
// would bridge them is then dropped, and no collision is reported for it: the answer is already
// recorded (FR-009, US3-2).
func (d decisions) forbids(a, b map[string]bool) bool {
	if len(d.forbidden) == 0 {
		return false
	}
	for x := range a {
		for y := range b {
			if d.forbidden[pairOf(x, y)] {
				return true
			}
		}
	}
	return false
}

// detaches reports whether a split took this identifier away from this device.
func (d decisions) detaches(key, token string) bool {
	return d.detached[key+"|"+token]
}

// silences reports whether a split took this identifier away from a device either side already
// resolves to. The identifier is then no longer a reason to call the two one device: the operator has
// said it does not belong to that box, and saying it again from the other end would undo the
// decision (FR-012, research R7).
func (d decisions) silences(token string, a, b map[string]bool) bool {
	if len(d.detached) == 0 {
		return false
	}
	for _, set := range []map[string]bool{a, b} {
		for k := range set {
			if d.detaches(k, token) {
				return true
			}
		}
	}
	return false
}

func pairOf(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}
