// Package demo provides a simulated Docker daemon for running the panel
// without Docker. Demo data is never mixed with real data: the agent runs
// either in demo mode or against Docker, never both.
//
// Stage 2: static containers on the in-memory fake daemon; stage 10 adds a
// dynamic scenario (failures, restarts, health changes).
package demo

import (
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/docker"
	dt "github.com/franbenaglia/docker-scope/internal/docker/dockertest"
)

// New returns a simulated daemon populated with demo containers.
func New(now time.Time) docker.API {
	id := func(n byte) string {
		// Distinct 12-char prefixes so short IDs differ, as with real Docker.
		return strings.Repeat(string('0'+n), 4) + "dem0" + strings.Repeat("0", 55) + string('0'+n)
	}
	return dt.New(
		dt.Container(id(1), "demo-web-1", now.Add(-3*time.Hour), dt.Compose("demo", "web"),
			dt.Health(container.Healthy), dt.Restart(container.RestartPolicyUnlessStopped)),
		dt.Container(id(2), "demo-api-1", now.Add(-50*time.Minute), dt.Compose("demo", "api"),
			dt.Health(container.Unhealthy), dt.Restart(container.RestartPolicyAlways)),
		dt.Container(id(3), "demo-worker-1", now.Add(-26*time.Hour), dt.Compose("demo", "worker")),
		dt.Container(id(4), "demo-migrate-1", now.Add(-26*time.Hour), dt.Compose("demo", "migrate"),
			dt.Exited(0, now.Add(-26*time.Hour+40*time.Second))),
		dt.Container(id(5), "scratch", now.Add(-2*time.Hour), dt.Exited(137, now.Add(-time.Hour))),
	)
}
