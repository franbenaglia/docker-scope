package logs

import (
	"strings"
	"testing"
)

func TestBufferBoundsAndDrops(t *testing.T) {
	b := NewBuffer(3, 1000, 2)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		b.Push(Line{Text: s})
	}
	lines, dropped := b.Take(10)
	if dropped != 2 || len(lines) != 3 || lines[0].Text != "c" {
		t.Errorf("tomadas=%v descartadas=%d", lines, dropped)
	}
	if _, d := b.Take(10); d != 0 {
		t.Error("el contador de descartes se reinicia al tomar")
	}

	b = NewBuffer(100, 10, 50) // by bytes
	b.Push(Line{Text: strings.Repeat("x", 6)})
	b.Push(Line{Text: strings.Repeat("y", 6)})
	lines, dropped = b.Take(10)
	if dropped != 1 || len(lines) != 1 || lines[0].Text[0] != 'y' {
		t.Errorf("por bytes: %v %d", lines, dropped)
	}
	big := NewBuffer(100, 10, 50)
	big.Push(Line{Text: strings.Repeat("z", 50)}) // a single oversized line is kept
	if big.Len() != 1 {
		t.Error("una sola línea grande no se descarta")
	}
}

func TestBufferReadySignal(t *testing.T) {
	b := NewBuffer(10, 1000, 2)
	b.Push(Line{Text: "1"})
	select {
	case <-b.Ready():
		t.Fatal("no hay lote todavía")
	default:
	}
	b.Push(Line{Text: "2"})
	select {
	case <-b.Ready():
	default:
		t.Fatal("lote listo")
	}
}
