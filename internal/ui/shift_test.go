package ui

import "testing"

func TestErrorMarkersFollowLines(t *testing.T) {
	a := &App{
		ed:    newEditor("live_loop :a do\n  play 60\n  sleep 1\nend\n"),
		marks: map[int]bool{2: true},
	}
	a.lineSnap = a.ed.Lines()
	a.errs = []codeErr{{Line: 2, Text: "nope"}}

	// A newline above the error pushes the marker down with the code.
	a.ed.row, a.ed.col = 0, 0
	a.ed.Newline()
	a.edited()
	if len(a.errs) != 1 || a.errs[0].Line != 3 {
		t.Fatalf("after insert: %+v", a.errs)
	}
	if !a.marks[3] || a.marks[2] {
		t.Fatalf("running mark after insert: %v", a.marks)
	}

	// Deleting the blank line pulls it back up.
	a.ed.row = 0
	a.ed.KillLine()
	a.edited()
	if len(a.errs) != 1 || a.errs[0].Line != 2 {
		t.Fatalf("after delete above: %+v", a.errs)
	}

	// Editing the line itself, without changing the line count, keeps the marker.
	a.ed.row, a.ed.col = 2, len([]rune(a.ed.Line(2)))
	a.ed.Insert('x')
	a.edited()
	if len(a.errs) != 1 || a.errs[0].Line != 2 {
		t.Fatalf("after same-line edit: %+v", a.errs)
	}

	// Deleting the marked line drops the marker. The log is separate.
	a.ed.row = 2
	a.ed.KillLine()
	a.edited()
	if len(a.errs) != 0 {
		t.Fatalf("deleted line kept a marker: %+v", a.errs)
	}
}

func TestBlockHighlightsTheWholeLoop(t *testing.T) {
	e := newEditor("use_bpm 120\nlive_loop :drums do\n  sample :bd_haus\n\n  sleep 0.5\nend\n")
	s, last, ok := e.Block(4) // the blank line inside the loop
	if !ok || s != 1 || last != 5 {
		t.Fatalf("block = %d–%d ok=%v", s, last, ok)
	}
	s, last, ok = e.Block(0)
	if !ok || s != 0 || last != 0 {
		t.Fatalf("use_bpm block = %d–%d ok=%v", s, last, ok)
	}
}
