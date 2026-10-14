package logs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func collect(t *testing.T, data []byte, opts Options) []Line {
	t.Helper()
	var out []Line
	if err := Parse(bytes.NewReader(data), opts, func(l Line) bool { out = append(out, l); return true }); err != nil {
		t.Fatal(err)
	}
	return out
}

// Captured from Docker 29 (json-file, timestamps): see docs/DECISIONS.md ADR-014.
var realMux = []byte("\x01\x00\x00\x00\x00\x00\x00$2026-10-14T04:59:26.717153753Z out1\n" +
	"\x01\x00\x00\x00\x00\x00\x00-2026-10-14T04:59:26.717180413Z \x1b[31mrojo\x1b[0m\n" +
	"\x02\x00\x00\x00\x00\x00\x00$2026-10-14T04:59:26.717153758Z err1\n" +
	"\x01\x00\x00\x00\x00\x00\x00&2026-10-14T04:59:26.742212092Z sin-fin")

func TestParseRealMultiplexed(t *testing.T) {
	got := collect(t, realMux, Options{Timestamps: true})
	want := []struct {
		s    Stream
		text string
	}{{Stdout, "out1"}, {Stdout, "rojo"}, {Stderr, "err1"}, {Stdout, "sin-fin"}}
	if len(got) != len(want) {
		t.Fatalf("líneas = %+v", got)
	}
	for i, w := range want {
		if got[i].Stream != w.s || got[i].Text != w.text {
			t.Errorf("línea %d = %+v, want %v %q", i, got[i], w.s, w.text)
		}
	}
	if !got[0].Time.Equal(time.Date(2026, 10, 14, 4, 59, 26, 717153753, time.UTC)) {
		t.Errorf("timestamp = %s", got[0].Time)
	}
}

func TestParseRealTTY(t *testing.T) {
	tty := []byte("2026-10-14T04:59:27.020619906Z tty1\r\n2026-10-14T04:59:27.020650069Z tty2\r\n")
	got := collect(t, tty, Options{TTY: true, Timestamps: true})
	if len(got) != 2 || got[0].Text != "tty1" || got[1].Stream != TTY || got[1].Time.IsZero() {
		t.Errorf("tty = %+v", got)
	}
}

func TestLinesSplitAcrossFramesAndReads(t *testing.T) {
	data := append(frame(1, "hola mu"), frame(1, "ndo\nsegunda\nter")...)
	data = append(data, frame(2, "error\n")...)
	data = append(data, frame(1, "cera\n")...)
	var got []string
	err := Parse(iotest.OneByteReader(bytes.NewReader(data)), Options{}, func(l Line) bool {
		got = append(got, string(l.Stream)+":"+l.Text)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "stdout:hola mundo|stdout:segunda|stderr:error|stdout:tercera"
	if strings.Join(got, "|") != want {
		t.Errorf("got %v", got)
	}
}

func TestLongLinesAreTruncated(t *testing.T) {
	long := strings.Repeat("x", 100)
	data := frame(1, long+"\ncorta\n"+long)
	got := collect(t, data, Options{MaxLineBytes: 10})
	if len(got) != 3 {
		t.Fatalf("líneas = %d", len(got))
	}
	if got[0].Text != strings.Repeat("x", 10) || !got[0].Truncated {
		t.Errorf("larga = %+v", got[0])
	}
	if got[1].Text != "corta" || got[1].Truncated {
		t.Errorf("corta = %+v", got[1])
	}
	if !got[2].Truncated || len(got[2].Text) != 10 {
		t.Errorf("larga sin newline final = %+v", got[2])
	}
	// Exactly at the cap is not truncated.
	if l := collect(t, frame(1, "0123456789\n"), Options{MaxLineBytes: 10}); l[0].Truncated {
		t.Error("una línea de exactamente el máximo no está truncada")
	}
}

func TestInvalidAndCorruptStreams(t *testing.T) {
	if err := Parse(bytes.NewReader([]byte("texto plano sin frames\n")), Options{}, func(Line) bool { return true }); err == nil {
		t.Error("un stream TTY leído como multiplexado debe dar error")
	}
	if err := Parse(bytes.NewReader([]byte{1, 0, 0}), Options{}, func(Line) bool { return true }); err == nil {
		t.Error("cabecera incompleta")
	}
	if err := Parse(bytes.NewReader(frame(1, "abc")[:9]), Options{}, func(Line) bool { return true }); err == nil {
		t.Error("frame truncado")
	}
	sys := collect(t, frame(3, "error del daemon\n"), Options{})
	if sys[0].Stream != System {
		t.Errorf("stream 3 = %v", sys[0].Stream)
	}
}

func TestStopEarly(t *testing.T) {
	n := 0
	err := Parse(bytes.NewReader(frame(1, "a\nb\nc\n")), Options{}, func(Line) bool { n++; return n < 2 })
	if !errors.Is(err, ErrStopped) || n != 2 {
		t.Errorf("err=%v n=%d", err, n)
	}
}

func TestTimestampAbsentOrInvalid(t *testing.T) {
	got := collect(t, frame(1, "no-es-fecha resto\n"), Options{Timestamps: true})
	if !got[0].Time.IsZero() || got[0].Text != "no-es-fecha resto" {
		t.Errorf("%+v", got[0])
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"\x1b[1;31mERROR\x1b[0m listo":                  "ERROR listo",
		"\x1b]0;título malicioso\x07texto":              "texto",
		"\x1b]8;;http://evil\x1b\\enlace\x1b]8;;\x1b\\": "enlace",
		"a\x1b[2Jb\x1b[Hc":                              "abc", // clear screen / cursor home
		"tab\tok":                                       "tab\tok",
		"nul\x00bell\x07bs\x08":                         "nulbellbs",
		"<script>alert(1)</script>":                     "<script>alert(1)</script>", // escaped by the UI, not here
		"inválido\xff\xfe":                              "inválido��",
		"c1\u009b31mrojo":                               "c1" + "31mrojo",
		"bidi‮ocult":                                    "bidiocult",
		"ESC al final\x1b":                              "ESC al final",
		"\x1b(Bcharset":                                 "charset",
		"ñandú ✓ 日本":                                    "ñandú ✓ 日本",
	}
	for in, want := range cases {
		if got := Sanitize([]byte(in)); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(realMux, false)
	f.Add([]byte("a\r\nb"), true)
	f.Fuzz(func(t *testing.T, data []byte, tty bool) {
		_ = Parse(bytes.NewReader(data), Options{TTY: tty, Timestamps: true, MaxLineBytes: 64}, func(l Line) bool {
			if len(l.Text) > 64*3 { // sanitizing may expand invalid bytes to U+FFFD (3 bytes)
				t.Fatalf("línea más larga que el máximo: %d", len(l.Text))
			}
			if strings.ContainsAny(l.Text, "\x1b\x00\n\r") {
				t.Fatalf("control sin sanitizar: %q", l.Text)
			}
			return true
		})
	})
}
