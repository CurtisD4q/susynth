package mint

import "strings"

// The page rail and the trail.
//
// Every screen an App opens has the app's pages down its left side as icons,
// in the same order as the launch menu, with the page the screen belongs to
// picked out. m opens the pages as a pop-up pane beside
// the icons, with a title and a rule under it and each page's icon and name:
// ↑↓ choose a page, Enter opens it, m or Esc closes it.
//
// The top line is the trail: the app, the screens under this one and this
// screen, then what the screen is working on (its Context).

type railItem struct{ icon, label string }

const (
	railMaxW  = 28 // widest the rail gets with names
	railIconW = 5  // the rail shut: the icons
	railHints = " ↑↓ move   ⏎ open   m/Esc back to the page "
)

// railWidth is the columns the rail takes from the screen on a screen w
// columns wide, from column 1 up to and including the blank column before
// the screen's own panes. The rail is shut (the icons only) until m opens it;
// open, it is drawn over the left of the page and takes nothing more. It is
// 0 when there is no rail.
func (s *Screen) railWidth(w int) int {
	if len(s.rail) == 0 || w < 48 {
		return 0
	}
	return railIconW
}

// railOpenWidth is the columns the pop-up spans, from column 1 across the
// icons to its right edge: its own width (the icon, the name, the marker and
// the two sides) plus the icons' columns, but never more than the page can
// spare.
func (s *Screen) railOpenWidth(w int) int {
	n := VLen("Pages")
	for _, it := range s.rail {
		n = max(n, VLen(CleanLine(it.label)))
	}
	pw := min(railMaxW, n+8, w-railIconW-5)
	s.railPopW = max(8, pw)
	return railIconW + s.railPopW - 1
}

// paintRail draws the shut rail: the icons, in columns 2 to rw-1, in the rows
// the pop-up's pages will be in.
func (s *Screen) paintRail(cv *canvas, top, bottom, rw int) {
	s.paintRailAt(cv, top, bottom, rw, false)
}

// paintRailOpen draws the pages as a pop-up pane over the left of the page,
// just right of the icons, which stay where they are. ow is its width.
func (s *Screen) paintRailOpen(cv *canvas, top, bottom, ow int) {
	s.railOpenW = ow
	s.paintRailAt(cv, top, bottom, ow, true)
}

// paintRailAt draws the icons (always) and, when open, the pop-up. Its rows
// line up with the icons': the title is level with the m, the rule below it,
// then one row per page.
func (s *Screen) paintRailAt(cv *canvas, top, bottom, rw int, open bool) {
	if rw <= 0 || bottom-top < 3 {
		s.railTop = 0
		return
	}
	left := 2
	first := top + 2
	rows := bottom - first // one row under the pages for the pop-up's edge
	s.railTop = first
	sel := s.railCur
	if s.railOn {
		sel = s.railSel
	}
	if sel >= 0 {
		if sel < s.railOff {
			s.railOff = sel
		}
		if sel >= s.railOff+rows {
			s.railOff = sel - rows + 1
		}
	}
	s.railOff = max(0, min(s.railOff, len(s.rail)-rows))
	shown := min(rows, len(s.rail)-s.railOff)

	// The icons.
	for r := 0; r < shown; r++ {
		it := s.rail[s.railOff+r]
		icon := CleanLine(it.icon)
		if icon == "" {
			icon = "·"
		}
		style := C.Text
		if s.railOff+r == s.railCur {
			style = C.Accent + C.Bold
		}
		cv.put(first+r, left, style+" "+icon+Reset)
	}
	if !open {
		return
	}

	// The pop-up: a bordered pane with a title and a rule under it.
	pl, pr := railIconW, rw
	edge := boxStyle(true)
	inner := pr - pl - 1
	blank := strings.Repeat(" ", max(0, inner))
	cv.put(top-1, pl, edge+look.TopLeft+strings.Repeat(look.Edge, max(0, inner))+look.TopRight+Reset)
	cv.put(top, pl, edge+look.Side+Reset+blank+edge+look.Side+Reset)
	cv.put(top, pl+2, C.Accent+C.Bold+"Pages"+Reset)
	cv.put(top+1, pl, edge+look.Side+Reset+edge+strings.Repeat(look.Edge, max(0, inner))+look.Side+Reset)
	for r := 0; r < shown; r++ {
		i := s.railOff + r
		it := s.rail[i]
		icon := CleanLine(it.icon)
		if icon == "" {
			icon = "·"
		}
		text := " " + icon + " " + Truncate(CleanLine(it.label), inner-5)
		tail := ""
		if i == s.railCur {
			tail = "◂"
		}
		text += strings.Repeat(" ", max(1, inner-1-VLen(text))) + tail
		style := C.Text
		if i == s.railCur {
			style = C.Accent + C.Bold
		}
		if i == s.railSel {
			style = C.SelBg + C.Bright + C.Bold
		}
		pad := strings.Repeat(" ", max(0, inner-VLen(text)))
		cv.put(first+r, pl, edge+look.Side+Reset+style+text+pad+Reset+edge+look.Side+Reset)
	}
	cv.put(first+shown, pl, edge+look.BottomLeft+strings.Repeat(look.Edge, max(0, inner))+look.BottomRight+Reset)
}

// railKey gives the rail the keys it wants. It returns the key to pass on
// (a click's column moved into the screen's own area), a result when the
// rail chose a page, and handled when the rail took the key.
func (s *Screen) railKey(k *Key) (*Key, *screenResult, bool) {
	if len(s.rail) == 0 || s.railW <= 0 || k == nil {
		return k, nil, false
	}
	if m := k.Mouse; m != nil {
		// The icons, and while it is open the pop-up beside them.
		edge := s.railW
		if s.railOn {
			edge = max(edge, s.railOpenW+1)
		}
		if m.Col < edge {
			if m.Click && s.railTop > 0 {
				if i := s.railOff + m.Row - s.railTop; m.Row >= s.railTop && i < len(s.rail) {
					s.railOn = false
					return nil, &screenResult{op: "page", page: i}, true
				}
			}
			return nil, nil, true
		}
		// A click on the page shuts the rail and goes through to the page.
		s.railOn = false
		moved := *k
		mm := *m
		mm.Col -= s.railW - 1
		moved.Mouse = &mm
		return &moved, nil, false
	}
	if s.railOn {
		switch k.Name {
		case "up", "k":
			s.railSel = max(0, s.railSel-1)
		case "down", "j":
			s.railSel = min(len(s.rail)-1, s.railSel+1)
		case "home":
			s.railSel = 0
		case "end":
			s.railSel = len(s.rail) - 1
		case "\r", "\n", "right":
			s.railOn = false
			return nil, &screenResult{op: "page", page: s.railSel}, true
		case "m", "esc", "left":
			s.railOn = false
		case "q":
			if s.Grab == nil { // a screen that takes its own keys quits its own way
				return nil, &screenResult{op: "quit"}, true
			}
		}
		return nil, nil, true
	}
	if k.Name != "m" || k.Special {
		return k, nil, false
	}
	if u := s.uiAt(s.focus); u != nil && (u.editing != nil || u.filtering) {
		return k, nil, false
	}
	if s.ownsKey("m") {
		return k, nil, false
	}
	s.railOn = true
	s.railSel = max(0, s.railCur)
	return nil, nil, true
}

// ownsKey reports whether the screen's own Keys use name.
func (s *Screen) ownsKey(name string) bool {
	for _, k := range s.Keys {
		if k.Key == name && k.Do != nil {
			return true
		}
	}
	return false
}

// paintTrail draws the top line: the app, the screens under this one and
// this one, then what it is working on. preview, when set, is what Enter
// does, at the right.
func (s *Screen) paintTrail(cv *canvas, w int, title, preview string) {
	var parts []string
	if app := CleanLine(s.Breadcrumb); app != "" {
		parts = append(parts, app)
	}
	for _, t := range s.trail {
		if t = CleanLine(t); t != "" {
			parts = append(parts, t)
		}
	}
	title = CleanLine(title)
	ctx := ""
	f := s.Context
	if f == nil {
		f = s.appContext
	}
	if f != nil {
		protect(func() { ctx = f() })
		ctx = CleanLine(ctx)
	}
	preview = CleanLine(preview)
	room := w - 3 // columns 2 to w-1
	if preview != "" {
		pv := VLen(preview)
		need := VLen(title) + 3 + pv
		if need > room {
			preview = ""
		} else {
			room -= pv + 3
		}
	}
	// Earlier steps give way first, from the left, then the context.
	sep := " › "
	trailW := func(ps []string, cut bool) int {
		n := VLen(title)
		for _, p := range ps {
			n += VLen(p) + VLen(sep)
		}
		if cut {
			n += VLen("…") + VLen(sep)
		}
		return n
	}
	cut := false
	for len(parts) > 0 && trailW(parts, cut) > room {
		parts, cut = parts[1:], true
	}
	used := trailW(parts, cut)
	var b strings.Builder
	if cut {
		b.WriteString(C.Text + "…" + sep)
	}
	for _, p := range parts {
		b.WriteString(C.Text + p + sep)
	}
	b.WriteString(C.Accent + C.Bold + Truncate(title, max(1, room)) + Reset)
	if ctx != "" && room-used > 4 {
		b.WriteString(C.Text + " · " + Truncate(ctx, room-used-3) + Reset)
	}
	cv.put(2, 2, b.String())
	if preview != "" {
		cv.put(2, w-1-VLen(preview), C.Text+preview+Reset)
	}
}

// enterPreview says what Enter does on a row: the row's own words when it
// has them (Does), else a plain description from its kind when that says
// more than the row does. It is "" otherwise.
func enterPreview(it *Row) string {
	if it == nil {
		return ""
	}
	if it.does != nil {
		t := ""
		protect(func() { t = it.does() })
		if t = CleanLine(t); t != "" {
			return "⏎ " + t
		}
		return ""
	}
	label := strings.TrimSpace(CleanLine(it.label))
	switch {
	case it.kind == "section":
		if it.collapsible && it.name != "" {
			return "⏎ opens or folds " + CleanLine(it.name)
		}
	case it.wtype == wtTableRow:
		// A table row says which row it opens, by its first cell.
		if it.table != nil && it.table.tableSelect != nil && len(it.cells) > 0 {
			if first := strings.TrimSpace(CleanLine(it.cells[0])); first != "" {
				return "⏎ opens " + first
			}
		}
	case label == "":
	case it.wtype == wtText || it.wtype == wtStepper:
		return "⏎ edits " + label
	case it.wtype == wtToggle || it.wtype == wtCheck:
		return "⏎ switches " + label
	case it.wtype == wtPicker:
		return "←→ changes " + label
	}
	// An action without its own words says nothing: its label is already
	// on the selected row.
	return ""
}

// selectedRow is the row selected in a pane, or nil.
func selectedRow(st *uiState) *Row {
	if st == nil {
		return nil
	}
	sel := selectable(st.items)
	if st.row < 1 || st.row > len(sel) {
		return nil
	}
	return &st.items[sel[st.row-1]-1]
}

// Does sets what Enter does on the row, in a few words ("runs nmap -sV
// 10.0.0.5"). It is shown in the header of the row's pane while the row is
// selected (or on the top line, on a screen without panes).
func (r Row) Does(f func() string) Row {
	r.does = f
	return r
}
