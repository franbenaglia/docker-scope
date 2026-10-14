package demo

import (
	"context"
	"log/slog"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/alerts"
)

// cycle is the scenario length; it repeats while the agent runs.
const cycle = 4 * time.Minute

type step struct {
	at time.Duration
	do func(d *Demo, now time.Time)
}

// scenario shows the alert lifecycle without Docker: an unhealthy service that
// recovers and degrades again, a crash loop, a memory spike and a stopped
// worker (see the table in ADR-017).
var scenario = []step{
	{20 * time.Second, func(d *Demo, _ time.Time) { d.SetHealth(d.ids["demo-api-1"], container.Healthy) }},
	{30 * time.Second, func(d *Demo, now time.Time) { d.AutoRestart(d.ids["demo-cache-1"], 1, now) }},
	{40 * time.Second, func(d *Demo, now time.Time) { d.AutoRestart(d.ids["demo-cache-1"], 1, now) }},
	{50 * time.Second, func(d *Demo, now time.Time) { d.AutoRestart(d.ids["demo-cache-1"], 1, now) }},
	{60 * time.Second, func(d *Demo, now time.Time) { d.AutoRestart(d.ids["demo-cache-1"], 1, now) }},
	{60 * time.Second, func(d *Demo, _ time.Time) { d.setSpike("demo-api-1", true) }},
	{90 * time.Second, func(d *Demo, now time.Time) { d.Die(d.ids["demo-worker-1"], 137, now) }},
	{130 * time.Second, func(d *Demo, now time.Time) { d.Start(d.ids["demo-worker-1"], now) }},
	{150 * time.Second, func(d *Demo, _ time.Time) { d.setSpike("demo-api-1", false) }},
	{160 * time.Second, func(d *Demo, _ time.Time) { d.SetHealth(d.ids["demo-api-1"], container.Unhealthy) }},
}

func (d *Demo) setSpike(name string, on bool) {
	d.stats.mu.Lock()
	defer d.stats.mu.Unlock()
	d.stats.spike[d.ids[name]] = on
}

// Run plays the scenario in a loop until ctx is cancelled.
func (d *Demo) Run(ctx context.Context) error {
	for {
		start := time.Now()
		for _, s := range scenario {
			wait := time.Until(start.Add(d.scaled(s.at)))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
				s.do(d, time.Now())
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(start.Add(d.scaled(cycle)))):
		}
	}
}

func (d *Demo) scaled(t time.Duration) time.Duration { return time.Duration(float64(t) * d.scale) }

// RuleManager creates alert rules (the alert engine).
type RuleManager interface {
	Rules() []alerts.Rule
	CreateRule(ctx context.Context, r alerts.Rule) (alerts.Rule, error)
}

// demoRuleName identifies the rule the demo adds to its own database.
const demoRuleName = "Demo: servicio detenido"

// EnsureRules adds a not_running rule for the demo Compose project (only in
// the demo database), so the scenario's stopped worker opens an incident.
func EnsureRules(ctx context.Context, m RuleManager, log *slog.Logger) {
	for _, r := range m.Rules() {
		if r.Name == demoRuleName {
			return
		}
	}
	_, err := m.CreateRule(ctx, alerts.Rule{
		Name: demoRuleName, Kind: alerts.NotRunning, Enabled: true,
		Scope: alerts.Scope{Project: "demo", Service: "worker"},
		For:   10 * time.Second, Cooldown: time.Minute,
	})
	if err != nil {
		log.Warn("no se pudo crear la regla del demo", "err", err)
	}
}
