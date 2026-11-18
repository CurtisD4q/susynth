package ui

import "strings"

// Editor is a small multi-line text editor (runes, no undo).
type Editor struct {
	lines    [][]rune
	row, col int
	top      int
	left     int
	want     int // preferred column when moving vertically
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

// Block returns the first and last row of the blank-line-delimited block at row.
func (e *Editor) Block(row int) (int, int, bool) {
	blank := func(i int) bool { return strings.TrimSpace(string(e.lines[i])) == "" }
	if row < 0 || row >= len(e.lines) || blank(row) {
		return 0, 0, false
	}
	s, t := row, row
	for s > 0 && !blank(s-1) {
		s--
	}
	for t+1 < len(e.lines) && !blank(t+1) {
		t++
	}
	return s, t, true
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
