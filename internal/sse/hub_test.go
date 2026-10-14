package sse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readEvents reads SSE events (name → data) from a live response.
func readEvents(t *testing.T, sc *bufio.Scanner, n int) []string {
	t.Helper()
	var out []string
	var name string
	for len(out) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, name+" "+strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func startServer(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Serve(w, r, func() (string, []byte) { return "hello", []byte(`{"v":1}`) })
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connect(t *testing.T, ctx context.Context, url string) (*http.Response, *bufio.Scanner) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewScanner(resp.Body)
}

func waitClients(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.Clients() != n {
		if time.Now().After(deadline) {
			t.Fatalf("clientes = %d, want %d", h.Clients(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStreamDeliversTypedEvents(t *testing.T) {
	h := NewHub(Options{Heartbeat: 50 * time.Millisecond})
	srv := startServer(t, h)
	resp, sc := connect(t, context.Background(), srv.URL)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}
	waitClients(t, h, 1)
	h.Publish("summary", []byte(`{"a":1}`))
	got := readEvents(t, sc, 3)
	if got[0] != `hello {"v":1}` || got[1] != `summary {"a":1}` || !strings.HasPrefix(got[2], "heartbeat ") {
		t.Errorf("eventos = %q", got)
	}
}

func TestMultilineDataIsFramed(t *testing.T) {
	h := NewHub(Options{})
	f := string(h.frame("x", []byte("a\nb")))
	if !strings.Contains(f, "data: a\ndata: b\n\n") || !strings.Contains(f, "event: x\n") {
		t.Errorf("frame = %q", f)
	}
}

func TestSlowClientGetsResync(t *testing.T) {
	h := NewHub(Options{QueueSize: 4})
	c, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 { // nobody reads: the queue overflows
		h.Publish("metrics", []byte{byte('0' + i)})
	}
	if h.Resyncs() == 0 {
		t.Fatal("se esperaba al menos un resync")
	}
	var frames []string
	for len(c.ch) > 0 {
		frames = append(frames, string(<-c.ch))
	}
	if len(frames) > 4 {
		t.Errorf("la cola no debe superar su tamaño: %d", len(frames))
	}
	var sawResync bool
	for _, f := range frames {
		if strings.Contains(f, "event: resync") {
			sawResync = true
		}
	}
	if !sawResync {
		t.Errorf("tras el desborde la cola debe contener resync: %q", frames)
	}
}

func TestOtherClientsUnaffectedBySlowOne(t *testing.T) {
	h := NewHub(Options{QueueSize: 2})
	slow, _ := h.Subscribe()
	_ = slow
	srv := startServer(t, h)
	_, sc := connect(t, context.Background(), srv.URL)
	waitClients(t, h, 2)
	readEvents(t, sc, 1) // hello
	for i := range 5 {
		h.Publish("e", []byte{byte('0' + i)})
		if got := readEvents(t, sc, 1); got[0] != "e "+string(rune('0'+i)) {
			t.Fatalf("cliente rápido: %q", got)
		}
	}
}

func TestMaxClientsAndDisconnect(t *testing.T) {
	h := NewHub(Options{MaxClients: 1})
	srv := startServer(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	connect(t, ctx, srv.URL)
	waitClients(t, h, 1)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("cliente extra: %d", resp.StatusCode)
	}
	cancel() // client goes away: the handler must unsubscribe
	waitClients(t, h, 0)
}

func TestCloseEndsStreams(t *testing.T) {
	h := NewHub(Options{})
	srv := startServer(t, h)
	_, sc := connect(t, context.Background(), srv.URL)
	waitClients(t, h, 1)
	readEvents(t, sc, 1)
	h.Close()
	waitClients(t, h, 0)
	for sc.Scan() { // body ends
	}
	if _, err := h.Subscribe(); err == nil {
		t.Error("un hub cerrado no acepta clientes")
	}
	h.Publish("x", nil) // no panic
}
