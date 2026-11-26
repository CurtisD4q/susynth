package ui

// Boxes.
//
// Each pane is drawn in a box of its own with its title and a few words
// about it on the line above, the way Mint's other programs draw theirs:
//
//	Editor · ^E run block · Esc-Enter run all     Tracks · 2 playing
//	╭─────────────────────────────────────────╮╭─────────────────────╮
//
// A pop-up is a question, not a pane: its title stays in its top border.
//
// The box that takes the keys (the editor, on the Play page) is in the
// accent colour; the others are muted. Box lines are never bold, so the
// large and contrast themes, which bold everything else, keep them light
// and the code stays the brightest thing on a projector.

// boxLine is the style of a box's lines.
func (a *App) boxLine(focused bool) style {
	th := a.th
	fg := th.Muted.fg
	if focused {
		fg = th.Pane.fg
	}
	return style{fg: fg, bg: th.Base.bg}
}

// drawBox draws a box whose corners are (x0, y0) and (x1, y1), with title
// and info set into the top border. Nothing is drawn inside it.
func (a *App) drawBox(f *frame, x0, y0, x1, y1 int, title, info string, focused bool) {
	if x1-x0 < 4 || y1 <= y0 {
		return
	}
	th := a.th
	line := a.boxLine(focused)
	f.hline(x0+1, y0, x1-x0-1, '─', line)
	f.hline(x0+1, y1, x1-x0-1, '─', line)
	f.vline(x0, y0+1, y1-y0-1, '│', line)
	f.vline(x1, y0+1, y1-y0-1, '│', line)
	f.put(x0, y0, '╭', line)
	f.put(x1, y0, '╮', line)
	f.put(x0, y1, '╰', line)
	f.put(x1, y1, '╯', line)

	// " Title · info " from the third column, leaving at least one cell of
	// line before the corner.
	end := x1 - 1 // the last cell the label may use
	x := x0 + 2
	if title == "" || end-x < 3 {
		return
	}
	ts := style{fg: th.Text.fg, bg: th.Base.bg, bold: true}
	if focused {
		ts = th.Pane
	}
	f.put(x, y0, ' ', th.Base)
	x = f.text(x+1, y0, title, ts, end-x-1)
	if info != "" && end-x >= 6 {
		x = f.text(x, y0, " · ", th.Muted, end-x)
		x = f.text(x, y0, info, th.Muted, end-x)
	}
	if x <= end {
		f.put(x, y0, ' ', th.Base)
	}
}

// drawPane draws a pane's box from (x0, y0) to (x1, y1) with its title and
// info on the row above, lined up with the box's left edge.
func (a *App) drawPane(f *frame, x0, y0, x1, y1 int, title, info string, focused bool) {
	a.drawBox(f, x0, y0, x1, y1, "", "", focused)
	if title == "" || y0 < 1 || x1-x0 < 4 {
		return
	}
	th := a.th
	ts := style{fg: th.Text.fg, bg: th.Base.bg, bold: true}
	if focused {
		ts = th.Pane
	}
	end := x1 // the title may run to the box's right edge
	x := f.text(x0+1, y0-1, title, ts, end-x0)
	if info != "" && end-x >= 6 {
		x = f.text(x, y0-1, " · ", th.Muted, end-x)
		f.text(x, y0-1, info, th.Muted, end-x)
	}
}
