// Package clean makes text from a program safe and tidy to show: it strips
// terminal escape sequences and control characters, expands tabs, resolves
// backspace overstrikes and marks characters that reorder or hide text. It
// has no interface code, so libraries that only read program output can use
// it without the rest of mint-kit.
package clean

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// ANSI matches a terminal escape sequence.
var ANSI = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// Line strips terminal escape sequences and control characters, expands
// tabs to 8-column stops, and resolves backspace overstrikes (\b / 0x08, as
// produced by man pages like N\bNA\bAM\bME\bE or _\bX).
//
// A non-breaking space (U+00A0) becomes an ordinary space. Programs use it
// as padding that must not break - tree(1) indents with it - but Mint
// measures and splits text by cells and spaces, so the two must be the same
// character downstream. It is a plain blank either way on screen.
//
// Text from a program is untrusted, so CleanLine also:
//   - drops C1 control characters (U+0080–U+009F). Some terminals treat
//     U+009B as an escape sequence introducer, the same as ESC [;
//   - drops the Unicode line and paragraph separators (U+2028, U+2029);
//   - shows bidirectional embedding, override and isolate controls
//     (U+202A–U+202E, U+2066–U+2069) as a visible marker such as <U+202E>
//     instead of letting them reorder what is displayed ("Trojan Source").
//
// The directional marks LRM, RLM and ALM, and the joiners ZWJ and ZWNJ,
// are kept: real Arabic, Hebrew, Persian, Indic and emoji text needs them.
// LineVisible also marks invisible characters, for views where two
// lines that look the same must not hide a difference.
func Line(raw string) string {
	return cleanLine(raw, false)
}

// LineVisible is Line for views that compare or audit text (diffs,
// findings, file previews, rule lists): zero-width and other invisible
// characters are shown as markers too, so nothing in the line is hidden.
func LineVisible(raw string) string {
	return cleanLine(raw, true)
}

// IsBidiControl reports whether r is a bidirectional embedding, override or
// isolate control, which changes the displayed order of the text around it.
func IsBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

// IsInvisible reports whether r is a format character that takes no space
// and has no legitimate use that would be lost by showing it as a marker.
func IsInvisible(r rune) bool {
	switch r {
	case 0x200B, // zero width space
		0x2060, // word joiner
		0xFEFF, // zero width no-break space (BOM)
		0x180E, // Mongolian vowel separator
		0x00AD: // soft hyphen
		return true
	}
	return false
}

// Marker is how a hidden or reordering character is shown: <U+202E>.
func Marker(r rune) string {
	return fmt.Sprintf("<U+%04X>", r)
}

func cleanLine(raw string, visible bool) string {
	out, _ := From(raw, visible, 0)
	return out
}

// From is Line for a piece of a line that starts at column
// col (tab stops are counted from the start of the line). It returns the
// column after the piece.
func From(raw string, visible bool, col int) (string, int) {
	s := ANSI.ReplaceAllString(raw, "")
	var out []rune
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\b':
			if len(out) > 0 && col > 0 {
				out = out[:len(out)-1]
				col--
			}
		case r == '\t':
			spaces := 8 - (col % 8)
			for j := 0; j < spaces; j++ {
				out = append(out, ' ')
			}
			col += spaces
		case r == '\u00a0':
			// Padding that must not break, which Mint cannot tell from a
			// space once it is measuring cells.
			out = append(out, ' ')
			col++
		case r == '\r' || r == '\n':
			// dropped
		case r < 32 || r == 0x7f:
			// drop other control characters
		case r >= 0x80 && r <= 0x9f:
			// drop C1 control characters (U+009B is CSI on some terminals)
		case r == 0x2028 || r == 0x2029:
			// drop line and paragraph separators
		case IsBidiControl(r) || (visible && IsInvisible(r)):
			m := Marker(r)
			out = append(out, []rune(m)...)
			col += len(m)
		case r == utf8.RuneError && size == 1:
			// drop invalid UTF-8 byte
		default:
			out = append(out, r)
			col++
		}
	}
	return string(out), col
}
