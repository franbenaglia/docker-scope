package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
)

// ContainerMeta is the persisted identity of a container.
type ContainerMeta struct {
	ID             string
	Name           string
	Image          string
	ComposeProject string
	ComposeService string
	SeenAt         time.Time
	Removed        bool // the container was removed at SeenAt
}

// UpsertContainers records container metadata.
func (s *Store) UpsertContainers(ctx context.Context, metas []ContainerMeta) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		up, err := tx.PrepareContext(ctx, `INSERT INTO containers
			(id, name, image, compose_project, compose_service, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET name = excluded.name, image = excluded.image,
				compose_project = excluded.compose_project, compose_service = excluded.compose_service,
				last_seen = excluded.last_seen, removed_at = NULL`)
		if err != nil {
			return err
		}
		defer up.Close()
		for _, m := range metas {
			at := ms(m.SeenAt)
			if m.Removed {
				if _, err := tx.ExecContext(ctx, `UPDATE containers SET removed_at = ?, last_seen = ? WHERE id = ?`,
					at, at, m.ID); err != nil {
					return err
				}
				continue
			}
			if _, err := up.ExecContext(ctx, m.ID, m.Name, m.Image, m.ComposeProject, m.ComposeService, at, at); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertSamples stores raw samples. Duplicates (same container and time) are ignored.
func (s *Store) InsertSamples(ctx context.Context, samples []collector.Sample) error {
	if len(samples) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO samples
			(container_id, ts, cpu_pct, mem_used, mem_limit, mem_limit_explicit, mem_pct,
			 net_rx_bps, net_tx_bps, blk_read_bps, blk_write_bps, pids)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, x := range samples {
			if _, err := st.ExecContext(ctx, x.ContainerID, ms(x.At), x.CPUPercent, u64(x.MemUsage), u64(x.MemLimit),
				x.MemLimitExplicit, x.MemPercent, x.NetRxRate, x.NetTxRate, x.BlkReadRate, x.BlkWriteRate,
				u64(x.Pids)); err != nil {
				return err
			}
		}
		return nil
	})
}

// u64 converts for SQLite, which stores signed 64-bit integers.
func u64(v *uint64) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

const rollupKey = "rollup_1m_until"

// Rollup aggregates raw samples of complete minutes before until into
// samples_1m. It is idempotent and resumes from a persisted watermark.
func (s *Store) Rollup(ctx context.Context, until time.Time) error {
	end := until.Truncate(time.Minute)
	start := time.Time{}
	if v, ok, err := s.getSetting(ctx, rollupKey); err != nil {
		return err
	} else if ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("marca de rollup inválida %q: %w", v, err)
		}
		start = fromMs(n)
	} else {
		var minTS sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM samples`).Scan(&minTS); err != nil {
			return err
		}
		if !minTS.Valid {
			return nil // nothing to roll up yet
		}
		start = fromMs(minTS.Int64).Truncate(time.Minute)
	}
	if !end.After(start) {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO samples_1m
			(container_id, ts, n, cpu_avg, cpu_max, mem_avg, mem_max, mem_limit, mem_pct_avg, mem_pct_max,
			 net_rx_bps, net_tx_bps, blk_read_bps, blk_write_bps, pids_max)
			SELECT container_id, (ts / 60000) * 60000 AS m, COUNT(*),
				AVG(cpu_pct), MAX(cpu_pct), AVG(mem_used), MAX(mem_used), MAX(mem_limit),
				AVG(mem_pct), MAX(mem_pct), AVG(net_rx_bps), AVG(net_tx_bps),
				AVG(blk_read_bps), AVG(blk_write_bps), MAX(pids)
			FROM samples WHERE ts >= ? AND ts < ?
			GROUP BY container_id, m`, ms(start), ms(end)); err != nil {
			return err
		}
		return setSetting(ctx, tx, rollupKey, strconv.FormatInt(ms(end), 10))
	})
}

// Retention configures how long data is kept.
type Retention struct {
	Raw     time.Duration // raw samples
	Rollups time.Duration // 1-minute rollups and removed containers
}

// PruneResult reports deleted rows.
type PruneResult struct{ Samples, Rollups, Containers int64 }

// Prune deletes data older than the retention.
func (s *Store) Prune(ctx context.Context, now time.Time, r Retention) (PruneResult, error) {
	var res PruneResult
	rawCut, rollCut := ms(now.Add(-r.Raw)), ms(now.Add(-r.Rollups))
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if res.Samples, err = execCount(ctx, tx, `DELETE FROM samples WHERE ts < ?`, rawCut); err != nil {
			return err
		}
		if res.Rollups, err = execCount(ctx, tx, `DELETE FROM samples_1m WHERE ts < ?`, rollCut); err != nil {
			return err
		}
		res.Containers, err = execCount(ctx, tx, `DELETE FROM containers WHERE removed_at IS NOT NULL AND removed_at < ?
			AND NOT EXISTS (SELECT 1 FROM samples_1m WHERE container_id = containers.id)`, rollCut)
		return err
	})
	return res, err
}

func execCount(ctx context.Context, tx *sql.Tx, q string, args ...any) (int64, error) {
	r, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// Point is one downsampled bucket. Nil = no data in the bucket.
type Point struct {
	TS        time.Time
	N         int // raw samples (or minutes, for rollups) in the bucket
	CPUAvg    *float64
	CPUMax    *float64
	MemAvg    *float64
	MemMax    *float64
	MemLimit  *float64
	MemPctAvg *float64
	MemPctMax *float64
	NetRx     *float64 // bytes/s, average
	NetTx     *float64
	BlkRead   *float64
	BlkWrite  *float64
	PidsMax   *float64
}

// Series is a downsampled metric history.
type Series struct {
	ContainerID string
	From, To    time.Time
	Bucket      time.Duration
	Source      string // "raw" or "1m"
	Points      []Point
}

// SeriesQuery selects a series. MaxPoints bounds the result (buckets are
// widened as needed); Step is the minimum bucket width (the sample interval).
type SeriesQuery struct {
	ContainerID string
	From, To    time.Time
	MaxPoints   int
	Step        time.Duration
	// RawSince is the oldest instant still covered by raw samples (now − raw
	// retention). Ranges starting earlier are served from 1-minute rollups.
	RawSince time.Time
}

// ErrInvalidQuery reports an invalid series query.
var ErrInvalidQuery = errors.New("consulta de serie inválida")

// Series returns the downsampled history of one container in [From, To).
func (s *Store) Series(ctx context.Context, q SeriesQuery) (Series, error) {
	if !q.To.After(q.From) || q.MaxPoints < 1 || q.Step <= 0 {
		return Series{}, fmt.Errorf("%w: rango o puntos", ErrInvalidQuery)
	}
	span := q.To.Sub(q.From)
	// Buckets are aligned to the epoch, so a span touches up to span/bucket+1
	// of them: size for MaxPoints−1 intervals to never exceed MaxPoints.
	minBucket := ceilDiv(span, max(1, q.MaxPoints-1))
	bucket := niceBucket(max(q.Step, minBucket))
	table, source := "samples", "raw"
	cols := `AVG(cpu_pct), MAX(cpu_pct), AVG(mem_used), MAX(mem_used), MAX(mem_limit), AVG(mem_pct), MAX(mem_pct),
		AVG(net_rx_bps), AVG(net_tx_bps), AVG(blk_read_bps), AVG(blk_write_bps), MAX(pids)`
	if q.From.Before(q.RawSince) {
		table, source = "samples_1m", "1m"
		bucket = niceBucket(max(time.Minute, bucket)) // nice values ≥ 1m are whole minutes
		cols = `AVG(cpu_avg), MAX(cpu_max), AVG(mem_avg), MAX(mem_max), MAX(mem_limit), AVG(mem_pct_avg), MAX(mem_pct_max),
			AVG(net_rx_bps), AVG(net_tx_bps), AVG(blk_read_bps), AVG(blk_write_bps), MAX(pids_max)`
	}
	b := bucket.Milliseconds()
	rows, err := s.db.QueryContext(ctx, `SELECT (ts / ?) * ? AS bucket, COUNT(*), `+cols+`
		FROM `+table+` WHERE container_id = ? AND ts >= ? AND ts < ?
		GROUP BY bucket ORDER BY bucket LIMIT ?`,
		b, b, q.ContainerID, ms(q.From), ms(q.To), q.MaxPoints)
	if err != nil {
		return Series{}, err
	}
	defer rows.Close()
	out := Series{ContainerID: q.ContainerID, From: q.From.UTC(), To: q.To.UTC(), Bucket: bucket, Source: source}
	for rows.Next() {
		var ts int64
		var p Point
		var f [12]sql.NullFloat64
		if err := rows.Scan(&ts, &p.N, &f[0], &f[1], &f[2], &f[3], &f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &f[10], &f[11]); err != nil {
			return Series{}, err
		}
		p.TS = fromMs(ts)
		dst := []**float64{&p.CPUAvg, &p.CPUMax, &p.MemAvg, &p.MemMax, &p.MemLimit, &p.MemPctAvg, &p.MemPctMax,
			&p.NetRx, &p.NetTx, &p.BlkRead, &p.BlkWrite, &p.PidsMax}
		for i, d := range dst {
			if f[i].Valid {
				v := f[i].Float64
				*d = &v
			}
		}
		out.Points = append(out.Points, p)
	}
	return out, rows.Err()
}

// niceBuckets are bucket widths that align chart axes to round times.
var niceBuckets = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// niceBucket returns the smallest nice width ≥ d (whole days beyond 24h).
func niceBucket(d time.Duration) time.Duration {
	for _, b := range niceBuckets {
		if b >= d {
			return b
		}
	}
	day := 24 * time.Hour
	return (d + day - 1) / day * day
}

func ceilDiv(d time.Duration, n int) time.Duration {
	return (d + time.Duration(n) - 1) / time.Duration(n)
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ContainerKnown reports whether the container has ever been recorded.
func (s *Store) ContainerKnown(ctx context.Context, id string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM containers WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
