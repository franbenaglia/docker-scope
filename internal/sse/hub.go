// Package sse implements a Server-Sent Events hub: one bounded queue per
// client, typed events, heartbeats and an explicit policy for slow clients.
//
// SSE is not durable delivery: when a client's queue overflows, its pending
// events are discarded and a "resync" event tells it to fetch a fresh
// snapshot over REST. Clients must also refetch the snapshot after every
// (re)connection.
package sse

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ErrTooManyClients is returned by Subscribe when MaxClients is reached.
var ErrTooManyClients = errors.New("sse: demasiados clientes")

// Options configures a Hub. Zero values get defaults.
type Options struct {
	MaxClients   int           // default 32
	QueueSize    int           // events buffered per client, default 64
	Heartbeat    time.Duration // default 15s
	WriteTimeout time.Duration // per write to a client, default 10s
	Retry        time.Duration // reconnection delay suggested to browsers, default 3s
}

// Hub fans out events to subscribed clients.
type Hub struct {
	opts Options

	mu      sync.Mutex
	clients map[*Client]struct{}
	closed  bool
	done    chan struct{}

	seq     atomic.Uint64
	resyncs atomic.Int64
}

// Client is one subscriber.
type Client struct {
	ch chan []byte
}

// NewHub creates a hub.
func NewHub(opts Options) *Hub {
	if opts.MaxClients <= 0 {
		opts.MaxClients = 32
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 64
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	if opts.Retry <= 0 {
		opts.Retry = 3 * time.Second
	}
	return &Hub{opts: opts, clients: map[*Client]struct{}{}, done: make(chan struct{})}
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Resyncs returns how many times a slow client's queue overflowed.
func (h *Hub) Resyncs() int64 { return h.resyncs.Load() }

// Subscribe registers a client.
func (h *Hub) Subscribe() (*Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("sse: hub cerrado")
	}
	if len(h.clients) >= h.opts.MaxClients {
		return nil, ErrTooManyClients
	}
	c := &Client{ch: make(chan []byte, h.opts.QueueSize)}
	h.clients[c] = struct{}{}
	return c, nil
}

// Unsubscribe removes a client.
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// Publish sends an event to every client without blocking. data must be a
// single line (e.g. compact JSON). A client whose queue is full loses its
// pending events and receives "resync" instead.
func (h *Hub) Publish(event string, data []byte) {
	frame := h.frame(event, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for c := range h.clients {
		select {
		case c.ch <- frame:
			continue
		default:
		}
		// Overflow: drop everything pending; the client resyncs from REST.
		h.resyncs.Add(1)
	drain:
		for {
			select {
			case <-c.ch:
			default:
				break drain
			}
		}
		select {
		case c.ch <- h.frame("resync", []byte(`{"reason":"slow_client"}`)):
		default: // the reader drained concurrently and refilled; it will catch up
		}
	}
}

// Close disconnects every client; active handlers return.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

func (h *Hub) frame(event string, data []byte) []byte {
	return encode(h.seq.Add(1), event, data)
}

// Serve streams events to one HTTP client until it disconnects, the hub
// closes or a write fails. hello, if not nil, provides the first event.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, hello func() (string, []byte)) {
	c, err := h.Subscribe()
	if errors.Is(err, ErrTooManyClients) {
		http.Error(w, "demasiadas conexiones de streaming", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "servicio cerrándose", http.StatusServiceUnavailable)
		return
	}
	defer h.Unsubscribe(c)

	st, err := Start(w, h.opts.Retry, h.opts.WriteTimeout)
	if err != nil {
		return
	}
	write := func(b []byte) bool { return st.write(b) == nil }
	if hello != nil {
		name, data := hello()
		if !write(h.frame(name, data)) {
			return
		}
	}
	hb := time.NewTicker(h.opts.Heartbeat)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case b := <-c.ch:
			if !write(b) {
				return
			}
		case t := <-hb.C:
			if !write(h.frame("heartbeat", []byte(`{"t":"`+t.UTC().Format(time.RFC3339)+`"}`))) {
				return
			}
		}
	}
}
