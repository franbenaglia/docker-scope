package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/fbenaglia/dockerscope/internal/docker"
)

type fakeDocker struct {
	list []container.Summary
	err  error
}

func (f fakeDocker) Ping(context.Context) (docker.PingInfo, error) {
	if f.err != nil {
		return docker.PingInfo{}, f.err
	}
	return docker.PingInfo{APIVersion: "1.52"}, nil
}

func (f fakeDocker) ListContainers(context.Context) ([]container.Summary, error) {
	return f.list, f.err
}

func newServer(d docker.API, web fstest.MapFS) *Server {
	return &Server{
		Docker: d, Mode: "docker", Web: web,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
}

func getSummary(t *testing.T, s *Server) SummaryDTO {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out SummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSummaryConnected(t *testing.T) {
	s := newServer(fakeDocker{list: []container.Summary{
		{ID: "a", State: container.StateRunning, Health: &container.HealthSummary{Status: container.Unhealthy}},
		{ID: "b", State: container.StateRunning},
		{ID: "c", State: container.StateExited},
		{ID: "d", State: container.StateCreated},
		{ID: "e", State: container.StatePaused},
	}}, nil)
	out := getSummary(t, s)
	if out.Daemon.Status != "connected" || out.Containers == nil {
		t.Fatalf("esperaba conectado: %+v", out)
	}
	want := CountsDTO{Total: 5, Running: 2, Paused: 1, Stopped: 2, Unhealthy: 1}
	if *out.Containers != want {
		t.Errorf("conteos = %+v, want %+v", *out.Containers, want)
	}
}

func TestSummaryDisconnectedIsNotZero(t *testing.T) {
	s := newServer(fakeDocker{err: errors.New("dial unix: no such file")}, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/summary", nil))
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["containers"] != nil {
		t.Errorf("con Docker desconectado containers debe ser null, no %v", raw["containers"])
	}
	if d := raw["daemon"].(map[string]any); d["status"] != "disconnected" {
		t.Errorf("status = %v", d["status"])
	}
}

func TestUnknownAPIEndpoint(t *testing.T) {
	s := newServer(fakeDocker{}, nil)
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
	s := newServer(fakeDocker{}, web)
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
	s := newServer(fakeDocker{}, fstest.MapFS{".gitkeep": {}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d", rec.Code)
	}
}
