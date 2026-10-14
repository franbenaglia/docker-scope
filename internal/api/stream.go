package api

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// Event types of GET /api/stream. Each carries one JSON object in data.
const (
	EvHello            = "hello"             // HelloDTO, first event of every connection
	EvSummary          = "summary"           // SummaryDTO
	EvDaemon           = "daemon"            // DaemonDTO, on connection state changes
	EvContainer        = "container"         // ContainerDTO, added or updated
	EvContainerRemoved = "container_removed" // {"id": "..."}
	EvMetrics          = "metrics"           // MetricsBatchDTO, after each collector round
	EvHeartbeat        = "heartbeat"         // {"t": RFC 3339}, every 15s
	EvResync           = "resync"            // {"reason": "..."}: refetch the REST snapshot
)

// HelloDTO opens a stream.
type HelloDTO struct {
	ServerTime      time.Time `json:"server_time"`
	Mode            string    `json:"mode"`
	IntervalSeconds float64   `json:"interval_seconds"`
	HeartbeatSecs   float64   `json:"heartbeat_seconds"`
}

// MetricsBatchDTO carries the latest sample of every running container
// sampled in a round. Containers absent from Items have no current sample.
type MetricsBatchDTO struct {
	At    time.Time              `json:"at"`
	Items map[string]*MetricsDTO `json:"items"`
}

// EventSources are the components whose changes are streamed.
type EventSources interface {
	OnChange(func(inventory.Change))
	OnConnection(func(inventory.ConnState))
}

// RoundSource is the collector's round listener registration.
type RoundSource interface {
	OnRound(func(collector.Round))
}

// summaryDebounce coalesces summary events after bursts of container changes.
const summaryDebounce = 250 * time.Millisecond

// Attach registers listeners that publish to s.Hub. Call before the sources
// run. Listeners only encode and enqueue: they never block.
func (s *Server) Attach(inv EventSources, col RoundSource) {
	var pending atomic.Bool
	summarySoon := func() {
		if pending.CompareAndSwap(false, true) {
			time.AfterFunc(summaryDebounce, func() {
				pending.Store(false)
				s.publish(EvSummary, s.summary())
			})
		}
	}
	inv.OnChange(func(ch inventory.Change) {
		if ch.Kind == inventory.Removed {
			s.publish(EvContainerRemoved, map[string]string{"id": ch.ID})
		} else {
			s.publish(EvContainer, s.containerWithMetrics(ch.Cur, s.Metrics.LatestAll(), s.Now()))
		}
		summarySoon()
	})
	inv.OnConnection(func(st inventory.ConnState) {
		s.publish(EvDaemon, daemonDTO(st))
		summarySoon()
	})
	col.OnRound(func(r collector.Round) {
		batch := MetricsBatchDTO{At: r.At, Items: make(map[string]*MetricsDTO, len(r.Samples))}
		for _, smp := range r.Samples {
			batch.Items[smp.ContainerID] = metricsDTO(smp)
		}
		s.publish(EvMetrics, batch)
		s.publish(EvSummary, s.summary())
	})
}

func (s *Server) publish(event string, v any) {
	b, err := json.Marshal(v) // compact: a single line, as SSE data requires
	if err != nil {
		s.Log.Error("codificar evento SSE", "evento", event, "err", err)
		return
	}
	s.Hub.Publish(event, b)
}

// handleStream serves GET /api/stream: the general metrics/events stream.
// Logs use a separate stream (stage 7).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	s.Hub.Serve(w, r, func() (string, []byte) {
		b, _ := json.Marshal(HelloDTO{
			ServerTime: s.Now().UTC(), Mode: s.Mode,
			IntervalSeconds: s.Metrics.Interval().Seconds(), HeartbeatSecs: s.HeartbeatSeconds,
		})
		return EvHello, b
	})
}
