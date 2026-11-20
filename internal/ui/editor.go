package ui

import (
	"strings"

	"github.com/susynth/susynth/internal/rubyvm"
)

// Editor is a small multi-line text editor (runes) with a selection and undo.
type Editor struct {
	lines    [][]rune
	row, col int
	top      int
	left     int
	want     int // preferred column when moving vertically

	anchor    pos // the other end of the selection
	selecting bool

	undo, redo []snapshot
	lastEdit   string
}

func newEditor(text string) *Editor {
	e := &Editor{}
	e.SetText(text)
	return e
}

func (e *Editor) SetText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	e.lines = nil
	for _, l := range strings.Split(s, "\n") {
		e.lines = append(e.lines, []rune(l))
	}
	e.row, e.col, e.top, e.left, e.want = 0, 0, 0, 0, 0
	e.selecting = false
}

func (e *Editor) Text() string {
	parts := make([]string, len(e.lines))
	for i, l := range e.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n") + "\n"
}

func (e *Editor) Line(i int) string {
	if i < 0 || i >= len(e.lines) {
		return ""
	}
	return string(e.lines[i])
}

// Lines is a copy of the buffer, one string per row.
func (e *Editor) Lines() []string {
	out := make([]string, len(e.lines))
	for i, l := range e.lines {
		out[i] = string(l)
	}
	return out
}

func (e *Editor) Insert(r rune) {
	l := e.lines[e.row]
	l = append(l, 0)
	copy(l[e.col+1:], l[e.col:])
	l[e.col] = r
	e.lines[e.row] = l
	e.col++
	e.want = e.col
}

func (e *Editor) Newline() {
	l := e.lines[e.row]
	tail := append([]rune(nil), l[e.col:]...)
	e.lines[e.row] = append([]rune(nil), l[:e.col]...)
	e.lines = append(e.lines, nil)
	copy(e.lines[e.row+2:], e.lines[e.row+1:])
	e.lines[e.row+1] = tail
	e.row++
	e.col, e.want = 0, 0
}

func (e *Editor) Backspace() {
	if e.col > 0 {
		l := e.lines[e.row]
		e.lines[e.row] = append(l[:e.col-1], l[e.col:]...)
		e.col--
	} else if e.row > 0 {
		prev := e.lines[e.row-1]
		e.col = len(prev)
		e.lines[e.row-1] = append(prev, e.lines[e.row]...)
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		e.row--
	}
	e.want = e.col
}

func (e *Editor) Delete() {
	l := e.lines[e.row]
	if e.col < len(l) {
		e.lines[e.row] = append(l[:e.col], l[e.col+1:]...)
	} else if e.row+1 < len(e.lines) {
		e.lines[e.row] = append(l, e.lines[e.row+1]...)
		e.lines = append(e.lines[:e.row+1], e.lines[e.row+2:]...)
	}
}

func (e *Editor) KillLine() {
	if len(e.lines) == 1 {
		e.lines[0] = nil
	} else {
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		if e.row >= len(e.lines) {
			e.row = len(e.lines) - 1
		}
	}
	e.clamp()
}

func (e *Editor) clamp() {
	if e.col > len(e.lines[e.row]) {
		e.col = len(e.lines[e.row])
	}
}

func (e *Editor) Left() {
	if e.col > 0 {
		e.col--
	} else if e.row > 0 {
		e.row--
		e.col = len(e.lines[e.row])
	}
	e.want = e.col
}

func (e *Editor) Right() {
	if e.col < len(e.lines[e.row]) {
		e.col++
	} else if e.row+1 < len(e.lines) {
		e.row++
		e.col = 0
	}
	e.want = e.col
}

func (e *Editor) Vert(d int) {
	e.row += d
	if e.row < 0 {
		e.row = 0
	}
	if e.row >= len(e.lines) {
		e.row = len(e.lines) - 1
	}
	e.col = e.want
	e.clamp()
}

func (e *Editor) Home() { e.col, e.want = 0, 0 }
func (e *Editor) End()  { e.col = len(e.lines[e.row]); e.want = e.col }

// Block returns the 0-based rows of the statement ^E would run with the cursor
// on row: a whole do/end or live_loop, blank lines inside it included.
func (e *Editor) Block(row int) (int, int, bool) {
	first, last, ok := rubyvm.BlockRange(e.Text(), row+1)
	if !ok {
		return 0, 0, false
	}
	return first - 1, last - 1, true
}

// scroll keeps the cursor inside a w x h text viewport.
func (e *Editor) scroll(w, h int) {
	if e.row < e.top {
		e.top = e.row
	}
	if e.row >= e.top+h {
		e.top = e.row - h + 1
	}
	if e.col < e.left {
		e.left = e.col
	}
	if e.col >= e.left+w-1 {
		e.left = e.col - w + 2
	}
}

// SetLine replaces one line's text and keeps the cursor on it.
func (e *Editor) SetLine(i int, text string) {
	if i < 0 || i >= len(e.lines) {
		return
	}
	e.lines[i] = []rune(text)
	if e.row == i {
		e.clamp()
	}
}

// ---- selection --------------------------------------------------------------

// pos is a place in the text: row and column (in runes).
type pos struct{ row, col int }

func (a pos) before(b pos) bool { return a.row < b.row || a.row == b.row && a.col < b.col }

// Mark starts a selection at the cursor if there isn't one.
func (e *Editor) Mark() {
	if !e.selecting {
		e.anchor, e.selecting = pos{e.row, e.col}, true
	}
}

// Unmark drops the selection (the text stays).
func (e *Editor) Unmark() { e.selecting = false }

// Selection returns the ordered selection, and false when nothing is selected.
func (e *Editor) Selection() (pos, pos, bool) {
	if !e.selecting {
		return pos{}, pos{}, false
	}
	a, b := e.anchor, pos{e.row, e.col}
	if b.before(a) {
		a, b = b, a
	}
	return a, b, a != b
}

// Selected reports whether the character at (row, col) is selected; col may
// be one past the end of the line to mean "the line break".
func (e *Editor) Selected(row, col int) bool {
	a, b, ok := e.Selection()
	if !ok {
		return false
	}
	p := pos{row, col}
	return !p.before(a) && p.before(b)
}

// SelectAll selects the whole text.
func (e *Editor) SelectAll() {
	e.anchor, e.selecting = pos{0, 0}, true
	e.row = len(e.lines) - 1
	e.col = len(e.lines[e.row])
	e.want = e.col
}

// textBetween returns the text from a up to (not including) b.
func (e *Editor) textBetween(a, b pos) string {
	if a.row == b.row {
		return string(e.lines[a.row][a.col:b.col])
	}
	var sb strings.Builder
	sb.WriteString(string(e.lines[a.row][a.col:]))
	for r := a.row + 1; r < b.row; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.lines[r]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(e.lines[b.row][:b.col]))
	return sb.String()
}

// SelectedText is the selected text, or "".
func (e *Editor) SelectedText() string {
	a, b, ok := e.Selection()
	if !ok {
		return ""
	}
	return e.textBetween(a, b)
}

// DeleteSelection removes the selected text and reports whether there was any.
func (e *Editor) DeleteSelection() bool {
	a, b, ok := e.Selection()
	e.selecting = false
	if !ok {
		return false
	}
	head := append([]rune(nil), e.lines[a.row][:a.col]...)
	tail := e.lines[b.row][b.col:]
	e.lines[a.row] = append(head, tail...)
	e.lines = append(e.lines[:a.row+1], e.lines[b.row+1:]...)
	e.row, e.col, e.want = a.row, a.col, a.col
	return true
}

// InsertText inserts text (which may span lines) at the cursor, replacing
// any selection. Tabs become two spaces and Windows line ends are tidied.
func (e *Editor) InsertText(s string) {
	e.DeleteSelection()
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "  ")
	parts := strings.Split(s, "\n")
	l := e.lines[e.row]
	head := append([]rune(nil), l[:e.col]...)
	tail := append([]rune(nil), l[e.col:]...)
	if len(parts) == 1 {
		e.lines[e.row] = append(append(head, []rune(parts[0])...), tail...)
		e.col += len([]rune(parts[0]))
		e.want = e.col
		return
	}
	newLines := make([][]rune, 0, len(parts))
	newLines = append(newLines, append(head, []rune(parts[0])...))
	for _, p := range parts[1 : len(parts)-1] {
		newLines = append(newLines, []rune(p))
	}
	last := []rune(parts[len(parts)-1])
	newLines = append(newLines, append(append([]rune(nil), last...), tail...))
	rest := append([][]rune(nil), e.lines[e.row+1:]...)
	e.lines = append(append(e.lines[:e.row], newLines...), rest...)
	e.row += len(parts) - 1
	e.col, e.want = len(last), len(last)
}

// ---- undo ---------------------------------------------------------------------

type snapshot struct {
	text     string
	row, col int
}

// Checkpoint saves the text before an edit so ^Z can bring it back. Edits of
// the same kind in a row ("type", typing one character after another) share
// one checkpoint, so undo removes a word's worth of typing, not one letter.
func (e *Editor) Checkpoint(kind string) {
	if kind != "" && kind == e.lastEdit {
		return
	}
	e.lastEdit = kind
	e.undo = append(e.undo, snapshot{e.Text(), e.row, e.col})
	if len(e.undo) > 300 {
		e.undo = e.undo[len(e.undo)-300:]
	}
	e.redo = nil
}

// BreakRun ends a run of same-kind edits (the cursor moved, say).
func (e *Editor) BreakRun() { e.lastEdit = "" }

func (e *Editor) restore(s snapshot) {
	top, left := e.top, e.left
	e.SetText(s.text)
	e.row = min(s.row, len(e.lines)-1)
	e.col = s.col
	e.clamp()
	e.want, e.top, e.left = e.col, top, left
}

// Undo goes back one checkpoint; it reports false when there is nothing to undo.
func (e *Editor) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	e.redo = append(e.redo, snapshot{e.Text(), e.row, e.col})
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.restore(s)
	e.lastEdit, e.selecting = "", false
	return true
}

// Redo re-applies what Undo took back.
func (e *Editor) Redo() bool {
	if len(e.redo) == 0 {
		return false
	}
	e.undo = append(e.undo, snapshot{e.Text(), e.row, e.col})
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.restore(s)
	e.lastEdit, e.selecting = "", false
	return true
}
