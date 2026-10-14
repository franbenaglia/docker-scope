package inventory

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"

	dt "github.com/franbenaglia/docker-scope/internal/docker/dockertest"
)

const (
	idWeb    = "aaaaaaaaaaaa0000000000000000000000000000000000000000000000000001"
	idDB     = "bbbbbbbbbbbb0000000000000000000000000000000000000000000000000002"
	idOld    = "cccccccccccc0000000000000000000000000000000000000000000000000003"
	idWebNew = "dddddddddddd0000000000000000000000000000000000000000000000000004"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	fake   *dt.Fake
	inv    *Inventory
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	changes []Change
	conns   []ConnState
}

func start(t *testing.T, fake *dt.Fake) *harness {
	t.Helper()
	h := &harness{t: t, fake: fake, done: make(chan struct{})}
	h.inv = New(fake, Options{
		ResyncInterval: time.Hour, // tests trigger reconciliation through reconnects
		MinBackoff:     time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	h.inv.OnChange(func(c Change) { h.mu.Lock(); h.changes = append(h.changes, c); h.mu.Unlock() })
	h.inv.OnConnection(func(s ConnState) { h.mu.Lock(); h.conns = append(h.conns, s); h.mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { _ = h.inv.Run(ctx); close(h.done) }()
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		h.t.Fatal("Run no terminó tras cancelar")
	}
}

func (h *harness) waitFor(what string, cond func(s Snapshot) bool) Snapshot {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := h.inv.Snapshot()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timeout esperando: %s (estado: %+v)", what, s.Conn)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) waitConnected() Snapshot {
	h.t.Helper()
	return h.waitFor("conectado", func(s Snapshot) bool { return s.Conn.Status == ConnConnected })
}

func (h *harness) takeChanges() []Change {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.changes
	h.changes = nil
	return out
}

func find(s Snapshot, id string) *Container {
	for _, c := range s.Containers {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func TestInitialSyncMarksPreexistingState(t *testing.T) {
	fake := dt.New(
		dt.Container(idWeb, "web", t0, dt.Compose("shop", "web"), dt.Health(container.Healthy)),
		dt.Container(idOld, "old", t0, dt.Exited(137, t0.Add(time.Minute))),
	)
	h := start(t, fake)
	s := h.waitConnected()

	if s.Host.NCPU != 8 || s.Host.MemTotal != 16<<30 {
		t.Errorf("host = %+v", s.Host)
	}
	if !s.Synced || s.Stale || len(s.Containers) != 2 {
		t.Fatalf("snapshot inesperado: synced=%v stale=%v n=%d", s.Synced, s.Stale, len(s.Containers))
	}
	web := find(s, idWeb)
	if web.ComposeProject != "shop" || web.ComposeService != "web" || web.ShortID() != idWeb[:12] {
		t.Errorf("web: %+v", web)
	}
	if web.Health != HealthStarting && web.Health != HealthHealthy {
		t.Errorf("health web = %q", web.Health)
	}
	old := find(s, idOld)
	if old.ExitCode == nil || *old.ExitCode != 137 {
		t.Errorf("exit code old = %v", old.ExitCode)
	}
	for _, c := range h.takeChanges() {
		if c.Kind != Added || !c.Initial || c.Event != nil {
			t.Errorf("cambio inicial inesperado: %+v", c)
		}
	}
}

func TestEventsUpdateState(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0, dt.Health(container.Starting)))
	h := start(t, fake)
	h.waitConnected()
	h.takeChanges()

	fake.SetHealth(idWeb, container.Unhealthy)
	h.waitFor("unhealthy", func(s Snapshot) bool { return find(s, idWeb).Health == HealthUnhealthy })
	fake.Die(idWeb, 1, t0.Add(time.Minute))
	s := h.waitFor("exited", func(s Snapshot) bool { return find(s, idWeb).State == container.StateExited })

	web := find(s, idWeb)
	if web.HealthCurrent() != "" {
		t.Errorf("un contenedor detenido no tiene salud actual, got %q", web.HealthCurrent())
	}
	if web.ExitCode == nil || *web.ExitCode != 1 {
		t.Errorf("exit code = %v", web.ExitCode)
	}
	ch := h.takeChanges()
	if len(ch) != 2 {
		t.Fatalf("esperaba 2 cambios, hubo %d", len(ch))
	}
	last := ch[1]
	if last.Kind != Updated || last.Initial || last.Event == nil || last.Event.Action != events.ActionDie {
		t.Errorf("cambio por die: %+v", last)
	}
	if last.Prev.State != container.StateRunning || last.Cur.State != container.StateExited {
		t.Errorf("prev/cur: %s → %s", last.Prev.State, last.Cur.State)
	}
}

func TestRecreateWithSameNameIsNewContainer(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0))
	h := start(t, fake)
	h.waitConnected()
	h.takeChanges()

	fake.Die(idWeb, 0, t0.Add(time.Minute))
	fake.Destroy(idWeb)
	fake.Create(dt.Container(idWebNew, "web", t0.Add(2*time.Minute), dt.Created()))
	fake.Start(idWebNew, t0.Add(2*time.Minute))

	s := h.waitFor("web nuevo en ejecución", func(s Snapshot) bool {
		c := find(s, idWebNew)
		return c != nil && c.Running() && find(s, idWeb) == nil
	})
	if n := find(s, idWebNew).ObservedRestarts; n != 0 {
		t.Errorf("el contenedor recreado no hereda reinicios: %d", n)
	}
	var removed bool
	for _, c := range h.takeChanges() {
		if c.Kind == Removed && c.ID == idWeb {
			removed = true
		}
	}
	if !removed {
		t.Error("falta el cambio Removed del contenedor viejo")
	}
}

func TestObservedRestartsVsRestartCount(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0, dt.Restart(container.RestartPolicyOnFailure)))
	h := start(t, fake)
	h.waitConnected()

	fake.AutoRestart(idWeb, 1, t0.Add(time.Minute))
	h.waitFor("1 reinicio", func(s Snapshot) bool { return find(s, idWeb).ObservedRestarts == 1 })

	// Manual stop + start: Docker's RestartCount does not change.
	fake.Die(idWeb, 0, t0.Add(2*time.Minute))
	fake.Start(idWeb, t0.Add(3*time.Minute))
	s := h.waitFor("2 reinicios", func(s Snapshot) bool { return find(s, idWeb).ObservedRestarts == 2 })

	web := find(s, idWeb)
	if web.RestartCount != 1 {
		t.Errorf("RestartCount de Docker = %d, want 1", web.RestartCount)
	}
	if !web.LastObservedRestart.Equal(t0.Add(3 * time.Minute)) {
		t.Errorf("LastObservedRestart = %s", web.LastObservedRestart)
	}
}

func TestDisconnectKeepsStaleStateAndReconnects(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0), dt.Container(idDB, "db", t0))
	h := start(t, fake)
	h.waitConnected()
	h.takeChanges()

	fake.SetDown(true)
	s := h.waitFor("desconectado", func(s Snapshot) bool { return s.Conn.Status == ConnDisconnected })
	if !s.Stale || s.Conn.LastError == "" {
		t.Errorf("con Docker caído los datos deben estar marcados stale: %+v", s.Conn)
	}
	for _, c := range s.Containers {
		if !c.Running() {
			t.Errorf("la desconexión no debe mostrar %s como detenido (estado %s)", c.Name, c.State)
		}
	}
	if len(h.takeChanges()) != 0 {
		t.Error("la desconexión no debe generar cambios de contenedores")
	}
	// Retries with backoff while down.
	pings := fake.Pings.Load()
	h.waitFor("reintentos", func(Snapshot) bool { return fake.Pings.Load() >= pings+3 })

	fake.SetDown(false)
	s = h.waitConnected()
	if s.Stale || len(s.Containers) != 2 {
		t.Errorf("tras reconectar: stale=%v n=%d", s.Stale, len(s.Containers))
	}
	if len(h.takeChanges()) != 0 {
		t.Error("reconectar sin cambios reales no debe generar cambios")
	}
}

func TestReconcileAfterDaemonRestart(t *testing.T) {
	fake := dt.New(
		dt.Container(idWeb, "web", t0, dt.Restart(container.RestartPolicyAlways)),
		dt.Container(idDB, "db", t0), // restart policy "no": stays down
	)
	h := start(t, fake)
	h.waitConnected()
	h.takeChanges()

	fake.RestartDaemon(t0.Add(time.Hour))
	s := h.waitFor("reconciliado", func(s Snapshot) bool {
		return s.Conn.Status == ConnConnected && find(s, idDB).State == container.StateExited
	})
	if web := find(s, idWeb); !web.Running() || web.ObservedRestarts != 1 {
		t.Errorf("web: running=%v observed=%d", web.Running(), web.ObservedRestarts)
	}
	for _, c := range h.takeChanges() {
		if c.Initial || c.Event != nil {
			t.Errorf("cambio por reconciliación debe ser no-inicial y sin evento: %+v", c)
		}
	}
}

func TestRemovedWhileDisconnected(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0), dt.Container(idOld, "old", t0, dt.Exited(0, t0)))
	h := start(t, fake)
	h.waitConnected()
	h.takeChanges()

	fake.SetDown(true)
	h.waitFor("desconectado", func(s Snapshot) bool { return s.Conn.Status == ConnDisconnected })
	fake.Destroy(idOld) // missed: no subscribers while down
	fake.SetDown(false)

	s := h.waitFor("old eliminado", func(s Snapshot) bool {
		return s.Conn.Status == ConnConnected && find(s, idOld) == nil
	})
	if len(s.Containers) != 1 {
		t.Errorf("n=%d", len(s.Containers))
	}
	ch := h.takeChanges()
	if len(ch) != 1 || ch[0].Kind != Removed || ch[0].ID != idOld {
		t.Errorf("cambios = %+v", ch)
	}
}

func TestReplayedEventsAreDeduplicated(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0, dt.Health(container.Starting)))
	h := start(t, fake)
	h.waitConnected()

	fake.SetHealth(idWeb, container.Healthy)
	h.waitFor("healthy", func(s Snapshot) bool { return find(s, idWeb).Health == HealthHealthy })
	h.takeChanges()
	inspects := fake.Inspects.Load()

	// Reconnect: the fake replays events since the last one processed.
	fake.SetDown(true)
	h.waitFor("desconectado", func(s Snapshot) bool { return s.Conn.Status == ConnDisconnected })
	fake.SetDown(false)
	h.waitConnected()

	if ch := h.takeChanges(); len(ch) != 0 {
		t.Errorf("eventos repetidos no deben generar cambios: %+v", ch)
	}
	// Only the reconciliation inspect (1 container); the replayed event is skipped.
	if got := fake.Inspects.Load() - inspects; got != 1 {
		t.Errorf("inspects tras reconectar = %d, want 1", got)
	}
}

func TestCancelClosesEventStream(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0))
	h := start(t, fake)
	h.waitConnected()
	h.stop()
	// After Run returns, new events must not reach the inventory.
	fake.Die(idWeb, 0, t0.Add(time.Minute))
	time.Sleep(10 * time.Millisecond)
	if !find(h.inv.Snapshot(), idWeb).Running() {
		t.Error("tras cancelar, el inventario no debe seguir procesando eventos")
	}
}

func TestNeverConnectedIsNotSynced(t *testing.T) {
	fake := dt.New(dt.Container(idWeb, "web", t0))
	fake.SetDown(true)
	h := start(t, fake)
	s := h.waitFor("desconectado", func(s Snapshot) bool { return s.Conn.Status == ConnDisconnected })
	if s.Synced || s.Stale || len(s.Containers) != 0 {
		t.Errorf("sin conexión inicial el estado es desconocido: %+v", s)
	}
}

func TestFromInspect(t *testing.T) {
	c := fromInspect(dt.Container(idWeb, "web", t0, dt.Created()))
	if c.ExitCode != nil || !c.StartedAt.IsZero() || c.Health != HealthNone {
		t.Errorf("created: exit=%v started=%v health=%q", c.ExitCode, c.StartedAt, c.Health)
	}
	if c.RestartPolicy != "no" || c.LogDriver != "json-file" || c.Name != "web" {
		t.Errorf("campos: %+v", c)
	}
	r := dt.Container(idWeb, "web", t0)
	r.State.Health = &container.Health{Status: container.NoHealthcheck}
	if fromInspect(r).Health != HealthNone {
		t.Error(`"none" de Docker debe mapearse a HealthNone`)
	}
	lim := fromInspect(dt.Container(idWeb, "web", t0, dt.MemoryLimit(64<<20), dt.NanoCPUs(500_000_000)))
	if lim.MemoryLimit != 64<<20 || lim.CPULimit != 0.5 {
		t.Errorf("límites: mem=%d cpu=%v", lim.MemoryLimit, lim.CPULimit)
	}
	q := dt.Container(idWeb, "web", t0)
	q.HostConfig.CPUQuota = 150_000
	if got := fromInspect(q).CPULimit; got != 1.5 {
		t.Errorf("cpu por quota/period por defecto = %v, want 1.5", got)
	}
	if up := fromInspect(r).Uptime(t0.Add(time.Hour)); up != time.Hour {
		t.Errorf("uptime = %s", up)
	}
}

func TestDedup(t *testing.T) {
	d := newDedup(2)
	if !d.add("a") || d.add("a") || !d.add("b") || !d.add("c") {
		t.Fatal("secuencia básica")
	}
	if !d.add("a") { // evicted
		t.Error(`"a" debería haber sido desalojado`)
	}
}
