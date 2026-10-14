package logs

import "sync"

// Buffer is a bounded FIFO of lines between a log reader and a slow writer.
// When it exceeds MaxLines or MaxBytes it drops the oldest lines and counts
// them, so a slow client never makes the agent buffer without limit.
type Buffer struct {
	mu       sync.Mutex
	lines    []Line
	bytes    int
	dropped  int
	maxLines int
	maxBytes int
	batch    int
	notify   chan struct{}
}

// NewBuffer creates a buffer. Ready signals when at least batch lines are queued.
func NewBuffer(maxLines, maxBytes, batch int) *Buffer {
	return &Buffer{maxLines: maxLines, maxBytes: maxBytes, batch: batch, notify: make(chan struct{}, 1)}
}

// Push appends a line, dropping the oldest ones if the buffer is full.
func (b *Buffer) Push(l Line) {
	b.mu.Lock()
	b.lines = append(b.lines, l)
	b.bytes += len(l.Text)
	for len(b.lines) > b.maxLines || (b.bytes > b.maxBytes && len(b.lines) > 1) {
		b.bytes -= len(b.lines[0].Text)
		b.lines[0] = Line{}
		b.lines = b.lines[1:]
		b.dropped++
	}
	ready := len(b.lines) >= b.batch
	b.mu.Unlock()
	if ready {
		select {
		case b.notify <- struct{}{}:
		default:
		}
	}
}

// Take removes up to n lines and returns them with the number of lines
// dropped since the previous Take.
func (b *Buffer) Take(n int) ([]Line, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n = min(n, len(b.lines))
	out := make([]Line, n)
	copy(out, b.lines[:n])
	for _, l := range out {
		b.bytes -= len(l.Text)
	}
	b.lines = b.lines[n:]
	d := b.dropped
	b.dropped = 0
	return out, d
}

// Len returns the number of queued lines.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Ready is signaled when a batch is available.
func (b *Buffer) Ready() <-chan struct{} { return b.notify }
