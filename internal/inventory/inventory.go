// Package inventory keeps the in-memory state of all containers, fed by a
// full listing (reconciliation) plus the Docker event stream. It reconnects
// with backoff when Docker goes away and keeps the last known state marked as
// stale instead of reporting containers as stopped.
//
// A single goroutine (Run) mutates the state; Snapshot can be called from any
// goroutine. Browser requests read snapshots and never hit Docker.
package inventory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/events"

	"github.com/franbenaglia/docker-scope/internal/docker"
)

// Options configures an Inventory. Zero values get defaults.
type Options struct {
	// ResyncInterval is the period of the safety full reconciliation while
	// connected. Default 60s.
	ResyncInterval time.Duration
	// MinBackoff and MaxBackoff bound the reconnection delay. Defaults 500ms / 30s.
	MinBackoff, MaxBackoff time.Duration
	// OpTimeout bounds each Docker call. Default 10s.
	OpTimeout time.Duration
	// InspectConcurrency bounds parallel inspects during reconciliation. Default 8.
	InspectConcurrency int
	Now                func() time.Time
	Log                *slog.Logger
}

func (o *Options) defaults() {
	o.ResyncInterval = cmp.Or(o.ResyncInterval, 60*time.Second)
	o.MinBackoff = cmp.Or(o.MinBackoff, 500*time.Millisecond)
	o.MaxBackoff = cmp.Or(o.MaxBackoff, 30*time.Second)
	o.OpTimeout = cmp.Or(o.OpTimeout, 10*time.Second)
	o.InspectConcurrency = cmp.Or(o.InspectConcurrency, 8)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
}

// ConnStatus is the state of the connection to the Docker daemon.
type ConnStatus string

const (
	ConnConnecting   ConnStatus = "connecting"   // never connected yet
	ConnConnected    ConnStatus = "connected"    // data is live
	ConnDisconnected ConnStatus = "disconnected" // data (if any) is stale
)

// ConnState describes the connection to the Docker daemon.
type ConnState struct {
	Status     ConnStatus
	APIVersion string
	Since      time.Time // when Status began
	LastError  string    // last connection error (kept after reconnecting)
	Attempts   int       // consecutive failed connection attempts
}

// ChangeKind is the kind of a container change.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Updated ChangeKind = "updated"
	Removed ChangeKind = "removed"
)

// Change describes a container change. Listeners receive copies they may keep.
type Change struct {
	Kind ChangeKind
	ID   string
	Prev *Container // nil for Added
	Cur  *Container // nil for Removed
	// Event is the Docker event that triggered the change; nil when the change
	// was detected by reconciliation (e.g. it happened while disconnected).
	Event *events.Message
	// Initial marks changes from the first full listing after the agent
	// started: pre-existing state, not transitions (e.g. a container already
	// stopped must not raise "stopped" alerts).
	Initial bool
	At      time.Time
}

// Snapshot is a consistent copy of the inventory.
type Snapshot struct {
	Conn ConnState
	// Synced is true once a full listing has completed; before that Containers is empty
	// and means "unknown", not "none".
	Synced bool
	// Stale is true when Docker is unreachable: Containers is the last known state.
	Stale bool
	// AsOf is when Containers was last known to be current.
	AsOf       time.Time
	Containers []*Container // sorted by name, then ID
}

// Inventory tracks containers. Create with New, register listeners, then Run.
type Inventory struct {
	api  docker.API
	opts Options

	mu         sync.RWMutex
	containers map[string]*Container
	conn       ConnState
	synced     bool

	// Owned by the Run goroutine.
	onChange  []func(Change)
	onConn    []func(ConnState)
	seen      *dedup
	lastEvent time.Time
}

// New creates an Inventory over api.
func New(api docker.API, opts Options) *Inventory {
	opts.defaults()
	return &Inventory{
		api:        api,
		opts:       opts,
		containers: map[string]*Container{},
		conn:       ConnState{Status: ConnConnecting, Since: opts.Now()},
		seen:       newDedup(4096),
	}
}

// OnChange registers a listener for container changes. Must be called before
// Run. Listeners run on the Run goroutine and must not block.
func (i *Inventory) OnChange(fn func(Change)) { i.onChange = append(i.onChange, fn) }

// OnConnection registers a listener for connection state changes. Same rules as OnChange.
func (i *Inventory) OnConnection(fn func(ConnState)) { i.onConn = append(i.onConn, fn) }

// Snapshot returns a copy of the current state.
func (i *Inventory) Snapshot() Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	s := Snapshot{Conn: i.conn, Synced: i.synced, Containers: make([]*Container, 0, len(i.containers))}
	if i.conn.Status == ConnConnected {
		s.AsOf = i.opts.Now()
	} else {
		s.AsOf = i.conn.Since
		s.Stale = i.synced
	}
	for _, c := range i.containers {
		s.Containers = append(s.Containers, c.clone())
	}
	slices.SortFunc(s.Containers, func(a, b *Container) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
	})
	return s
}

// Get returns a copy of the container with the given full ID.
func (i *Inventory) Get(id string) (*Container, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	c, ok := i.containers[id]
	if !ok {
		return nil, false
	}
	return c.clone(), true
}

// Run maintains the inventory until ctx is cancelled. It always returns nil
// after cancellation; connection errors are retried with backoff.
func (i *Inventory) Run(ctx context.Context) error {
	backoff := i.opts.MinBackoff
	for {
		connected, err := i.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if connected {
			backoff = i.opts.MinBackoff
		}
		i.setDisconnected(err)
		// Full jitter in [backoff/2, backoff) avoids synchronized retries.
		wait := backoff/2 + rand.N(backoff/2+1)
		i.opts.Log.Warn("Docker no disponible; reintentando", "err", err, "espera", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		backoff = min(backoff*2, i.opts.MaxBackoff)
	}
}

// session connects, reconciles and consumes events until an error occurs.
// connected reports whether the session reached the connected state.
func (i *Inventory) session(ctx context.Context) (connected bool, err error) {
	pctx, cancel := context.WithTimeout(ctx, i.opts.OpTimeout)
	ping, err := i.api.Ping(pctx)
	cancel()
	if err != nil {
		return false, fmt.Errorf("ping: %w", err)
	}

	sctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	// Subscribe before listing so nothing between the listing and the
	// subscription is lost. Replays since the last processed event; replayed
	// events are deduplicated and re-inspected, so processing is idempotent.
	msgs, errs := i.api.Events(sctx, i.lastEvent)

	if err := i.reconcile(ctx); err != nil {
		return false, fmt.Errorf("reconciliar: %w", err)
	}
	i.setConnected(ping.APIVersion)

	resync := time.NewTicker(i.opts.ResyncInterval)
	defer resync.Stop()
	for {
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case err := <-errs:
			if err == nil {
				err = errors.New("stream de eventos cerrado")
			}
			return true, fmt.Errorf("eventos: %w", err)
		case m, ok := <-msgs:
			if !ok {
				// The SDK reports the reason on errs; wait for it.
				msgs = nil
				continue
			}
			if err := i.handleEvent(ctx, m); err != nil {
				return true, err
			}
		case <-resync.C:
			if err := i.reconcile(ctx); err != nil {
				return true, fmt.Errorf("reconciliar: %w", err)
			}
		}
	}
}

// ignoredActions are container events that never change tracked state.
var ignoredActions = map[events.Action]bool{
	events.ActionAttach: true, events.ActionDetach: true, events.ActionResize: true,
	events.ActionTop: true, events.ActionExport: true, events.ActionCopy: true,
	events.ActionArchivePath: true, events.ActionExtractToDir: true, events.ActionCommit: true,
}

func (i *Inventory) handleEvent(ctx context.Context, m events.Message) error {
	if m.Type != events.ContainerEventType || m.Actor.ID == "" {
		return nil
	}
	key := fmt.Sprintf("%s|%s|%d", m.Actor.ID, m.Action, m.TimeNano)
	if !i.seen.add(key) {
		return nil
	}
	if t := time.Unix(0, m.TimeNano); t.After(i.lastEvent) {
		i.lastEvent = t
	}
	if ignoredActions[m.Action] || strings.HasPrefix(string(m.Action), "exec_") {
		return nil
	}
	id := m.Actor.ID
	if m.Action == events.ActionDestroy {
		i.remove(id, &m)
		return nil
	}
	ictx, cancel := context.WithTimeout(ctx, i.opts.OpTimeout)
	r, err := i.api.InspectContainer(ictx, id)
	cancel()
	if errors.Is(err, docker.ErrNotFound) {
		i.remove(id, &m)
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspeccionar %.12s tras evento %s: %w", id, m.Action, err)
	}
	i.upsert(fromInspect(r), &m, false)
	return nil
}

// reconcile replaces the inventory with a fresh listing, emitting changes for
// what differs (including what happened while disconnected).
func (i *Inventory) reconcile(ctx context.Context) error {
	lctx, cancel := context.WithTimeout(ctx, i.opts.OpTimeout)
	list, err := i.api.ListContainers(lctx)
	cancel()
	if err != nil {
		return err
	}

	found := make([]*Container, len(list))
	errs := make([]error, len(list))
	sem := make(chan struct{}, i.opts.InspectConcurrency)
	var wg sync.WaitGroup
	for n, s := range list {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			ictx, cancel := context.WithTimeout(ctx, i.opts.OpTimeout)
			defer cancel()
			r, err := i.api.InspectContainer(ictx, s.ID)
			switch {
			case errors.Is(err, docker.ErrNotFound): // removed after listing
			case err != nil:
				errs[n] = err
			default:
				found[n] = fromInspect(r)
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	i.mu.RLock()
	initial := !i.synced
	var gone []string
	for id := range i.containers {
		gone = append(gone, id)
	}
	i.mu.RUnlock()
	present := make(map[string]bool, len(found))
	for _, c := range found {
		if c != nil {
			present[c.ID] = true
		}
	}
	slices.Sort(gone)
	for _, id := range gone {
		if !present[id] {
			i.remove(id, nil)
		}
	}
	for _, c := range found {
		if c != nil {
			i.upsert(c, nil, initial)
		}
	}
	i.mu.Lock()
	i.synced = true
	i.mu.Unlock()
	return nil
}

func (i *Inventory) upsert(c *Container, ev *events.Message, initial bool) {
	i.mu.Lock()
	prev := i.containers[c.ID]
	if prev != nil {
		c.ObservedRestarts = prev.ObservedRestarts
		c.LastObservedRestart = prev.LastObservedRestart
		if !prev.StartedAt.IsZero() && c.StartedAt.After(prev.StartedAt) {
			c.ObservedRestarts++
			c.LastObservedRestart = c.StartedAt
		}
		if reflect.DeepEqual(prev, c) {
			i.mu.Unlock()
			return
		}
	}
	i.containers[c.ID] = c
	i.mu.Unlock()

	ch := Change{Kind: Added, ID: c.ID, Cur: c.clone(), Event: ev, Initial: initial, At: i.opts.Now()}
	if prev != nil {
		ch.Kind = Updated
		ch.Prev = prev // replaced, never mutated again
	}
	i.emit(ch)
}

func (i *Inventory) remove(id string, ev *events.Message) {
	i.mu.Lock()
	prev, ok := i.containers[id]
	delete(i.containers, id)
	i.mu.Unlock()
	if ok {
		i.emit(Change{Kind: Removed, ID: id, Prev: prev, Event: ev, At: i.opts.Now()})
	}
}

func (i *Inventory) emit(ch Change) {
	for _, fn := range i.onChange {
		fn(ch)
	}
}

func (i *Inventory) setConnected(apiVersion string) {
	i.mu.Lock()
	i.conn = ConnState{Status: ConnConnected, APIVersion: apiVersion, Since: i.opts.Now(), LastError: i.conn.LastError}
	st := i.conn
	i.mu.Unlock()
	i.opts.Log.Info("conectado a Docker", "api", apiVersion)
	i.notifyConn(st)
}

func (i *Inventory) setDisconnected(err error) {
	i.mu.Lock()
	prev := i.conn
	st := ConnState{Status: ConnDisconnected, APIVersion: prev.APIVersion, Since: i.opts.Now(), Attempts: 1}
	if prev.Status == ConnDisconnected {
		st.Since = prev.Since
		st.Attempts = prev.Attempts + 1
	}
	if err != nil {
		st.LastError = err.Error()
	}
	i.conn = st
	i.mu.Unlock()
	if prev.Status != ConnDisconnected {
		i.notifyConn(st)
	}
}

func (i *Inventory) notifyConn(st ConnState) {
	for _, fn := range i.onConn {
		fn(st)
	}
}

// dedup remembers the last n keys.
type dedup struct {
	set  map[string]struct{}
	ring []string
	next int
}

func newDedup(n int) *dedup {
	return &dedup{set: make(map[string]struct{}, n), ring: make([]string, n)}
}

// add returns false if key was already seen.
func (d *dedup) add(key string) bool {
	if _, ok := d.set[key]; ok {
		return false
	}
	if old := d.ring[d.next]; old != "" {
		delete(d.set, old)
	}
	d.ring[d.next] = key
	d.next = (d.next + 1) % len(d.ring)
	d.set[key] = struct{}{}
	return true
}
