package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/susynth/susynth/internal/engine"
)

const gutterW = 4

// draw lays out one frame and returns the cursor position (-1 if hidden).
func (a *App) draw(w, h int) (*frame, int, int) {
	th := a.th
	f := newFrame(w, h, th.Base)
	if w < 60 || h < 12 {
		f.text(1, 1, "terminal too small", th.ToastErr, 0)
		f.text(1, 2, "need at least 60x12", th.Muted, 0)
		return f, -1, -1
	}
	v := a.eng.Snapshot()

	// title, page tabs, and the single divider under them
	f.text(1, 0, "susynth", th.Title, 0)
	tx := a.drawTabs(f, 12, w)
	set := a.setName
	if set == "" {
		set = "untitled"
	}
	if a.dirty {
		set += " •"
	}
	right := fmt.Sprintf("%s · %s", set, a.sinkName())
	if rx := w - len([]rune(right)) - 1; rx > tx+2 {
		f.text(rx, 0, right, th.Muted, 0)
	}
	f.hline(0, 1, w, '─', th.Divider)

	top, bot := 2, h-3 // body rows, inclusive
	ew := w * 11 / 20
	if w >= 110 {
		ew = w * 3 / 5
	}
	if th.CellW > 2 { // projector themes: give the step display more room
		ew = w * 9 / 20
	}
	if ew < 30 {
		ew = 30
	}
	cx, cy := -1, -1
	if a.page == pagePlay {
		f.vline(ew, top, bot-top+1, '│', th.Divider)
		cx, cy = a.drawEditor(f, 0, top, ew, bot-top+1)
		if a.grid != nil {
			a.drawGrid(f, ew+2, top, w-ew-3, bot-top+1)
		} else {
			a.drawTracks(f, ew+2, top, w-ew-3, bot-top+1, v)
		}
	} else {
		a.drawPage(f, 0, top, w, bot-top+1)
	}

	a.drawToast(f, h-2, w, &cx, &cy)
	a.drawTransport(f, h-1, w, v)
	if a.prompt == nil && cx >= 0 && cy > bot {
		cx = -1
	}
	return f, cx, cy
}

func (a *App) sinkName() string {
	if a.sink == nil {
		return "no audio"
	}
	return "audio: " + a.sink.Name()
}

// ---- editor pane ---------------------------------------------------------------

func (a *App) drawEditor(f *frame, x, y, w, h int) (int, int) {
	th := a.th
	f.text(x+1, y, "EDITOR", th.Pane, 0)
	f.text(x+9, y, "^E eval block · Esc-Enter eval all", th.Muted, w-10)

	errN := len(a.errs)
	if errN > 3 {
		errN = 3
	}
	textH := h - 1 - errN
	if textH < 1 {
		textH = 1
	}
	textW := w - gutterW - 1
	a.ed.scroll(textW, textH)

	bad := map[int]bool{}
	for _, e := range a.errs {
		bad[e.Line] = true
	}
	for i := 0; i < textH; i++ {
		row := a.ed.top + i
		if row >= len(a.ed.lines) {
			break
		}
		yy := y + 1 + i
		base := th.Text
		if row == a.ed.row {
			f.fill(x, yy, w, 1, th.CursorRow)
			base = th.CursorRow
		}
		switch {
		case bad[row]:
			f.put(x, yy, '▌', th.GutterErr)
		case a.marks[row]:
			f.put(x, yy, '▌', th.GutterOK)
		}
		f.text(x+1, yy, fmt.Sprintf("%2d", row+1), th.Gutter.over(base), 2)
		a.drawCode(f, x+gutterW, yy, a.ed.lines[row], a.ed.left, textW, base)
	}
	// error rows: red, never a dialog
	for i := 0; i < errN; i++ {
		e := a.errs[len(a.errs)-errN+i]
		yy := y + h - errN + i
		f.fill(x, yy, w, 1, th.ErrRow)
		f.text(x+1, yy, "✗ "+e.String(), th.ErrRow, w-2)
	}
	return x + gutterW + a.ed.col - a.ed.left, y + 1 + a.ed.row - a.ed.top
}

// over fills in the background of s from base when s has none.
func (s style) over(base style) style {
	if s.bg < 0 {
		s.bg = base.bg
	}
	return s
}

// drawCode paints one line with light syntax colouring.
func (a *App) drawCode(f *frame, x, y int, line []rune, left, w int, base style) {
	th := a.th
	inStr, inMod, inCom := false, false, false
	word := 0
	prevSpace := true
	for i, r := range line {
		s := th.Text.over(base)
		switch {
		case inCom:
			s = th.Comment.over(base)
		case r == '"':
			inStr = !inStr
			s = th.String.over(base)
		case inStr:
			s = th.String.over(base)
		case r == '#':
			inMod = true
			s = th.Modifier.over(base)
		case inMod:
			s = th.Modifier.over(base)
		case r == '-' && i+1 < len(line) && line[i+1] == '-' && prevSpace:
			inCom = true
			s = th.Comment.over(base)
		case word == 0 && !unicode.IsSpace(r):
			s = th.Keyword.over(base)
		}
		if unicode.IsSpace(r) {
			if !prevSpace && !inStr {
				word++
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		if i >= left && i-left < w {
			f.put(x+i-left, y, r, s)
		}
	}
}

// ---- track panes + step display --------------------------------------------------

func (a *App) drawTracks(f *frame, x, y, w, h int, v engine.View) {
	th := a.th
	f.text(x, y, "TRACKS", th.Pane, 0)
	if len(v.Tracks) == 0 {
		f.text(x, y+2, "no tracks yet", th.Muted, w)
		f.text(x, y+3, `write  d1 "bd*2 [sn cp]"`, th.Muted, w)
		f.text(x, y+4, "then press ^E", th.Muted, w)
		return
	}
	anySolo := false
	for _, t := range v.Tracks {
		anySolo = anySolo || t.Solo
	}
	cw := th.CellW
	for cw > 1 && 16*cw+3 > w {
		cw--
	}
	block := 3
	if th.CellW > 2 {
		block = 4
	}
	maxT := (h - 1) / block
	if maxT < 1 {
		maxT = 1
	}
	playCell := -1
	if v.Playing {
		playCell = int(v.Phase * engine.StepCells)
	}
	for i, t := range v.Tracks {
		if i >= maxT {
			f.text(x, y+1+i*block, fmt.Sprintf("+%d more track(s)", len(v.Tracks)-i), th.Muted, w)
			break
		}
		yy := y + 1 + i*block
		quiet := t.Mute || (anySolo && !t.Solo)

		f.text(x, yy, fmt.Sprintf("d%d", t.ID), th.Keyword, 0)
		// level bar (8 cells = 100%)
		n := int(t.Level*8 + 0.5)
		for k := 0; k < 8; k++ {
			if k < n {
				f.put(x+4+k, yy, '▮', th.LevelOn)
			} else {
				f.put(x+4+k, yy, '▯', th.LevelOff)
			}
		}
		cx := x + 13
		f.text(cx, yy, fmt.Sprintf("%3d%%", int(t.Level*100+0.5)), th.Muted, 0)
		cx += 5
		if t.Mute {
			cx = f.text(cx, yy, " MUTE ", th.MuteTag, 0) + 1
		}
		if t.Solo {
			cx = f.text(cx, yy, " SOLO ", th.SoloTag, 0) + 1
		}
		if cx < x+w {
			f.text(cx, yy, t.Src, th.Muted, x+w-cx)
		}

		// step grid; the playhead cell is highlighted
		sx := x + 1
		for c := 0; c < engine.StepCells; c++ {
			s := th.StepOff
			ch := '·'
			if t.Steps[c].On {
				s = th.StepOn
				ch = rune(t.Steps[c].Label)
			}
			if quiet {
				s = style{fg: s.fg, bg: s.bg, dim: true}
				if !t.Steps[c].On {
					s = th.StepOff
				}
			}
			if c == playCell {
				s = th.Playhead
				if quiet {
					s.dim = true
				}
			}
			f.put(sx, yy+1, ch, s)
			if c == playCell && cw > 1 {
				f.put(sx+1, yy+1, ' ', s)
			}
			sx += cw
			if c%4 == 3 {
				sx++
			}
		}
	}
}

// ---- toast / prompt row ------------------------------------------------------------

func (a *App) drawToast(f *frame, y, w int, cx, cy *int) {
	th := a.th
	if a.prompt != nil {
		f.fill(0, y, w, 1, th.Toast)
		lbl := " " + a.prompt.label
		x := f.text(0, y, lbl, th.Toast, 0)
		x = f.text(x, y, string(a.prompt.buf), style{fg: th.Toast.fg, bg: th.Toast.bg, bold: true}, w-x-1)
		*cx, *cy = x, y
		return
	}
	if a.toast != "" && time.Now().Before(a.toastUntil) {
		s := th.Toast
		if a.toastErr {
			s = th.ToastErr
		}
		f.fill(0, y, w, 1, s)
		f.text(1, y, a.toast, s, w-2)
		return
	}
	if a.cfg.Hints {
		f.text(1, y, a.pageHint(), th.Muted, w-2)
	}
}

// ---- transport bar (the pinned run bar) ------------------------------------------------

func (a *App) drawTransport(f *frame, y, w int, v engine.View) {
	th := a.th
	f.fill(0, y, w, 1, th.Bar)
	x := 1
	if v.Playing {
		x = f.text(x, y, "▶ play", style{fg: th.StepOn.fg, bg: th.Bar.bg, bold: true}, 0)
	} else {
		x = f.text(x, y, "■ stop", th.Bar, 0)
	}
	x = f.text(x+2, y, fmt.Sprintf("%.0f bpm", v.BPM), th.Bar, 0)
	x = f.text(x+2, y, fmt.Sprintf("cycle %d", v.Cycle+1), th.Bar, 0)
	x += 2
	for b := 1; b <= engine.BeatsPerCycle; b++ {
		r, s := '○', th.BarKey
		if v.Playing && b == v.Beat {
			r, s = '●', style{fg: th.StepOn.fg, bg: th.Bar.bg, bold: true}
		}
		f.put(x, y, r, s)
		x += 2
	}
	x += 1
	if v.Recording {
		secs := int(v.RecTime.Seconds())
		x = f.text(x, y, fmt.Sprintf(" ● REC %02d:%02d ", secs/60, secs%60), th.Rec, 0) + 1
	} else {
		x = f.text(x, y, "rec off", th.BarKey, 0) + 1
	}
	if v.Hushed {
		x = f.text(x+1, y, " HUSH ", th.Hush, 0) + 1
	}
	if v.Pending > 0 {
		x = f.text(x+1, y, " next cycle ", th.Pending, 0) + 1
	}
	if v.Underruns > 0 {
		x = f.text(x+1, y, fmt.Sprintf("xruns %d", v.Underruns), th.BarKey, 0)
	}
	hint := "^E eval  ^G hush  ^P play  ^R rec  ^S/^O set  ^Q quit"
	if hx := w - len([]rune(hint)) - 1; hx > x+2 {
		f.text(hx, y, hint, th.BarKey, 0)
	}
	_ = strings.TrimSpace
}

// drawTabs paints the page tabs after the title and returns the next free x.
func (a *App) drawTabs(f *frame, x, w int) int {
	th := a.th
	if w < 100 { // too narrow for all tabs: show only where you are
		return f.text(x, 0, fmt.Sprintf("F%d %s  (F1-F7 pages)", int(a.page)+1, pageNames[a.page]), th.Muted, 0)
	}
	for i, n := range pageNames {
		s := th.Muted
		if page(i) == a.page {
			s = th.Playhead
		}
		x = f.text(x, 0, fmt.Sprintf(" F%d %s ", i+1, n), s, 0) + 1
	}
	return x
}
