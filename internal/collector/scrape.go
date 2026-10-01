package collector

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/darnodo/NetMapper/internal/fact"
	"github.com/darnodo/NetMapper/internal/frontier"
	"github.com/darnodo/NetMapper/internal/parse"
	"github.com/darnodo/NetMapper/internal/perimeter"
	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/transport"
)

// scrape collects every fact family, except identity and neighbours which the find wrote, in one
// session per transport. A family the device's pack has no recipe for is still written, as
// unsupported, so the gap shows in the snapshot instead of passing silently. Each family is written as soon as it is known;
// a retried scrape skips the families it already wrote.
func (c *Collector) scrape(ctx context.Context, j *job, t *frontier.Task) error {
	addr, err := j.resolve(ctx, t.Target)
	if errors.Is(err, perimeter.ErrOutOfPerimeter) {
		return frontier.Skip(ctx, c.DB, *t, "out_of_perimeter")
	} else if err != nil {
		return err
	}
	t.Target = addr
	var version string
	if err := c.DB.QueryRow(ctx, `
		SELECT coalesce(parsed -> 0 ->> 'os_version', '') FROM observation
		WHERE snapshot_id = $1 AND task_id = $2 AND fact_family = 'identity'`,
		j.SnapshotID, t.ParentTaskID).Scan(&version); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := c.DB.Query(ctx, `SELECT fact_family FROM observation WHERE snapshot_id = $1 AND task_id = $2`, j.SnapshotID, t.ID)
	if err != nil {
		return err
	}
	written, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}

	d := c.device(j, t, t.Platform)
	defer d.close()
	for _, family := range slices.Sorted(maps.Keys(fact.Families)) {
		if family == "identity" || family == "neighbours" || slices.Contains(written, family) {
			continue
		}
		obs, parseErr, err := c.family(ctx, d, j, t, t.Platform, version, family)
		if err != nil {
			return err
		}
		if _, err := c.write(ctx, t, obs, parseErr, nil); err != nil {
			return err
		}
	}
	return frontier.Complete(ctx, c.DB, *t, "done")
}

// family runs the first implementation of family whose version fits and whose transport opens,
// uploads each step's output, and parses it. With none to run, it records the family unsupported
// with detail no_recipe (the pack has no recipe for it) or no_matching_version (no implementation
// fits the device's version). The step's output is held in memory once
// (research R12). An idle timeout records the family unreachable with detail timeout; a deadline
// fails the task with no observation for the family.
func (c *Collector) family(ctx context.Context, d *device, j *job, t *frontier.Task, platform, version, family string) (obs store.Observation, parseErr, err error) {
	obs = c.observation(j, t, family, platform)
	impls := c.Registry.Implementations(platform, family, version)
	if len(impls) == 0 {
		obs.Status, obs.Detail = "unsupported", "no_matching_version"
		if !slices.Contains(c.Registry.Families(platform), family) {
			obs.Detail = "no_recipe"
		}
		return obs, nil, nil
	}
	var tried []*opened
	for _, im := range impls {
		o, err := d.session(ctx, im.Transport, nil)
		if err != nil {
			return obs, nil, err
		}
		if o.s == nil {
			tried = append(tried, o)
			continue
		}
		obs.Transport, obs.RecipeID = im.Transport, c.Registry.RecipeID(platform, im)
		var outputs [][]byte
		for _, st := range im.Steps {
			step := d.step()
			step.Command, step.Walk = st.Command, st.Walk
			out, err := o.s.Run(ctx, step)
			var ae *transport.AuthError
			switch {
			case errors.Is(err, transport.ErrIdleTimeout), errors.Is(err, transport.ErrSilent):
				d.drop(im.Transport)
				obs.Status, obs.Detail = "unreachable", "timeout"
				return obs, nil, nil
			case errors.As(err, &ae):
				d.drop(im.Transport)
				obs.Status = "denied"
				return obs, nil, c.keep(ctx, &obs.Raw, step.String(), ae.Evidence)
			case err != nil:
				d.drop(im.Transport)
				return obs, nil, stepErr(family, j, err)
			}
			if err := c.keep(ctx, &obs.Raw, step.String(), out.Bytes); err != nil {
				return obs, nil, err
			}
			outputs = append(outputs, out.Bytes)
		}
		obs.Status, obs.Parsed, parseErr = parse.Parse(c.Registry, platform, family, im, outputs)
		return obs, parseErr, nil
	}
	status, ev, err := verdict(tried...)
	if err != nil {
		return obs, nil, err
	}
	obs.Status = status
	if ev != nil {
		obs.Transport = ev.transport
		err = c.keep(ctx, &obs.Raw, ev.evidenceBy, ev.evidence)
	}
	return obs, nil, err
}
