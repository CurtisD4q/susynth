package ui

import "strings"

// The page rail.
//
// The pages run down the left of the window, numbered as Esc then the
// number opens them. Esc m moves the keys into the rail: ↑↓ choose a page,
// Enter opens it, Esc or m goes back. At 80 columns and wider the rail shows
// the numbers and names; narrower, the numbers only.
//
// The top line is the trail: susynth › the page · the set · the audio.

// railWidth is the columns the shut rail takes in a window w columns wide,
// including the blank columns after it: "Pages" and the page numbers. Opened
// with Esc m it is drawn over the left of the page and takes nothing more.
func railWidth(w int) int {
	if w < 48 {
		return 4
	}
	return 9
}

// railOpenWidth is the columns the open rail covers, with names.
func railOpenWidth(w int) int {
	n := len("Pages")
	for _, p := range pageNames {
		n = max(n, len([]rune(p)))
	}
	return min(n+8, max(railWidth(w), w-10)) // " 1 ", the name, " ◂" and the edge
}

// drawRail draws the shut rail: its title on row y, the rule under it, and
// the page numbers from y+2 to bot.
func (a *App) drawRail(f *frame, rw, y, bot int) {
	a.drawRailAt(f, rw, y, bot, false)
}

// drawRailOpen draws the open rail over the page: the page under it is
// cleared, the names are shown, and an edge runs down its right side.
func (a *App) drawRailOpen(f *frame, ow, y, bot int) {
	th := a.th
	for r := y; r <= bot; r++ {
		for x := 0; x < ow; x++ {
			f.text(x, r, " ", th.Text, 1)
		}
		f.text(ow-1, r, "│", a.boxLine(true), 1)
	}
	a.drawRailAt(f, ow, y, bot, true)
}

func (a *App) drawRailAt(f *frame, rw, y, bot int, open bool) {
	th := a.th
	right := rw - 2 // the last column the rail draws in
	if open {
		right = rw - 2
	} else if rw > 4 {
		right = rw - 3
	}
	if rw > 4 {
		ts := style{fg: th.Text.fg, bg: th.Base.bg, bold: true}
		if a.railOn {
			ts = th.Pane
		}
		f.text(0, y, "Pages", ts, right+1)
		f.hline(0, y+1, right+1, '─', a.boxLine(a.railOn))
	}
	for i, name := range pageNames {
		row := y + 2 + i
		if row > bot {
			break
		}
		text := " " + string(rune('1'+i))
		if open {
			text += " " + name
		}
		st := th.Text
		if page(i) == a.page {
			st = th.Pane
			if open {
				text += strings.Repeat(" ", max(1, right-len([]rune(text)))) + "◂"
			}
		}
		if open && i == a.railSel {
			st = th.CursorRow
			text += strings.Repeat(" ", max(0, right+1-len([]rune(text))))
		}
		f.text(0, row, text, st, right+1)
	}
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
	x := f.text(0, 0, "◇ susynth", th.Muted, w)
	x = f.text(x, 0, " › ", th.Muted, w-x)
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
		f.text(x, 0, ctx, th.Muted, w-x)
	}
}
