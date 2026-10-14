package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/docker"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

type fakeInv struct{ snap inventory.Snapshot }

func (f fakeInv) Snapshot() inventory.Snapshot { return f.snap }

type fakeMetrics map[string]collector.Sample

func (f fakeMetrics) LatestAll() map[string]collector.Sample { return f }
func (f fakeMetrics) Interval() time.Duration                { return 5 * time.Second }

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newServer(snap inventory.Snapshot, web fstest.MapFS) *Server {
	return newServerMetrics(snap, nil, web)
}

func newServerMetrics(snap inventory.Snapshot, m fakeMetrics, web fstest.MapFS) *Server {
	return &Server{
		Inventory: fakeInv{snap}, Metrics: m, Mode: "docker", Web: web,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now },
	}
}

func get(t *testing.T, s *Server, path string, out any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}

func ctr(id string, state container.ContainerState, health string) *inventory.Container {
	return &inventory.Container{ID: id, Name: id, State: state, Health: health}
}

func connected() inventory.ConnState {
	return inventory.ConnState{Status: inventory.ConnConnected, APIVersion: "1.52", Since: now}
}

func TestSummaryConnected(t *testing.T) {
	s := newServer(inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now, Containers: []*inventory.Container{
		ctr("a", container.StateRunning, inventory.HealthUnhealthy),
		ctr("b", container.StateRunning, inventory.HealthNone),
		ctr("c", container.StateExited, inventory.HealthUnhealthy), // last known, not current
		ctr("d", container.StateCreated, inventory.HealthNone),
		ctr("e", container.StatePaused, inventory.HealthNone),
	}}, nil)
	var out SummaryDTO
	get(t, s, "/api/summary", &out)
	if out.Daemon.Status != "connected" || out.Containers == nil || out.Stale {
		t.Fatalf("esperaba conectado: %+v", out)
	}
	want := CountsDTO{Total: 5, Running: 2, Paused: 1, Stopped: 2, Unhealthy: 1}
	if *out.Containers != want {
		t.Errorf("conteos = %+v, want %+v", *out.Containers, want)
	}
}

func TestSummaryNeverSyncedIsNull(t *testing.T) {
	s := newServer(inventory.Snapshot{
		Conn: inventory.ConnState{Status: inventory.ConnDisconnected, Since: now, LastError: "dial unix"},
	}, nil)
	var raw map[string]any
	get(t, s, "/api/summary", &raw)
	if raw["containers"] != nil {
		t.Errorf("sin sincronizar, containers debe ser null (desconocido), no %v", raw["containers"])
	}
	if d := raw["daemon"].(map[string]any); d["status"] != "disconnected" || d["last_error"] != "dial unix" {
		t.Errorf("daemon = %v", d)
	}
}

func TestSummaryStaleKeepsLastKnown(t *testing.T) {
	asOf := now.Add(-time.Minute)
	s := newServer(inventory.Snapshot{
		Conn:   inventory.ConnState{Status: inventory.ConnDisconnected, Since: asOf},
		Synced: true, Stale: true, AsOf: asOf,
		Containers: []*inventory.Container{ctr("a", container.StateRunning, inventory.HealthNone)},
	}, nil)
	var out SummaryDTO
	get(t, s, "/api/summary", &out)
	if !out.Stale || !out.AsOf.Equal(asOf) || out.Containers == nil || out.Containers.Running != 1 {
		t.Errorf("datos desactualizados mal marcados: %+v", out)
	}
}

func TestContainersList(t *testing.T) {
	started := now.Add(-90 * time.Second)
	code := 137
	s := newServer(inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now, Containers: []*inventory.Container{
		{ID: "0123456789abcdef", Name: "web", State: container.StateRunning, Health: inventory.HealthHealthy,
			StartedAt: started, RestartCount: 1, ObservedRestarts: 3, ComposeProject: "p", ComposeService: "s"},
		{ID: "fedcba9876543210", Name: "old", State: container.StateExited, Health: inventory.HealthUnhealthy,
			ExitCode: &code},
	}}, nil)
	var out ContainerListDTO
	get(t, s, "/api/containers", &out)
	if len(out.Items) != 2 || !out.Synced {
		t.Fatalf("items = %d", len(out.Items))
	}
	old, web := out.Items[0], out.Items[1] // sorted by name
	if web.ShortID != "0123456789ab" || web.UptimeSeconds == nil || *web.UptimeSeconds != 90 || !web.HealthCurrent {
		t.Errorf("web: %+v", web)
	}
	if web.RestartCount != 1 || web.ObservedRestarts != 3 || web.ComposeProject != "p" {
		t.Errorf("web reinicios/compose: %+v", web)
	}
	if old.UptimeSeconds != nil || old.HealthCurrent || old.ExitCode == nil || *old.ExitCode != 137 || old.StartedAt != nil {
		t.Errorf("old: %+v", old)
	}
}

func TestUnknownAPIEndpoint(t *testing.T) {
	s := newServer(inventory.Snapshot{}, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d", rec.Code)
	}
	var e ErrorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error.Code != "not_found" {
		t.Errorf("cuerpo de error inesperado: %s", rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	web := fstest.MapFS{
		"index.html":  {Data: []byte("<html>app</html>")},
		"assets/a.js": {Data: []byte("js")},
	}
	s := newServer(inventory.Snapshot{}, web)
	for path, want := range map[string]string{
		"/":               "<html>app</html>",
		"/containers/xyz": "<html>app</html>",
		"/assets/a.js":    "js",
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSPANotBuilt(t *testing.T) {
	s := newServer(inventory.Snapshot{}, fstest.MapFS{".gitkeep": {}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d", rec.Code)
	}
}

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

func TestMetricsAndResources(t *testing.T) {
	snap := inventory.Snapshot{
		Conn: connected(), Synced: true, AsOf: now,
		Host: docker.HostInfo{NCPU: 8, MemTotal: 16 << 30},
		Containers: []*inventory.Container{
			{ID: "a", Name: "a", State: container.StateRunning, MemoryLimit: 64 << 20, CPULimit: 0.5},
			{ID: "b", Name: "b", State: container.StateRunning},
			{ID: "c", Name: "c", State: container.StateRunning}, // no sample yet
			{ID: "d", Name: "d", State: container.StateExited},  // stale sample must be ignored
		},
	}
	m := fakeMetrics{
		"a": {ContainerID: "a", At: now, CPUPercent: f64(150), MemUsage: u64(32 << 20), MemLimit: u64(64 << 20), MemLimitExplicit: true, MemPercent: f64(50)},
		"b": {ContainerID: "b", At: now, MemUsage: u64(10 << 20)}, // first sample: no CPU yet
		"d": {ContainerID: "d", At: now, CPUPercent: f64(999), MemUsage: u64(1 << 30)},
	}
	s := newServerMetrics(snap, m, nil)

	var sum SummaryDTO
	get(t, s, "/api/summary", &sum)
	r := sum.Resources
	if r == nil || r.HostCPUs != 8 || r.HostMemBytes != 16<<30 || r.Running != 3 {
		t.Fatalf("resources = %+v", r)
	}
	if r.CPUCoresUsed == nil || *r.CPUCoresUsed != 1.5 || r.CPUSampled != 1 {
		t.Errorf("cpu: %v núcleos con %d muestras", r.CPUCoresUsed, r.CPUSampled)
	}
	if r.MemUsedBytes == nil || *r.MemUsedBytes != 42<<20 || r.MemSampled != 2 {
		t.Errorf("mem: %v con %d muestras", r.MemUsedBytes, r.MemSampled)
	}

	var raw struct {
		Items []map[string]any `json:"items"`
	}
	get(t, s, "/api/containers", &raw)
	byID := map[string]map[string]any{}
	for _, it := range raw.Items {
		byID[it["id"].(string)] = it
	}
	a := byID["a"]
	if a["mem_limit_bytes"].(float64) != 64<<20 || a["cpu_limit_cores"].(float64) != 0.5 {
		t.Errorf("límites a: %v %v", a["mem_limit_bytes"], a["cpu_limit_cores"])
	}
	am := a["metrics"].(map[string]any)
	if am["cpu_percent"].(float64) != 150 || am["mem_limit_explicit"] != true {
		t.Errorf("métricas a: %v", am)
	}
	if bm := byID["b"]["metrics"].(map[string]any); bm["cpu_percent"] != nil {
		t.Errorf("cpu no disponible debe ser null, no %v", bm["cpu_percent"])
	}
	if byID["c"]["metrics"] != nil || byID["d"]["metrics"] != nil || byID["b"]["cpu_limit_cores"] != nil {
		t.Error("sin muestra / detenido / sin límite → null")
	}
}
