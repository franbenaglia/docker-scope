package logs

import (
	"strings"
	"unicode/utf8"
)

// Sanitize makes log text safe to display as plain text:
//   - ANSI escape sequences (CSI such as colors and cursor moves, OSC such as
//     window titles or hyperlinks, and other ESC sequences) are removed;
//   - other control characters are removed, except tab;
//   - invalid UTF-8 becomes U+FFFD.
//
// HTML is not escaped here: the panel renders text nodes, never HTML.
func Sanitize(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c == 0x1b { // ESC
			i = skipEscape(b, i)
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			sb.WriteRune(utf8.RuneError)
		case r == '\t':
			sb.WriteByte('\t')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// C0/C1 controls (C1 includes the 8-bit CSI 0x9b): dropped.
		case r == ' ' || r == ' ' || r == '‮' || r == '‭':
			// Line separators and bidi overrides could disguise content.
		default:
			sb.WriteRune(r)
		}
		i += max(size, 1)
	}
	return sb.String()
}

// skipEscape returns the index after the escape sequence starting at b[i].
func skipEscape(b []byte, i int) int {
	i++ // ESC
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '[': // CSI: parameters/intermediates then a final byte in 0x40–0x7e
		i++
		for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
			i++
		}
		return min(i+1, len(b))
	case ']', 'P', '_', '^', 'X': // OSC/DCS/APC/PM/SOS: until BEL or ST (ESC \)
		i++
		for i < len(b) {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i
	default: // two-byte sequences (e.g. ESC c, ESC 7) and charset selections
		if b[i] >= 0x20 && b[i] <= 0x2f && i+1 < len(b) { // intermediate + final
			return i + 2
		}
		return i + 1
	}
}
