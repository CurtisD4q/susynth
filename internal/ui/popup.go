package ui

import (
	"strings"
	"time"
)

// Pop-ups.
//
// Off the Play page, a question is a box in the middle of the window over a
// faint copy of the page, with its title in the top border and its keys in
// the bottom border. On the Play page questions stay on the line under the
// code, so nothing ever covers the code while music is playing. The keys
// are the same either way.

// popup is what a pop-up shows. field is the text being typed, if it asks
// for some.
type popup struct {
	title string
	lines []string
	keys  string
	field *[]rune
}

// currentPopup is the question to show as a pop-up now, if any.
func (a *App) currentPopup() (popup, bool) {
	if a.page == pagePlay {
		return popup{}, false
	}
	if p := a.prompt; p != nil {
		title := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p.label), ":"))
		return popup{title: capitalise(title), keys: "Enter confirm · Esc cancel", field: &p.buf}, true
	}
	if a.ps.armed != "" && (a.page == pageSets || a.page == pageRecs) {
		what := a.ps.armed
		if a.page == pageSets {
			what = "set " + what
		}
		return popup{
			title: "Delete " + what + "?",
			lines: []string{"It cannot be brought back."},
			keys:  "d delete · any other key keeps it",
		}, true
	}
	if a.page == pagePerform && a.dirty && time.Now().Before(a.discardUntil) {
		return popup{
			title: "Unsaved changes",
			lines: []string{"The editor has changes that are not saved.", "Go live again to discard them, or press ^S to save first."},
			keys:  "go live again to discard · ^S save",
		}, true
	}
	return popup{}, false
}

// drawPopup dims the page and draws p in a box in the middle. It returns
// where the cursor goes, or -1, -1.
func (a *App) drawPopup(f *frame, p popup) (int, int) {
	th := a.th
	for i := range f.cells {
		f.cells[i].s.dim = true
		f.cells[i].s.bold = false
	}
	want := len([]rune(p.title)) + 8
	want = max(want, len([]rune(p.keys))+8, 44)
	for _, l := range p.lines {
		want = max(want, len([]rune(l))+6)
	}
	bw := min(want, f.w-4)
	if bw < 20 {
		bw = min(20, f.w)
	}
	// border, blank, the lines, [blank, the field,] blank, border
	bh := len(p.lines) + 4
	if p.field != nil {
		bh += 2
	}
	bh = min(bh, f.h-2)
	x0 := (f.w - bw) / 2
	y0 := (f.h - bh) / 2
	x1, y1 := x0+bw-1, y0+bh-1
	f.fill(x0, y0, bw, bh, th.Base)
	a.drawBox(f, x0, y0, x1, y1, p.title, "", true)
	a.drawBoxKeys(f, x0, x1, y1, p.keys)
	y := y0 + 2
	for _, l := range p.lines {
		if y >= y1-1 {
			break
		}
		f.text(x0+2, y, l, th.Text, bw-4)
		y++
	}
	if p.field == nil {
		return -1, -1
	}
	if len(p.lines) > 0 {
		y++
	}
	if y >= y1 {
		return -1, -1
	}
	fw := bw - 4
	f.fill(x0+2, y, fw, 1, th.CursorRow)
	buf := []rune(string(*p.field))
	// Show the end of a long entry, where the typing is.
	if len(buf) > fw-1 {
		buf = buf[len(buf)-(fw-1):]
	}
	cx := f.text(x0+2, y, string(buf), style{fg: th.CursorRow.fg, bg: th.CursorRow.bg, bold: true}, fw)
	return cx, y
}

// drawBoxKeys sets keys into a box's bottom border, on the right:
//
//	╰──────── Enter confirm · Esc cancel ─╯
func (a *App) drawBoxKeys(f *frame, x0, x1, y int, keys string) {
	n := len([]rune(keys))
	if keys == "" || x1-x0-4 < n+2 {
		return
	}
	x := x1 - 2 - n - 1
	f.put(x, y, ' ', a.th.Base)
	f.text(x+1, y, keys, a.th.Muted, n)
	f.put(x+1+n, y, ' ', a.th.Base)
}

func capitalise(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
