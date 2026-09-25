// Package graph projects a resolved snapshot into the interfaces of each device entity and the edges
// between them. It reads the collected zone and the entity set, writes only its own tables, contacts
// no device and resolves no secret. It reads platform packs, for one thing only: the naming rules
// that turn the port spelling a neighbour reports for the far end of a cable into a canonical name
// (FR-003, research R3).
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/store"
)

// What Project refuses to do, for callers that need to tell an operator why.
var (
	ErrNotFound    = errors.New("snapshot not found")
	ErrNotClosed   = errors.New("snapshot is not closed")
	ErrNotResolved = errors.New("snapshot has no entity set")
)

// projectorVersion identifies the projection that produced a set. Bump it by hand when a change
// alters results, so the sets the old version wrote can be found and recomputed instead of being
// re-crawled (research R12).
const projectorVersion = 1

// Result is what a projection produced, for an operator command to print.
type Result struct {
	SnapshotID    int64
	Interfaces    int
	Edges         int
	Disagreements int
}

// entity is one resolved device, with what the projector needs from it: the key its references are
// built from, the platform whose naming rules canonicalise a far-end spelling, the addresses it
// answered on, its strong identifiers, and the find tasks that collected for it (research R2).
type entity struct {
	id          int64
	key         string
	platform    string
	targets     []string
	identifiers map[string]string
	tasks       []int64
	// The identity observation per address, with when it was collected: a has_address edge cites one
	// of these and must carry that one's range, not the entity's (FR-008).
	answeredOn map[string]answer
	ports      map[string]*iface
	first      time.Time
	last       time.Time
}

// answer is one identity observation an address produced.
type answer struct {
	obs int64
	at  time.Time
}

// ref is how this entity is named as an edge endpoint.
func (e *entity) ref() string { return "dev:" + e.key }

// port returns the interface by canonical name, creating it with the given source when it is new. A
// port the device described itself outranks one a neighbour revealed, so `device` never loses to
// `neighbour` (FR-006).
func (e *entity) port(name, source string, at time.Time) *iface {
	p := e.ports[name]
	if p == nil {
		p = &iface{owner: e, name: name, source: source, first: at, last: at,
			aliases: map[string]alias{}, evidence: map[int64]bool{}}
		e.ports[name] = p
		return p
	}
	if source == sourceDevice {
		p.source = sourceDevice
	}
	if at.Before(p.first) {
		p.first = at
	}
	if at.After(p.last) {
		p.last = at
	}
	return p
}

const (
	sourceDevice    = "device"
	sourceNeighbour = "neighbour"
)

// iface is one port of one device. Computed, never collected.
type iface struct {
	id     int64
	owner  *entity
	name   string
	source string

	description string
	adminState  string
	operState   string
	mac         string
	speedBPS    *int64
	mtu         *int64

	first, last time.Time
	aliases     map[string]alias
	evidence    map[int64]bool
}

// ref is how this port is named as an edge endpoint.
func (p *iface) ref() string { return "if:" + p.owner.key + "/" + p.name }

// alias is one spelling of a port, with where it came from.
type alias struct {
	spelling string
	source   string
	obs      int64
}

// see records a spelling, with who wrote it: `device` when the owner of the port named it, whether in
// its interfaces list or as the local end of its own neighbours row, and `neighbour` when another
// device named it while reporting the far end of a cable. That is the distinction US1 scenario 2 is
// about, and it is not the one interface.source draws.
//
// The lowest observation id wins when the same spelling arrives twice, and a spelling the owner wrote
// stays `device` however low the other observation's id is: a tie on the id must not flip the
// meaning. Both are tie-breaks and both are tested with inputs that tie (research R5).
func (p *iface) see(spelling, source string, obs int64) {
	if spelling == "" {
		return
	}
	a, ok := p.aliases[spelling]
	if ok && a.source == sourceDevice && source != sourceDevice {
		return
	}
	if ok && a.obs <= obs && (a.source == source || source != sourceDevice) {
		return
	}
	p.aliases[spelling] = alias{spelling: spelling, source: source, obs: min(obs, existing(a, ok, obs))}
}

// existing is the observation id already recorded for a spelling, or the new one when there is none,
// so promoting a spelling to `device` keeps the earliest evidence rather than resetting it.
func existing(a alias, ok bool, obs int64) int64 {
	if ok {
		return a.obs
	}
	return obs
}

// edge is one relationship between two endpoints, named by them.
type edge struct {
	typ         string
	fromRef     string
	toRef       string
	fromEntity  *entity
	toEntity    *entity
	fromPort    *iface
	toPort      *iface
	confidence  string
	attributes  map[string]any
	first, last time.Time
	evidence    []edgeEvidence
}

func (e *edge) name() string { return e.typ + ":" + e.fromRef + "|" + e.toRef }

type edgeEvidence struct {
	obs  int64
	side string
}

// disagreement is two devices contradicting each other about which ports are cabled.
type disagreement struct {
	subject  string
	detail   map[string]any
	evidence []int64
}

// projection is the whole set being built, held in memory for the length of one transaction.
type projection struct {
	snap     snapshot
	entities []*entity // by device key
	edges    []*edge
	findings []disagreement
}

// Project replaces a closed snapshot's interfaces and edges with the ones its observations and its
// entity set produce. It is the single entry point the engine's sweep, the operator subcommand and
// the tests go through, which is what makes "same inputs, same result" testable (FR-017).
func Project(ctx context.Context, db *pgxpool.Pool, reg *pack.Registry, snapshotID int64) (Result, error) {
	s, err := describe(ctx, db, snapshotID)
	if err != nil {
		return Result{}, err
	}
	if s.state != "closed" {
		return Result{}, fmt.Errorf("snapshot %d: %w", snapshotID, ErrNotClosed)
	}
	if !s.resolved {
		return Result{}, fmt.Errorf("snapshot %d: %w", snapshotID, ErrNotResolved)
	}
	var r Result
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		var err error
		r, err = projectIn(ctx, tx, reg, s)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return r, nil
}

// projectIn does the whole projection in one transaction: read, build, replace. Everything is inside
// it because a consumer must never see a partial set, and because the entity set the ports hang off
// must not change under it while they are being built (FR-018, research R12).
func projectIn(ctx context.Context, tx pgx.Tx, reg *pack.Registry, s snapshot) (Result, error) {
	// The lock is on the snapshot, not on the perimeter: this feature adds no cross-snapshot state,
	// so there is no registry to serialise and nothing two snapshots share (research R12).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, projectionLockClass, s.id); err != nil {
		return Result{}, err
	}

	p := &projection{snap: s}
	if err := p.readEntities(ctx, tx); err != nil {
		return Result{}, err
	}
	ifRows, err := p.readFamily(ctx, tx, "interfaces")
	if err != nil {
		return Result{}, err
	}
	nbRows, err := p.readFamily(ctx, tx, "neighbours")
	if err != nil {
		return Result{}, err
	}

	p.addDevicePorts(ifRows)
	reports := p.readReports(reg, nbRows)
	p.addReportedPorts(reports)
	p.buildLinks(reports)
	p.addAddressEdges()

	if err := p.write(ctx, tx); err != nil {
		return Result{}, err
	}
	return Result{SnapshotID: s.id, Interfaces: p.countPorts(), Edges: len(p.edges),
		Disagreements: len(p.findings)}, nil
}

// projectionLockClass keeps this feature's advisory locks from colliding with the ones 003 takes on a
// perimeter name, which use the single-argument form.
const projectionLockClass = 4

func (p *projection) countPorts() int {
	n := 0
	for _, e := range p.entities {
		n += len(e.ports)
	}
	return n
}

type snapshot struct {
	id           int64
	jobID        int64
	state        string
	resolved     bool
	resolutionAt time.Time
}

// describe reads the snapshot, the job that produced it, and whether it carries an entity set. A
// snapshot with no resolution row is not projected and is not an error either (FR-001).
func describe(ctx context.Context, db store.DB, id int64) (snapshot, error) {
	s := snapshot{id: id}
	var at *time.Time
	err := db.QueryRow(ctx, `
		SELECT s.state, j.id, r.computed_at
		FROM snapshot s
		JOIN job j ON j.snapshot_id = s.id
		LEFT JOIN resolution r ON r.snapshot_id = s.id
		WHERE s.id = $1`, id).Scan(&s.state, &s.jobID, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, fmt.Errorf("snapshot %d: %w", id, ErrNotFound)
	}
	if at != nil {
		s.resolved, s.resolutionAt = true, *at
	}
	return s, err
}

// readEntities reads the entity set with the find tasks behind it. An entity is reached from its
// claims, each claim from its identity observation, and that observation's task is the find that
// collected for the device: the neighbours observation carries the same task id, and the interfaces
// observation is the child scrape's (research R2). Ordered by device key, then task, so the whole
// projection runs in one fixed order (FR-017).
func (p *projection) readEntities(ctx context.Context, db store.DB) error {
	rows, err := db.Query(ctx, `
		SELECT e.id, e.device_key, e.attributes, e.first_seen, e.last_seen,
		       o.task_id, host(o.target), o.id, o.collected_at
		FROM entity e
		JOIN entity_claim ec ON ec.entity_id = e.id
		JOIN identifier_claim c
		  ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
		JOIN observation o ON o.snapshot_id = c.snapshot_id AND o.id = c.observation_id
		WHERE e.snapshot_id = $1 AND o.fact_family = 'identity'
		GROUP BY e.id, e.device_key, e.attributes, e.first_seen, e.last_seen,
		         o.task_id, host(o.target), o.id, o.collected_at
		ORDER BY e.device_key, o.task_id, o.id`, p.snap.id)
	if err != nil {
		return err
	}
	defer rows.Close()

	byID := map[int64]*entity{}
	for rows.Next() {
		var (
			id, task, obs   int64
			key, target     string
			attrs           []byte
			first, last, at time.Time
		)
		if err := rows.Scan(&id, &key, &attrs, &first, &last, &task, &target, &obs, &at); err != nil {
			return err
		}
		e := byID[id]
		if e == nil {
			var a struct {
				Platform    string            `json:"platform"`
				Targets     []string          `json:"targets"`
				Identifiers map[string]string `json:"identifiers"`
			}
			if err := json.Unmarshal(attrs, &a); err != nil {
				return err
			}
			e = &entity{id: id, key: key, platform: a.Platform, targets: a.Targets,
				identifiers: a.Identifiers, answeredOn: map[string]answer{},
				ports: map[string]*iface{}, first: first, last: last}
			byID[id] = e
			p.entities = append(p.entities, e)
		}
		if !slices.Contains(e.tasks, task) {
			e.tasks = append(e.tasks, task)
		}
		if _, ok := e.answeredOn[target]; !ok {
			e.answeredOn[target] = answer{obs: obs, at: at}
		}
	}
	return rows.Err()
}

// famRow is one row of a fact family with the observation and the entity it belongs to.
type famRow struct {
	owner *entity
	obs   int64
	at    time.Time
	row   map[string]any
}

// readFamily reads one fact family of the snapshot's active parse generation (FR-019), mapped to the
// entity that collected it. An `interfaces` observation belongs to the scrape task whose parent is
// the find; a `neighbours` observation belongs to the find itself, so both are reached by resolving
// the observation's task to its find.
func (p *projection) readFamily(ctx context.Context, db store.DB, family string) ([]famRow, error) {
	byTask := map[int64]*entity{}
	for _, e := range p.entities {
		for _, t := range e.tasks {
			byTask[t] = e
		}
	}
	rows, err := db.Query(ctx, `
		SELECT coalesce(t.parent_task_id, o.task_id), o.task_id, o.id, o.collected_at, o.parsed
		FROM observation o
		JOIN parse_generation pg
		  ON pg.snapshot_id = o.snapshot_id AND pg.id = o.parse_generation_id AND pg.active
		LEFT JOIN task t ON t.job_id = $2 AND t.id = o.task_id
		WHERE o.snapshot_id = $1 AND o.fact_family = $3 AND o.status = 'collected'
		ORDER BY o.id`, p.snap.id, p.snap.jobID, family)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []famRow
	for rows.Next() {
		var parent, task, obs int64
		var at time.Time
		var parsed []map[string]any
		if err := rows.Scan(&parent, &task, &obs, &at, &parsed); err != nil {
			return nil, err
		}
		// The observation's own task first: a neighbours observation is written by the find itself.
		// Its parent second: an interfaces observation is written by the find's scrape.
		owner := byTask[task]
		if owner == nil {
			owner = byTask[parent]
		}
		if owner == nil {
			continue // collected for a device the entity set does not account for
		}
		for _, r := range parsed {
			out = append(out, famRow{owner: owner, obs: obs, at: at, row: r})
		}
	}
	return out, rows.Err()
}

// addAddressEdges records each address a device answered on as its own kind of edge, so a consumer
// can ask what answers at an address without reading identifier claims (FR-013). The addresses come
// from the entity attributes, which is what resolution decided, rather than from the observations
// again (research R11).
func (p *projection) addAddressEdges() {
	for _, e := range p.entities {
		for _, addr := range e.targets {
			a, ok := e.answeredOn[addr]
			if !ok {
				continue
			}
			// The range is that one observation's, not the entity's. A device reached on two addresses
			// answered on each at its own moment, and an edge that cites one observation while claiming
			// the range of all of them is not recording when its own evidence was collected (FR-008).
			p.edges = append(p.edges, &edge{
				typ: "has_address", fromRef: e.ref(), toRef: "addr:" + addr,
				fromEntity: e, confidence: "direct",
				attributes: map[string]any{"address": addr},
				first:      a.at, last: a.at,
				evidence: []edgeEvidence{{obs: a.obs, side: "from"}},
			})
		}
	}
}

func str(row map[string]any, key string) string {
	s, _ := row[key].(string)
	return s
}

func num(row map[string]any, key string) *int64 {
	switch v := row[key].(type) {
	case int64:
		return &v
	case float64:
		n := int64(v)
		return &n
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return &n
		}
	}
	return nil
}
