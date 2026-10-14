package collector

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	dt "github.com/franbenaglia/docker-scope/internal/docker/dockertest"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// fakeInv serves a mutable snapshot.
type fakeInv struct {
	mu   sync.Mutex
	snap inventory.Snapshot
}

func (f *fakeInv) Snapshot() inventory.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeInv) set(status inventory.ConnStatus, ctrs ...*inventory.Container) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = inventory.Snapshot{Conn: inventory.ConnState{Status: status}, Synced: true, Containers: ctrs}
}

func running(id string) *inventory.Container {
	return &inventory.Container{ID: id, Name: id, State: container.StateRunning, StartedAt: started}
}

func stopped(id string) *inventory.Container {
	return &inventory.Container{ID: id, Name: id, State: container.StateExited, StartedAt: started}
}

// counterStats returns a StatsFunc where each call advances 5s and the
// container uses exactly one core.
func counterStats() func(string, time.Time) container.StatsResponse {
	var mu sync.Mutex
	n := map[string]uint64{}
	return func(id string, _ time.Time) container.StatsResponse {
		mu.Lock()
		n[id]++
		k := n[id]
		mu.Unlock()
		return stats(t0.Add(time.Duration(k)*5*time.Second), k*5e9, k*40e9)
	}
}

func newCollector(fake *dt.Fake, inv Inventory, opts Options) *Collector {
	opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(fake, inv, opts)
}

func TestRoundSamplesRunningContainersOnce(t *testing.T) {
	fake := dt.New(
		dt.Container("a", "a", started), dt.Container("b", "b", started),
		dt.Container("c", "c", started, dt.Exited(0, t0)),
	)
	fake.StatsFunc = counterStats()
	inv := &fakeInv{}
	inv.set(inventory.ConnConnected, running("a"), running("b"), stopped("c"))
	col := newCollector(fake, inv, Options{})

	var rounds []Round
	col.OnRound(func(r Round) { rounds = append(rounds, r) })

	col.round(context.Background())
	if got := fake.Stats.Load(); got != 2 {
		t.Fatalf("stats pedidos = %d, want 2 (sólo contenedores en ejecución)", got)
	}
	a, ok := col.Latest("a")
	if !ok || a.CPUPercent != nil || a.MemUsage == nil {
		t.Errorf("primera ronda: memoria sí, CPU no disponible: %+v", a)
	}
	if _, ok := col.Latest("c"); ok {
		t.Error("un contenedor detenido no tiene métricas")
	}

	col.round(context.Background())
	a, _ = col.Latest("a")
	near(t, "cpu ronda 2", a.CPUPercent, 100)
	if len(rounds) != 2 || len(rounds[1].Samples) != 2 {
		t.Errorf("rondas = %d", len(rounds))
	}
}

func TestRoundSkippedWhileDisconnected(t *testing.T) {
	fake := dt.New(dt.Container("a", "a", started))
	fake.StatsFunc = counterStats()
	inv := &fakeInv{}
	inv.set(inventory.ConnConnected, running("a"))
	col := newCollector(fake, inv, Options{})
	col.round(context.Background())

	inv.set(inventory.ConnDisconnected, running("a"))
	col.round(context.Background())
	if fake.Stats.Load() != 1 {
		t.Error("desconectado: no se consulta Docker")
	}
	if _, ok := col.Latest("a"); !ok {
		t.Error("desconectado: se conserva la última muestra (la API la marca stale)")
	}
}

func TestStoppedContainerDropsOut(t *testing.T) {
	fake := dt.New(dt.Container("a", "a", started))
	fake.StatsFunc = counterStats()
	inv := &fakeInv{}
	inv.set(inventory.ConnConnected, running("a"))
	col := newCollector(fake, inv, Options{})
	col.round(context.Background())

	// The daemon already sees it stopped, the inventory not yet: Docker
	// returns empty stats, which must not become a sample of zeros.
	fake.Die("a", 0, t0)
	col.round(context.Background())
	if _, ok := col.Latest("a"); ok {
		t.Error("stats vacías no deben producir una muestra")
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	var ctrs []container.InspectResponse
	inv := &fakeInv{}
	var invCtrs []*inventory.Container
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		ctrs = append(ctrs, dt.Container(id, id, started))
		invCtrs = append(invCtrs, running(id))
	}
	fake := dt.New(ctrs...)
	fake.StatsDelay = 20 * time.Millisecond
	inv.set(inventory.ConnConnected, invCtrs...)
	col := newCollector(fake, inv, Options{Concurrency: 3})
	col.round(context.Background())
	if m := fake.MaxInFlight.Load(); m > 3 || m < 2 {
		t.Errorf("máximo en vuelo = %d, want ≤ 3", m)
	}
	if fake.Stats.Load() != 10 {
		t.Errorf("stats = %d", fake.Stats.Load())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	fake := dt.New(dt.Container("a", "a", started))
	fake.StatsDelay = time.Hour
	inv := &fakeInv{}
	inv.set(inventory.ConnConnected, running("a"))
	col := newCollector(fake, inv, Options{Interval: time.Second, Timeout: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = col.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run no terminó al cancelar con una petición de stats en curso")
	}
}
