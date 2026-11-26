package ui

import "strings"

// The page rail.
//
// The pages run down the left of the window as icons, with an m above them.
// Esc m opens the pages as a pop-up pane beside the icons, with names and the
// numbers that open each page (Esc then the number), and moves the keys into
// it: ↑↓ choose a page, Enter opens it, Esc or m goes back and shuts it.
//
// The top line is the trail: susynth › the page · the set · the audio.

// railWidth is the columns the shut rail takes in a window w columns wide,
// including the blank column after it: the page icons. Opened with Esc m it
// is drawn over the left of the page and takes nothing more.
func railWidth(w int) int {
	return 5
}

// pageIcons are the pages' icons, in page order.
var pageIcons = []string{"▶", "≡", "●", "♪", "⚙", "?", "◈"}

// railPopW is the pop-up's own width: the icon, the name, the number, the
// marker and the two sides.
func railPopW(w int) int {
	n := len("Pages")
	for _, p := range pageNames {
		n = max(n, len([]rune(p)))
	}
	return max(12, min(n+10, w-railWidth(w)-4))
}

// drawRail draws the icons, with an m above them (where the pop-up's title
// goes) that opens the pop-up. They are in the rows the pages' own rows use.
func (a *App) drawRail(f *frame, rw, y, bot int) {
	th := a.th
	mark := th.Muted
	if a.railOn {
		mark = th.Pane
	}
	f.text(1, y, "m", mark, 1)
	for i := range pageNames {
		row := y + 2 + i
		if row > bot {
			break
		}
		st := th.Text
		if page(i) == a.page {
			st = th.Pane
		}
		f.text(1, row, pageIcons[i], st, 1)
	}
}

// drawRailOpen draws the pages as a pop-up pane just right of the icons,
// which stay where they are: a bordered box with a title and a rule under
// it, then a row per page, level with its icon, and the number that opens
// it (Esc then the number) at the right.
func (a *App) drawRailOpen(f *frame, rw, y, bot int) {
	th := a.th
	line := a.boxLine(true)
	l := rw - 1
	pw := railPopW(f.w)
	r := l + pw - 1
	shown := min(len(pageNames), bot-(y+2))
	f.fill(l, y-1, pw, shown+4, th.Text)
	f.put(l, y-1, '╭', line)
	f.hline(l+1, y-1, pw-2, '─', line)
	f.put(r, y-1, '╮', line)
	f.vline(l, y, shown+2, '│', line)
	f.vline(r, y, shown+2, '│', line)
	f.text(l+2, y, "Pages", th.Pane, pw-3)
	f.hline(l+1, y+1, pw-2, '─', line)
	for i := 0; i < shown; i++ {
		row := y + 2 + i
		text := " " + pageIcons[i] + " " + pageNames[i]
		tail := string(rune('1' + i))
		if page(i) == a.page {
			tail += "◂"
		} else {
			tail += " "
		}
		inner := pw - 2
		text += strings.Repeat(" ", max(1, inner-len([]rune(text))-len([]rune(tail)))) + tail
		st := th.Text
		if page(i) == a.page {
			st = th.Pane
		}
		if i == a.railSel {
			st = th.CursorRow
		}
		f.text(l+1, row, text, st, inner)
	}
	f.put(l, y+2+shown, '╰', line)
	f.hline(l+1, y+2+shown, pw-2, '─', line)
	f.put(r, y+2+shown, '╯', line)
}

// railKey takes the keys while the rail has them. It reports whether it
// used k.
func (a *App) railKey(k key) bool {
	if !a.railOn {
		return false
	}
	switch k.kind {
	case kUp:
		a.railSel = max(0, a.railSel-1)
	case kDown:
		a.railSel = min(int(numPages)-1, a.railSel+1)
	case kHome:
		a.railSel = 0
	case kEnd:
		a.railSel = int(numPages) - 1
	case kEnter, kRight:
		a.railOn = false
		a.gotoPage(page(a.railSel))
	case kEsc, kLeft:
		a.railOn = false
	case kRune:
		if k.r == 'm' {
			a.railOn = false
		}
	case kCtrl:
		// The transport keys still work.
		return false
	}
	return true
}

// drawTrail draws the top line: susynth › the page · the set · the audio.
func (a *App) drawTrail(f *frame, w int) {
	th := a.th
	x := f.text(0, 0, "◇ susynth", th.Text, w)
	x = f.text(x, 0, " › ", th.Text, w-x)
	x = f.text(x, 0, pageNames[a.page], th.Title, w-x)
	set := a.setName
	if set == "" {
		set = "untitled"
	}
	if a.dirty {
		set += " •"
	}
	ctx := " · " + set + " · " + a.sinkName()
	if w-x > 4 {
		f.text(x, 0, ctx, th.Text, w-x)
	}
}
