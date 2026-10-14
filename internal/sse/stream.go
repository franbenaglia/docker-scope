package sse

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Stream writes SSE events to one HTTP response, with a deadline per write.
type Stream struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
	seq     uint64
}

// Start sets SSE headers and sends the reconnection delay.
func Start(w http.ResponseWriter, retry, writeTimeout time.Duration) (*Stream, error) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &Stream{w: w, rc: http.NewResponseController(w), timeout: writeTimeout}
	return s, s.write([]byte("retry: " + strconv.FormatInt(retry.Milliseconds(), 10) + "\n\n"))
}

// Send writes one event. data may contain newlines (split into data lines).
func (s *Stream) Send(event string, data []byte) error {
	s.seq++
	return s.write(encode(s.seq, event, data))
}

func (s *Stream) write(b []byte) error {
	_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	return s.rc.Flush()
}

func encode(id uint64, event string, data []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "id: %d\nevent: %s\n", id, event)
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}
