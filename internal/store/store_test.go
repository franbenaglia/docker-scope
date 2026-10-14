package store

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
)

var (
	ctx   = context.Background()
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(ctx, dir, "test.db", quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func f(v float64) *float64 { return &v }
func u(v uint64) *uint64   { return &v }

// sample builds a sample at t0+sec seconds.
func sample(id string, sec int, cpu float64, mem uint64) collector.Sample {
	return collector.Sample{
		ContainerID: id, At: t0.Add(time.Duration(sec) * time.Second),
		CPUPercent: f(cpu), MemUsage: u(mem), MemLimit: u(1000), MemPercent: f(float64(mem) / 10),
		NetRxRate: f(100), Pids: u(3),
	}
}

func TestOpenPermissionsAndMigrations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s := open(t, dir)
	if v, err := s.SchemaVersion(ctx); err != nil || v != 2 {
		t.Fatalf("versión = %d, %v", v, err)
	}
	if err := s.InsertSamples(ctx, []collector.Sample{sample("a", 0, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, s.Path(): 0o600, s.Path() + "-wal": 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s: modo %o, want %o", filepath.Base(path), got, want)
		}
	}
	s.Close()

	// Reopening keeps data and does not reapply migrations.
	s2 := open(t, dir)
	if v, _ := s2.SchemaVersion(ctx); v != 2 {
		t.Errorf("versión tras reabrir = %d", v)
	}
	ser, err := s2.Series(ctx, SeriesQuery{ContainerID: "a", From: t0.Add(-time.Minute), To: t0.Add(time.Minute),
		MaxPoints: 100, Step: 5 * time.Second, RawSince: t0.Add(-time.Hour)})
	if err != nil || len(ser.Points) != 1 {
		t.Errorf("los datos deben sobrevivir al reinicio: %d puntos, %v", len(ser.Points), err)
	}
}

func TestExistingFileGetsTightened(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	open(t, dir)
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("modo = %o", st.Mode().Perm())
	}
}

func TestSeriesRawDownsampling(t *testing.T) {
	s := open(t, t.TempDir())
	var batch []collector.Sample
	for i := range 12 { // 60s of 5s samples: cpu 0..110
		batch = append(batch, sample("a", i*5, float64(i*10), uint64(100+i)))
	}
	batch = append(batch, sample("b", 0, 999, 999)) // other container
	nilCPU := sample("a", 60, 0, 200)
	nilCPU.CPUPercent = nil // first sample after a restart
	batch = append(batch, nilCPU)
	if err := s.InsertSamples(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSamples(ctx, batch[:1]); err != nil {
		t.Errorf("duplicados se ignoran: %v", err)
	}

	q := SeriesQuery{ContainerID: "a", From: t0, To: t0.Add(65 * time.Second), MaxPoints: 1000,
		Step: 5 * time.Second, RawSince: t0.Add(-time.Hour)}
	full, err := s.Series(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if full.Source != "raw" || full.Bucket != 5*time.Second || len(full.Points) != 13 {
		t.Fatalf("serie completa: source=%s bucket=%s n=%d", full.Source, full.Bucket, len(full.Points))
	}
	if last := full.Points[12]; last.CPUAvg != nil || last.MemAvg == nil {
		t.Errorf("CPU ausente debe seguir ausente (no 0): %+v", last)
	}

	// At most 4 points over 65s: buckets of ≥ 65s/3, aligned to the epoch.
	q.MaxPoints = 4
	ds, err := s.Series(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Bucket != 30*time.Second {
		t.Errorf("bucket = %s, want 30s (el valor lindo ≥ 65s/3)", ds.Bucket)
	}
	if len(ds.Points) > 4 {
		t.Errorf("puntos = %d, máximo 4", len(ds.Points))
	}
	var n int
	for _, p := range ds.Points {
		n += p.N
		if p.CPUMax != nil && p.CPUAvg != nil && *p.CPUMax < *p.CPUAvg {
			t.Errorf("max < avg en %s", p.TS)
		}
	}
	if n != 13 {
		t.Errorf("las muestras agregadas suman %d, want 13", n)
	}
}

func TestSeriesValidation(t *testing.T) {
	s := open(t, t.TempDir())
	bad := []SeriesQuery{
		{From: t0, To: t0, MaxPoints: 10, Step: time.Second},
		{From: t0, To: t0.Add(time.Hour), MaxPoints: 0, Step: time.Second},
		{From: t0, To: t0.Add(time.Hour), MaxPoints: 10},
	}
	for _, q := range bad {
		if _, err := s.Series(ctx, q); err == nil {
			t.Errorf("consulta inválida aceptada: %+v", q)
		}
	}
}

func TestRollupAndLongRange(t *testing.T) {
	s := open(t, t.TempDir())
	var batch []collector.Sample
	for i := range 36 { // 3 minutes, 5s samples
		batch = append(batch, sample("a", i*5, float64(i), 100))
	}
	if err := s.InsertSamples(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// Only complete minutes before the watermark are rolled up.
	if err := s.Rollup(ctx, t0.Add(2*time.Minute+30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollup(ctx, t0.Add(2*time.Minute+40*time.Second)); err != nil { // idempotent
		t.Fatal(err)
	}
	q := SeriesQuery{ContainerID: "a", From: t0, To: t0.Add(3 * time.Minute), MaxPoints: 100,
		Step: 5 * time.Second, RawSince: t0.Add(time.Hour)} // force rollups
	ser, err := s.Series(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if ser.Source != "1m" || ser.Bucket != time.Minute || len(ser.Points) != 2 {
		t.Fatalf("rollups: source=%s bucket=%s n=%d", ser.Source, ser.Bucket, len(ser.Points))
	}
	p := ser.Points[0]
	if *p.CPUAvg != 5.5 || *p.CPUMax != 11 || p.N != 1 {
		t.Errorf("minuto 0: avg=%v max=%v n=%d", *p.CPUAvg, *p.CPUMax, p.N)
	}

	// The third minute is rolled up once complete, continuing from the watermark.
	if err := s.Rollup(ctx, t0.Add(3*time.Minute+30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if ser, _ = s.Series(ctx, q); len(ser.Points) != 3 {
		t.Errorf("tras el siguiente rollup: %d puntos", len(ser.Points))
	}
}

func TestPrune(t *testing.T) {
	s := open(t, t.TempDir())
	old, recent := sample("a", -3*86400, 1, 1), sample("a", 0, 2, 2)
	if err := s.InsertSamples(ctx, []collector.Sample{old, recent}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollup(ctx, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := s.UpsertContainers(ctx, []ContainerMeta{
		{ID: "gone", Name: "gone", SeenAt: t0.Add(-10 * 24 * time.Hour)},
		{ID: "gone", SeenAt: t0.Add(-9 * 24 * time.Hour), Removed: true},
		{ID: "a", Name: "a", SeenAt: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Prune(ctx, t0.Add(time.Minute), Retention{Raw: 24 * time.Hour, Rollups: 2 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if res.Samples != 1 || res.Rollups != 1 || res.Containers != 1 {
		t.Errorf("prune = %+v, want 1/1/1", res)
	}
}

func TestWriterFlushesAndDrops(t *testing.T) {
	s := open(t, t.TempDir())
	w := NewWriter(s, WriterOptions{QueueSize: 2, Retention: Retention{Raw: time.Hour, Rollups: time.Hour},
		Now: func() time.Time { return t0 }})
	// Not running yet: the queue holds 2 batches, the third is dropped.
	for i := range 3 {
		w.EnqueueSamples(collector.Round{Samples: []collector.Sample{sample("a", i, 1, 1)}})
	}
	if w.Dropped() != 1 {
		t.Errorf("descartados = %d", w.Dropped())
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = w.Run(runCtx); close(done) }()
	cancel()
	<-done
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM samples`).Scan(&n); err != nil || n != 2 {
		t.Errorf("filas tras flush = %d, %v", n, err)
	}
}

func TestNiceBucket(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		time.Millisecond:        time.Second,
		5 * time.Second:         5 * time.Second,
		6122 * time.Millisecond: 10 * time.Second,
		61 * time.Second:        2 * time.Minute,
		7 * time.Hour:           12 * time.Hour,
		25 * time.Hour:          48 * time.Hour,
	}
	for in, want := range cases {
		if got := niceBucket(in); got != want {
			t.Errorf("niceBucket(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSeriesNeverExceedsMaxPoints(t *testing.T) {
	s := open(t, t.TempDir())
	var batch []collector.Sample
	for i := range 720 { // 1h of 5s samples
		batch = append(batch, sample("a", i*5, 1, 1))
	}
	if err := s.InsertSamples(ctx, batch); err != nil {
		t.Fatal(err)
	}
	for _, mp := range []int{2, 7, 10, 59, 100, 333, 1000} {
		for _, off := range []time.Duration{0, 1234 * time.Millisecond, 17 * time.Second} {
			q := SeriesQuery{ContainerID: "a", From: t0.Add(off), To: t0.Add(time.Hour + off), MaxPoints: mp,
				Step: 5 * time.Second, RawSince: t0.Add(-time.Hour)}
			ser, err := s.Series(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(ser.Points) > mp {
				t.Errorf("max=%d off=%s: %d puntos (bucket %s)", mp, off, len(ser.Points), ser.Bucket)
			}
		}
	}
}
