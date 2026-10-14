// Package dockertest provides an in-memory fake of docker.API that emulates
// the daemon behaviour DockerScope relies on: listing, inspect, an event
// stream with "since" replay, and daemon outages.
package dockertest

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"

	"github.com/franbenaglia/docker-scope/internal/docker"
)

// ErrDown is returned by every call while the fake daemon is down.
var ErrDown = errors.New("dockertest: daemon down")

// Fake is a concurrency-safe fake Docker daemon. The zero value is not usable; use New.
type Fake struct {
	mu   sync.Mutex
	ctrs map[string]container.InspectResponse
	log  []events.Message // event history, used for "since" replay
	subs map[*sub]struct{}
	down bool
	last int64 // last event TimeNano, keeps event times strictly increasing

	// StatsFunc produces stats for running containers. Nil returns empty
	// stats (Read set, everything else zero). Must be safe for concurrent use.
	StatsFunc func(id string, now time.Time) container.StatsResponse
	// StatsDelay is slept (outside the lock) before answering ContainerStats.
	StatsDelay time.Duration
	// LogsFunc produces the raw log stream (use Mux for multiplexed frames).
	// Nil returns an empty stream. Must be safe for concurrent use.
	LogsFunc func(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error)

	Pings, Lists, Inspects, EventSubs, Stats atomic.Int64
	inFlight, MaxInFlight                    atomic.Int64
}

type sub struct {
	msgs chan events.Message
	errs chan error
}

var _ docker.API = (*Fake)(nil)

// New returns a running fake daemon with the given containers (no events emitted).
func New(ctrs ...container.InspectResponse) *Fake {
	f := &Fake{ctrs: map[string]container.InspectResponse{}, subs: map[*sub]struct{}{}}
	for _, c := range ctrs {
		f.ctrs[c.ID] = c
	}
	return f
}

// --- docker.API ---

func (f *Fake) Ping(context.Context) (docker.PingInfo, error) {
	f.Pings.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return docker.PingInfo{}, ErrDown
	}
	return docker.PingInfo{APIVersion: "1.52", OSType: "linux"}, nil
}

// FakeHost is the host reported by Fake.Info.
var FakeHost = docker.HostInfo{NCPU: 8, MemTotal: 16 << 30, CgroupVersion: "2", ServerVersion: "fake", OS: "Fake Linux"}

func (f *Fake) Info(context.Context) (docker.HostInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return docker.HostInfo{}, ErrDown
	}
	return FakeHost, nil
}

func (f *Fake) ListContainers(context.Context) ([]container.Summary, error) {
	f.Lists.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, ErrDown
	}
	out := make([]container.Summary, 0, len(f.ctrs))
	for _, c := range f.ctrs {
		s := container.Summary{
			ID: c.ID, Names: []string{c.Name}, Image: c.Config.Image,
			Labels: c.Config.Labels, State: c.State.Status,
		}
		if c.State.Health != nil {
			s.Health = &container.HealthSummary{Status: c.State.Health.Status}
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b container.Summary) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *Fake) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	f.Inspects.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return container.InspectResponse{}, ErrDown
	}
	c, ok := f.ctrs[id]
	if !ok {
		return container.InspectResponse{}, fmt.Errorf("%w: %s", docker.ErrNotFound, id)
	}
	return clone(c), nil
}

func (f *Fake) ContainerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	f.Stats.Add(1)
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.MaxInFlight.Load()
		if n <= m || f.MaxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if f.StatsDelay > 0 {
		select {
		case <-time.After(f.StatsDelay):
		case <-ctx.Done():
			return container.StatsResponse{}, ctx.Err()
		}
	}
	f.mu.Lock()
	c, ok := f.ctrs[id]
	down, fn := f.down, f.StatsFunc
	running := ok && c.State.Running
	f.mu.Unlock()
	if down {
		return container.StatsResponse{}, ErrDown
	}
	if !ok {
		return container.StatsResponse{}, fmt.Errorf("%w: %s", docker.ErrNotFound, id)
	}
	if !running {
		// Docker answers stopped containers with zeroed stats, Read included.
		return container.StatsResponse{ID: id, Name: c.Name, OSType: "linux"}, nil
	}
	now := time.Now().UTC()
	if fn == nil {
		return container.StatsResponse{ID: id, Name: c.Name, OSType: "linux", Read: now}, nil
	}
	st := fn(id, now)
	st.ID, st.Name, st.OSType = id, c.Name, "linux"
	if st.Read.IsZero() {
		st.Read = now
	}
	return st, nil
}

func (f *Fake) ContainerLogs(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	_, ok := f.ctrs[id]
	down, fn := f.down, f.LogsFunc
	f.mu.Unlock()
	switch {
	case down:
		return nil, ErrDown
	case !ok:
		return nil, fmt.Errorf("%w: %s", docker.ErrNotFound, id)
	case fn == nil:
		return io.NopCloser(strings.NewReader("")), nil
	}
	return fn(ctx, id, opts)
}

// Mux encodes data as one multiplexed log frame (stream 1 = stdout, 2 = stderr).
func Mux(stream byte, data string) []byte {
	h := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[4:], uint32(len(data)))
	return append(h, data...)
}

func (f *Fake) Events(ctx context.Context, since time.Time) (<-chan events.Message, <-chan error) {
	f.EventSubs.Add(1)
	s := &sub{msgs: make(chan events.Message, 1024), errs: make(chan error, 1)}
	f.mu.Lock()
	if f.down {
		f.mu.Unlock()
		s.errs <- ErrDown
		return s.msgs, s.errs
	}
	if !since.IsZero() {
		for _, m := range f.log {
			if m.TimeNano >= since.UnixNano() {
				s.msgs <- m
			}
		}
	}
	f.subs[s] = struct{}{}
	f.mu.Unlock()

	go func() {
		<-ctx.Done()
		f.mu.Lock()
		_, ok := f.subs[s]
		delete(f.subs, s)
		f.mu.Unlock()
		if ok {
			s.errs <- ctx.Err()
		}
	}()
	return s.msgs, s.errs
}

// --- daemon control ---

// SetDown simulates the daemon becoming unreachable (true) or back (false).
// Going down ends every open event stream with ErrDown.
func (f *Fake) SetDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
	if down {
		for s := range f.subs {
			s.errs <- ErrDown
		}
		clear(f.subs)
	}
}

// RestartDaemon simulates a daemon restart: streams end, the in-memory event
// history is lost, and containers whose restart policy is not "no" are
// started again (StartedAt changes; RestartCount is not incremented, as with a
// real daemon restart). Containers without a policy end up exited.
func (f *Fake) RestartDaemon(at time.Time) {
	f.SetDown(true)
	f.mu.Lock()
	f.log = nil
	for id, c := range f.ctrs {
		if c.State.Running {
			pol := c.HostConfig.RestartPolicy.Name
			if pol == container.RestartPolicyAlways || pol == container.RestartPolicyUnlessStopped {
				c.State.StartedAt = at.UTC().Format(time.RFC3339Nano)
			} else {
				setExited(&c, 255, at)
			}
			f.ctrs[id] = c
		}
	}
	f.down = false
	f.mu.Unlock()
}

// Create adds a container in "created" state and emits "create".
func (f *Fake) Create(c container.InspectResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctrs[c.ID] = c
	f.emitLocked(c, events.ActionCreate)
}

// Start marks a container running at time at and emits "start".
func (f *Fake) Start(id string, at time.Time) {
	f.update(id, func(c *container.InspectResponse) events.Action {
		setRunning(c, at)
		return events.ActionStart
	})
}

// Die marks a container exited with code at time at and emits "die".
func (f *Fake) Die(id string, code int, at time.Time) {
	f.update(id, func(c *container.InspectResponse) events.Action {
		setExited(c, code, at)
		return events.ActionDie
	})
}

// AutoRestart emulates the restart policy restarting a dead container:
// "die" then "start", with RestartCount incremented.
func (f *Fake) AutoRestart(id string, code int, at time.Time) {
	f.Die(id, code, at)
	f.update(id, func(c *container.InspectResponse) events.Action {
		c.RestartCount++
		setRunning(c, at.Add(time.Millisecond))
		return events.ActionStart
	})
}

// SetHealth sets the health status and emits "health_status: <status>".
func (f *Fake) SetHealth(id string, status container.HealthStatus) {
	f.update(id, func(c *container.InspectResponse) events.Action {
		if c.State.Health == nil {
			c.State.Health = &container.Health{}
		}
		c.State.Health.Status = status
		return events.Action(string(events.ActionHealthStatus) + ": " + string(status))
	})
}

// Destroy removes a container and emits "destroy".
func (f *Fake) Destroy(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.ctrs[id]
	if !ok {
		return
	}
	delete(f.ctrs, id)
	f.emitLocked(c, events.ActionDestroy)
}

// Emit sends an arbitrary event for container id (which may not exist).
func (f *Fake) Emit(id string, action events.Action) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitLocked(container.InspectResponse{ID: id}, action)
}

// SetQuiet mutates a container without emitting an event (e.g. a change missed
// while disconnected).
func (f *Fake) SetQuiet(id string, fn func(c *container.InspectResponse)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.ctrs[id]
	fn(&c)
	f.ctrs[id] = c
}

func (f *Fake) update(id string, fn func(c *container.InspectResponse) events.Action) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.ctrs[id]
	if !ok {
		panic("dockertest: contenedor desconocido " + id)
	}
	action := fn(&c)
	f.ctrs[id] = c
	f.emitLocked(c, action)
}

func (f *Fake) emitLocked(c container.InspectResponse, action events.Action) {
	now := time.Now().UnixNano()
	if now <= f.last {
		now = f.last + 1
	}
	f.last = now
	attrs := map[string]string{"name": strings.TrimPrefix(c.Name, "/")}
	if c.Config != nil {
		attrs["image"] = c.Config.Image
	}
	m := events.Message{
		Type: events.ContainerEventType, Action: action, Scope: "local",
		Actor: events.Actor{ID: c.ID, Attributes: attrs},
		Time:  now / int64(time.Second), TimeNano: now,
	}
	f.log = append(f.log, m)
	if f.down {
		return
	}
	for s := range f.subs {
		select {
		case s.msgs <- m:
		default:
			panic("dockertest: buffer de eventos lleno")
		}
	}
}

// --- container builders ---

// Option customizes a container built with Container.
type Option func(c *container.InspectResponse)

// Container builds an InspectResponse. By default it is running since startedAt,
// with restart policy "no", no healthcheck and no TTY.
func Container(id, name string, startedAt time.Time, opts ...Option) container.InspectResponse {
	c := container.InspectResponse{
		ID:      id,
		Name:    "/" + name,
		Created: startedAt.Add(-time.Second).UTC().Format(time.RFC3339Nano),
		Image:   "sha256:" + id,
		State:   &container.State{},
		Config:  &container.Config{Image: name + ":latest", Labels: map[string]string{}},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			LogConfig:     container.LogConfig{Type: "json-file"},
		},
	}
	setRunning(&c, startedAt)
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Exited makes the container exited with code at time at.
func Exited(code int, at time.Time) Option {
	return func(c *container.InspectResponse) { setExited(c, code, at) }
}

// Created makes the container never started.
func Created() Option {
	return func(c *container.InspectResponse) {
		c.State = &container.State{Status: container.StateCreated, StartedAt: zeroTime, FinishedAt: zeroTime}
	}
}

// Health adds a healthcheck with the given status.
func Health(status container.HealthStatus) Option {
	return func(c *container.InspectResponse) {
		c.Config.Healthcheck = &container.HealthConfig{Test: []string{"CMD", "true"}}
		c.State.Health = &container.Health{Status: status}
	}
}

// Compose adds Docker Compose project/service labels.
func Compose(project, service string) Option {
	return func(c *container.InspectResponse) {
		c.Config.Labels["com.docker.compose.project"] = project
		c.Config.Labels["com.docker.compose.service"] = service
	}
}

// MemoryLimit sets an explicit memory limit (docker run --memory).
func MemoryLimit(bytes int64) Option {
	return func(c *container.InspectResponse) { c.HostConfig.Memory = bytes }
}

// NanoCPUs sets a CPU limit (docker run --cpus=N → N×1e9).
func NanoCPUs(n int64) Option {
	return func(c *container.InspectResponse) { c.HostConfig.NanoCPUs = n }
}

// Restart sets the restart policy.
func Restart(mode container.RestartPolicyMode) Option {
	return func(c *container.InspectResponse) { c.HostConfig.RestartPolicy.Name = mode }
}

const zeroTime = "0001-01-01T00:00:00Z"

func setRunning(c *container.InspectResponse, at time.Time) {
	h := c.State.Health
	*c.State = container.State{
		Status: container.StateRunning, Running: true, Pid: 1234,
		StartedAt: at.UTC().Format(time.RFC3339Nano), FinishedAt: zeroTime, Health: h,
	}
	if h != nil {
		c.State.Health.Status = container.Starting
	}
}

func setExited(c *container.InspectResponse, code int, at time.Time) {
	c.State.Status = container.StateExited
	c.State.Running = false
	c.State.Pid = 0
	c.State.ExitCode = code
	c.State.FinishedAt = at.UTC().Format(time.RFC3339Nano)
}

func clone(c container.InspectResponse) container.InspectResponse {
	if c.State != nil {
		s := *c.State
		if s.Health != nil {
			h := *s.Health
			s.Health = &h
		}
		c.State = &s
	}
	return c
}
