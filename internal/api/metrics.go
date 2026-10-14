package api

import (
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// Metrics is the read side of collector.Collector.
type Metrics interface {
	LatestAll() map[string]collector.Sample
	Interval() time.Duration
}

// MetricsDTO is the latest sample of a container. Null fields mean "not
// available" (first sample, counter reset, not reported), never zero.
type MetricsDTO struct {
	At time.Time `json:"at"`
	// CPUPercent is in % of one core: 100 = one full core; up to online_cpus×100.
	CPUPercent *float64 `json:"cpu_percent"`
	OnlineCPUs int      `json:"online_cpus"`

	// MemUsageBytes excludes the inactive page cache when mem_cache_excluded.
	MemUsageBytes    *uint64 `json:"mem_usage_bytes"`
	MemCacheExcluded bool    `json:"mem_cache_excluded"`
	// MemLimitBytes is the effective limit; if mem_limit_explicit is false it is
	// the host memory (no limit configured).
	MemLimitBytes    *uint64  `json:"mem_limit_bytes"`
	MemLimitExplicit bool     `json:"mem_limit_explicit"`
	MemPercent       *float64 `json:"mem_percent"`

	NetRxBytes *uint64  `json:"net_rx_bytes"` // cumulative since start
	NetTxBytes *uint64  `json:"net_tx_bytes"`
	NetRxBps   *float64 `json:"net_rx_bps"` // bytes per second
	NetTxBps   *float64 `json:"net_tx_bps"`

	BlkReadBytes  *uint64  `json:"blk_read_bytes"` // block I/O traffic, not disk space
	BlkWriteBytes *uint64  `json:"blk_write_bytes"`
	BlkReadBps    *float64 `json:"blk_read_bps"`
	BlkWriteBps   *float64 `json:"blk_write_bps"`

	Pids *uint64 `json:"pids"`
}

func metricsDTO(s collector.Sample) *MetricsDTO {
	return &MetricsDTO{
		At: s.At, CPUPercent: s.CPUPercent, OnlineCPUs: s.OnlineCPUs,
		MemUsageBytes: s.MemUsage, MemCacheExcluded: s.MemCacheExcluded,
		MemLimitBytes: s.MemLimit, MemLimitExplicit: s.MemLimitExplicit, MemPercent: s.MemPercent,
		NetRxBytes: s.NetRx, NetTxBytes: s.NetTx, NetRxBps: s.NetRxRate, NetTxBps: s.NetTxRate,
		BlkReadBytes: s.BlkRead, BlkWriteBytes: s.BlkWrite, BlkReadBps: s.BlkReadRate, BlkWriteBps: s.BlkWriteRate,
		Pids: s.Pids,
	}
}

// ResourcesDTO aggregates usage over running containers with an explicit unit
// and denominator (SPEC: no misleading aggregate percentages).
type ResourcesDTO struct {
	// CPUCoresUsed is the sum of cpu_percent/100 over containers with CPU data.
	CPUCoresUsed *float64 `json:"cpu_cores_used"`
	HostCPUs     int      `json:"host_cpus"`
	// MemUsedBytes is the sum of mem_usage_bytes over containers with memory data.
	MemUsedBytes *uint64 `json:"mem_used_bytes"`
	HostMemBytes int64   `json:"host_mem_bytes"`
	// CPUSampled / MemSampled count the containers included in each sum, out of Running.
	CPUSampled int `json:"cpu_sampled"`
	MemSampled int `json:"mem_sampled"`
	Running    int `json:"running"`
	// IntervalSeconds is the sampling interval.
	IntervalSeconds float64 `json:"interval_seconds"`
}

func resources(snap inventory.Snapshot, latest map[string]collector.Sample, interval time.Duration) ResourcesDTO {
	r := ResourcesDTO{HostCPUs: snap.Host.NCPU, HostMemBytes: snap.Host.MemTotal, IntervalSeconds: interval.Seconds()}
	var cores float64
	var mem uint64
	for _, c := range snap.Containers {
		if !c.Running() {
			continue
		}
		r.Running++
		s, ok := latest[c.ID]
		if !ok {
			continue
		}
		if s.CPUPercent != nil {
			cores += *s.CPUPercent / 100
			r.CPUSampled++
		}
		if s.MemUsage != nil {
			mem += *s.MemUsage
			r.MemSampled++
		}
	}
	if r.CPUSampled > 0 {
		r.CPUCoresUsed = &cores
	}
	if r.MemSampled > 0 {
		r.MemUsedBytes = &mem
	}
	return r
}
