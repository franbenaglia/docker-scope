package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/inventory"
)

func listServer() *Server {
	mk := func(id, name string, st container.ContainerState, health, proj, svc string, startedAgo time.Duration) *inventory.Container {
		c := &inventory.Container{ID: id, Name: name, Image: name + ":1", State: st, Health: health,
			ComposeProject: proj, ComposeService: svc, Labels: map[string]string{"k": "v"}}
		if st == container.StateRunning {
			c.StartedAt = now.Add(-startedAgo)
		}
		return c
	}
	snap := inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now, Containers: []*inventory.Container{
		mk("aaa111", "api", container.StateRunning, "unhealthy", "shop", "api", time.Hour),
		mk("bbb222", "db", container.StateRunning, "healthy", "shop", "db", 3*time.Hour),
		mk("ccc333", "worker", container.StateRunning, "none", "", "", time.Minute),
		mk("ddd444", "migrate", container.StateExited, "unhealthy", "shop", "migrate", 0),
		mk("eee555", "cache", container.StatePaused, "none", "other", "cache", 0),
	}}
	m := fakeMetrics{
		"aaa111": {ContainerID: "aaa111", CPUPercent: f64(50), MemUsage: u64(300)},
		"bbb222": {ContainerID: "bbb222", CPUPercent: f64(150), MemUsage: u64(100)},
		// worker: no sample yet → sorts last
	}
	return newServerMetrics(snap, m, nil)
}

func names(out ContainerListDTO) string {
	var n []string
	for _, it := range out.Items {
		n = append(n, it.Name)
	}
	return strings.Join(n, ",")
}

func TestContainerFiltersSortPagination(t *testing.T) {
	s := listServer()
	cases := map[string]string{
		"":                                      "api,cache,db,migrate,worker",
		"?state=running":                        "api,db,worker",
		"?state=stopped,paused":                 "cache,migrate",
		"?health=unhealthy":                     "api", // migrate's unhealthy is not current
		"?health=none":                          "cache,worker",
		"?project=shop":                         "api,db,migrate",
		"?project=":                             "worker",
		"?project=shop&service=db":              "db",
		"?q=WORK":                               "worker",
		"?q=ddd4":                               "migrate", // ID prefix
		"?q=shop":                               "api,db,migrate",
		"?sort=cpu&order=desc":                  "db,api,cache,migrate,worker",
		"?sort=cpu":                             "api,db,cache,migrate,worker", // missing values always last
		"?sort=mem&order=desc":                  "api,db,cache,migrate,worker",
		"?sort=uptime&order=desc&state=running": "db,api,worker",
		"?limit=2":                              "api,cache",
		"?limit=2&offset=4":                     "worker",
		"?offset=10":                            "",
	}
	for q, want := range cases {
		var out ContainerListDTO
		get(t, s, "/api/containers"+q, &out)
		if got := names(out); got != want {
			t.Errorf("%q: %s, want %s", q, got, want)
		}
	}
	var out ContainerListDTO
	get(t, s, "/api/containers?limit=2&offset=1", &out)
	if out.Total != 5 || out.Limit != 2 || out.Offset != 1 || strings.Join(out.Projects, ",") != "other,shop" {
		t.Errorf("paginación/facetas: total=%d limit=%d offset=%d projects=%v", out.Total, out.Limit, out.Offset, out.Projects)
	}
}

func TestContainerListValidation(t *testing.T) {
	s := listServer()
	for _, q := range []string{"?state=dead", "?health=ok", "?sort=size", "?order=up", "?limit=0",
		"?limit=501", "?offset=-1", "?limit=x", "?q=" + strings.Repeat("a", 201)} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/containers"+q, nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_parameter") {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestContainerDetail(t *testing.T) {
	s := listServer()
	var d ContainerDetailDTO
	get(t, s, "/api/containers/aaa111", &d)
	if d.Name != "api" || d.Labels["k"] != "v" || d.Metrics == nil || !d.Synced {
		t.Errorf("detalle: %+v", d)
	}
	for path, want := range map[string]int{
		"/api/containers/aaa":   http.StatusNotFound, // prefixes are not identities
		"/api/containers/nope":  http.StatusNotFound,
		"/api/containers/a%2Fb": http.StatusBadRequest,
	} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

