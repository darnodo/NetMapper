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
// The source and the observation always come from the same report. A spelling first seen in a
// neighbour's report and later written by the owner is replaced outright rather than keeping the
// earlier observation, because an alias that says `device` while citing another device's observation
// breaks the chain Principle I exists for: the row would claim the owner named its own port and point
// at evidence where it did not. Within one source the earliest observation wins, which is the
// tie-break tested with inputs that tie (research R5).
func (p *iface) see(spelling, source string, obs int64) {
	if spelling == "" {
		return
	}
	switch a, ok := p.aliases[spelling]; {
	case !ok:
	case a.source == source:
		if a.obs <= obs {
			return
		}
	case a.source == sourceDevice:
		return // the owner's own naming outranks a neighbour's, whatever the observation ids are
	}
	p.aliases[spelling] = alias{spelling: spelling, source: source, obs: obs}
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
	byTask   map[int64]*entity
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
//
// The job comes from snapshot.job_id, which is NOT NULL and points at the one job that opened the
// snapshot. job.snapshot_id is the back-pointer and carries no unique constraint, so reading the job
// through it would pick an arbitrary row if one ever existed twice, and readFamily would then match
// no task at all.
func describe(ctx context.Context, db store.DB, id int64) (snapshot, error) {
	s := snapshot{id: id}
	var at *time.Time
	err := db.QueryRow(ctx, `
		SELECT s.state, s.job_id, r.computed_at
		FROM snapshot s
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

// readEntities reads the entity set and the find tasks behind it. An entity is normally reached from
// its claims: each claim carries its identity observation, and that observation's task is the find
// that collected for the device, so the neighbours observation shares the task id and the interfaces
// observation is the child scrape's (research R2).
//
// An entity can carry no claim at all. Identity resolution mints such a device on the address it
// answered on and marks it weak, which is the case its registry handles explicitly when nothing
// strong is left to name a device with. Reaching entities through an inner join on entity_claim
// dropped those silently: no ports, no has_address edge, and a projection row reporting a count that
// excluded them. So the entities are read on their own, the identity observations are read on their
// own, and an observation with no claim is matched to the entity that recorded its address.
func (p *projection) readEntities(ctx context.Context, db store.DB) error {
	rows, err := db.Query(ctx, `
		SELECT e.id, e.device_key, e.attributes
		FROM entity e WHERE e.snapshot_id = $1 ORDER BY e.device_key`, p.snap.id)
	if err != nil {
		return err
	}
	defer rows.Close()

	byID := map[int64]*entity{}
	byTarget := map[string]*entity{}
	for rows.Next() {
		var (
			id    int64
			key   string
			attrs []byte
		)
		if err := rows.Scan(&id, &key, &attrs); err != nil {
			return err
		}
		var a struct {
			Platform    string            `json:"platform"`
			Targets     []string          `json:"targets"`
			Identifiers map[string]string `json:"identifiers"`
		}
		if err := json.Unmarshal(attrs, &a); err != nil {
			return err
		}
		e := &entity{id: id, key: key, platform: a.Platform, targets: a.Targets,
			identifiers: a.Identifiers, answeredOn: map[string]answer{},
			ports: map[string]*iface{}}
		byID[id] = e
		p.entities = append(p.entities, e)
		for _, t := range a.Targets {
			// An address belongs to one device, so a second claimant means neither can be matched on
			// it and the fallback below simply finds nothing.
			if seen, ok := byTarget[t]; ok && seen != e {
				byTarget[t] = nil
			} else if !ok {
				byTarget[t] = e
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return p.readIdentities(ctx, db, byID, byTarget)
}

// readIdentities attaches each identity observation of the snapshot to the entity it belongs to, by
// claim where there is one and by the address it answered on where there is not. Ordered by task then
// observation, so the tasks of an entity and the first observation recorded per address are the same
// on every run (FR-017).
func (p *projection) readIdentities(ctx context.Context, db store.DB, byID map[int64]*entity, byTarget map[string]*entity) error {
	rows, err := db.Query(ctx, `
		SELECT o.task_id, host(o.target), o.id, o.collected_at,
		       (SELECT min(ec.entity_id) FROM entity_claim ec
		        JOIN identifier_claim c
		          ON c.snapshot_id = ec.snapshot_id AND c.id = ec.identifier_claim_id
		        WHERE c.snapshot_id = o.snapshot_id AND c.observation_id = o.id)
		FROM observation o
		WHERE o.snapshot_id = $1 AND o.fact_family = 'identity' AND o.status = 'collected'
		ORDER BY o.task_id, o.id`, p.snap.id)
	if err != nil {
		return err
	}
	defer rows.Close()

	p.byTask = map[int64]*entity{}
	for rows.Next() {
		var (
			task, obs int64
			target    string
			at        time.Time
			entityID  *int64
		)
		if err := rows.Scan(&task, &target, &obs, &at, &entityID); err != nil {
			return err
		}
		var e *entity
		if entityID != nil {
			e = byID[*entityID]
		} else {
			e = byTarget[target]
		}
		if e == nil {
			continue // an observation resolution did not account for
		}
		if !slices.Contains(e.tasks, task) {
			e.tasks = append(e.tasks, task)
			p.byTask[task] = e
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
		owner := p.byTask[task]
		if owner == nil {
			owner = p.byTask[parent]
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
