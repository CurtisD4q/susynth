package ui

import (
	mint "github.com/CurtisD4q/pmin-kit"

	"testing"
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

func TestShiftArrowsAndAltKeys(t *testing.T) {
	for _, c := range []struct {
		in   mint.Key
		want key
	}{
		{mint.Key{Name: "up", Special: true, Shift: true}, key{kind: kUp, shift: true}},
		{mint.Key{Name: "left", Special: true, Shift: true}, key{kind: kLeft, shift: true}},
		{mint.Key{Name: "home", Special: true, Shift: true}, key{kind: kHome, shift: true}},
		{mint.Key{Name: "pgup", Special: true, Shift: true}, key{kind: kPgUp, shift: true}},
		{mint.Key{Name: "right", Special: true, Ctrl: true}, key{kind: kRight, ctrl: true}}, // Ctrl+Right: not a selection
		{mint.Key{Name: "up", Special: true, Alt: true}, key{kind: kUp, alt: true}},
		{mint.Key{Name: "M-m", Special: true}, key{kind: kRune, r: 'm', alt: true}},
		{mint.Key{Name: "M-\r", Special: true}, key{kind: kEnter, alt: true}},
		{mint.Key{Name: "esc", Special: true}, key{kind: kEsc}},
		{mint.Key{Name: "\x05"}, key{kind: kCtrl, r: 'e'}},
		{mint.Key{Name: "\r"}, key{kind: kEnter}},
		{mint.Key{Name: "\x7f"}, key{kind: kBackspace}},
		{mint.Key{Name: "é"}, key{kind: kRune, r: 'é'}},
	} {
		got, ok := keyFrom(&c.in)
		if !ok || got != c.want {
			t.Errorf("%+v: got %+v want %+v", c.in, got, c.want)
		}
	}
}
