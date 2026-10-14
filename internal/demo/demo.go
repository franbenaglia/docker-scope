// Package demo provides a simulated Docker source for running the panel
// without Docker. Demo data is never mixed with real data: the agent runs
// either in demo mode or against Docker, never both.
//
// Minimal static version for stage 1; stage 10 adds dynamic simulation.
package demo

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/fbenaglia/dockerscope/internal/docker"
)

// Source implements docker.API with fixed simulated containers.
type Source struct{}

var _ docker.API = Source{}

func (Source) Ping(context.Context) (docker.PingInfo, error) {
	return docker.PingInfo{APIVersion: "demo", OSType: "linux"}, nil
}

func (Source) ListContainers(context.Context) ([]container.Summary, error) {
	now := time.Now().Unix()
	mk := func(id, name string, state container.ContainerState, health container.HealthStatus) container.Summary {
		s := container.Summary{
			ID:      id,
			Names:   []string{"/" + name},
			Image:   "demo/" + name + ":latest",
			Created: now - 3600,
			State:   state,
			Labels: map[string]string{
				"com.docker.compose.project": "demo",
				"com.docker.compose.service": name,
			},
		}
		if health != "" {
			s.Health = &container.HealthSummary{Status: health}
		}
		return s
	}
	return []container.Summary{
		mk("demo0000000000000000000000000000000000000000000000000000000000a1", "web", container.StateRunning, container.Healthy),
		mk("demo0000000000000000000000000000000000000000000000000000000000a2", "api", container.StateRunning, container.Unhealthy),
		mk("demo0000000000000000000000000000000000000000000000000000000000a3", "worker", container.StateRunning, ""),
		mk("demo0000000000000000000000000000000000000000000000000000000000a4", "migrate", container.StateExited, ""),
	}, nil
}
