// Package collector runs find and scrape tasks. It is the only code that resolves secrets and
// talks to devices (principle III).
package collector

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/perimeter"
	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/transport"
)

type Collector struct {
	DB         *pgxpool.Pool
	ID         string
	Workers    int
	Kinds      []string
	Registry   *pack.Registry
	Transports map[string]transport.Transport // "ssh", "snmp"; audited
	Secrets    secret.SecretBackend
	Raw        *store.RawStore
	Log        *slog.Logger

	mu   sync.Mutex
	jobs map[int64]*job
}

// New wraps every transport with the audit log.
func New(db *pgxpool.Pool, id string, reg *pack.Registry, raw *store.RawStore, secrets secret.SecretBackend, transports map[string]transport.Transport, log *slog.Logger) *Collector {
	audit := &store.Audit{DB: db, Actor: "collector:" + id}
	audited := map[string]transport.Transport{}
	for name, t := range transports {
		audited[name] = transport.WithAudit(t, audit)
	}
	return &Collector{DB: db, ID: id, Workers: 64, Kinds: []string{"find", "scrape"}, Registry: reg,
		Transports: audited, Secrets: secrets, Raw: raw, Log: log.With("collector", id)}
}

// job is what a running job's tasks need; it does not change while the job runs.
type job struct {
	jobrunner.Params
	ID           int64
	SnapshotID   int64
	GenerationID int64
	Perimeter    perimeter.Perimeter
	Creds        []credSet
}

// Run claims and runs tasks until ctx ends, then stops claiming and lets in-flight tasks finish
// for up to the longest lease before cancelling them. An unfinished task returns to the frontier
// when its lease expires.
func (c *Collector) Run(ctx context.Context) error {
	slots := make(chan struct{}, c.Workers)
	taskCtx, cancelTasks := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTasks()
	var wg sync.WaitGroup
	var drain time.Duration
	for ctx.Err() == nil {
		poll := time.Second
		jobs, err := c.running(ctx)
		if err != nil && ctx.Err() == nil {
			c.Log.Error("list jobs", "err", err)
		}
		for i, j := range jobs {
			drain = max(drain, j.Discovery.Lease)
			if i == 0 || j.Discovery.PollInterval < poll {
				poll = j.Discovery.PollInterval
			}
			free := c.Workers - len(slots)
			if free <= 0 {
				continue
			}
			tasks, err := frontier.Claim(ctx, c.DB, c.ID, j.ID, c.Kinds, min(j.Discovery.ClaimBatch, free), j.Discovery.Lease, j.Discovery.MaxTaskAttempts)
			if err != nil {
				if ctx.Err() == nil {
					c.Log.Error("claim", "job", j.ID, "err", err)
				}
				continue
			}
			for _, t := range tasks {
				slots <- struct{}{}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-slots }()
					c.handle(taskCtx, j, t)
				}()
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drain):
		cancelTasks()
		<-done
	}
	return nil
}

// handle runs one task under a lease renewed every lease/3. If the lease is lost, the task
// context is cancelled and nothing more is written for it.
func (c *Collector) handle(ctx context.Context, j *job, t frontier.Task) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	log := c.Log.With("job", j.ID, "task", t.ID, "kind", t.Kind, "target", t.Target)
	held := t // the renewal's own copy: the task below updates t
	go func() {
		tick := time.NewTicker(j.Discovery.Lease / 3)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := frontier.Renew(ctx, c.DB, held, j.Discovery.Lease); err != nil {
					if ctx.Err() == nil {
						log.Warn("lease lost", "err", err)
						cancel()
					}
					return
				}
			}
		}
	}()
	if t.CredAttempts == nil {
		t.CredAttempts = map[string]frontier.CredCount{}
	}
	var err error
	if t.Kind == "find" {
		err = c.find(ctx, j, &t)
	} else {
		err = c.scrape(ctx, j, &t)
	}
	if err == nil {
		return
	}
	if ctx.Err() != nil || errors.Is(err, frontier.ErrLost) {
		log.Warn("task abandoned", "err", err)
		return
	}
	kind, msg := frontier.KindError, err.Error()
	var f *failure
	if errors.As(err, &f) {
		kind, msg = f.kind, f.msg
	}
	if kind == frontier.KindCredentialPartial {
		// The device refused what it was shown while another set never resolved: it may sit
		// outside the declared authentication regime (research R6).
		log.Warn("device refused every credential presented, others unresolved", "detail", msg)
	} else {
		log.Error("task failed", "failure", kind, "err", msg)
	}
	if err := frontier.Fail(context.WithoutCancel(ctx), c.DB, t, kind, msg, j.Discovery.MaxTaskAttempts); err != nil {
		log.Error("record failure", "err", err)
	}
}

// running lists the running jobs, loading each one's perimeter and credential sets once.
func (c *Collector) running(ctx context.Context) ([]*job, error) {
	rows, err := c.DB.Query(ctx, `
		SELECT j.id, j.snapshot_id, pg.id, j.parameters FROM job j
		JOIN parse_generation pg ON pg.snapshot_id = j.snapshot_id AND pg.active
		WHERE j.state = 'running' ORDER BY j.id`)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*job, error) {
		j := &job{}
		return j, r.Scan(&j.ID, &j.SnapshotID, &j.GenerationID, &j.Params)
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jobs == nil {
		c.jobs = map[int64]*job{}
	}
	var out []*job
	for _, j := range all {
		if cached := c.jobs[j.ID]; cached != nil {
			out = append(out, cached)
			continue
		}
		if err := c.DB.QueryRow(ctx, `SELECT include, exclude FROM perimeter WHERE id = $1`, j.PerimeterID).
			Scan(&j.Perimeter.Include, &j.Perimeter.Exclude); err != nil {
			return nil, err
		}
		rows, err := c.DB.Query(ctx, `
			SELECT id, name, kind, coalesce(username, ''), secret_ref, max_attempts_per_device,
			       coalesce(auth_protocol, ''), coalesce(priv_protocol, '')
			FROM credential_set WHERE $1 = ANY(perimeter_ids) ORDER BY position`, j.PerimeterID)
		if err != nil {
			return nil, err
		}
		j.Creds, err = pgx.CollectRows(rows, pgx.RowToStructByPos[credSet])
		if err != nil {
			return nil, err
		}
		c.jobs[j.ID] = j
		out = append(out, j)
	}
	return out, nil
}

// resolve is the dial guard: every address a task will reach goes through the perimeter here.
func (j *job) resolve(ctx context.Context, a netip.Addr) (netip.Addr, error) {
	return j.Perimeter.Resolve(ctx, a.String())
}
