package collector

import (
	"math"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

var (
	t0      = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	started = t0.Add(-time.Hour)
	maxGap  = 10 * time.Minute
)

const hostMem = 16 << 30

// stats builds a cgroup v2-like response. cpu/sys in ns.
func stats(at time.Time, cpu, sys uint64) container.StatsResponse {
	return container.StatsResponse{
		Read: at,
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: cpu},
			SystemUsage: sys,
			OnlineCPUs:  8,
		},
		MemoryStats: container.MemoryStats{
			Usage: 100 << 20, Limit: hostMem,
			Stats: map[string]uint64{"inactive_file": 40 << 20},
		},
		Networks: map[string]container.NetworkStats{
			"eth0": {RxBytes: 1000, TxBytes: 500},
			"eth1": {RxBytes: 24, TxBytes: 12},
		},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "read", Value: 4096}, {Op: "write", Value: 8192},
		}},
		PidsStats: container.PidsStats{Current: 3},
	}
}

func mustExtract(t *testing.T, st container.StatsResponse) raw {
	t.Helper()
	r, ok := extract(st, started)
	if !ok {
		t.Fatal("extract: sin muestra")
	}
	return r
}

func near(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil, want %v", what, want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", what, *got, want)
	}
}

func TestFirstSampleHasNoRates(t *testing.T) {
	s := compute("x", nil, mustExtract(t, stats(t0, 1e9, 1e12)), 0, maxGap)
	if s.CPUPercent != nil || s.NetRxRate != nil || s.BlkReadRate != nil {
		t.Errorf("la primera muestra no puede tener tasas: cpu=%v net=%v blk=%v", s.CPUPercent, s.NetRxRate, s.BlkReadRate)
	}
	// Cumulative and instantaneous values are available.
	if s.MemUsage == nil || s.NetRx == nil || *s.NetRx != 1024 || s.BlkWrite == nil || *s.BlkWrite != 8192 {
		t.Errorf("acumulados: %+v", s)
	}
	if s.Pids == nil || *s.Pids != 3 {
		t.Errorf("pids = %v", s.Pids)
	}
}

func TestCPUPercentOfOneCore(t *testing.T) {
	prev := mustExtract(t, stats(t0, 10e9, 1000e9))
	// 5s on 8 CPUs → system advances 40e9 ns. Container used 5e9 ns = one full core.
	cur := mustExtract(t, stats(t0.Add(5*time.Second), 15e9, 1040e9))
	s := compute("x", &prev, cur, 0, maxGap)
	near(t, "cpu 1 núcleo", s.CPUPercent, 100)

	// Three cores busy → 300%: above 100% is expected on multi-core hosts.
	cur = mustExtract(t, stats(t0.Add(5*time.Second), 25e9, 1040e9))
	near(t, "cpu 3 núcleos", compute("x", &prev, cur, 0, maxGap).CPUPercent, 300)
}

func TestCPUOnlineFallbackToPercpu(t *testing.T) {
	a, b := stats(t0, 0, 0), stats(t0.Add(time.Second), 1e9, 4e9)
	for _, st := range []*container.StatsResponse{&a, &b} {
		st.CPUStats.OnlineCPUs = 0
		st.CPUStats.CPUUsage.PercpuUsage = []uint64{0, 0, 0, 0}
	}
	prev := mustExtract(t, a)
	s := compute("x", &prev, mustExtract(t, b), 0, maxGap)
	near(t, "cpu con percpu", s.CPUPercent, 100)
	if s.OnlineCPUs != 4 {
		t.Errorf("online = %d", s.OnlineCPUs)
	}
}

func TestCountersResetGiveNoRates(t *testing.T) {
	prev := mustExtract(t, stats(t0, 50e9, 1000e9))
	st := stats(t0.Add(5*time.Second), 1e9, 1040e9) // CPU counter went backwards
	st.Networks = map[string]container.NetworkStats{"eth0": {RxBytes: 10, TxBytes: 10}}
	st.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{{Op: "read", Value: 1}, {Op: "write", Value: 1}}
	s := compute("x", &prev, mustExtract(t, st), 0, maxGap)
	if s.CPUPercent != nil || s.NetRxRate != nil || s.NetTxRate != nil || s.BlkReadRate != nil || s.BlkWriteRate != nil {
		t.Errorf("un contador que retrocede no debe producir tasas (ni negativas): %+v", s)
	}
}

func TestRestartedContainerResetsDeltas(t *testing.T) {
	prev := mustExtract(t, stats(t0, 1e9, 1000e9))
	cur, _ := extract(stats(t0.Add(5*time.Second), 2e9, 1040e9), started.Add(time.Hour)) // new StartedAt
	if s := compute("x", &prev, cur, 0, maxGap); s.CPUPercent != nil || s.NetRxRate != nil {
		t.Error("tras un reinicio no se comparan contadores de ejecuciones distintas")
	}
}

func TestLongGapGivesNoRates(t *testing.T) {
	prev := mustExtract(t, stats(t0, 1e9, 1000e9))
	cur := mustExtract(t, stats(t0.Add(time.Hour), 2e9, 2000e9))
	if s := compute("x", &prev, cur, 0, maxGap); s.CPUPercent != nil {
		t.Error("intervalos mayores a maxGap no producen tasas")
	}
	same := mustExtract(t, stats(t0, 2e9, 2000e9)) // same timestamp: dt = 0
	if s := compute("x", &prev, same, 0, maxGap); s.CPUPercent != nil {
		t.Error("dt = 0 no produce tasas")
	}
}

func TestNetworkAndBlkioRates(t *testing.T) {
	prev := mustExtract(t, stats(t0, 0, 0))
	st := stats(t0.Add(2*time.Second), 0, 0)
	st.Networks = map[string]container.NetworkStats{
		"eth0": {RxBytes: 3000, TxBytes: 900},
		"eth1": {RxBytes: 24, TxBytes: 12},
	}
	st.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{
		{Op: "Read", Value: 4096 + 2048}, {Op: "Write", Value: 8192 + 4096}, {Op: "Total", Value: 1 << 30},
	}
	s := compute("x", &prev, mustExtract(t, st), 0, maxGap)
	near(t, "rx/s", s.NetRxRate, 1000)
	near(t, "tx/s", s.NetTxRate, 200)
	near(t, "read/s", s.BlkReadRate, 1024)
	near(t, "write/s", s.BlkWriteRate, 2048)
	if s.CPUPercent != nil {
		t.Error("sin avance del contador de sistema no hay CPU%")
	}
}

func TestMissingNetworkAndBlkioAreUnavailable(t *testing.T) {
	st := stats(t0, 1, 1)
	st.Networks = nil                      // --network none
	st.BlkioStats = container.BlkioStats{} // cgroup v2 without I/O
	st.PidsStats = container.PidsStats{}   // not reported
	s := compute("x", nil, mustExtract(t, st), 0, maxGap)
	if s.NetRx != nil || s.NetTx != nil || s.BlkRead != nil || s.BlkWrite != nil || s.Pids != nil {
		t.Errorf("ausente ≠ 0: %+v", s)
	}
}

func TestMemoryV2ExcludesInactiveFile(t *testing.T) {
	s := compute("x", nil, mustExtract(t, stats(t0, 0, 0)), 0, maxGap)
	if s.MemUsage == nil || *s.MemUsage != 60<<20 || !s.MemCacheExcluded {
		t.Fatalf("mem = %v excluded=%v", s.MemUsage, s.MemCacheExcluded)
	}
	if s.MemLimitExplicit || s.MemLimit == nil || *s.MemLimit != hostMem {
		t.Errorf("sin --memory el límite es el del host y no es explícito: %+v", s)
	}
	near(t, "mem%", s.MemPercent, float64(60<<20)/float64(hostMem)*100)
}

func TestMemoryV1AndExplicitLimit(t *testing.T) {
	st := stats(t0, 0, 0)
	st.MemoryStats = container.MemoryStats{
		Usage: 50 << 20, Limit: 64 << 20,
		Stats: map[string]uint64{"total_inactive_file": 10 << 20, "inactive_file": 1 << 20},
	}
	s := compute("x", nil, mustExtract(t, st), 64<<20, maxGap)
	if *s.MemUsage != 40<<20 {
		t.Errorf("v1 debe usar total_inactive_file: %d", *s.MemUsage)
	}
	if !s.MemLimitExplicit {
		t.Error("límite configurado debe marcarse explícito")
	}
	near(t, "mem%", s.MemPercent, 62.5)
}

func TestMemoryEdgeCases(t *testing.T) {
	st := stats(t0, 0, 0)
	st.MemoryStats = container.MemoryStats{Usage: 10 << 20, Limit: 1<<63 - 4096} // v1 "unlimited"
	s := compute("x", nil, mustExtract(t, st), 0, maxGap)
	if s.MemCacheExcluded || *s.MemUsage != 10<<20 {
		t.Errorf("sin estadística de caché se usa usage tal cual: %+v", s)
	}
	if s.MemLimit != nil || s.MemPercent != nil {
		t.Error("un límite absurdo no debe producir porcentaje")
	}

	st.MemoryStats = container.MemoryStats{Usage: 5, Stats: map[string]uint64{"inactive_file": 10}}
	if s := compute("x", nil, mustExtract(t, st), 0, maxGap); *s.MemUsage != 5 {
		t.Error("inactive_file mayor que usage no se resta")
	}

	st.MemoryStats = container.MemoryStats{}
	if s := compute("x", nil, mustExtract(t, st), 0, maxGap); s.MemUsage != nil || s.MemPercent != nil {
		t.Error("memoria no reportada debe ser no disponible")
	}
}

func TestExtractStoppedContainer(t *testing.T) {
	if _, ok := extract(container.StatsResponse{}, started); ok {
		t.Error("stats sin Read (contenedor detenido) no son una muestra")
	}
}
