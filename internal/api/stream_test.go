package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
	"github.com/franbenaglia/docker-scope/internal/sse"
)

type fakeSources struct {
	change func(inventory.Change)
	conn   func(inventory.ConnState)
	round  func(collector.Round)
}

func (f *fakeSources) OnChange(fn func(inventory.Change))        { f.change = fn }
func (f *fakeSources) OnConnection(fn func(inventory.ConnState)) { f.conn = fn }
func (f *fakeSources) OnRound(fn func(collector.Round))          { f.round = fn }

type event struct {
	name string
	data map[string]any
}

func nextEvent(t *testing.T, sc *bufio.Scanner) event {
	t.Helper()
	var ev event
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data); err != nil {
				t.Fatalf("data no es JSON: %q", line)
			}
		case line == "" && ev.name != "":
			return ev
		}
	}
	t.Fatal("stream terminado")
	return ev
}

// expect reads events until one named name arrives (skipping others).
func expect(t *testing.T, sc *bufio.Scanner, name string) event {
	t.Helper()
	for range 20 {
		if ev := nextEvent(t, sc); ev.name == name {
			return ev
		}
	}
	t.Fatalf("no llegó el evento %s", name)
	return event{}
}

func TestStreamPublishesTypedEvents(t *testing.T) {
	c := &inventory.Container{ID: "abc", Name: "web", State: container.StateRunning, StartedAt: now}
	s := newServerMetrics(inventory.Snapshot{Conn: connected(), Synced: true, AsOf: now,
		Containers: []*inventory.Container{c}}, fakeMetrics{}, nil)
	s.Hub = sse.NewHub(sse.Options{})
	src := &fakeSources{}
	s.Attach(src, src)

	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(s.Hub.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)

	hello := expect(t, sc, EvHello)
	if hello.data["interval_seconds"] != 5.0 || hello.data["mode"] != "docker" {
		t.Errorf("hello = %v", hello.data)
	}

	src.change(inventory.Change{Kind: inventory.Updated, ID: "abc", Cur: c})
	if ev := expect(t, sc, EvContainer); ev.data["id"] != "abc" || ev.data["name"] != "web" {
		t.Errorf("container = %v", ev.data)
	}
	// A burst of changes yields a debounced summary.
	if ev := expect(t, sc, EvSummary); ev.data["containers"] == nil {
		t.Errorf("summary = %v", ev.data)
	}

	src.round(collector.Round{At: now, Samples: []collector.Sample{{ContainerID: "abc", At: now, CPUPercent: f64(12)}}})
	m := expect(t, sc, EvMetrics)
	items := m.data["items"].(map[string]any)
	if items["abc"].(map[string]any)["cpu_percent"] != 12.0 {
		t.Errorf("metrics = %v", m.data)
	}

	src.change(inventory.Change{Kind: inventory.Removed, ID: "abc", Prev: c})
	if ev := expect(t, sc, EvContainerRemoved); ev.data["id"] != "abc" {
		t.Errorf("removed = %v", ev.data)
	}

	src.conn(inventory.ConnState{Status: inventory.ConnDisconnected, Since: now, LastError: "x"})
	if ev := expect(t, sc, EvDaemon); ev.data["status"] != "disconnected" {
		t.Errorf("daemon = %v", ev.data)
	}
}
