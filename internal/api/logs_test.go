package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/docker"
	dt "github.com/franbenaglia/docker-scope/internal/docker/dockertest"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

type logSourceFunc func(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error)

func (f logSourceFunc) ContainerLogs(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
	return f(ctx, id, opts)
}

func logsServer(src logSourceFunc) *Server {
	s := newServerMetrics(inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now, Containers: []*inventory.Container{
		{ID: "mux1", Name: "api", State: container.StateRunning, LogDriver: "json-file"},
		{ID: "tty1", Name: "shell", State: container.StateRunning, TTY: true, LogDriver: "json-file"},
		{ID: "none1", Name: "silent", State: container.StateRunning, LogDriver: "none"},
	}}, fakeMetrics{}, nil)
	s.Logs = src
	return s
}

func staticLogs(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
	switch id {
	case "mux1":
		data := string(dt.Mux(1, "2026-01-01T00:00:00Z <b>hola</b>\n")) + string(dt.Mux(2, "2026-01-01T00:00:01Z \x1b[31mfallo\x1b[0m\n"))
		return io.NopCloser(strings.NewReader(data)), nil
	case "tty1":
		return io.NopCloser(strings.NewReader("2026-01-01T00:00:00Z prompt$ \r\n")), nil
	case "none1":
		return nil, docker.ErrLogsUnsupported
	}
	return nil, docker.ErrNotFound
}

func TestLogsTail(t *testing.T) {
	var gotTail int
	s := logsServer(func(ctx context.Context, id string, o docker.LogsOptions) (io.ReadCloser, error) {
		gotTail = o.Tail
		if o.Follow {
			t.Error("el tail REST no sigue el stream")
		}
		return staticLogs(ctx, id, o)
	})
	var out LogsDTO
	get(t, s, "/api/containers/mux1/logs", &out)
	if gotTail != 200 || len(out.Lines) != 2 || out.TTY {
		t.Fatalf("tail por defecto: %d %+v", gotTail, out)
	}
	if out.Lines[0].Text != "<b>hola</b>" || out.Lines[0].Stream != "stdout" || out.Lines[0].Time == nil {
		t.Errorf("línea 0 = %+v (el HTML se deja como texto: lo escapa la UI)", out.Lines[0])
	}
	if out.Lines[1].Text != "fallo" || out.Lines[1].Stream != "stderr" {
		t.Errorf("línea 1 = %+v", out.Lines[1])
	}
	get(t, s, "/api/containers/tty1/logs?tail=5", &out)
	if gotTail != 5 || !out.TTY || out.Lines[0].Stream != "tty" || out.Lines[0].Text != "prompt$ " {
		t.Errorf("tty: %+v", out)
	}
	cases := map[string]int{
		"/api/containers/mux1/logs?tail=-1":   http.StatusBadRequest,
		"/api/containers/mux1/logs?tail=5001": http.StatusBadRequest,
		"/api/containers/mux1/logs?tail=x":    http.StatusBadRequest,
		"/api/containers/nope/logs":           http.StatusNotFound,
		"/api/containers/none1/logs":          http.StatusConflict,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
		if want == http.StatusConflict && !strings.Contains(rec.Body.String(), "«none»") {
			t.Errorf("el mensaje debe nombrar el driver: %s", rec.Body.String())
		}
	}
	s502 := logsServer(func(context.Context, string, docker.LogsOptions) (io.ReadCloser, error) {
		return nil, errors.New("conexión rechazada")
	})
	rec := httptest.NewRecorder()
	s502.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/containers/mux1/logs", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("error de Docker: %d", rec.Code)
	}
}

func TestLogsTailIsSizeLimited(t *testing.T) {
	line := strings.Repeat("x", 8000) + "\n"
	s := logsServer(func(context.Context, string, docker.LogsOptions) (io.ReadCloser, error) {
		var b strings.Builder
		for range 400 { // ~3.2 MB
			b.Write(dt.Mux(1, line))
		}
		return io.NopCloser(strings.NewReader(b.String())), nil
	})
	var out LogsDTO
	get(t, s, "/api/containers/mux1/logs?tail=5000", &out)
	if !out.Limited || len(out.Lines) > maxLogResponse/8000 {
		t.Errorf("limited=%v líneas=%d", out.Limited, len(out.Lines))
	}
}

type sseReader struct{ sc *bufio.Scanner }

func (r sseReader) next(t *testing.T) (string, map[string]any) {
	t.Helper()
	var name string
	for r.sc.Scan() {
		line := r.sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var d map[string]any
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d)
			return name, d
		}
	}
	t.Fatal("stream terminado")
	return "", nil
}

func TestLogsStreamFollowAndEnd(t *testing.T) {
	pr, pw := io.Pipe()
	var opts docker.LogsOptions
	s := logsServer(func(ctx context.Context, id string, o docker.LogsOptions) (io.ReadCloser, error) {
		opts = o
		return pr, nil
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/containers/mux1/logs/stream?tail=50")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := sseReader{bufio.NewScanner(resp.Body)}
	if ev, d := r.next(t); ev != "hello" || d["container_name"] != "api" || !opts.Follow || opts.Tail != 50 {
		t.Fatalf("hello = %s %v (opts %+v)", ev, d, opts)
	}
	go pw.Write(dt.Mux(1, "uno\ndos\n"))
	ev, d := r.next(t)
	lines, _ := d["lines"].([]any)
	if ev != "lines" || len(lines) != 2 {
		t.Fatalf("lines = %s %v", ev, d)
	}
	pw.Close() // the container stopped: Docker closes the stream
	if ev, d := r.next(t); ev != "end" || d["reason"] != "eof" {
		t.Errorf("end = %s %v", ev, d)
	}
}

func TestLogsStreamCancelsOnDisconnectAndLimits(t *testing.T) {
	var cancelled atomic.Int32
	s := logsServer(func(ctx context.Context, id string, o docker.LogsOptions) (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			<-ctx.Done() // like the Docker client: cancelling closes the stream
			cancelled.Add(1)
			pw.CloseWithError(ctx.Err())
		}()
		return pr, nil
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var cancels []context.CancelFunc
	for range maxLogStreams {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/containers/mux1/logs/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		sseReader{bufio.NewScanner(resp.Body)}.next(t) // hello
	}
	resp, err := http.Get(srv.URL + "/api/containers/mux1/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("stream extra: %d", resp.StatusCode)
	}
	for _, c := range cancels {
		c()
	}
	deadline := time.Now().Add(2 * time.Second)
	for cancelled.Load() < maxLogStreams || s.logStreams.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("cancelados=%d abiertos=%d", cancelled.Load(), s.logStreams.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
