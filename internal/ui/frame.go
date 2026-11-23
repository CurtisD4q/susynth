package ui

import (
	"github.com/CurtisD4q/pmin-base/clean"

	"fmt"
	"strings"
)

// style is one cell's look. fg/bg are 256-colour indexes, -1 = terminal default.
type style struct {
	fg, bg int16
	bold   bool
	rev    bool
	dim    bool
}

type cell struct {
	r rune
	s style
}

// frame is an off-screen grid; only changed rows are sent to the terminal.
type frame struct {
	w, h  int
	cells []cell
}

func newFrame(w, h int, base style) *frame {
	f := &frame{w: w, h: h, cells: make([]cell, w*h)}
	for i := range f.cells {
		f.cells[i] = cell{' ', base}
	}
	return f
}

func (f *frame) put(x, y int, r rune, s style) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) { // a control character would reach the terminal raw
		r = '·'
	}
	f.cells[y*f.w+x] = cell{r, s}
}

// text draws str at (x,y) clipped to maxw columns (0 = to the edge). It
// returns the x after the last drawn rune. A clipped string ends in "…".
// Names, messages and log lines can come from files or from Ruby, so the
// text is cleaned first: escape sequences and control characters cannot
// reach the terminal, and characters that reorder text are shown.
func (f *frame) text(x, y int, str string, s style, maxw int) int {
	str = clean.Line(str)
	if maxw <= 0 || x+maxw > f.w {
		maxw = f.w - x
	}
	rs := []rune(str)
	if len(rs) > maxw {
		if maxw <= 0 {
			return x
		}
		rs = append(rs[:maxw-1], '…')
	}
	for i, r := range rs {
		f.put(x+i, y, r, s)
	}
	return x + len(rs)
}

func (f *frame) fill(x, y, w, h int, s style) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			f.put(xx, yy, ' ', s)
		}
	}
}

func (f *frame) hline(x, y, w int, r rune, s style) {
	for i := 0; i < w; i++ {
		f.put(x+i, y, r, s)
	}
}

func (f *frame) vline(x, y, h int, r rune, s style) {
	for i := 0; i < h; i++ {
		f.put(x, y+i, r, s)
	}
}

func sgr(s style) string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	if s.bold {
		b.WriteString(";1")
	}
	if s.dim {
		b.WriteString(";2")
	}
	if s.rev {
		b.WriteString(";7")
	}
	if s.fg >= 0 {
		fmt.Fprintf(&b, ";38;5;%d", s.fg)
	}
	if s.bg >= 0 {
		fmt.Fprintf(&b, ";48;5;%d", s.bg)
	}
	b.WriteByte('m')
	return b.String()
}

// rows serialises each row to a self-contained ANSI string.
func (f *frame) rows() []string {
	out := make([]string, f.h)
	var b strings.Builder
	for y := 0; y < f.h; y++ {
		b.Reset()
		var cur style
		first := true
		for x := 0; x < f.w; x++ {
			c := f.cells[y*f.w+x]
			if first || c.s != cur {
				b.WriteString(sgr(c.s))
				cur, first = c.s, false
			}
			b.WriteRune(c.r)
		}
		out[y] = b.String()
	}
	return out
}

// tint recolours one cell (selection highlight), keeping its character.
func (f *frame) tint(x, y int, s style) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	f.cells[y*f.w+x].s = s
}
