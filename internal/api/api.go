// Package api exposes the HTTP JSON API and serves the embedded panel.
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/sse"
	"github.com/franbenaglia/docker-scope/internal/store"
)

// Inventory is the read side of inventory.Inventory.
type Inventory interface {
	Snapshot() inventory.Snapshot
}

// Server holds the HTTP handlers' dependencies.
type Server struct {
	Inventory Inventory
	Metrics   Metrics
	History   History
	Retention store.Retention
	Hub       *sse.Hub
	Alerts    Alerts
	// HeartbeatSeconds is announced in the hello event (clients use it to
	// detect a dead stream).
	HeartbeatSeconds float64
	Mode             string // "docker" or "demo"
	Web              fs.FS  // built frontend (index.html at the root)
	Log              *slog.Logger
	Now              func() time.Time
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/containers", s.handleContainers)
	mux.HandleFunc("GET /api/containers/{id}", s.handleContainer)
	mux.HandleFunc("GET /api/containers/{id}/series", s.handleSeries)
	if s.Alerts != nil {
		s.routeAlerts(mux)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint inexistente")
	})
	mux.Handle("/", spaHandler(s.Web))
	return mux
}

// DaemonDTO describes the Docker daemon connection.
type DaemonDTO struct {
	Status     string    `json:"status"` // "connecting" | "connected" | "disconnected"
	APIVersion string    `json:"api_version,omitempty"`
	Since      time.Time `json:"since"`
	LastError  string    `json:"last_error,omitempty"`
	Attempts   int       `json:"attempts,omitempty"` // consecutive failed reconnection attempts
}

// DataStateDTO qualifies how current container data is. Embedded in responses.
type DataStateDTO struct {
	// Synced is false until the first full listing: no data means unknown, not "none".
	Synced bool `json:"synced"`
	// Stale is true when Docker is unreachable and data is the last known state.
	Stale bool      `json:"stale"`
	AsOf  time.Time `json:"as_of"`
}

// SummaryDTO is the response of GET /api/summary.
type SummaryDTO struct {
	Mode   string    `json:"mode"`
	Daemon DaemonDTO `json:"daemon"`
	DataStateDTO
	Containers  *CountsDTO        `json:"containers"` // null until the first sync
	Resources   *ResourcesDTO     `json:"resources"`  // null until the first sync
	Alerts      *AlertsSummaryDTO `json:"alerts"`
	GeneratedAt time.Time         `json:"generated_at"`
}

// CountsDTO counts containers by state.
type CountsDTO struct {
	Total      int `json:"total"`
	Running    int `json:"running"`
	Paused     int `json:"paused"`
	Restarting int `json:"restarting"`
	Stopped    int `json:"stopped"`   // created, exited, dead, removing
	Unhealthy  int `json:"unhealthy"` // running containers only
}

// ContainerDTO is one container in GET /api/containers.
type ContainerDTO struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	State   string `json:"state"`
	// Health: "none" (no HEALTHCHECK), "starting", "healthy", "unhealthy".
	Health string `json:"health"`
	// HealthCurrent is false when the container is not running: Health is then
	// the last status Docker kept.
	HealthCurrent bool       `json:"health_current"`
	CreatedAt     *time.Time `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	UptimeSeconds *int64     `json:"uptime_seconds"` // running only
	ExitCode      *int       `json:"exit_code"`      // exited/dead only
	OOMKilled     bool       `json:"oom_killed"`

	RestartPolicy       string     `json:"restart_policy"`
	RestartCount        int        `json:"restart_count"`     // reported by Docker
	ObservedRestarts    int        `json:"observed_restarts"` // observed by the agent
	LastObservedRestart *time.Time `json:"last_observed_restart"`

	ComposeProject string `json:"compose_project,omitempty"`
	ComposeService string `json:"compose_service,omitempty"`

	// Configured limits; null when none.
	MemLimitBytes *int64   `json:"mem_limit_bytes"`
	CPULimitCores *float64 `json:"cpu_limit_cores"`

	// Metrics is the latest sample; null if not available (not running, first
	// round pending, Docker unreachable before any sample).
	Metrics *MetricsDTO `json:"metrics"`
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.summary())
}

func (s *Server) summary() SummaryDTO {
	snap := s.Inventory.Snapshot()
	out := SummaryDTO{
		Mode:         s.Mode,
		Daemon:       daemonDTO(snap.Conn),
		DataStateDTO: dataState(snap),
		GeneratedAt:  s.Now().UTC(),
	}
	if snap.Synced {
		c := countContainers(snap.Containers)
		out.Containers = &c
		r := resources(snap, s.Metrics.LatestAll(), s.Metrics.Interval())
		out.Resources = &r
	}
	out.Alerts = s.alertsSummary()
	return out
}

// containerWithMetrics builds the DTO of c with its latest sample, if any.
func (s *Server) containerWithMetrics(c *inventory.Container, latest map[string]collector.Sample, now time.Time) ContainerDTO {
	d := containerDTO(c, now)
	if m, ok := latest[c.ID]; ok && c.Running() {
		d.Metrics = metricsDTO(m)
	}
	return d
}

func daemonDTO(c inventory.ConnState) DaemonDTO {
	return DaemonDTO{
		Status: string(c.Status), APIVersion: c.APIVersion, Since: c.Since.UTC(),
		LastError: c.LastError, Attempts: c.Attempts,
	}
}

func dataState(s inventory.Snapshot) DataStateDTO {
	return DataStateDTO{Synced: s.Synced, Stale: s.Stale, AsOf: s.AsOf.UTC()}
}

func containerDTO(c *inventory.Container, now time.Time) ContainerDTO {
	d := ContainerDTO{
		ID: c.ID, ShortID: c.ShortID(), Name: c.Name, Image: c.Image, State: string(c.State),
		Health: c.Health, HealthCurrent: c.HealthCurrent() != "",
		CreatedAt: timePtr(c.CreatedAt), StartedAt: timePtr(c.StartedAt), FinishedAt: timePtr(c.FinishedAt),
		ExitCode: c.ExitCode, OOMKilled: c.OOMKilled,
		RestartPolicy: c.RestartPolicy, RestartCount: c.RestartCount,
		ObservedRestarts: c.ObservedRestarts, LastObservedRestart: timePtr(c.LastObservedRestart),
		ComposeProject: c.ComposeProject, ComposeService: c.ComposeService,
	}
	if c.Running() && !c.StartedAt.IsZero() {
		up := int64(c.Uptime(now) / time.Second)
		d.UptimeSeconds = &up
	}
	if c.MemoryLimit > 0 {
		d.MemLimitBytes = &c.MemoryLimit
	}
	if c.CPULimit > 0 {
		d.CPULimitCores = &c.CPULimit
	}
	return d
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func countContainers(list []*inventory.Container) CountsDTO {
	var c CountsDTO
	for _, ct := range list {
		c.Total++
		switch ct.State {
		case container.StateRunning:
			c.Running++
		case container.StatePaused:
			c.Paused++
		case container.StateRestarting:
			c.Restarting++
		default:
			c.Stopped++
		}
		if ct.HealthCurrent() == inventory.HealthUnhealthy {
			c.Unhealthy++
		}
	}
	return c
}

// ErrorDTO is the uniform error body.
type ErrorDTO struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e ErrorDTO
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
