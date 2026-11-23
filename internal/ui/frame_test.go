package ui

import (
	"strings"
	"testing"
)

func row(f *frame, y int) string {
	var b strings.Builder
	for x := 0; x < f.w; x++ {
		b.WriteRune(f.cells[y*f.w+x].r)
	}
	return strings.TrimRight(b.String(), " ")
}

// A file name or Ruby message cannot reach the terminal as escape
// sequences or control characters.
func TestFrameTextIsCleaned(t *testing.T) {
	f := newFrame(40, 2, style{})
	f.text(0, 0, "set\x1b[2Jname\x07.rb", style{}, 0)
	if got := row(f, 0); got != "setname.rb" {
		t.Fatalf("%q", got)
	}
	f.put(0, 1, '\x1b', style{})
	if f.cells[f.w].r == '\x1b' {
		t.Fatal("put let a raw ESC into the frame")
	}
}

func TestPadClips(t *testing.T) {
	if got := pad("recording.wav", 6); got != "recor…" {
		t.Fatalf("%q", got)
	}
	if got := pad("ab", 4); got != "ab  " {
		t.Fatalf("%q", got)
	}
}
