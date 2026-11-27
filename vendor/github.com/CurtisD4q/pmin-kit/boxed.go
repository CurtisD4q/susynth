package mint

import (
	"strings"
)

// boxed reports whether the screen draws its panes in boxes. Boxed only
// changes a screen that has panes.
func (s *Screen) boxed() bool { return s != nil && s.Boxed && len(s.Panes) > 0 }

// paneInfo is the live text shown after a pane's title in its box.
func (s *Screen) paneInfo(index int) string {
	var f func() string
	if index <= 0 {
		f = s.TopInfo
	} else if p := s.paneAt(index); p != nil {
		f = p.Info
	}
	if f == nil {
		return ""
	}
	var out string
	protect(func() { out = f() })
	return CleanLine(out)
}

// paneDigit moves focus to pane n when k is the digit n, on a boxed screen
// with that many panes, unless the screen's own Keys use the digit. It
// reports whether it moved focus.
func (s *Screen) paneDigit(k *Key) bool {
	if !s.boxed() || k == nil || k.Special || k.Mouse != nil || len(k.Name) != 1 {
		return false
	}
	d := k.Name[0]
	if d < '1' || d > '9' {
		return false
	}
	n := int(d - '1')
	if n >= s.paneCount() {
		return false
	}
	for _, sk := range s.Keys {
		if sk.Key == k.Name {
			return false
		}
	}
	s.focus = n
	return true
}

// boxCols is the left and right border columns of a pane's box. The
// first box starts in column 2 and the last ends in column w-1, a column
// in from the screen's edges; a box beside another starts in the column
// that pane's rule used, so one blank column separates them.
func boxCols(c colSpan) (left, right int) {
	left = c.x0
	if c.x0 <= 1 {
		left = 2
	}
	return left, c.x0 + c.w - 2
}

// paintBoxes draws each pane of a boxed screen as a title, a rule under
// it, and the pane's rows under the rule. There are no sides or bottom: a
// blank column separates panes side by side, and a blank row panes one
// above the other. The last band runs to the bottom of the content area.
//
//	Queue · 20 · newest            ⏎ opens it
//	──────────────────────────────────────────
//	 row
func (s *Screen) paintBoxes(cv *canvas, at region, use func(*uiState), hooks rowHooks) {
	sp := s.splitSpacing()
	lastBand := lastOpenBand(s.bands)
	bottom := at.top + at.rows - 1
	// With no side to mark the edge, a scrollbar shows only its thumb.
	track := look.ScrollTrack
	look.ScrollTrack = " "
	defer func() { look.ScrollTrack = track }()
	for i, b := range s.bands {
		// The title is on the divider row, the rule under it.
		titleRow := b.divRow
		ruleRow := b.top
		if b.rows <= 0 {
			ruleRow = 0
		}
		lastRow := 0
		if b.rows > 0 {
			// The row a box's bottom used is the blank row before the
			// next band.
			lastRow = b.top + b.rows - 2
		}
		if i == lastBand {
			lastRow = bottom
		}
		for _, c := range b.spans(at.w) {
			left, right := boxCols(c)
			if right-left < 6 {
				continue
			}
			focused := c.index == s.focus
			inner := lastRow - ruleRow
			if ruleRow <= 0 || inner < 1 {
				// A collapsed or hidden pane, or no room for a row: its
				// title alone, so it can still be found and focused.
				if titleRow > 0 {
					paintBoxTitle(cv, titleRow, left, right, s.boxTitle(c.index, false), focused)
				}
				continue
			}
			cv.put(ruleRow, left, boxStyle(focused)+strings.Repeat(look.Edge, right-left+1)+Reset)
			width := right - left + 1
			if f := s.drawAt(c.index); f != nil {
				s.paintCustom(cv.view(left, width), f, ruleRow+1, inner, width, focused)
				if titleRow > 0 {
					paintBoxTitle(cv, titleRow, left, right, s.boxTitle(c.index, focused), focused)
				}
				continue
			}
			pst := s.uiAt(c.index)
			use(pst)
			pst.x0 = left - 1
			preparePane(pst, s.sectionsAt(c.index), width, sp, inner)
			paintRows(cv.view(left, width), pst, pst.items, region{ruleRow + 1, inner, width}, focused, hooks)
			if titleRow > 0 {
				paintBoxTitle(cv, titleRow, left, right, s.boxTitle(c.index, focused), focused)
			}
		}
	}
}

// paintBoxTitle draws a pane's title and info on the line above its rule,
// starting where the rule starts, and what Enter does at the right.
func paintBoxTitle(cv *canvas, row, left, right int, l boxLabel, focused bool) {
	room := right - left + 1
	if room < 1 || l.title == "" {
		return
	}
	ts := C.Text + C.Bold
	if focused {
		ts = C.Accent + C.Bold
	}
	title, info, preview := l.title, l.info, l.tag
	if VLen(title) >= room {
		title, info, preview = Truncate(title, room), "", ""
	}
	left2 := room - VLen(title)
	if preview != "" {
		// The preview needs a gap of two after the title; it gives way to
		// nothing else, so the info is cut first.
		if VLen(preview)+3 > left2 {
			preview = Truncate(preview, left2-3)
			if VLen(preview) < 4 {
				preview = ""
			}
		}
		if preview != "" {
			left2 -= VLen(preview) + 3
		}
	}
	if info != "" {
		info = Truncate(info, left2-3)
		if VLen(info) < 1 {
			info = ""
		}
	}
	text := ts + title + Reset
	if info != "" {
		text += C.Text + " · " + info + Reset
	}
	cv.put(row, left, text)
	if preview != "" {
		cv.put(row, right-VLen(preview), C.Text+preview+Reset)
	}
}

// boxTitle is a pane's header: the title, then any info, and for the
// focused pane what Enter does on its selected row (in tag).
func (s *Screen) boxTitle(index int, focused bool) boxLabel {
	l := boxLabel{title: CleanLine(s.paneTitle(index)), info: s.paneInfo(index)}
	if focused {
		l.tag = enterPreview(selectedRow(s.uiAt(index)))
	}
	// A filter on the pane shows beside its title.
	if st := s.uiAt(index); st != nil && st.filter != "" {
		f := "/ " + CleanLine(st.filter)
		if l.info != "" {
			l.info += " · " + f
		} else {
			l.info = f
		}
	}
	return l
}

type boxLabel struct{ title, info, tag string }

// boxStyle is the colour of a box's lines: the accent for the focused
// pane, the muted colour for the rest, so focus is plain at a glance.
func boxStyle(focused bool) string {
	if focused {
		return C.Accent
	}
	return C.Muted
}

// paintBoxEdge draws a box's top border with the label set into it:
//
//	╭─ Title · info ─────────────────╮
//
// closed is false for a pane drawn as its title line alone; its ends are
// then plain line ends instead of corners.
func paintBoxEdge(cv *canvas, row, left, right int, l boxLabel, focused, closed bool) {
	line := boxStyle(focused)
	lc, rc := look.TopLeft, look.TopRight
	if !closed {
		lc, rc = look.Edge, look.Edge
	}
	inner := right - left - 1 // cells between the corners
	tag := l.tag
	// Room for the label: one edge cell before it, a space each side, and
	// the tag with one edge cell on each side of it.
	tagRoom := 0 // the tag and the edge cell after it
	if tag != "" {
		tagRoom = VLen(tag) + 1
	}
	// The label's text gets what is left after the edge cell before it,
	// its two spaces, the tag, and at least one cell of line.
	room := inner - 4 - tagRoom
	if room < 4 && tag != "" {
		tag, tagRoom = "", 0
		room = inner - 4
	}
	title, info := l.title, l.info
	label := ""
	if room >= 1 && title != "" {
		ts, is := C.Accent+C.Bold, C.Muted
		if !focused {
			ts, is = C.Text+C.Bold, C.Muted
		}
		full := title
		if info != "" {
			full = title + " · " + info
		}
		if VLen(full) > room {
			if VLen(title) >= room {
				title, info = Truncate(title, room), ""
			} else {
				info = Truncate(info, room-VLen(title)-3)
				if VLen(info) < 1 {
					info = ""
				}
			}
		}
		label = ts + title + Reset
		if info != "" {
			label += is + " · " + info + Reset
		}
		label = " " + label + " "
	}
	used := 1 + VLen(label) + tagRoom
	fill := inner - used
	if fill < 0 {
		fill = 0
	}
	var b strings.Builder
	b.WriteString(line + lc + look.Edge + Reset)
	b.WriteString(label)
	b.WriteString(line + strings.Repeat(look.Edge, fill) + Reset)
	if tag != "" {
		ts := C.Muted
		if focused {
			ts = C.Accent
		}
		b.WriteString(ts + tag + Reset + line + look.Edge + Reset)
	}
	b.WriteString(line + rc + Reset)
	cv.put(row, left, b.String())
}

// lastOpenBand is the index of the band that may run down to the bottom of
// the content area: the last band, when it shows rows. It is -1 when the
// last band is a collapsed or hidden pane's title line, which must keep its
// row.
func lastOpenBand(bands []paneBand) int {
	if n := len(bands); n > 0 && bands[n-1].rows > 0 {
		return n - 1
	}
	return -1
}
