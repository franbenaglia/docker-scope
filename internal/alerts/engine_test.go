package alerts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

// --- fakes ---

type memStore struct {
	mu        sync.Mutex
	rules     []Rule
	maint     []Maintenance
	incidents []Incident
	nextID    int64
	failNext  bool
}

func (m *memStore) id() int64 { m.nextID++; return m.nextID }

func (m *memStore) ListRules(context.Context) ([]Rule, error) { return slices.Clone(m.rules), nil }
func (m *memStore) InsertRule(_ context.Context, r *Rule) error {
	r.ID = m.id()
	m.rules = append(m.rules, *r)
	return nil
}
func (m *memStore) UpdateRule(_ context.Context, r *Rule) error {
	for i := range m.rules {
		if m.rules[i].ID == r.ID {
			m.rules[i] = *r
			return nil
		}
	}
	return ErrNotFound
}
func (m *memStore) DeleteRule(_ context.Context, id int64) error {
	m.rules = slices.DeleteFunc(m.rules, func(r Rule) bool { return r.ID == id })
	return nil
}
func (m *memStore) ListMaintenance(_ context.Context, now time.Time) ([]Maintenance, error) {
	return slices.DeleteFunc(slices.Clone(m.maint), func(x Maintenance) bool { return !x.Until.After(now) }), nil
}
func (m *memStore) InsertMaintenance(_ context.Context, x *Maintenance) error {
	x.ID = m.id()
	m.maint = append(m.maint, *x)
	return nil
}
func (m *memStore) DeleteMaintenance(_ context.Context, id int64) error {
	m.maint = slices.DeleteFunc(m.maint, func(x Maintenance) bool { return x.ID == id })
	return nil
}
func (m *memStore) OpenIncidents(context.Context) ([]Incident, error) {
	var out []Incident
	for _, i := range m.incidents {
		if i.State == Open {
			out = append(out, i)
		}
	}
	return out, nil
}
func (m *memStore) InsertIncident(_ context.Context, inc *Incident) error {
	if m.failNext {
		m.failNext = false
		return errors.New("disco lleno")
	}
	for _, i := range m.incidents { // the unique index of the real schema
		if i.State == Open && i.RuleID == inc.RuleID && i.ContainerID == inc.ContainerID {
			return errors.New("UNIQUE constraint failed")
		}
	}
	inc.ID = m.id()
	m.incidents = append(m.incidents, *inc)
	return nil
}
func (m *memStore) UpdateIncident(_ context.Context, inc *Incident) error {
	for i := range m.incidents {
		if m.incidents[i].ID == inc.ID {
			m.incidents[i] = *inc
			return nil
		}
	}
	return ErrNotFound
}
func (m *memStore) GetIncident(_ context.Context, id int64) (Incident, error) {
	for _, i := range m.incidents {
		if i.ID == id {
			return i, nil
		}
	}
	return Incident{}, ErrNotFound
}
func (m *memStore) ListIncidents(context.Context, IncidentQuery) ([]Incident, int, error) {
	return slices.Clone(m.incidents), len(m.incidents), nil
}

type fakeInv struct {
	snap inventory.Snapshot
}

func (f *fakeInv) Snapshot() inventory.Snapshot { return f.snap }

type fakeMet map[string]collector.Sample

func (f fakeMet) LatestAll() map[string]collector.Sample { return f }

// --- harness ---

type h struct {
	t      *testing.T
	store  *memStore
	inv    *fakeInv
	met    fakeMet
	eng    *Engine
	now    time.Time
	events []Event
}

func newH(t *testing.T, rules ...Rule) *h {
	t.Helper()
	x := &h{t: t, store: &memStore{}, inv: &fakeInv{}, met: fakeMet{}, now: t0}
	for _, r := range rules {
		if err := x.store.InsertRule(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}
	x.inv.snap = inventory.Snapshot{Conn: inventory.ConnState{Status: inventory.ConnConnected}, Synced: true}
	x.eng = New(x.store, x.inv, x.met, Options{Now: func() time.Time { return x.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	x.eng.OnEvent(func(e Event) { x.events = append(x.events, e) })
	if err := x.eng.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return x
}

// set replaces the containers in the snapshot.
func (x *h) set(cs ...*inventory.Container) { x.inv.snap.Containers = cs }

// at runs a tick at t0+sec seconds.
func (x *h) at(sec float64) {
	x.now = t0.Add(time.Duration(sec * float64(time.Second)))
	x.eng.Tick(ctx, x.now)
}

func (x *h) open() []Incident {
	var out []Incident
	for _, i := range x.store.incidents {
		if i.State == Open {
			out = append(out, i)
		}
	}
	return out
}

func (x *h) take() []EventType {
	var out []EventType
	for _, e := range x.events {
		out = append(out, e.Type)
	}
	x.events = nil
	return out
}

func running(id, name string) *inventory.Container {
	return &inventory.Container{ID: id, Name: name, State: container.StateRunning, Health: inventory.HealthNone, StartedAt: t0.Add(-time.Hour)}
}

func withHealth(c *inventory.Container, health string) *inventory.Container {
	cp := *c
	cp.Health = health
	return &cp
}

func exited(c *inventory.Container, code int) *inventory.Container {
	cp := *c
	cp.State = container.StateExited
	cp.ExitCode = &code
	return &cp
}

func f(v float64) *float64 { return &v }

var unhealthyRule = Rule{Name: "u", Kind: Unhealthy, Enabled: true, For: 30 * time.Second, Cooldown: 5 * time.Minute}

// --- tests ---

func TestUnhealthyMinDurationOpenResolve(t *testing.T) {
	x := newH(t, unhealthyRule)
	web := running("w1", "web")
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(0)
	if p := x.eng.Pending(); len(p) != 1 || !p[0].OpensAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("pendiente = %+v", p)
	}
	x.at(29)
	if len(x.open()) != 0 {
		t.Fatal("no debe abrir antes de la duración mínima")
	}
	x.at(30)
	open := x.open()
	if len(open) != 1 || !open[0].StartedAt.Equal(t0) || !open[0].OpenedAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("incidente = %+v", open)
	}
	if !strings.Contains(open[0].Summary, "unhealthy") || open[0].Suppressed {
		t.Errorf("incidente: %+v", open[0])
	}
	for range 10 { // dedup: still one
		x.at(31)
	}
	if len(x.store.incidents) != 1 {
		t.Errorf("deduplicación: %d incidentes", len(x.store.incidents))
	}
	x.set(withHealth(web, inventory.HealthHealthy))
	x.at(40)
	if len(x.open()) != 0 || x.store.incidents[0].Resolution != "recuperado" || x.store.incidents[0].ResolvedAt == nil {
		t.Errorf("recuperación: %+v", x.store.incidents[0])
	}
	if got := x.take(); !slices.Equal(got, []EventType{EvOpened, EvResolved}) {
		t.Errorf("eventos = %v", got)
	}
}

func TestPendingCancelledWhenConditionClears(t *testing.T) {
	x := newH(t, unhealthyRule)
	web := running("w1", "web")
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(0)
	x.set(withHealth(web, inventory.HealthHealthy))
	x.at(20)
	if len(x.eng.Pending()) != 0 {
		t.Fatal("el pendiente debe cancelarse")
	}
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(25)
	x.at(54) // 29s since the new start
	if len(x.open()) != 0 {
		t.Fatal("la duración se cuenta desde el nuevo inicio")
	}
	x.at(55)
	if len(x.open()) != 1 {
		t.Fatal("debe abrir a los 30s del nuevo inicio")
	}
}

func TestCooldownDelaysReopening(t *testing.T) {
	x := newH(t, unhealthyRule)
	web := running("w1", "web")
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(0)
	x.at(30) // opened
	x.set(withHealth(web, inventory.HealthHealthy))
	x.at(40) // resolved; cooldown until 340
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(50)
	p := x.eng.Pending()
	if len(p) != 1 || !p[0].InCooldown || !p[0].OpensAt.Equal(t0.Add(340*time.Second)) {
		t.Fatalf("pendiente en cooldown = %+v", p)
	}
	x.at(339)
	if len(x.open()) != 0 {
		t.Fatal("no reabre durante el cooldown")
	}
	x.at(340)
	if len(x.open()) != 1 || len(x.store.incidents) != 2 {
		t.Fatal("reabre al terminar el cooldown si la condición sigue")
	}
}

func TestRecoverForAvoidsFlapping(t *testing.T) {
	r := unhealthyRule
	r.For, r.RecoverFor = 0, 20*time.Second
	x := newH(t, r)
	web := running("w1", "web")
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(0)
	x.set(withHealth(web, inventory.HealthHealthy))
	x.at(1)
	x.at(10)
	x.set(withHealth(web, inventory.HealthUnhealthy))
	x.at(11) // flaps back before RecoverFor
	x.set(withHealth(web, inventory.HealthHealthy))
	x.at(12)
	x.at(31)
	if len(x.open()) != 1 {
		t.Fatal("no se resuelve hasta 20s seguidos sin la condición")
	}
	x.at(32)
	if len(x.open()) != 0 || len(x.store.incidents) != 1 {
		t.Fatalf("resuelto sin abrir un segundo incidente: %+v", x.store.incidents)
	}
}

func TestMemoryHysteresis(t *testing.T) {
	x := newH(t, Rule{Name: "m", Kind: Memory, Enabled: true, Threshold: 80, Hysteresis: 5, For: 0})
	web := running("w1", "web")
	x.set(web)
	sample := func(p float64) {
		x.met["w1"] = collector.Sample{ContainerID: "w1", MemPercent: f(p), MemLimit: ptrU(512 << 20), MemLimitExplicit: true}
	}
	sample(85)
	x.at(0)
	open := x.open()
	if len(open) != 1 || !strings.Contains(open[0].Summary, "límite de 512.0 MiB") {
		t.Fatalf("incidente = %+v", open)
	}
	sample(78) // below threshold but within the hysteresis band
	x.at(1)
	if len(x.open()) != 1 {
		t.Fatal("dentro de la banda de histéresis no se resuelve")
	}
	sample(90)
	x.at(2)
	sample(74.9)
	x.at(3)
	if len(x.open()) != 0 {
		t.Fatal("bajo umbral − histéresis se resuelve")
	}
	if p := x.store.incidents[0].Peak; p == nil || *p != 90 {
		t.Errorf("pico = %v, want 90", p)
	}
}

func ptrU(v uint64) *uint64 { return &v }

func TestCPUPendingCancelledInBandAndUnknownHolds(t *testing.T) {
	x := newH(t, Rule{Name: "c", Kind: CPU, Enabled: true, Threshold: 150, Hysteresis: 20, For: 10 * time.Second})
	x.set(running("w1", "web"))
	x.met["w1"] = collector.Sample{CPUPercent: f(200)}
	x.at(0)
	delete(x.met, "w1") // no sample: unknown, the pending condition is kept
	x.at(5)
	if len(x.eng.Pending()) != 1 {
		t.Fatal("sin dato no hay transición")
	}
	x.met["w1"] = collector.Sample{CPUPercent: f(140)} // band: cancels pending
	x.at(6)
	if len(x.eng.Pending()) != 0 {
		t.Fatal("bajo el umbral se cancela el pendiente")
	}
	x.met["w1"] = collector.Sample{CPUPercent: f(151)}
	x.at(7)
	x.at(17)
	open := x.open()
	if len(open) != 1 || !strings.Contains(open[0].Summary, "100% = 1 núcleo") {
		t.Fatalf("incidente CPU = %+v", open)
	}
}

func TestRestartsInWindow(t *testing.T) {
	x := newH(t, Rule{Name: "r", Kind: Restarts, Enabled: true, Threshold: 3, Window: 5 * time.Minute})
	c := running("w1", "web")
	c.ObservedRestarts = 7 // history before the engine started: not counted
	x.set(c)
	x.at(0)
	restart := func(sec float64) {
		cp := *c
		cp.ObservedRestarts++
		cp.LastObservedRestart = t0.Add(time.Duration(sec) * time.Second)
		c = &cp
		x.set(c)
		x.at(sec)
	}
	restart(10)
	restart(70)
	if len(x.open()) != 0 {
		t.Fatal("2 reinicios no alcanzan el umbral de 3")
	}
	restart(130)
	open := x.open()
	if len(open) != 1 || !strings.Contains(open[0].Summary, "3 veces en 5m") {
		t.Fatalf("incidente = %+v", open)
	}
	x.at(309) // window still holds 3
	if len(x.open()) != 1 {
		t.Fatal("sigue abierto mientras la ventana contenga 3 reinicios")
	}
	x.at(311) // the restart at 10s left the window
	if len(x.open()) != 0 {
		t.Fatal("se resuelve cuando la ventana baja del umbral")
	}
}

func TestNotRunning(t *testing.T) {
	r := Rule{Name: "nr", Kind: NotRunning, Enabled: true, Scope: Scope{Project: "shop"}, For: 5 * time.Second}
	x := newH(t, r)
	api := running("a1", "api")
	api.ComposeProject = "shop"
	old := exited(running("o1", "old"), 1)
	old.ComposeProject = "shop"
	other := exited(running("x1", "other"), 1) // out of scope
	x.set(api, old, other)
	x.at(0)
	x.at(60)
	if len(x.open()) != 0 {
		t.Fatal("los contenedores ya detenidos al arrancar no alertan")
	}
	x.eng.Observe(inventory.Change{ID: "a1", Event: &events.Message{Action: events.ActionKill, TimeNano: t0.UnixNano()}})
	x.set(exited(api, 137), old, other)
	x.at(61)
	x.at(66)
	open := x.open()
	if len(open) != 1 || open[0].ContainerName != "api" {
		t.Fatalf("incidente = %+v", open)
	}
	if !strings.Contains(open[0].Cause, "SIGKILL") || !strings.Contains(open[0].Cause, "kill") || !strings.Contains(open[0].Summary, "137") {
		t.Errorf("causa/resumen = %q / %q", open[0].Cause, open[0].Summary)
	}
	x.set(api, old, other)
	x.at(70)
	if len(x.open()) != 0 {
		t.Fatal("se resuelve al volver a ejecutarse")
	}
}

func TestNotRunningPausedDoesNotAlert(t *testing.T) {
	x := newH(t, Rule{Name: "nr", Kind: NotRunning, Enabled: true, Scope: Scope{Container: "api"}})
	api := running("a1", "api")
	x.set(api)
	x.at(0)
	p := *api
	p.State = container.StatePaused
	x.set(&p)
	x.at(10)
	if len(x.open())+len(x.eng.Pending()) != 0 {
		t.Fatal("pausar no es dejar de ejecutarse")
	}
}

func TestNotRunningRemovedBetweenTicksAndRecreated(t *testing.T) {
	x := newH(t, Rule{Name: "nr", Kind: NotRunning, Enabled: true, Scope: Scope{Container: "api"}})
	x.set(running("a1", "api"))
	x.at(0)
	x.set() // docker rm -f: never seen stopped
	x.at(1)
	open := x.open()
	if len(open) != 1 || !strings.Contains(open[0].Summary, "eliminado") || !strings.Contains(open[0].Cause, "posible") {
		t.Fatalf("incidente por eliminación = %+v", open)
	}
	x.set(running("a2", "api")) // recreated: new ID, same name
	x.at(2)
	if len(x.open()) != 0 || x.store.incidents[0].Resolution != "recreado con un ID nuevo y en ejecución" {
		t.Fatalf("resolución = %+v", x.store.incidents[0])
	}
	x.at(3)
	if len(x.store.incidents) != 1 {
		t.Error("el contenedor nuevo no debe abrir otro incidente")
	}
}

func TestRemovedContainerResolvesOtherKinds(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.at(0)
	x.set()
	x.at(1)
	if len(x.open()) != 0 || x.store.incidents[0].Resolution != "contenedor eliminado" {
		t.Fatalf("%+v", x.store.incidents)
	}
}

func TestMaintenanceSuppressesButRecords(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true})
	if _, err := x.eng.AddMaintenance(ctx, "web", t0.Add(time.Minute), "deploy"); err != nil {
		t.Fatal(err)
	}
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy), withHealth(running("d1", "db"), inventory.HealthUnhealthy))
	x.at(0)
	byName := map[string]Incident{}
	for _, i := range x.open() {
		byName[i.ContainerName] = i
	}
	if !byName["web"].Suppressed || byName["db"].Suppressed {
		t.Errorf("mantenimiento por contenedor: web=%v db=%v", byName["web"].Suppressed, byName["db"].Suppressed)
	}
	if st := x.eng.Status(); len(st.Maintenance) != 1 || st.Open != 2 {
		t.Errorf("status = %+v", st)
	}
	// Expired windows stop suppressing.
	x.store.incidents = nil
	x.eng.tracks = map[key]*track{}
	x.at(61)
	for _, i := range x.open() {
		if i.Suppressed {
			t.Errorf("mantenimiento vencido no suprime: %+v", i)
		}
	}
	if _, err := x.eng.AddMaintenance(ctx, "", t0, ""); !errors.Is(err, ErrValidation) {
		t.Error("until en el pasado debe ser inválido")
	}
	if _, err := x.eng.AddMaintenance(ctx, "", t0.Add(40*24*time.Hour), ""); !errors.Is(err, ErrValidation) {
		t.Error("más de 30 días debe ser inválido")
	}
}

func TestDisconnectedFreezesEvaluation(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true, For: 10 * time.Second})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.at(0)
	x.at(10)
	x.inv.snap.Conn.Status = inventory.ConnDisconnected
	x.set(withHealth(running("w1", "web"), inventory.HealthHealthy))
	x.at(20)
	if len(x.open()) != 1 {
		t.Fatal("desconectado: no se resuelve con datos viejos")
	}
	x.set()
	x.at(30)
	if len(x.open()) != 1 || x.store.incidents[0].Resolution != "" {
		t.Fatal("desconectado: un contenedor ausente no se da por eliminado")
	}
}

func TestAckDoesNotResolve(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.at(0)
	id := x.open()[0].ID
	inc, err := x.eng.Ack(ctx, id)
	if err != nil || inc.AckedAt == nil || inc.State != Open {
		t.Fatalf("ack = %+v, %v", inc, err)
	}
	if st := x.eng.Status(); st.Open != 1 || st.Unacked != 0 {
		t.Errorf("status = %+v", st)
	}
	x.at(1)
	if len(x.open()) != 1 {
		t.Error("reconocer no resuelve")
	}
	if _, err := x.eng.Ack(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("ack inexistente: %v", err)
	}
}

func TestRuleChangesResolveOpenIncidents(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true}, Rule{Name: "u2", Kind: Unhealthy, Enabled: true})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.at(0)
	rules := x.eng.Rules()
	r := rules[0]
	r.For = time.Minute
	if _, err := x.eng.UpdateRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := x.eng.DeleteRule(ctx, rules[1].ID); err != nil {
		t.Fatal(err)
	}
	var res []string
	for _, i := range x.store.incidents {
		res = append(res, i.Resolution)
	}
	slices.Sort(res)
	if len(x.open()) != 0 || !slices.Equal(res, []string{"regla eliminada", "regla modificada"}) {
		t.Fatalf("resoluciones = %v", res)
	}
	x.at(1) // the updated rule starts a new pending period
	if len(x.open()) != 0 || len(x.eng.Pending()) != 1 {
		t.Error("la regla modificada vuelve a contar su duración")
	}
	if err := x.eng.DeleteRule(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Error("borrar inexistente")
	}
	r.Enabled = false
	if _, err := x.eng.UpdateRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	x.at(100)
	if len(x.open())+len(x.eng.Pending()) != 0 {
		t.Error("una regla deshabilitada no evalúa")
	}
}

func TestLoadRestoresOpenIncidents(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.at(0)
	// New engine over the same store (agent restart).
	y := New(x.store, x.inv, x.met, Options{Now: func() time.Time { return x.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := y.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if st := y.Status(); st.Open != 1 {
		t.Fatalf("status tras cargar = %+v", st)
	}
	y.Tick(ctx, t0.Add(time.Second))
	if len(x.store.incidents) != 1 {
		t.Fatal("no se duplica el incidente tras reiniciar")
	}
	x.set(withHealth(running("w1", "web"), inventory.HealthHealthy))
	y.Tick(ctx, t0.Add(2*time.Second))
	if x.store.incidents[0].State != Resolved {
		t.Error("se resuelve tras reiniciar el agente")
	}
}

func TestStoreFailureRetries(t *testing.T) {
	x := newH(t, Rule{Name: "u", Kind: Unhealthy, Enabled: true})
	x.set(withHealth(running("w1", "web"), inventory.HealthUnhealthy))
	x.store.failNext = true
	x.at(0)
	if len(x.open()) != 0 {
		t.Fatal("fallo de escritura: no queda abierto en memoria sin persistir")
	}
	x.at(1)
	if len(x.open()) != 1 {
		t.Fatal("se reintenta en el siguiente tick")
	}
}

func TestRuleValidation(t *testing.T) {
	bad := []Rule{
		{Name: "", Kind: Unhealthy},
		{Name: "x", Kind: "disk"},
		{Name: "x", Kind: NotRunning}, // needs a scope
		{Name: "x", Kind: Memory, Threshold: 120},
		{Name: "x", Kind: Memory, Threshold: 80, Hysteresis: 80},
		{Name: "x", Kind: CPU, Threshold: 0},
		{Name: "x", Kind: Restarts, Threshold: 2.5, Window: time.Minute},
		{Name: "x", Kind: Restarts, Threshold: 3, Window: time.Second},
		{Name: "x", Kind: Unhealthy, For: -time.Second},
		{Name: "x", Kind: Unhealthy, Cooldown: 48 * time.Hour},
	}
	for _, r := range bad {
		if err := r.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("regla inválida aceptada: %+v", r)
		}
	}
	good := []Rule{
		{Name: "x", Kind: NotRunning, Scope: Scope{Container: "api"}},
		{Name: "x", Kind: CPU, Threshold: 250, Hysteresis: 20},
		{Name: "x", Kind: Restarts, Threshold: 3, Window: 5 * time.Minute},
	}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("regla válida rechazada: %v", err)
		}
	}
}

func TestFormatting(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "30s", 5 * time.Minute: "5m", 90 * time.Minute: "1h30m", 0: "0s"} {
		if got := dur(d); got != want {
			t.Errorf("dur(%s) = %q, want %q", d, got, want)
		}
	}
	for code, want := range map[int]string{0: "código 0", 137: "SIGKILL", 143: "SIGTERM", 139: "señal 11", 127: "no encontrado", 1: "posible error de la aplicación"} {
		if got := exitMeaning(code, false); !strings.Contains(got, want) {
			t.Errorf("exitMeaning(%d) = %q", code, got)
		}
	}
	if !strings.Contains(exitMeaning(137, true), "OOM") {
		t.Error("OOMKilled")
	}
}
