// Package demo provides a simulated Docker daemon for running the panel
// without Docker. Demo data is never mixed with real data: the agent runs
// either in demo mode or against Docker, never both.
//
// Stage 3: static containers with synthetic metrics on the in-memory fake
// daemon; stage 10 adds a dynamic scenario (failures, restarts, health changes).
package demo

import (
	"hash/fnv"
	"math"
	"strings"
	"sync"
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
	f := dt.New(
		dt.Container(id(1), "demo-web-1", now.Add(-3*time.Hour), dt.Compose("demo", "web"),
			dt.Health(container.Healthy), dt.Restart(container.RestartPolicyUnlessStopped)),
		dt.Container(id(2), "demo-api-1", now.Add(-50*time.Minute), dt.Compose("demo", "api"),
			dt.Health(container.Unhealthy), dt.Restart(container.RestartPolicyAlways),
			dt.MemoryLimit(512<<20), dt.NanoCPUs(1_500_000_000)),
		dt.Container(id(3), "demo-worker-1", now.Add(-26*time.Hour), dt.Compose("demo", "worker")),
		dt.Container(id(4), "demo-migrate-1", now.Add(-26*time.Hour), dt.Compose("demo", "migrate"),
			dt.Exited(0, now.Add(-26*time.Hour+40*time.Second))),
		dt.Container(id(5), "scratch", now.Add(-2*time.Hour), dt.Exited(137, now.Add(-time.Hour))),
	)
	st := newStats(now, dt.FakeHost)
	st.memLimit[id(2)] = 512 << 20
	f.StatsFunc = st.next
	f.LogsFunc = logs(func(cid string) bool { return cid != id(4) && cid != id(5) }) // 4 and 5 are exited
	return f
}

// stats generates cumulative counters that evolve smoothly over time, with a
// per-container profile derived from the ID.
type stats struct {
	mu    sync.Mutex
	start time.Time
	host  docker.HostInfo
	ctrs  map[string]*counters
	// memLimit mirrors HostConfig.Memory: the cgroup limit Docker reports.
	memLimit map[string]uint64
}

type counters struct {
	last                 time.Time
	cpu, rx, tx, rd, wr  float64
	cpuBase, memBase, io float64
	phase                float64
}

func newStats(start time.Time, host docker.HostInfo) *stats {
	return &stats{start: start, host: host, ctrs: map[string]*counters{}, memLimit: map[string]uint64{}}
}

func (s *stats) next(id string, now time.Time) container.StatsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ctrs[id]
	if !ok {
		h := fnv.New32a()
		h.Write([]byte(id))
		r := float64(h.Sum32()%1000) / 1000 // 0..1 per container
		c = &counters{last: now, cpuBase: 0.05 + 1.2*r, memBase: float64(40<<20) + r*float64(300<<20), io: 2e3 + r*5e4, phase: r * 2 * math.Pi}
		s.ctrs[id] = c
	}
	dt := now.Sub(c.last).Seconds()
	c.last = now
	t := now.Sub(s.start).Seconds()
	wave := 1 + 0.6*math.Sin(t/45+c.phase) // slow oscillation
	cores := c.cpuBase * wave
	c.cpu += cores * dt * 1e9
	c.rx += c.io * wave * dt
	c.tx += c.io * 0.4 * wave * dt
	c.rd += c.io * 0.1 * dt
	c.wr += c.io * 0.3 * wave * dt
	mem := c.memBase * (1 + 0.15*math.Sin(t/90+c.phase))
	limit := uint64(s.host.MemTotal)
	if l, ok := s.memLimit[id]; ok {
		limit = l
	}

	ncpu := uint32(s.host.NCPU)
	return container.StatsResponse{
		Read: now,
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: uint64(c.cpu)},
			SystemUsage: uint64(now.Sub(time.Unix(0, 0)).Seconds() * float64(ncpu) * 1e9),
			OnlineCPUs:  ncpu,
		},
		MemoryStats: container.MemoryStats{
			Usage: uint64(mem) + 8<<20, Limit: limit,
			Stats: map[string]uint64{"inactive_file": 8 << 20},
		},
		Networks: map[string]container.NetworkStats{"eth0": {RxBytes: uint64(c.rx), TxBytes: uint64(c.tx)}},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "read", Value: uint64(c.rd)}, {Op: "write", Value: uint64(c.wr)},
		}},
		PidsStats: container.PidsStats{Current: 3 + uint64(c.cpuBase*10)},
	}
}
