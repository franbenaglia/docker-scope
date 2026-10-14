// Package logs reads Docker log streams: demultiplexes stdout/stderr frames
// (or the single TTY stream), splits lines with a size cap, parses Docker
// timestamps and sanitizes text so it is safe to display. Logs are never
// persisted (SPEC "Logs").
package logs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Stream identifies where a line came from.
type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
	// TTY: the container has a terminal, so Docker merges stdout and stderr.
	TTY Stream = "tty"
	// System: an error message from the Docker daemon inside the stream.
	System Stream = "system"
)

// Line is one log line.
type Line struct {
	Time      time.Time // from Docker (zero if absent or unparsable)
	Stream    Stream
	Text      string // sanitized
	Truncated bool   // longer than MaxLineBytes: the rest was discarded
}

// DefaultMaxLineBytes caps one line.
const DefaultMaxLineBytes = 8 << 10

// maxFrame bounds a multiplexed frame header size; Docker frames are far
// smaller, so anything bigger means a corrupt or non-multiplexed stream.
const maxFrame = 1 << 30

// Options configures Parse.
type Options struct {
	TTY          bool // the stream is raw (container with TTY), not multiplexed
	Timestamps   bool // lines start with a Docker RFC 3339 timestamp and a space
	MaxLineBytes int  // default DefaultMaxLineBytes
}

// ErrStopped is returned by Parse when emit asked to stop.
var ErrStopped = errors.New("logs: detenido por el consumidor")

// Parse reads r until EOF, calling emit for each line. emit returning false
// stops parsing with ErrStopped. A final line without newline is emitted too.
func Parse(r io.Reader, opts Options, emit func(Line) bool) error {
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = DefaultMaxLineBytes
	}
	if opts.TTY {
		a := newAssembler(TTY, opts, emit)
		br := bufio.NewReaderSize(r, 32<<10)
		buf := make([]byte, 32<<10)
		for {
			n, err := br.Read(buf)
			if n > 0 && !a.write(buf[:n]) {
				return ErrStopped
			}
			if errors.Is(err, io.EOF) {
				if !a.flush() {
					return ErrStopped
				}
				return nil
			}
			if err != nil {
				return err
			}
		}
	}

	asm := map[byte]*assembler{
		1: newAssembler(Stdout, opts, emit),
		2: newAssembler(Stderr, opts, emit),
		3: newAssembler(System, opts, emit),
	}
	var hdr [8]byte
	payload := make([]byte, 32<<10)
	br := bufio.NewReaderSize(r, 32<<10)
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				for _, k := range []byte{1, 2, 3} {
					if !asm[k].flush() {
						return ErrStopped
					}
				}
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("logs: cabecera de frame incompleta")
			}
			return err
		}
		a := asm[hdr[0]]
		size := binary.BigEndian.Uint32(hdr[4:])
		if a == nil || hdr[1] != 0 || hdr[2] != 0 || hdr[3] != 0 || size > maxFrame {
			return fmt.Errorf("logs: frame multiplexado inválido (¿contenedor con TTY?)")
		}
		for remaining := int(size); remaining > 0; {
			n := min(remaining, len(payload))
			if _, err := io.ReadFull(br, payload[:n]); err != nil {
				return fmt.Errorf("logs: frame truncado: %w", err)
			}
			remaining -= n
			if !a.write(payload[:n]) {
				return ErrStopped
			}
		}
	}
}

// assembler splits a byte stream into lines with a size cap.
type assembler struct {
	stream    Stream
	opts      Options
	emit      func(Line) bool
	buf       []byte
	truncated bool // current line exceeded the cap; discard until newline
}

func newAssembler(s Stream, opts Options, emit func(Line) bool) *assembler {
	return &assembler{stream: s, opts: opts, emit: emit}
}

func (a *assembler) write(p []byte) bool {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if len(a.buf)+len(chunk) > a.opts.MaxLineBytes {
			a.truncated = true
		}
		if room := a.opts.MaxLineBytes - len(a.buf); room > 0 {
			a.buf = append(a.buf, chunk[:min(room, len(chunk))]...)
		}
		if i < 0 {
			return true
		}
		if !a.line() {
			return false
		}
		p = p[i+1:]
	}
	return true
}

func (a *assembler) flush() bool {
	if len(a.buf) == 0 && !a.truncated {
		return true
	}
	return a.line()
}

func (a *assembler) line() bool {
	raw := bytes.TrimSuffix(a.buf, []byte("\r"))
	l := Line{Stream: a.stream, Truncated: a.truncated}
	if a.opts.Timestamps {
		if sp := bytes.IndexByte(raw, ' '); sp > 0 && sp <= 40 {
			if t, err := time.Parse(time.RFC3339Nano, string(raw[:sp])); err == nil {
				l.Time = t.UTC()
				raw = raw[sp+1:]
			}
		}
	}
	l.Text = Sanitize(raw)
	a.buf = a.buf[:0]
	a.truncated = false
	return a.emit(l)
}
