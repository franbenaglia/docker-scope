package store

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
)

// WriterOptions configures a Writer.
type WriterOptions struct {
	Retention      Retention
	QueueSize      int           // pending batches; default 256
	RollupEvery    time.Duration // default 1m
	PruneEvery     time.Duration // default 1h
	Now            func() time.Time
	ShutdownBudget time.Duration // time to flush the queue on shutdown; default 5s
}

// Writer serializes all writes on one goroutine so producers (collector,
// inventory listeners) never block on the database. When the queue is full,
// batches are dropped and counted (Dropped) rather than stalling sampling.
type Writer struct {
	s       *Store
	opts    WriterOptions
	queue   chan func(context.Context) error
	dropped atomic.Int64
	failed  atomic.Int64
}

// NewWriter creates a writer for s.
func NewWriter(s *Store, opts WriterOptions) *Writer {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 256
	}
	if opts.RollupEvery <= 0 {
		opts.RollupEvery = time.Minute
	}
	if opts.PruneEvery <= 0 {
		opts.PruneEvery = time.Hour
	}
	if opts.ShutdownBudget <= 0 {
		opts.ShutdownBudget = 5 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Writer{s: s, opts: opts, queue: make(chan func(context.Context) error, opts.QueueSize)}
}

// Dropped returns how many batches were discarded because the queue was full.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Failed returns how many batches failed to write.
func (w *Writer) Failed() int64 { return w.failed.Load() }

// EnqueueSamples queues a collector round. Never blocks.
func (w *Writer) EnqueueSamples(r collector.Round) {
	if len(r.Samples) == 0 {
		return
	}
	w.enqueue(func(ctx context.Context) error { return w.s.InsertSamples(ctx, r.Samples) })
}

// EnqueueContainers queues container metadata. Never blocks.
func (w *Writer) EnqueueContainers(m ...ContainerMeta) {
	w.enqueue(func(ctx context.Context) error { return w.s.UpsertContainers(ctx, m) })
}

func (w *Writer) enqueue(job func(context.Context) error) {
	select {
	case w.queue <- job:
	default:
		if w.dropped.Add(1) == 1 || w.dropped.Load()%100 == 0 {
			w.s.log.Warn("cola de escritura llena: se descartan datos", "descartados", w.dropped.Load())
		}
	}
}

// Run writes queued batches and runs maintenance until ctx is cancelled, then
// flushes what is queued within ShutdownBudget.
func (w *Writer) Run(ctx context.Context) error {
	w.maintain(ctx, true)
	rollup := time.NewTicker(w.opts.RollupEvery)
	defer rollup.Stop()
	prune := time.NewTicker(w.opts.PruneEvery)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			w.flush()
			return nil
		case job := <-w.queue:
			w.run(ctx, job)
		case <-rollup.C:
			w.maintain(ctx, false)
		case <-prune.C:
			w.maintain(ctx, true)
		}
	}
}

func (w *Writer) flush() {
	deadline := time.Now().Add(w.opts.ShutdownBudget)
	for time.Now().Before(deadline) {
		select {
		case job := <-w.queue:
			w.run(context.Background(), job)
		default:
			return
		}
	}
	if n := len(w.queue); n > 0 {
		w.s.log.Warn("apagado: quedaron lotes sin escribir", "lotes", n)
	}
}

// run executes one job. Writes are detached from the caller's cancellation so
// a batch dequeued while shutting down is still written; each write is
// bounded by its own timeout instead.
func (w *Writer) run(ctx context.Context, job func(context.Context) error) {
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := job(jctx); err != nil {
		w.failed.Add(1)
		w.s.log.Error("error al escribir en la base de datos", "err", err)
	}
}

// maintain rolls up complete minutes (with a 30s margin for late samples)
// and, if prune is set, deletes expired data.
func (w *Writer) maintain(ctx context.Context, prune bool) {
	now := w.opts.Now()
	if err := w.s.Rollup(ctx, now.Add(-30*time.Second)); err != nil && ctx.Err() == nil {
		w.s.log.Error("error en rollup de métricas", "err", err)
	}
	if !prune {
		return
	}
	res, err := w.s.Prune(ctx, now, w.opts.Retention)
	if err != nil {
		if ctx.Err() == nil {
			w.s.log.Error("error en limpieza por retención", "err", err)
		}
		return
	}
	if res.Samples+res.Rollups+res.Containers+res.Incidents > 0 {
		w.s.log.Info("limpieza por retención", "muestras", res.Samples, "rollups", res.Rollups,
			"contenedores", res.Containers, "incidentes", res.Incidents)
	}
}
