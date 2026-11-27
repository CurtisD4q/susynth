package mint

import (
	"fmt"
	"strings"
	"time"
)

func (ss *screenSession) status() []Badge {
	if ss.screen.Status != nil {
		return callStatus(ss.screen.Status)
	}
	return callStatus(ss.screen.appStatus)
}

func (ss *screenSession) finish(cv *canvas) {
	ss.lastDraw = time.Now()
	if ss.screen.frameBuf != nil {
		ss.screen.frameBuf.WriteString(cv.b.String())
		return
	}
	if ss.screen.live() {
		tail := esc + "?25l" + esc + "H"
		if ss.screen.cursor != "" && !ss.screen.railOn {
			tail = ss.screen.cursor + esc + "?25h"
		}
		h, w := TermSize()
		showDiff(cv.b.String(), h, w, tail)
		return
	}
	cv.paint()
}

// usePane points widget and marked at the pane being drawn.
func (ss *screenSession) usePane(u *uiState) { ss.st = u }

// refresh runs the screen's Refresh. A panic in it shows as the banner.
func (ss *screenSession) refresh() {
	if ss.screen.Refresh == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			ss.screen.Banner = "Error: " + Truncate(CleanLine(fmt.Sprint(p)), 70)
		}
	}()
	ss.screen.Refresh(ss.screen)
}

// followCursor records the row under the cursor on a screen without panes
// before it is drawn, so Selected and SelectedTableRow answer there as
// they do on a split screen (paintSplit records it for those). When that
// changes the selection, Refresh runs again, so what it builds from the
// cursor is drawn in the same frame.
func (ss *screenSession) followCursor(w int) {
	s := ss.screen
	if len(s.Panes) > 0 || s.Draw != nil {
		return
	}
	cw := w
	if rw := s.railWidth(w); rw > 0 {
		cw = w - rw + 1
	}
	record := func() bool {
		before, beforeRow := s.focusSig(), s.selSig()
		st := s.uiAt(0)
		ss.st = st
		st.items = filterRows(st.flatten(s.Sections, cw, s.SectionSpacing), st.filter)
		ss.clamp()
		s.noteSelection(st)
		s.noteFocusSelection()
		return s.focusSig() != before || s.selSig() != beforeRow
	}
	if record() {
		ss.refresh()
		record() // Refresh may have rebuilt the rows
	}
}

func (ss *screenSession) draw(h, w int) {
	ss.refresh()
	ss.followCursor(w)
	ss.screen.cursor = ""
	cv := begin(h, w)
	if w < 48 || h < 12 {
		cv.frame(h, w)
		cv.put(2, 3, C.Amber+"Terminal too small"+Reset)
		cv.put(3, 3, C.Muted+fmt.Sprintf("need 48x12, have %dx%d", w, h)+Reset)
		ss.finish(cv)
		return
	}
	cv.frame(h, w)
	title := ss.screen.Title
	if title == "" {
		title = "Screen"
	}
	if ss.screen.Icon != "" {
		title = ss.screen.Icon + " " + title
	}
	// The top line is the trail, with a rule under it and a blank row
	// below that. A banner goes under the rule, with a blank row after it
	// too.
	paintRule(cv, 3, w)
	contentTop := 5
	if ss.screen.Banner != "" {
		// A long banner wraps onto up to three lines, the last cut with an
		// ellipsis, and the content moves down to make room.
		lines := wrapLines(CleanLine(ss.screen.Banner), max(8, w-4))
		if len(lines) > 3 {
			lines = lines[:3]
			lines[2] = Truncate(lines[2]+" …", max(8, w-4))
		}
		for i, l := range lines {
			cv.put(4+i, 2, C.Amber+Truncate(l, max(8, w-4))+Reset)
		}
		contentTop = 5 + len(lines)
	}
	contentBottom := h - 2
	avail := contentBottom - contentTop + 1
	if avail < 1 {
		ss.screen.paintTrail(cv, w, title, "")
		ss.finish(cv)
		return
	}
	// The page rail takes the left of the screen; the screen's own rows
	// are drawn beside it, in cc, as if they had the screen to themselves.
	rw := ss.screen.railWidth(w)
	ss.screen.railW = rw
	cc, cw := cv, w
	if rw > 0 {
		cw = w - rw + 1
		cc = cv.view(rw, cw)
		ss.screen.paintRail(cv, contentTop, contentBottom, rw)
	}
	// The open rail is drawn last, over the left of the page.
	overlay := func() {
		if rw > 0 && ss.screen.railOn {
			ss.screen.paintRailOpen(cv, contentTop, contentBottom, ss.screen.railOpenWidth(w))
		}
	}
	addRailHints := func(hints string) string {
		if rw == 0 {
			return hints
		}
		if ss.screen.railOn {
			return railHints
		}
		if ss.screen.Grab != nil {
			return hints // the screen names its own keys
		}
		if !ss.screen.ownsKey("m") && !strings.Contains(hints, "m pages") {
			hints = addHint(hints, "m pages")
		}
		return hints
	}
	// The dock (the run command and its preview) is always the bottom
	// pane, however short the terminal. When rows are short the panes
	// above give way first: the blank line under the dock's rule goes,
	// then the panes shrink to a single row, and only then is the dock
	// itself shortened (it scrolls to the row in use).
	dockH, dockRule, dockGap := 0, 0, 0
	if len(ss.screen.Dock) > 0 {
		dockH = ss.screen.dockHeight(cw)
	}
	if dockH > 0 {
		dockRule, dockGap = 1, 1
		if avail-dockH-dockRule-dockGap < paneMinRows {
			dockGap = 0
		}
		if room := avail - dockRule - dockGap - 1; dockH > room {
			dockH = room
		}
		if dockH < 1 {
			dockH, dockRule, dockGap = 0, 0, 0
		}
	}
	paneAvail := avail - dockH - dockRule - dockGap
	ss.screen.viewTop, ss.screen.viewAvail, ss.screen.viewWidth = contentTop, paneAvail, cw
	if len(ss.screen.Panes) > 0 {
		ss.screen.paintSplit(cc, region{contentTop, paneAvail, cw}, ss.usePane, ss.hooks())
		if dockH > 0 {
			paintRule(cc, contentTop+paneAvail, cw)
			ss.screen.paintDock(cc, region{contentTop + paneAvail + dockRule + dockGap, dockH, cw}, ss.usePane, ss.hooks())
		}
		ss.st = ss.screen.uiAt(ss.screen.focus)
		it, _ := ss.current()
		preview := ""
		if !ss.screen.boxed() {
			preview = enterPreview(it)
		}
		ss.screen.paintTrail(cv, w, title, preview)
		hints := withSplitHints(footerHints(ss.st.editing != nil, ss.screen.Hints, it), !ss.screen.inSplitGroup(ss.screen.focus))
		if ss.screen.Grab != nil {
			hints = ss.screen.Hints
		} else if ss.screen.boxed() {
			hints = strings.Replace(hints, "Tab/S-Tab pane", fmt.Sprintf("Tab/1-%d pane", min(9, ss.screen.paneCount())), 1)
			// The footer has a row of its own for keys: name the filter and
			// the key list, unless the screen uses those keys itself.
			if ss.ownKey("/") == nil && !strings.Contains(hints, "/ filter") {
				hints = addHint(hints, "/ filter")
			}
			if ss.ownKey("?") == nil && !strings.Contains(hints, "? keys") {
				hints = addHint(hints, "? keys")
			}
		}
		hints = addRailHints(hints)
		if ss.st.filtering {
			hints = filterHints
		}
		badges := ss.status()
		if ss.screen.boxed() {
			cv.footer2(h, w, hints, badges, ss.filterLine())
		} else {
			cv.footerLine(h, w, hints, badges, ss.filterLine())
		}
		ss.st.footSig = footerSig(badges)
		overlay()
		ss.finish(cv)
		return
	}
	ss.screen.bands = nil
	if f := ss.screen.Draw; f != nil {
		// The screen draws its content itself.
		ss.screen.paintCustom(cc.view(2, max(1, cw-2)), f, contentTop, paneAvail, max(1, cw-2), true)
		ss.screen.paintTrail(cv, w, title, "")
		hints := addRailHints(ss.screen.Hints)
		badges := ss.status()
		cv.footer2(h, w, hints, badges, "")
		ss.st = ss.screen.uiAt(0)
		ss.st.footSig = footerSig(badges)
		overlay()
		ss.finish(cv)
		return
	}
	ss.st = ss.screen.uiAt(0)
	ss.st.page = paneAvail
	items := filterRows(ss.st.flatten(ss.screen.Sections, cw, ss.screen.SectionSpacing), ss.st.filter)
	ss.st.items = items
	sel := selectable(items)
	ss.clamp()
	selItem := 0
	if ss.st.row >= 1 && ss.st.row <= len(sel) {
		selItem = sel[ss.st.row-1]
	}
	if selItem != 0 {
		if selItem-ss.st.scroll > paneAvail-1 {
			ss.st.scroll = selItem - paneAvail + 1
		}
		if selItem-ss.st.scroll < 1 {
			ss.st.scroll = selItem - 1
		}
	}
	if ss.st.scroll < 0 {
		ss.st.scroll = 0
	}
	if maxScroll := max(0, len(items)-paneAvail); ss.st.scroll > maxScroll {
		ss.st.scroll = maxScroll
	}
	// One pane is drawn as the panes of a split screen are.
	paintRows(cc, ss.st, items, region{contentTop, paneAvail, cw}, ss.screen.focus == 0, ss.hooks())
	if dockH > 0 {
		paintRule(cc, contentTop+paneAvail, cw)
		ss.screen.paintDock(cc, region{contentTop + paneAvail + dockRule + dockGap, dockH, cw}, ss.usePane, ss.hooks())
	}
	ss.st = ss.screen.uiAt(ss.screen.focus)
	it, _ := ss.current()
	ss.screen.paintTrail(cv, w, title, enterPreview(it))
	hints := addRailHints(footerHints(ss.st.editing != nil, ss.screen.Hints, it))
	if ss.screen.Grab != nil {
		hints = ss.screen.Hints
	}
	if ss.st.filtering {
		hints = filterHints
	}
	badges := ss.status()
	if ss.screen.live() {
		// A screen that moves on its own has the two-row footer: what is
		// happening above, the keys below.
		cv.footer2(h, w, hints, badges, ss.filterLine())
	} else {
		cv.footerLine(h, w, hints, badges, ss.filterLine())
	}
	ss.st.footSig = footerSig(badges)
	overlay()
	ss.finish(cv)
}
