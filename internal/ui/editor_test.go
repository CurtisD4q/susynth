package ui

import (
	"testing"
	"time"
)

func TestSelectCutPasteAcrossLines(t *testing.T) {
	e := newEditor("live_loop :a do\n  play 60\n  sleep 1\nend\n")
	e.row, e.col = 1, 2 // start of "play 60"
	e.Mark()
	e.Vert(1)
	e.End() // through "sleep 1"
	if got := e.SelectedText(); got != "play 60\n  sleep 1" {
		t.Fatalf("selected %q", got)
	}
	if !e.Selected(1, 7) || e.Selected(0, 3) || e.Selected(2, 9) {
		t.Error("Selected() disagrees with the selection")
	}
	cut := e.SelectedText()
	e.DeleteSelection()
	if e.Text() != "live_loop :a do\n  \nend\n" || e.row != 1 || e.col != 2 {
		t.Fatalf("after cut %q at %d,%d", e.Text(), e.row, e.col)
	}
	e.InsertText(cut)
	if e.Text() != "live_loop :a do\n  play 60\n  sleep 1\nend\n" {
		t.Fatalf("after paste %q", e.Text())
	}
	if e.row != 2 || e.col != 9 {
		t.Errorf("cursor after paste %d,%d", e.row, e.col)
	}
}

func TestPasteReplacesSelectionAndTidiesText(t *testing.T) {
	e := newEditor("play 60\n")
	e.col = 5
	e.Mark()
	e.End()
	e.InsertText("72\r\n\tsleep 1")
	if e.Text() != "play 72\n  sleep 1\n" {
		t.Fatalf("got %q", e.Text())
	}
}

func TestSelectAllAndUndoRedo(t *testing.T) {
	e := newEditor("a\nb\n")
	e.SelectAll()
	if e.SelectedText() != "a\nb" {
		t.Fatalf("select all %q", e.SelectedText())
	}
	e.Checkpoint("")
	e.DeleteSelection()
	e.Checkpoint("type")
	e.Insert('x')
	e.Checkpoint("type") // same run: no new checkpoint
	e.Insert('y')
	if e.Text() != "xy\n" {
		t.Fatalf("got %q", e.Text())
	}
	e.Undo()
	if e.Text() != "\n" {
		t.Errorf("undo typing: %q", e.Text())
	}
	e.Undo()
	if e.Text() != "a\nb\n" {
		t.Errorf("undo delete: %q", e.Text())
	}
	if e.Undo() {
		t.Error("nothing left to undo")
	}
	e.Redo()
	e.Redo()
	if e.Text() != "xy\n" {
		t.Errorf("redo: %q", e.Text())
	}
}

func feed(s string) func(time.Duration) (byte, bool) {
	b := []byte(s)
	return func(time.Duration) (byte, bool) {
		if len(b) == 0 {
			return 0, false
		}
		c := b[0]
		b = b[1:]
		return c, true
	}
}

func TestShiftArrowsAndBracketedPaste(t *testing.T) {
	for in, want := range map[string]key{
		"1;2A": {kind: kUp, shift: true},
		"1;2D": {kind: kLeft, shift: true},
		"1;2H": {kind: kHome, shift: true},
		"5;2~": {kind: kPgUp, shift: true},
		"1;5C": {kind: kRight}, // Ctrl+Right: not a selection
		"B":    {kind: kDown},
	} {
		if got := parseCSI(feed(in)); got != want {
			t.Errorf("%q: got %+v want %+v", in, got, want)
		}
	}
	got := parseCSI(feed("200~live_loop :a do\r  play 60\x1b[201~"))
	if got.kind != kPaste || got.text != "live_loop :a do\r  play 60" {
		t.Errorf("paste: %+v", got)
	}
}
