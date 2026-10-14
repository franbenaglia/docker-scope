package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/franbenaglia/docker-scope/internal/store"
)

// History is the read side of store.Store.
type History interface {
	Series(ctx context.Context, q store.SeriesQuery) (store.Series, error)
	ContainerKnown(ctx context.Context, id string) (bool, error)
}

// Series query limits.
const (
	defaultPoints = 300
	minPoints     = 10
	maxPoints     = 1000
	minRange      = time.Minute
	defaultRange  = time.Hour
)

// Docker IDs are 64 hex chars; accept a conservative superset (demo IDs, tests).
var idRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// SeriesDTO is the response of GET /api/containers/{id}/series.
type SeriesDTO struct {
	ContainerID   string     `json:"container_id"`
	From          time.Time  `json:"from"`
	To            time.Time  `json:"to"`
	BucketSeconds float64    `json:"bucket_seconds"`
	StepSeconds   float64    `json:"step_seconds"` // sampling interval
	Source        string     `json:"source"`       // "raw" or "1m" (1-minute rollups)
	Points        []PointDTO `json:"points"`
}

// PointDTO is one bucket; metric fields are null when there is no data.
type PointDTO struct {
	T          time.Time `json:"t"` // bucket start (UTC)
	N          int       `json:"n"`
	CPUAvg     *float64  `json:"cpu_avg"` // % of one core
	CPUMax     *float64  `json:"cpu_max"`
	MemAvg     *float64  `json:"mem_avg"` // bytes
	MemMax     *float64  `json:"mem_max"`
	MemLimit   *float64  `json:"mem_limit"`
	MemPctAvg  *float64  `json:"mem_pct_avg"`
	MemPctMax  *float64  `json:"mem_pct_max"`
	NetRxBps   *float64  `json:"net_rx_bps"`
	NetTxBps   *float64  `json:"net_tx_bps"`
	BlkReadBps *float64  `json:"blk_read_bps"`
	BlkWrBps   *float64  `json:"blk_write_bps"`
	PidsMax    *float64  `json:"pids_max"`
}

// handleSeries serves GET /api/containers/{id}/series?range=1h&points=300
// or ?from=RFC3339&to=RFC3339. Ranges are clamped to the retention window.
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "id de contenedor inválido")
		return
	}
	q := r.URL.Query()
	now := s.Now().UTC()

	points := defaultPoints
	if v := q.Get("points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minPoints || n > maxPoints {
			writeError(w, http.StatusBadRequest, "invalid_parameter",
				"points debe ser un entero entre "+strconv.Itoa(minPoints)+" y "+strconv.Itoa(maxPoints))
			return
		}
		points = n
	}

	to, from := now, now.Add(-defaultRange)
	switch {
	case q.Get("range") != "" && (q.Get("from") != "" || q.Get("to") != ""):
		writeError(w, http.StatusBadRequest, "invalid_parameter", "use range o from/to, no ambos")
		return
	case q.Get("range") != "":
		d, err := parseRange(q.Get("range"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "range inválido (ejemplos: 15m, 6h, 7d)")
			return
		}
		from = now.Add(-d)
	case q.Get("from") != "" || q.Get("to") != "":
		var err error
		if from, err = parseTime(q.Get("from"), now.Add(-defaultRange)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "from debe ser RFC 3339")
			return
		}
		if to, err = parseTime(q.Get("to"), now); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "to debe ser RFC 3339")
			return
		}
	}
	// Clamp to what can exist: the retention window, up to now.
	from = latest(from, now.Add(-s.Retention.Rollups))
	if to.After(now) {
		to = now
	}
	if to.Sub(from) < minRange {
		writeError(w, http.StatusBadRequest, "invalid_range",
			"el rango debe durar al menos 1 minuto y estar dentro de la retención")
		return
	}

	known, err := s.History.ContainerKnown(r.Context(), id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !known {
		writeError(w, http.StatusNotFound, "not_found", "contenedor sin historial")
		return
	}
	ser, err := s.History.Series(r.Context(), store.SeriesQuery{
		// The store range is [From, To); +1ms makes "to" inclusive so the most
		// recent sample is never cut off.
		ContainerID: id, From: from, To: to.Add(time.Millisecond), MaxPoints: points,
		Step: s.Metrics.Interval(), RawSince: now.Add(-s.Retention.Raw),
	})
	if errors.Is(err, store.ErrInvalidQuery) {
		writeError(w, http.StatusBadRequest, "invalid_range", err.Error())
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := SeriesDTO{
		ContainerID: id, From: ser.From, To: to, Source: ser.Source,
		BucketSeconds: ser.Bucket.Seconds(), StepSeconds: s.Metrics.Interval().Seconds(),
		Points: make([]PointDTO, 0, len(ser.Points)),
	}
	for _, p := range ser.Points {
		out.Points = append(out.Points, PointDTO{
			T: p.TS, N: p.N, CPUAvg: p.CPUAvg, CPUMax: p.CPUMax, MemAvg: p.MemAvg, MemMax: p.MemMax,
			MemLimit: p.MemLimit, MemPctAvg: p.MemPctAvg, MemPctMax: p.MemPctMax,
			NetRxBps: p.NetRx, NetTxBps: p.NetTx, BlkReadBps: p.BlkRead, BlkWrBps: p.BlkWrite, PidsMax: p.PidsMax,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// parseRange parses durations like "90s", "15m", "6h" or "7d".
func parseRange(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 || days > 3650 {
			return 0, errors.New("días inválidos")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("duración inválida")
	}
	return d, nil
}

func parseTime(v string, def time.Time) (time.Time, error) {
	if v == "" {
		return def, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	return t.UTC(), err
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.Log.Error("error interno", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "error interno")
}
