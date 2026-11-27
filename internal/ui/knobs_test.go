package ui

import (
	"strings"
	"testing"

	"github.com/susynth/susynth/internal/engine"
)

func TestNumberAt(t *testing.T) {
	for _, c := range []struct {
		line string
		col  int
		want string
	}{
		{"  play 60, amp: 0.8", 8, "60"},
		{"  play 60, amp: 0.8", 9, "60"}, // just after it
		{"  play 60, amp: 0.8", 17, "0.8"},
		{"  sleep 0.25", 12, "0.25"},
		{"  play :e3", 9, ""},
		{"  sample :drum_tom2", 18, ""},
		{"  pan: -0.5", 8, "-0.5"},
		{"  x = a - 5", 11, "5"},
		{"  3.times do", 2, "3"},
		{"  play 60", 3, ""},
	} {
		s, e, ok := numberAt([]rune(c.line), c.col)
		got := ""
		if ok {
			got = c.line[s:e]
		}
		if got != c.want {
			t.Errorf("%q at %d: %q, want %q", c.line, c.col, got, c.want)
		}
	}
	for _, c := range []struct {
		in  string
		dir int
		big bool
		out string
	}{
		{"60", 1, false, "61"}, {"60", -1, true, "50"}, {"0.8", 1, false, "0.9"},
		{"0.25", -1, false, "0.24"}, {"0.1", -1, false, "0.0"}, {"-0.5", 1, true, "0.5"}, {"0", -1, false, "-1"},
	} {
		if got, _ := nudged(c.in, c.dir, c.big); got != c.out {
			t.Errorf("nudged(%s, %d, %v) = %s, want %s", c.in, c.dir, c.big, got, c.out)
		}
	}
}

func TestNudgeChangesTheNumberAndUndoes(t *testing.T) {
	a := testApp()
	a.ed.row, a.ed.col = 2, 9 // "  play 60" with the cursor after 60
	a.handleKey(key{kind: kUp, ctrl: true})
	a.handleKey(key{kind: kUp, ctrl: true})
	a.handleKey(key{kind: kDown, ctrl: true, shift: true})
	if got := a.ed.Line(2); got != "  play 52" {
		t.Fatalf("line %q", got)
	}
	if !strings.Contains(a.status, "62 → 52") {
		t.Fatalf("status %q", a.status)
	}
	a.ed.Undo()
	if got := a.ed.Line(2); got != "  play 60" {
		t.Fatalf("one undo should take every nudge back: %q", got)
	}
	// Esc then ↑ does the same where Ctrl+↑ is taken
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kUp})
	if got := a.ed.Line(2); got != "  play 61" || a.ed.row != 2 {
		t.Fatalf("Esc ↑: line %q row %d", got, a.ed.row)
	}
	// off a number, Ctrl+↑ says how it works and the cursor stays
	a.ed.row, a.ed.col = 1, 3
	a.handleKey(key{kind: kUp, ctrl: true})
	if a.ed.row != 1 || !strings.Contains(a.status, "put the cursor on a number") {
		t.Fatalf("row %d status %q", a.ed.row, a.status)
	}
}

func TestLitRowsFollowEdits(t *testing.T) {
	a := testApp()
	a.eng.Play()
	a.eng.ThreadStarted("bass", true)
	a.runStarted(7)
	a.lineSnap = a.ed.Lines()
	// two lines added above the loop after it ran
	a.ed.row, a.ed.col = 0, 0
	a.ed.InsertText("# a\n# b\n")
	a.edited()
	h := engine.Hit{At: 100, Track: "bass", Word: "60", Line: 3, Run: 7}
	h.Params.Gain = 1
	h.Params.Wave, h.Params.Note = "sine", 60
	a.eng.Schedule(h)
	a.eng.Render(engine.SampleRate / 50)
	lit := a.litRows()
	if _, ok := lit[4]; !ok || len(lit) != 1 {
		t.Fatalf("lit rows %v, want row 4 (line 3 moved down two)", lit)
	}
	if s, e := litSpan([]rune(a.ed.Line(4))); a.ed.Line(4)[s:e] != "play 60" {
		t.Fatalf("lit span %q", a.ed.Line(4)[s:e])
	}
	for in, want := range map[string]string{
		"  play (ring :e2, :g2).tick, release: 0.2": "play (ring :e2, :g2).tick",
		"  8.times { sample :hh, amp: 0.3 }":        "sample :hh",
		"  sleep 1":                                 "sleep 1",
	} {
		s, e := litSpan([]rune(in))
		if got := string([]rune(in)[s:e]); got != want {
			t.Errorf("litSpan(%q) = %q, want %q", in, got, want)
		}
	}
	screen := strings.Join(a.drawRows(110), "\n")
	if !strings.Contains(screen, "play 60") {
		t.Fatal(screen)
	}
}
