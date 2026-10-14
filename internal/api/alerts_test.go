package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/alerts"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/store"
)

type alertsFixture struct {
	srv *Server
	eng *alerts.Engine
}

func newAlertsFixture(t *testing.T) alertsFixture {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(context.Background(), t.TempDir(), "a.db", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	snap := inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now, Containers: []*inventory.Container{
		{ID: "w1", Name: "web", State: container.StateRunning, Health: inventory.HealthUnhealthy, StartedAt: now},
	}}
	s := newServerMetrics(snap, fakeMetrics{}, nil)
	eng := alerts.New(st, fakeInv{snap}, fakeMetrics{}, alerts.Options{Now: func() time.Time { return now }, Log: quiet})
	if err := eng.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Alerts = eng
	return alertsFixture{srv: s, eng: eng}
}

func do(t *testing.T, s *Server, method, path, body string, out any) int {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v (%s)", method, path, err, rec.Body.String())
		}
	}
	if rec.Code >= 400 && !strings.Contains(rec.Body.String(), `"code"`) {
		t.Errorf("%s %s: error sin formato uniforme: %s", method, path, rec.Body.String())
	}
	return rec.Code
}

func TestRulesCRUD(t *testing.T) {
	f := newAlertsFixture(t)
	var list struct{ Items []RuleDTO }
	do(t, f.srv, "GET", "/api/rules", "", &list)
	if len(list.Items) != 4 {
		t.Fatalf("reglas sembradas = %d", len(list.Items))
	}
	for _, r := range list.Items {
		if r.Kind == "cpu" && !strings.Contains(r.ThresholdUnit, "núcleo") {
			t.Errorf("unidad de cpu = %q", r.ThresholdUnit)
		}
	}
	cases := map[string]int{
		`{"name":"x","kind":"not_running","enabled":true}`: http.StatusBadRequest, // no scope
		`{"name":"x","kind":"memory","threshold":150}`:     http.StatusBadRequest,
		`{"name":"x","kind":"unhealthy","extra":1}`:        http.StatusBadRequest, // unknown field
		`{"name":"x","kind":"unhealthy"} {"again":1}`:      http.StatusBadRequest,
		`no es json`: http.StatusBadRequest,
		`{"name":"x","kind":"unhealthy","name2":` + strings.Repeat("1", 70000) + `}`: http.StatusRequestEntityTooLarge,
	}
	for body, want := range cases {
		if got := do(t, f.srv, "POST", "/api/rules", body, nil); got != want {
			t.Errorf("POST %.60s: %d, want %d", body, got, want)
		}
	}
	var created RuleDTO
	body := `{"name":"api caída","kind":"not_running","enabled":true,"scope":{"container":"api"},"for_seconds":10,"cooldown_seconds":60}`
	if got := do(t, f.srv, "POST", "/api/rules", body, &created); got != http.StatusCreated || created.ID == 0 || created.ForSeconds != 10 {
		t.Fatalf("crear: %d %+v", got, created)
	}
	var updated RuleDTO
	body = strings.Replace(body, `"for_seconds":10`, `"for_seconds":20`, 1)
	if got := do(t, f.srv, "PUT", "/api/rules/"+itoa(created.ID), body, &updated); got != http.StatusOK || updated.ForSeconds != 20 {
		t.Errorf("actualizar: %d %+v", got, updated)
	}
	if got := do(t, f.srv, "PUT", "/api/rules/9999", body, nil); got != http.StatusNotFound {
		t.Errorf("actualizar inexistente: %d", got)
	}
	if got := do(t, f.srv, "DELETE", "/api/rules/"+itoa(created.ID), "", nil); got != http.StatusNoContent {
		t.Errorf("borrar: %d", got)
	}
	if got := do(t, f.srv, "DELETE", "/api/rules/"+itoa(created.ID), "", nil); got != http.StatusNotFound {
		t.Errorf("borrar dos veces: %d", got)
	}
	if got := do(t, f.srv, "DELETE", "/api/rules/abc", "", nil); got != http.StatusBadRequest {
		t.Errorf("id inválido: %d", got)
	}
}

func TestIncidentsAckAndMaintenance(t *testing.T) {
	f := newAlertsFixture(t)
	// Seeded "Unhealthy persistente" (30s): pending first, then open.
	f.eng.Tick(context.Background(), now)
	var pending struct{ Items []PendingDTO }
	do(t, f.srv, "GET", "/api/incidents/pending", "", &pending)
	if len(pending.Items) != 1 || pending.Items[0].ContainerName != "web" {
		t.Fatalf("pendientes = %+v", pending.Items)
	}
	f.eng.Tick(context.Background(), now.Add(30*time.Second))

	var page struct {
		Items []IncidentDTO
		Total int
	}
	do(t, f.srv, "GET", "/api/incidents?state=open", "", &page)
	if page.Total != 1 || page.Items[0].State != "open" || page.Items[0].AckedAt != nil {
		t.Fatalf("incidentes = %+v", page)
	}
	var acked IncidentDTO
	if got := do(t, f.srv, "POST", "/api/incidents/"+itoa(page.Items[0].ID)+"/ack", "", &acked); got != http.StatusOK ||
		acked.AckedAt == nil || acked.State != "open" {
		t.Errorf("ack: %d %+v", got, acked)
	}
	if got := do(t, f.srv, "POST", "/api/incidents/999/ack", "", nil); got != http.StatusNotFound {
		t.Errorf("ack inexistente: %d", got)
	}
	for q, want := range map[string]int{"?state=x": 400, "?limit=0": 400, "?limit=201": 400, "?offset=-1": 400, "?container=..": 400} {
		if got := do(t, f.srv, "GET", "/api/incidents"+q, "", nil); got != want {
			t.Errorf("incidents%s: %d", q, got)
		}
	}

	var m MaintenanceDTO
	if got := do(t, f.srv, "POST", "/api/maintenance", `{"duration_seconds":600,"reason":"deploy"}`, &m); got != http.StatusCreated || !m.Global {
		t.Fatalf("mantenimiento: %d %+v", got, m)
	}
	for _, body := range []string{`{}`, `{"duration_seconds":60,"until":"2030-01-01T00:00:00Z"}`, `{"until":"2000-01-01T00:00:00Z"}`, `{"duration_seconds":99999999}`} {
		if got := do(t, f.srv, "POST", "/api/maintenance", body, nil); got != http.StatusBadRequest {
			t.Errorf("mantenimiento %s: %d", body, got)
		}
	}
	var sum SummaryDTO
	do(t, f.srv, "GET", "/api/summary", "", &sum)
	if sum.Alerts == nil || sum.Alerts.Open != 1 || sum.Alerts.Unacked != 0 || len(sum.Alerts.Maintenance) != 1 {
		t.Errorf("summary.alerts = %+v", sum.Alerts)
	}
	if got := do(t, f.srv, "DELETE", "/api/maintenance/"+itoa(m.ID), "", nil); got != http.StatusNoContent {
		t.Errorf("fin de mantenimiento: %d", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
