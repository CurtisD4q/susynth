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

// railWidth is the columns the rail takes in a window w columns wide,
// including the blank column after it.
func railWidth(w int) int {
	if w < 80 {
		return 4 // " 1 " and the blank column
	}
	n := len("Pages")
	for _, p := range pageNames {
		n = max(n, len([]rune(p)))
	}
	return n + 7 // " 1 ", the name, " ◂", and the blank column
}

// drawRail draws the rail: its title on row y, the rule under it, and the
// pages from y+2 to bot.
func (a *App) drawRail(f *frame, rw, y, bot int) {
	th := a.th
	names := rw > 4
	right := rw - 2 // the last column the rail draws in
	if names {
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
		if names {
			text += " " + name
		}
		st := th.Text
		if page(i) == a.page {
			st = th.Pane
			if names {
				text += strings.Repeat(" ", max(1, right-len([]rune(text)))) + "◂"
			}
		}
		if a.railOn && i == a.railSel {
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
