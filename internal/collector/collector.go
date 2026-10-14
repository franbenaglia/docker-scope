// Package collector samples container metrics on a single ticker shared by
// every consumer: browser requests read the latest samples and never trigger
// Docker calls (SPEC "Funcionamiento del agente" 7–8).
package collector

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/franbenaglia/docker-scope/internal/docker"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// Inventory is the read side of inventory.Inventory.
type Inventory interface {
	Snapshot() inventory.Snapshot
}

// Options configures a Collector. Zero values get defaults.
type Options struct {
	Interval    time.Duration // default 5s
	Concurrency int           // parallel stats requests, default 4
	// Timeout bounds each stats request. Default min(Interval, 10s).
	Timeout time.Duration
	// MaxGap is the longest interval over which rates are computed; longer
	// gaps (e.g. after a disconnection) yield "not available". Default 10×Interval.
	MaxGap time.Duration
	Log    *slog.Logger
}

func (o *Options) defaults() {
	o.Interval = cmp.Or(o.Interval, 5*time.Second)
	o.Concurrency = cmp.Or(o.Concurrency, 4)
	o.Timeout = cmp.Or(o.Timeout, min(o.Interval, 10*time.Second))
	o.MaxGap = cmp.Or(o.MaxGap, 10*o.Interval)
	if o.Log == nil {
		o.Log = slog.Default()
	}
}

// Round is the result of one sampling round.
type Round struct {
	At      time.Time
	Samples []Sample // running containers that answered in this round
}

// Collector samples metrics. Create with New, register listeners, then Run.
type Collector struct {
	api  docker.API
	inv  Inventory
	opts Options

	mu     sync.RWMutex
	latest map[string]Sample

	// Owned by the Run goroutine.
	prev    map[string]raw
	onRound []func(Round)
}

// New creates a Collector.
func New(api docker.API, inv Inventory, opts Options) *Collector {
	opts.defaults()
	return &Collector{api: api, inv: inv, opts: opts, latest: map[string]Sample{}, prev: map[string]raw{}}
}

// OnRound registers a listener called after each round, on the Run goroutine.
// Must be called before Run; listeners must not block.
func (c *Collector) OnRound(fn func(Round)) { c.onRound = append(c.onRound, fn) }

// Interval returns the sampling interval.
func (c *Collector) Interval() time.Duration { return c.opts.Interval }

// Latest returns the latest sample of a container.
func (c *Collector) Latest(id string) (Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.latest[id]
	return s, ok
}

// LatestAll returns a copy of the latest samples by container ID.
func (c *Collector) LatestAll() map[string]Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.latest)
}

// Run samples until ctx is cancelled. Rounds never overlap: if one takes
// longer than the interval, the missed ticks are dropped.
func (c *Collector) Run(ctx context.Context) error {
	t := time.NewTicker(c.opts.Interval)
	defer t.Stop()
	for {
		c.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

type result struct {
	id     string
	sample Sample
	raw    raw
	ok     bool
}

func (c *Collector) round(ctx context.Context) {
	snap := c.inv.Snapshot()
	if snap.Conn.Status != inventory.ConnConnected {
		// Keep the last samples; the API marks them stale with the inventory.
		return
	}
	var running []*inventory.Container
	for _, ct := range snap.Containers {
		if ct.Running() {
			running = append(running, ct)
		}
	}

	results := make([]result, len(running))
	sem := make(chan struct{}, c.opts.Concurrency)
	var wg sync.WaitGroup
	for i, ct := range running {
		prev, hasPrev := c.prev[ct.ID] // read-only while workers run
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
			st, err := c.api.ContainerStats(sctx, ct.ID)
			cancel()
			if err != nil {
				if !errors.Is(err, docker.ErrNotFound) && ctx.Err() == nil {
					c.opts.Log.Debug("stats no disponibles", "id", ct.ShortID(), "err", err)
				}
				return
			}
			r, ok := extract(st, ct.StartedAt)
			if !ok {
				return // stopped between the snapshot and the request
			}
			var p *raw
			if hasPrev {
				p = &prev
			}
			results[i] = result{id: ct.ID, raw: r, ok: true,
				sample: compute(ct.ID, p, r, ct.MemoryLimit, c.opts.MaxGap)}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	round := Round{At: time.Now().UTC()}
	latest := make(map[string]Sample, len(results))
	prev := make(map[string]raw, len(results))
	for _, r := range results {
		if r.ok {
			latest[r.id] = r.sample
			prev[r.id] = r.raw
			round.Samples = append(round.Samples, r.sample)
		}
	}
	// Containers that stopped or failed this round drop out: their metrics
	// are "not available", not the last values.
	c.prev = prev
	c.mu.Lock()
	c.latest = latest
	c.mu.Unlock()
	for _, fn := range c.onRound {
		fn(round)
	}
}
