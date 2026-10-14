package demo

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/franbenaglia/docker-scope/internal/alerts"
	"github.com/franbenaglia/docker-scope/internal/docker"
)

func TestScenarioPlaysAndRepeats(t *testing.T) {
	d := New(time.Now())
	d.scale = 0.0002 // one 4-minute cycle ≈ 48ms
	ctx, cancel := context.WithCancel(context.Background())
	msgs, _ := d.Events(ctx, time.Time{})
	done := make(chan struct{})
	go func() { _ = d.Run(ctx); close(done) }()

	seen := map[string]int{}
	deadline := time.After(2 * time.Second)
	for seen["start"] < 10 || seen["health_status: unhealthy"] < 2 {
		select {
		case m := <-msgs:
			seen[string(m.Action)]++
		case <-deadline:
			t.Fatalf("eventos tras 2s: %v", seen)
		}
	}
	cancel()
	<-done
	if seen["die"] < 5 || seen["health_status: healthy"] < 1 {
		t.Errorf("eventos = %v", seen)
	}
}

func TestSpikeAndRunningState(t *testing.T) {
	d := New(time.Now())
	api := d.ids["demo-api-1"]
	d.setSpike("demo-api-1", true)
	st, err := d.ContainerStats(context.Background(), api)
	if err != nil {
		t.Fatal(err)
	}
	if pct := float64(st.MemoryStats.Usage-8<<20) / float64(st.MemoryStats.Limit); pct < 0.85 {
		t.Errorf("pico de memoria = %.2f del límite", pct)
	}
	w := d.ids["demo-worker-1"]
	if !d.running(w) {
		t.Fatal("worker en ejecución al inicio")
	}
	d.Die(w, 137, time.Now())
	if d.running(w) {
		t.Error("worker detenido tras die")
	}
	// A stopped container's logs end after the tail (no follow).
	rc, err := d.ContainerLogs(context.Background(), w, docker.LogsOptions{Tail: 2, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	if !strings.Contains(string(b), "Z ") {
		t.Errorf("logs = %q", b)
	}
}

type fakeRules struct{ rules []alerts.Rule }

func (f *fakeRules) Rules() []alerts.Rule { return f.rules }
func (f *fakeRules) CreateRule(_ context.Context, r alerts.Rule) (alerts.Rule, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	f.rules = append(f.rules, r)
	return r, nil
}

func TestEnsureRulesIsIdempotent(t *testing.T) {
	m := &fakeRules{}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	EnsureRules(context.Background(), m, quiet)
	EnsureRules(context.Background(), m, quiet)
	if len(m.rules) != 1 || m.rules[0].Kind != alerts.NotRunning {
		t.Errorf("reglas = %+v", m.rules)
	}
}
