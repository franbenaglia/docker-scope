package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/franbenaglia/docker-scope/internal/docker"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/logs"
	"github.com/franbenaglia/docker-scope/internal/sse"
)

// LogSource reads container logs from Docker.
type LogSource interface {
	ContainerLogs(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error)
}

// Log limits (SPEC "Logs": bounded by lines and bytes, capped line size).
const (
	defaultTail       = 200
	maxTail           = 5000
	maxLogResponse    = 2 << 20 // REST: stop collecting after 2 MiB of text
	maxLogStreams     = 8       // concurrent follow streams
	streamQueueLines  = 2000    // per stream, between Docker and a slow browser
	streamQueueBytes  = 1 << 20
	streamBatch       = 200
	streamFlushPeriod = 200 * time.Millisecond
	logHeartbeat      = 15 * time.Second
	logReadTimeout    = 15 * time.Second
)

// LogLineDTO is one sanitized log line (render as text, never as HTML).
type LogLineDTO struct {
	Time      *time.Time `json:"time"`
	Stream    string     `json:"stream"` // stdout | stderr | tty (merged) | system (daemon message)
	Text      string     `json:"text"`
	Truncated bool       `json:"truncated,omitempty"`
}

// LogsDTO is the response of GET /api/containers/{id}/logs.
type LogsDTO struct {
	ContainerID string       `json:"container_id"`
	TTY         bool         `json:"tty"` // stdout and stderr cannot be told apart
	LogDriver   string       `json:"log_driver"`
	Lines       []LogLineDTO `json:"lines"`
	// Limited is true when the response was cut at 2 MiB of text.
	Limited bool `json:"limited"`
}

// LogStreamHelloDTO opens a log stream.
type LogStreamHelloDTO struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	TTY           bool   `json:"tty"`
	LogDriver     string `json:"log_driver"`
	Tail          int    `json:"tail"`
}

func lineDTO(l logs.Line) LogLineDTO {
	d := LogLineDTO{Stream: string(l.Stream), Text: l.Text, Truncated: l.Truncated}
	if !l.Time.IsZero() {
		t := l.Time
		d.Time = &t
	}
	return d
}

// logTarget validates the request and finds the container.
func (s *Server) logTarget(w http.ResponseWriter, r *http.Request) (*inventory.Container, int, bool) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "id de contenedor inválido")
		return nil, 0, false
	}
	tail := defaultTail
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxTail {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "tail debe estar entre 0 y "+strconv.Itoa(maxTail))
			return nil, 0, false
		}
		tail = n
	}
	snap := s.Inventory.Snapshot()
	i := slices.IndexFunc(snap.Containers, func(c *inventory.Container) bool { return c.ID == id })
	if i < 0 {
		writeError(w, http.StatusNotFound, "not_found", "contenedor inexistente (se identifica por el ID completo)")
		return nil, 0, false
	}
	return snap.Containers[i], tail, true
}

func (s *Server) logsError(w http.ResponseWriter, c *inventory.Container, err error) {
	switch {
	case errors.Is(err, docker.ErrLogsUnsupported):
		writeError(w, http.StatusConflict, "logs_unavailable",
			"El logging driver «"+c.LogDriver+"» de este contenedor no permite leer logs desde Docker. "+
				"Se pueden leer con json-file, local o journald, o con la caché de dual logging de Docker activada.")
	case errors.Is(err, docker.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "el contenedor ya no existe")
	default:
		s.Log.Warn("no se pudieron leer logs", "id", c.ShortID(), "err", err)
		writeError(w, http.StatusBadGateway, "docker_error", "Docker no respondió al pedir los logs")
	}
}

// handleLogs serves GET /api/containers/{id}/logs?tail=200: the last lines, once.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	c, tail, ok := s.logTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), logReadTimeout)
	defer cancel()
	rc, err := s.Logs.ContainerLogs(ctx, c.ID, docker.LogsOptions{Tail: tail})
	if err != nil {
		s.logsError(w, c, err)
		return
	}
	defer rc.Close()
	out := LogsDTO{ContainerID: c.ID, TTY: c.TTY, LogDriver: c.LogDriver, Lines: []LogLineDTO{}}
	size := 0
	err = logs.Parse(rc, logs.Options{TTY: c.TTY, Timestamps: true}, func(l logs.Line) bool {
		size += len(l.Text)
		if size > maxLogResponse {
			out.Limited = true
			return false
		}
		out.Lines = append(out.Lines, lineDTO(l))
		return true
	})
	if err != nil && !errors.Is(err, logs.ErrStopped) {
		s.logsError(w, c, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLogsStream serves GET /api/containers/{id}/logs/stream?tail=200: SSE
// with the last lines and then new ones, separate from /api/stream.
// Events: hello, lines {lines: []}, dropped {count} (slow client), heartbeat,
// end {reason, message}. Closing the connection cancels the Docker request.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	c, tail, ok := s.logTarget(w, r)
	if !ok {
		return
	}
	if s.logStreams.Add(1) > maxLogStreams {
		s.logStreams.Add(-1)
		writeError(w, http.StatusServiceUnavailable, "too_many_streams",
			"demasiados streams de logs abiertos (máx. "+strconv.Itoa(maxLogStreams)+")")
		return
	}
	defer s.logStreams.Add(-1)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	rc, err := s.Logs.ContainerLogs(ctx, c.ID, docker.LogsOptions{Tail: tail, Follow: true})
	if err != nil {
		s.logsError(w, c, err)
		return
	}
	defer rc.Close()

	buf := logs.NewBuffer(streamQueueLines, streamQueueBytes, streamBatch)
	done := make(chan error, 1)
	go func() {
		done <- logs.Parse(rc, logs.Options{TTY: c.TTY, Timestamps: true}, func(l logs.Line) bool {
			buf.Push(l)
			return ctx.Err() == nil
		})
	}()

	st, err := sse.Start(w, 3*time.Second, 10*time.Second)
	if err != nil {
		return
	}
	send := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		return st.Send(event, b) == nil
	}
	flush := func() bool {
		for {
			lines, dropped := buf.Take(streamBatch)
			if dropped > 0 && !send("dropped", map[string]int{"count": dropped}) {
				return false
			}
			if len(lines) == 0 {
				return true
			}
			out := make([]LogLineDTO, len(lines))
			for i, l := range lines {
				out[i] = lineDTO(l)
			}
			if !send("lines", map[string]any{"lines": out}) {
				return false
			}
		}
	}
	if !send("hello", LogStreamHelloDTO{ContainerID: c.ID, ContainerName: c.Name, TTY: c.TTY, LogDriver: c.LogDriver, Tail: tail}) {
		return
	}
	tick := time.NewTicker(streamFlushPeriod)
	defer tick.Stop()
	hb := time.NewTicker(logHeartbeat)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-buf.Ready():
			if !flush() {
				return
			}
		case <-tick.C:
			if !flush() {
				return
			}
		case t := <-hb.C:
			if !send("heartbeat", map[string]string{"t": t.UTC().Format(time.RFC3339)}) {
				return
			}
		case err := <-done:
			if !flush() {
				return
			}
			end := map[string]string{"reason": "eof", "message": "Docker cerró el stream (el contenedor se detuvo o fue eliminado)"}
			if err != nil && !errors.Is(err, logs.ErrStopped) && ctx.Err() == nil {
				end = map[string]string{"reason": "error", "message": "error al leer logs: " + err.Error()}
			}
			send("end", end)
			return
		}
	}
}
