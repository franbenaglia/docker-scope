// Package api exposes the HTTP JSON API and serves the embedded panel.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/fbenaglia/dockerscope/internal/docker"
)

// Server holds the HTTP handlers' dependencies.
type Server struct {
	Docker docker.API
	Mode   string // "docker" or "demo"
	Web    fs.FS  // built frontend (index.html at the root)
	Log    *slog.Logger
	Now    func() time.Time
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint inexistente")
	})
	mux.Handle("/", spaHandler(s.Web))
	return mux
}

// SummaryDTO is the response of GET /api/summary.
type SummaryDTO struct {
	Mode        string     `json:"mode"`
	Daemon      DaemonDTO  `json:"daemon"`
	Containers  *CountsDTO `json:"containers"` // null when Docker is unreachable: unknown, not zero
	GeneratedAt time.Time  `json:"generated_at"`
}

// DaemonDTO describes the Docker daemon connection.
type DaemonDTO struct {
	Status     string `json:"status"` // "connected" | "disconnected"
	APIVersion string `json:"api_version,omitempty"`
	Error      string `json:"error,omitempty"`
}

// CountsDTO counts containers by state.
type CountsDTO struct {
	Total      int `json:"total"`
	Running    int `json:"running"`
	Paused     int `json:"paused"`
	Restarting int `json:"restarting"`
	Stopped    int `json:"stopped"` // created, exited, dead, removing
	Unhealthy  int `json:"unhealthy"`
}

// handleSummary queries Docker directly. Stage 2 replaces this with the shared
// in-memory inventory so browser requests do not hit Docker.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out := SummaryDTO{Mode: s.Mode, GeneratedAt: s.Now().UTC()}
	ping, err := s.Docker.Ping(ctx)
	if err == nil {
		var list []container.Summary
		list, err = s.Docker.ListContainers(ctx)
		if err == nil {
			out.Daemon = DaemonDTO{Status: "connected", APIVersion: ping.APIVersion}
			c := countContainers(list)
			out.Containers = &c
		}
	}
	if err != nil {
		s.Log.Warn("docker no disponible", "err", err)
		out.Daemon = DaemonDTO{Status: "disconnected", Error: "no se pudo contactar al daemon Docker"}
	}
	writeJSON(w, http.StatusOK, out)
}

func countContainers(list []container.Summary) CountsDTO {
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
		if ct.Health != nil && ct.Health.Status == container.Unhealthy {
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
