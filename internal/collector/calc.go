package collector

import (
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
)

// Sample is the metrics of one container at one point in time. Nil pointers
// mean "not available" (first sample, counter reset, field not reported):
// never an invented zero.
type Sample struct {
	ContainerID string
	At          time.Time // when the daemon read the stats (UTC)

	// CPUPercent is in "% of one core" (ADR-002): 100 = one full core, so it
	// can reach OnlineCPUs×100 on multi-core hosts.
	CPUPercent *float64
	OnlineCPUs int

	// MemUsage excludes the inactive page cache when Docker reports it
	// (MemCacheExcluded), like `docker stats` (ADR-003).
	MemUsage         *uint64
	MemCacheExcluded bool
	// MemLimit is the effective cgroup limit. When MemLimitExplicit is false
	// no limit was configured and MemLimit is the host memory.
	MemLimit         *uint64
	MemLimitExplicit bool
	MemPercent       *float64 // MemUsage / MemLimit × 100

	// Network: cumulative bytes since the container started (all interfaces)
	// and rates in bytes/s. Nil if the container has no network.
	NetRx, NetTx         *uint64
	NetRxRate, NetTxRate *float64

	// Block I/O: cumulative bytes read/written by the cgroup and rates in
	// bytes/s. This is I/O traffic, not disk space used.
	BlkRead, BlkWrite         *uint64
	BlkReadRate, BlkWriteRate *float64

	Pids *uint64
}

// raw holds the counters of one stats response needed for deltas.
type raw struct {
	at        time.Time
	startedAt time.Time // container StartedAt: a new value means counters restarted

	cpuTotal, cpuSystem uint64
	onlineCPUs          int

	memUsage, memLimit uint64
	memInactive        *uint64

	net             bool
	netRx, netTx    uint64
	blk             bool
	blkRead, blkWrt uint64
	pids            uint64
}

// noLimit is above any real memory size; cgroup v1 reports ~2^63 as "unlimited".
const noLimit = 1 << 60

// extract reads the counters from a stats response. ok is false when the
// response carries no sample (Docker returns zero values for stopped containers).
func extract(st container.StatsResponse, startedAt time.Time) (r raw, ok bool) {
	if st.Read.IsZero() || st.Read.Year() <= 1 {
		return raw{}, false
	}
	r = raw{at: st.Read.UTC(), startedAt: startedAt}

	cpu := st.CPUStats
	r.cpuTotal = cpu.CPUUsage.TotalUsage
	r.cpuSystem = cpu.SystemUsage
	r.onlineCPUs = int(cpu.OnlineCPUs)
	if r.onlineCPUs == 0 {
		r.onlineCPUs = len(cpu.CPUUsage.PercpuUsage)
	}

	mem := st.MemoryStats
	r.memUsage = mem.Usage
	r.memLimit = mem.Limit
	// Same rule as the Docker CLI: cgroup v1 reports total_inactive_file,
	// cgroup v2 inactive_file. Only subtract when it is below usage.
	for _, key := range []string{"total_inactive_file", "inactive_file"} {
		if v, ok := mem.Stats[key]; ok && v < mem.Usage {
			r.memInactive = &v
			break
		}
	}

	if len(st.Networks) > 0 {
		r.net = true
		for _, n := range st.Networks {
			r.netRx += n.RxBytes
			r.netTx += n.TxBytes
		}
	}

	// cgroup v2 lists only devices with activity, so a nil list may mean "no
	// I/O yet" or "not supported": report unavailable rather than guess zero.
	if entries := st.BlkioStats.IoServiceBytesRecursive; entries != nil {
		r.blk = true
		for _, e := range entries {
			switch strings.ToLower(e.Op) { // v1: "Read"/"Write", v2: "read"/"write"
			case "read":
				r.blkRead += e.Value
			case "write":
				r.blkWrt += e.Value
			}
		}
	}
	r.pids = st.PidsStats.Current
	return r, true
}

// compute builds a Sample from the current counters and the previous ones
// (nil for the first sample). explicitMemLimit is HostConfig.Memory (0 = none).
// maxGap bounds the interval over which rates are still computed.
func compute(id string, prev *raw, cur raw, explicitMemLimit int64, maxGap time.Duration) Sample {
	s := Sample{ContainerID: id, At: cur.at, OnlineCPUs: cur.onlineCPUs}

	// Deltas are only meaningful against a previous sample of the same run.
	var dt float64
	if prev != nil && prev.startedAt.Equal(cur.startedAt) {
		if d := cur.at.Sub(prev.at); d > 0 && d <= maxGap {
			dt = d.Seconds()
		}
	}
	valid := dt > 0

	if valid && cur.onlineCPUs > 0 && cur.cpuTotal >= prev.cpuTotal && cur.cpuSystem > prev.cpuSystem {
		dCPU := float64(cur.cpuTotal - prev.cpuTotal)
		dSys := float64(cur.cpuSystem - prev.cpuSystem)
		s.CPUPercent = ptr(dCPU / dSys * float64(cur.onlineCPUs) * 100)
	}

	if cur.memUsage > 0 {
		used := cur.memUsage
		if cur.memInactive != nil {
			used -= *cur.memInactive
			s.MemCacheExcluded = true
		}
		s.MemUsage = ptr(used)
		if cur.memLimit > 0 && cur.memLimit < noLimit {
			s.MemLimit = ptr(cur.memLimit)
			s.MemLimitExplicit = explicitMemLimit > 0
			s.MemPercent = ptr(float64(used) / float64(cur.memLimit) * 100)
		}
	}

	if cur.net {
		s.NetRx, s.NetTx = ptr(cur.netRx), ptr(cur.netTx)
		if valid && prev.net {
			s.NetRxRate = rate(prev.netRx, cur.netRx, dt)
			s.NetTxRate = rate(prev.netTx, cur.netTx, dt)
		}
	}
	if cur.blk {
		s.BlkRead, s.BlkWrite = ptr(cur.blkRead), ptr(cur.blkWrt)
		if valid && prev.blk {
			s.BlkReadRate = rate(prev.blkRead, cur.blkRead, dt)
			s.BlkWriteRate = rate(prev.blkWrt, cur.blkWrt, dt)
		}
	}
	if cur.pids > 0 {
		s.Pids = ptr(cur.pids)
	}
	return s
}

// rate returns the per-second rate, or nil if the counter went backwards
// (reset): never a negative rate.
func rate(prev, cur uint64, dt float64) *float64 {
	if cur < prev {
		return nil
	}
	return ptr(float64(cur-prev) / dt)
}

func ptr[T any](v T) *T { return &v }
