package ui

// Column lists.
//
// A list of sets, recordings or the set list is drawn in columns: a bold
// heading over a short rule for each column, numbers lined up on the right,
// details in the muted colour, and a mark in the margin for a row's state,
// such as ● for the set that is open.

type column struct {
	title string
	width int  // cells; the last column takes what is left
	right bool // numbers and times line up on the right
	dim   bool // a detail beside the column that matters
}

// mark is a row's state, drawn in the margin before it. A zero mark is a
// blank margin.
type mark struct {
	r rune
	s style
}

// drawTable draws a column list in the box area at (x, y), w by h, with the
// row sel selected. It scrolls with a.ps.top so the selection stays in view.
func (a *App) drawTable(f *frame, x, y, w, h int, cols []column, rows [][]string, marks []mark, sel int) {
	th := a.th
	if len(cols) == 0 || h < 3 {
		return
	}
	// The cursor is in column x+1; a margin for marks follows if any row
	// has one.
	left := x + 2
	hasMarks := false
	for _, m := range marks {
		if m.r != 0 {
			hasMarks = true
			break
		}
	}
	if hasMarks {
		left = x + 4
	}
	end := x + w - 1 // the last cell a row may use
	widths := make([]int, len(cols))
	cx := left
	for i, c := range cols {
		wd := c.width
		// The last column takes the rest of the row, unless it is lined up
		// on the right: then it keeps its width, next to the others.
		if (i == len(cols)-1 && !c.right) || cx+wd > end {
			wd = end - cx + 1
		}
		if wd < 0 {
			wd = 0
		}
		widths[i] = wd
		cx += wd + 2
	}
	cell := func(yy, i, cx int, s string, st style) {
		wd := widths[i]
		if wd <= 0 {
			return
		}
		if cols[i].right {
			if n := len([]rune(s)); n < wd {
				cx += wd - n
			}
		}
		f.text(cx, yy, s, st, wd)
	}
	// Headings, and a rule under each.
	heading := style{fg: th.Text.fg, bg: th.Base.bg, bold: true}
	cx = left
	for i, c := range cols {
		cell(y, i, cx, c.title, heading)
		n := len([]rune(c.title))
		if i < len(cols)-1 || n > widths[i] {
			n = widths[i]
		}
		rx := cx
		if c.right {
			rx = cx + widths[i] - n
		}
		f.hline(rx, y+1, n, '─', a.boxLine(false))
		cx += widths[i] + 2
	}

	visible := h - 2
	if sel < a.ps.top {
		a.ps.top = sel
	}
	if sel >= a.ps.top+visible {
		a.ps.top = sel - visible + 1
	}
	if a.ps.top < 0 {
		a.ps.top = 0
	}
	for i := 0; i < visible; i++ {
		j := a.ps.top + i
		if j >= len(rows) {
			break
		}
		yy := y + 2 + i
		base := th.Text
		if j == sel {
			f.fill(x, yy, w, 1, th.CursorRow)
			base = th.CursorRow
			f.put(x+1, yy, '›', style{fg: th.Keyword.fg, bg: base.bg, bold: true})
		}
		if hasMarks && j < len(marks) && marks[j].r != 0 {
			m := marks[j].s
			m.bg = base.bg
			f.put(x+2, yy, marks[j].r, m)
		}
		cx = left
		for c := range cols {
			v := ""
			if c < len(rows[j]) {
				v = rows[j][c]
			}
			st := base
			if cols[c].dim && j != sel {
				st = style{fg: th.Muted.fg, bg: base.bg}
			}
			cell(yy, c, cx, v, st)
			cx += widths[c] + 2
		}
	}
}
