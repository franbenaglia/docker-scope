package alerts

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// Inventory and Metrics are the read sides the engine evaluates.
type Inventory interface {
	Snapshot() inventory.Snapshot
}

type Metrics interface {
	LatestAll() map[string]collector.Sample
}

// Options configures an Engine.
type Options struct {
	Tick time.Duration // evaluation period, default 1s
	Now  func() time.Time
	Log  *slog.Logger
}

// EventType tells listeners what happened to an incident.
type EventType string

const (
	EvOpened   EventType = "opened"
	EvResolved EventType = "resolved"
	EvAcked    EventType = "acked"
)

// Event is emitted on incident changes.
type Event struct {
	Type     EventType
	Incident Incident
}

// Pending is a condition that holds but has not opened an incident yet.
type Pending struct {
	RuleID        int64
	RuleName      string
	Kind          Kind
	ContainerID   string
	ContainerName string
	Since         time.Time
	OpensAt       time.Time // earliest opening: Since+For, or the end of the cooldown
	InCooldown    bool
	Value         *float64
}

// Status summarizes the engine for the API.
type Status struct {
	Open, Unacked, Pending int
	Maintenance            []Maintenance // active windows
}

// cond is the result of evaluating a rule on a container.
type cond int

const (
	unknown cond = iota // no data: no transition
	high                // condition holds
	between             // hysteresis band: cancels pending, does not resolve
	clear               // condition does not hold
)

type key struct {
	rule int64
	ctr  string
}

type track struct {
	name         string // container name
	since        time.Time
	clearSince   time.Time
	lastResolved time.Time
	peak         *float64
	incident     *Incident // open incident or nil
}

// recentEvents remembers the last few Docker events of a container and the
// exit code of the last "die", to describe probable causes.
type recentEvents struct {
	actions  []string // oldest first, at most maxRecent
	at       time.Time
	exitCode *int
}

const maxRecent = 4

// Engine evaluates rules. Load, register listeners, then Run.
type Engine struct {
	store Store
	inv   Inventory
	met   Metrics
	opts  Options

	mu          sync.Mutex
	rules       map[int64]Rule
	maint       []Maintenance
	tracks      map[key]*track
	seenRunning map[string]bool          // container ID observed running by the agent
	restartLog  map[string][]time.Time   // observed restart times per container ID
	restartSeen map[string]int           // last ObservedRestarts per container ID
	events      map[string]*recentEvents // recent Docker events per container ID
	listeners   []func(Event)
}

// New creates an engine.
func New(store Store, inv Inventory, met Metrics, opts Options) *Engine {
	if opts.Tick <= 0 {
		opts.Tick = time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Engine{
		store: store, inv: inv, met: met, opts: opts,
		rules: map[int64]Rule{}, tracks: map[key]*track{},
		seenRunning: map[string]bool{}, restartLog: map[string][]time.Time{},
		restartSeen: map[string]int{}, events: map[string]*recentEvents{},
	}
}

// OnEvent registers a listener (before Run). Listeners must not block.
func (e *Engine) OnEvent(fn func(Event)) { e.listeners = append(e.listeners, fn) }

// Load reads rules, maintenance windows and open incidents from the store.
func (e *Engine) Load(ctx context.Context) error {
	rules, err := e.store.ListRules(ctx)
	if err != nil {
		return fmt.Errorf("cargar reglas: %w", err)
	}
	maint, err := e.store.ListMaintenance(ctx, e.opts.Now())
	if err != nil {
		return fmt.Errorf("cargar mantenimiento: %w", err)
	}
	open, err := e.store.OpenIncidents(ctx)
	if err != nil {
		return fmt.Errorf("cargar incidentes abiertos: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range rules {
		e.rules[r.ID] = r
	}
	e.maint = maint
	for i := range open {
		inc := open[i]
		e.tracks[key{inc.RuleID, inc.ContainerID}] = &track{name: inc.ContainerName, since: inc.StartedAt, peak: inc.Peak, incident: &inc}
	}
	return nil
}

// Observe records recent Docker events of a container (and the exit code of
// "die" events), used to describe probable causes. Register with inventory.OnChange.
func (e *Engine) Observe(ch inventory.Change) {
	if ch.Event == nil {
		return
	}
	at := time.Unix(0, ch.Event.TimeNano).UTC()
	action := string(ch.Event.Action)
	e.mu.Lock()
	defer e.mu.Unlock()
	re := e.events[ch.ID]
	if re == nil || at.Sub(re.at) > time.Minute {
		re = &recentEvents{} // a new sequence: older events are not related
		e.events[ch.ID] = re
	}
	re.at = at
	if n := len(re.actions); n == 0 || re.actions[n-1] != action {
		re.actions = append(re.actions, action)
		if len(re.actions) > maxRecent {
			re.actions = re.actions[1:]
		}
	}
	if ch.Event.Action == events.ActionDie {
		if code, err := strconv.Atoi(ch.Event.Actor.Attributes["exitCode"]); err == nil {
			re.exitCode = &code
		}
	}
}

// Run evaluates every Tick until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	t := time.NewTicker(e.opts.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			e.Tick(ctx, e.opts.Now())
		}
	}
}

// Tick runs one evaluation at now. Exported for tests and deterministic use.
func (e *Engine) Tick(ctx context.Context, now time.Time) {
	snap := e.inv.Snapshot()
	if !snap.Synced || snap.Conn.Status != inventory.ConnConnected {
		// Stale data: never confuse a lost connection with containers going down.
		return
	}
	latest := e.met.LatestAll()

	e.mu.Lock()
	var out []Event
	defer func() {
		e.mu.Unlock()
		for _, ev := range out {
			e.emit(ev)
		}
	}()
	emit := func(ev Event) { out = append(out, ev) }

	e.maint = slices.DeleteFunc(e.maint, func(m Maintenance) bool { return !m.Until.After(now) })

	present := make(map[string]*inventory.Container, len(snap.Containers))
	runningByName := map[string]*inventory.Container{}
	for _, c := range snap.Containers {
		present[c.ID] = c
		if c.Running() {
			e.seenRunning[c.ID] = true
			runningByName[c.Name] = c
		}
		e.countRestarts(c, now)
	}

	rules := slices.SortedFunc(func(yield func(Rule) bool) {
		for _, r := range e.rules {
			if !yield(r) {
				return
			}
		}
	}, func(a, b Rule) int { return cmp.Compare(a.ID, b.ID) })

	evaluated := map[key]bool{}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		for _, c := range snap.Containers {
			if !r.Scope.Matches(c) {
				continue
			}
			k := key{r.ID, c.ID}
			evaluated[k] = true
			cnd, val := e.eval(r, c, latest, now)
			keepIdle := r.Kind == NotRunning && e.seenRunning[c.ID]
			e.step(ctx, k, r, c.Name, cnd, val, now, func() (string, string) { return e.describe(r, c, val, latest) },
				"recuperado", keepIdle, emit)
		}
	}

	// Tracks not evaluated above: container removed, rule disabled/deleted or
	// container out of scope.
	for k, t := range e.tracks {
		if evaluated[k] {
			continue
		}
		r, ok := e.rules[k.rule]
		_, exists := present[k.ctr]
		switch {
		case !ok || !r.Enabled:
			e.resolveTrack(ctx, k, t, now, "regla deshabilitada o eliminada", emit)
		case exists:
			e.resolveTrack(ctx, k, t, now, "fuera del alcance de la regla", emit)
		case r.Kind == NotRunning:
			// Removed: still "not running" unless recreated (new ID, same name).
			cnd := high
			resolution := "recuperado"
			if _, again := runningByName[t.name]; again {
				cnd, resolution = clear, "recreado con un ID nuevo y en ejecución"
			} else if !e.seenRunning[k.ctr] && t.incident == nil {
				cnd = clear
			}
			e.step(ctx, k, r, t.name, cnd, nil, now, func() (string, string) {
				return fmt.Sprintf("%s fue eliminado y no hay otro contenedor con ese nombre en ejecución", t.name),
					e.removedCause(k.ctr)
			}, resolution, false, emit)
		default:
			e.resolveTrack(ctx, k, t, now, "contenedor eliminado", emit)
		}
	}

	// Forget bookkeeping of containers that are gone and have no track.
	for id := range e.seenRunning {
		if _, ok := present[id]; !ok && !e.hasTrack(id) {
			delete(e.seenRunning, id)
			delete(e.restartLog, id)
			delete(e.restartSeen, id)
			delete(e.events, id)
		}
	}
}

func (e *Engine) hasTrack(id string) bool {
	for k := range e.tracks {
		if k.ctr == id {
			return true
		}
	}
	return false
}

// countRestarts turns increases of the inventory's observed-restart counter
// into timestamps for the restarts window.
func (e *Engine) countRestarts(c *inventory.Container, now time.Time) {
	prev, seen := e.restartSeen[c.ID]
	e.restartSeen[c.ID] = c.ObservedRestarts
	if seen && c.ObservedRestarts > prev {
		at := c.LastObservedRestart
		if at.IsZero() {
			at = now
		}
		for range c.ObservedRestarts - prev {
			e.restartLog[c.ID] = append(e.restartLog[c.ID], at)
		}
	}
	log := e.restartLog[c.ID]
	cut := now.Add(-maxDuration)
	for len(log) > 0 && log[0].Before(cut) {
		log = log[1:]
	}
	e.restartLog[c.ID] = log
}

func (e *Engine) eval(r Rule, c *inventory.Container, latest map[string]collector.Sample, now time.Time) (cond, *float64) {
	switch r.Kind {
	case NotRunning:
		switch c.State {
		case container.StateRunning:
			return clear, nil
		case container.StatePaused:
			return between, nil // deliberate pause: neither opens nor resolves
		}
		if !e.seenRunning[c.ID] {
			// Never seen running (e.g. already stopped when the agent started):
			// can keep an open incident open, but never opens one.
			return between, nil
		}
		return high, nil
	case Unhealthy:
		// Health is only current while running; a stopped container clears the
		// incident (not_running rules cover stops).
		if c.HealthCurrent() == inventory.HealthUnhealthy {
			return high, nil
		}
		return clear, nil
	case Restarts:
		n := 0
		for _, t := range e.restartLog[c.ID] {
			if !t.Before(now.Add(-r.Window)) {
				n++
			}
		}
		v := float64(n)
		if v >= r.Threshold {
			return high, &v
		}
		return clear, &v
	case CPU, Memory:
		if !c.Running() {
			return clear, nil
		}
		s, ok := latest[c.ID]
		if !ok {
			return unknown, nil
		}
		v := s.CPUPercent
		if r.Kind == Memory {
			v = s.MemPercent
		}
		if v == nil {
			return unknown, nil
		}
		switch {
		case *v > r.Threshold:
			return high, v
		case *v < r.Threshold-r.Hysteresis:
			return clear, v
		default:
			return between, v
		}
	}
	return unknown, nil
}

// step advances the state machine of one (rule, container) pair.
//
// keepIdle keeps an idle track (no pending, no incident) so a watched
// container that disappears between ticks (docker rm -f) is still noticed.
func (e *Engine) step(ctx context.Context, k key, r Rule, name string, cnd cond, val *float64, now time.Time,
	describe func() (summary, cause string), resolution string, keepIdle bool, emit func(Event)) {
	if cnd == unknown {
		return
	}
	t := e.tracks[k]
	if t == nil {
		if cnd != high && !keepIdle {
			return
		}
		t = &track{name: name}
		e.tracks[k] = t
	}
	t.name = name
	if val != nil && (t.peak == nil || *val > *t.peak) {
		v := *val
		t.peak = &v
	}

	if t.incident != nil { // open
		if cnd != clear {
			t.clearSince = time.Time{}
			return
		}
		if t.clearSince.IsZero() {
			t.clearSince = now
		}
		if now.Sub(t.clearSince) >= r.RecoverFor {
			e.resolveTrack(ctx, k, t, now, resolution, emit)
		}
		return
	}

	// Not open: pending or idle.
	if cnd != high {
		inCooldown := !t.lastResolved.IsZero() && now.Sub(t.lastResolved) < r.Cooldown
		if keepIdle || inCooldown {
			e.tracks[k] = &track{name: name, lastResolved: t.lastResolved} // cancels any pending
		} else {
			delete(e.tracks, k)
		}
		return
	}
	if t.since.IsZero() {
		t.since = now
		t.peak = nil
		if val != nil {
			v := *val
			t.peak = &v
		}
	}
	if now.Sub(t.since) < r.For || now.Sub(t.lastResolved) < r.Cooldown && !t.lastResolved.IsZero() {
		return
	}
	summary, cause := describe()
	inc := &Incident{
		RuleID: r.ID, RuleName: r.Name, Kind: r.Kind, ContainerID: k.ctr, ContainerName: name,
		State: Open, StartedAt: t.since, OpenedAt: now, Suppressed: e.inMaintenance(name),
		Summary: summary, Cause: cause, Peak: t.peak,
	}
	if err := e.store.InsertIncident(ctx, inc); err != nil {
		e.opts.Log.Error("no se pudo registrar el incidente; se reintentará", "regla", r.Name, "contenedor", name, "err", err)
		return
	}
	t.incident = inc
	t.clearSince = time.Time{}
	emit(Event{Type: EvOpened, Incident: *inc})
}

func (e *Engine) resolveTrack(ctx context.Context, k key, t *track, now time.Time, resolution string, emit func(Event)) {
	if t.incident == nil {
		delete(e.tracks, k)
		return
	}
	inc := *t.incident
	inc.State = Resolved
	inc.ResolvedAt = &now
	inc.Resolution = resolution
	inc.Peak = t.peak
	if err := e.store.UpdateIncident(ctx, &inc); err != nil {
		e.opts.Log.Error("no se pudo resolver el incidente; se reintentará", "id", inc.ID, "err", err)
		return
	}
	e.tracks[k] = &track{name: t.name, lastResolved: now}
	emit(Event{Type: EvResolved, Incident: inc})
}

func (e *Engine) inMaintenance(name string) bool {
	for _, m := range e.maint {
		if m.Container == "" || m.Container == name {
			return true
		}
	}
	return false
}

func (e *Engine) emit(ev Event) {
	for _, fn := range e.listeners {
		fn(ev)
	}
}

// --- read side ---

// Rules returns all rules ordered by ID.
func (e *Engine) Rules() []Rule {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Rule, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Rule) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Pending returns conditions that hold but have not opened an incident.
func (e *Engine) Pending() []Pending {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Pending
	for k, t := range e.tracks {
		if t.incident != nil || t.since.IsZero() {
			continue
		}
		r := e.rules[k.rule]
		opens := t.since.Add(r.For)
		cool := !t.lastResolved.IsZero() && t.lastResolved.Add(r.Cooldown).After(opens)
		if cool {
			opens = t.lastResolved.Add(r.Cooldown)
		}
		out = append(out, Pending{RuleID: r.ID, RuleName: r.Name, Kind: r.Kind, ContainerID: k.ctr,
			ContainerName: t.name, Since: t.since, OpensAt: opens, InCooldown: cool, Value: t.peak})
	}
	slices.SortFunc(out, func(a, b Pending) int { return a.Since.Compare(b.Since) })
	return out
}

// Status returns counters and active maintenance windows.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.opts.Now()
	var s Status
	for _, t := range e.tracks {
		switch {
		case t.incident != nil:
			s.Open++
			if t.incident.AckedAt == nil {
				s.Unacked++
			}
		case !t.since.IsZero():
			s.Pending++
		}
	}
	for _, m := range e.maint {
		if m.Until.After(now) {
			s.Maintenance = append(s.Maintenance, m)
		}
	}
	return s
}
