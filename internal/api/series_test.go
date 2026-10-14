package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/store"
)

func seriesServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), "t.db", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.UpsertContainers(ctx, []store.ContainerMeta{{ID: "abc", Name: "abc", SeenAt: now}}); err != nil {
		t.Fatal(err)
	}
	var samples []collector.Sample
	for i := range 60 { // last 5 minutes
		samples = append(samples, collector.Sample{ContainerID: "abc", At: now.Add(-time.Duration(i) * 5 * time.Second), CPUPercent: f64(float64(i))})
	}
	if err := st.InsertSamples(ctx, samples); err != nil {
		t.Fatal(err)
	}
	s := newServerMetrics(inventory.Snapshot{}, fakeMetrics{}, nil)
	s.History = st
	s.Retention = store.Retention{Raw: 24 * time.Hour, Rollups: 7 * 24 * time.Hour}
	return s
}

func TestSeriesEndpoint(t *testing.T) {
	s := seriesServer(t)
	var out SeriesDTO
	get(t, s, "/api/containers/abc/series?range=10m&points=20", &out)
	if out.Source != "raw" || len(out.Points) == 0 || len(out.Points) > 20 || out.StepSeconds != 5 {
		t.Fatalf("serie: source=%s n=%d step=%v", out.Source, len(out.Points), out.StepSeconds)
	}
	var n int
	for _, p := range out.Points {
		n += p.N
	}
	if n != 60 {
		t.Errorf("muestras agregadas = %d, want 60", n)
	}

	// A range beyond retention is clamped, and served from rollups.
	get(t, s, "/api/containers/abc/series?range=30d", &out)
	if want := now.Add(-7 * 24 * time.Hour); !out.From.Equal(want) || out.Source != "1m" {
		t.Errorf("from = %s (want %s), source=%s", out.From, want, out.Source)
	}
	get(t, s, "/api/containers/abc/series?from=2025-12-31T23:30:00Z&to=2026-01-01T00:00:00Z", &out)
	if out.To != now {
		t.Errorf("to = %s", out.To)
	}
}

func TestSeriesValidationErrors(t *testing.T) {
	s := seriesServer(t)
	cases := map[string]int{
		"/api/containers/abc/series?points=5":                                          http.StatusBadRequest,
		"/api/containers/abc/series?points=x":                                          http.StatusBadRequest,
		"/api/containers/abc/series?points=5000":                                       http.StatusBadRequest,
		"/api/containers/abc/series?range=1h&from=2026-01-01T00:00:00Z":                http.StatusBadRequest,
		"/api/containers/abc/series?range=-1h":                                         http.StatusBadRequest,
		"/api/containers/abc/series?range=abc":                                         http.StatusBadRequest,
		"/api/containers/abc/series?range=30s":                                         http.StatusBadRequest,
		"/api/containers/abc/series?from=ayer":                                         http.StatusBadRequest,
		"/api/containers/abc/series?from=2020-01-01T00:00:00Z&to=2020-01-02T00:00:00Z": http.StatusBadRequest, // outside retention
		"/api/containers/..bad/series":                                                 http.StatusBadRequest,
		"/api/containers/zzz/series":                                                   http.StatusNotFound,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d (%s)", path, rec.Code, want, rec.Body.String())
		}
	}
}
