package ui

import (
	txt "github.com/CurtisD4q/pmin-base/text"

	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/rubyvm"
)

const gutterW = 4

// draw lays out one frame and returns the cursor position (-1 if hidden).
func (a *App) draw(w, h int) (*frame, int, int) {
	th := a.th
	out := newFrame(w, h, th.Base)
	if w < 60 || h < 12 {
		out.text(1, 1, "terminal too small", th.ToastErr, 0)
		out.text(1, 2, "need at least 60x12", th.Muted, 0)
		return out, -1, -1
	}
	// No frame round the window: only the panes and pop-ups have borders.
	// As in Mint's other programs, the page starts a row down and a column
	// in from the edges.
	in, cx, cy := a.drawInner(w-2, h-1)
	for y := 0; y < in.h; y++ {
		copy(out.cells[(y+1)*w+1:(y+1)*w+1+in.w], in.cells[y*in.w:(y+1)*in.w])
	}
	if cx >= 0 {
		cx, cy = cx+1, cy+1
	}
	return out, cx, cy
}

// drawInner lays out the window's contents inside the frame.
func (a *App) drawInner(w, h int) (*frame, int, int) {
	th := a.th
	f := newFrame(w, h, th.Base)
	v := a.eng.Snapshot()

	// Row 0 is the trail and row 1 is blank. The rail and the panes have
	// their titles on row 2 and their rules on row 3; a pane draws its rows
	// from row 4, under its rule.
	a.drawTrail(f, w)
	rw := railWidth(w)
	top, bot := 3, h-3
	a.drawRail(f, rw, top-1, bot)
	// The panes are laid out as if they had the window to themselves,
	// beside the rail, then copied over.
	pw := w - rw
	pf := newFrame(pw, h, th.Base)
	cx, cy := a.drawPanes(pf, pw, top, bot, v)
	for y := top - 1; y <= bot; y++ {
		copy(f.cells[y*w+rw:y*w+rw+pw], pf.cells[y*pw:(y+1)*pw])
	}
	if cx >= 0 {
		cx += rw
	}

	a.drawToast(f, h-2, w, &cx, &cy)
	a.drawTransport(f, h-1, w, v)
	if a.prompt == nil && cx >= 0 && cy > bot {
		cx = -1
	}
	if p, ok := a.currentPopup(); ok {
		cx, cy = a.drawPopup(f, p)
	}
	return f, cx, cy
}

// drawPanes draws the page's panes in a frame w columns wide, their rules
// on row top and their rows down to bot. It returns the cursor, or -1.
func (a *App) drawPanes(f *frame, w, top, bot int, v engine.View) (int, int) {
	th := a.th
	iw := w - 2 // the width the panes were laid out in
	ew := iw * 11 / 20
	if iw >= 110 {
		ew = iw * 3 / 5
	}
	if th.CellW > 2 { // projector themes: give the step display more room
		ew = iw * 9 / 20
	}
	if ew < 30 {
		ew = 30
	}
	cx, cy := -1, -1
	if a.page == pagePlay {
		// The editor's code is in columns 1..ew, under its rule; the side
		// pane is beside it, after one blank column.
		a.drawPane(f, 0, top, ew+1, bot, "Editor", "^E run block · Esc-Enter run all", !a.railOn)
		cx, cy = a.drawEditor(f, 1, top, ew, bot-top)
		sx, sw := ew+4, w-ew-5
		title, info := a.sideLabel(v)
		a.drawPane(f, ew+3, top, w-1, bot, title, info, false)
		switch a.side {
		case sideRoll:
			a.drawRoll(f, sx, top, sw, bot-top, v)
		case sideLog:
			a.drawLog(f, sx, top, sw, bot-top)
		default:
			a.drawTracks(f, sx, top, sw, bot-top, v)
		}
	} else if a.ps.split = a.hasDetails(w); a.ps.split {
		// A list and, beside it, the row under the cursor in full.
		lw := iw * 11 / 20
		a.drawPane(f, 0, top, lw+1, bot, pageNames[a.page], a.pageInfo(), !a.railOn)
		a.drawPage(f, 1, top, lw, bot-top)
		a.drawPane(f, lw+3, top, w-1, bot, "Details", "", false)
		a.drawDetails(f, lw+4, top, w-lw-6, bot-top)
	} else {
		a.drawPane(f, 0, top, w-1, bot, pageNames[a.page], a.pageInfo(), !a.railOn)
		a.drawPage(f, 1, top, w-2, bot-top)
	}
	return cx, cy
}

func (a *App) sinkName() string {
	au := "no audio"
	if a.sink != nil {
		au = "audio: " + a.sink.Name()
	}
	if a.vm != nil && !a.vm.IsReady() {
		return a.vm.Status() + " · " + au
	}
	return au
}

// ---- editor pane ---------------------------------------------------------------

func (a *App) drawEditor(f *frame, x, y, w, h int) (int, int) {
	th := a.th

	maxErr := h / 4
	if maxErr < 1 {
		maxErr = 1
	}
	if maxErr > 8 {
		maxErr = 8
	}
	errN := len(a.errs)
	if errN > maxErr {
		errN = maxErr
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
	bf, bl, bok := rubyvm.BlockRange(a.ed.Text(), a.ed.row+1)
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
		num := th.Gutter.over(base)
		switch {
		case bad[row]:
			f.put(x, yy, '▌', th.GutterErr)
		case a.marks[row]:
			f.put(x, yy, '▌', th.GutterOK)
		case bok && row+1 >= bf && row+1 <= bl:
			// the statement ^E will run, including blank lines inside it
			f.put(x, yy, '┊', th.Keyword.over(base))
			num = th.Keyword.over(base)
		}
		f.text(x+1, yy, fmt.Sprintf("%2d", row+1), num, 2)
		a.drawCode(f, x+gutterW, yy, a.ed.lines[row], a.ed.left, textW, base)
		// selection: the selected characters, plus one cell for a selected line break
		for c := a.ed.left; c <= len(a.ed.lines[row]) && c-a.ed.left < textW; c++ {
			if a.ed.Selected(row, c) {
				f.tint(x+gutterW+c-a.ed.left, yy, th.Select)
			}
		}
	}
	// error rows: red, never a dialog
	for i := 0; i < errN; i++ {
		e := a.errs[len(a.errs)-errN+i]
		yy := y + h - errN + i
		f.fill(x, yy, w, 1, th.ErrRow)
		f.text(x+1, yy, "✗ "+e.String(), style{fg: th.ErrRow.fg, bg: th.ErrRow.bg, bold: true}, w-2)
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

var rubyKeywords = map[string]bool{
	"do": true, "end": true, "if": true, "elsif": true, "else": true, "unless": true, "while": true,
	"until": true, "case": true, "when": true, "then": true, "def": true, "return": true, "and": true,
	"or": true, "not": true, "nil": true, "true": true, "false": true, "begin": true, "rescue": true,
	"ensure": true, "break": true, "next": true, "yield": true, "self": true, "in": true, "loop": true,
	"live_loop": true, "in_thread": true, "define": true, "with_fx": true, "sync": true, "cue": true,
	"stop": true, "density": true, "at": true,
}

var soundWords = map[string]bool{
	"play": true, "sample": true, "sleep": true, "synth": true, "play_chord": true, "play_pattern": true,
	"play_pattern_timed": true, "use_synth": true, "with_synth": true, "use_bpm": true, "with_bpm": true,
	"wait": true, "puts": true,
}

func isIdent(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// drawCode paints one line of Ruby with light syntax colouring: keywords,
// sound calls, :symbols, "strings", numbers and # comments.
func (a *App) drawCode(f *frame, x, y int, line []rune, left, w int, base style) {
	th := a.th
	put := func(i int, r rune, s style) {
		if i >= left && i-left < w {
			f.put(x+i-left, y, r, s.over(base))
		}
	}
	i := 0
	for i < len(line) {
		r := line[i]
		switch {
		case r == '#':
			for ; i < len(line); i++ {
				put(i, line[i], th.Comment)
			}
		case r == '"' || r == '\'':
			q := r
			put(i, r, th.String)
			for i++; i < len(line); i++ {
				put(i, line[i], th.String)
				if line[i] == '\\' && i+1 < len(line) {
					i++
					put(i, line[i], th.String)
					continue
				}
				if line[i] == q {
					i++
					break
				}
			}
		case r == ':' && i+1 < len(line) && (unicode.IsLetter(line[i+1]) || line[i+1] == '_') && (i == 0 || !isIdent(line[i-1]) && line[i-1] != ':'):
			put(i, r, th.String)
			for i++; i < len(line) && (isIdent(line[i]) || line[i] == '?'); i++ {
				put(i, line[i], th.String)
			}
		case unicode.IsDigit(r) && (i == 0 || !isIdent(line[i-1])):
			for ; i < len(line) && (unicode.IsDigit(line[i]) || line[i] == '.'); i++ {
				put(i, line[i], th.Modifier)
			}
		case isIdent(r):
			j := i
			for j < len(line) && (isIdent(line[j]) || line[j] == '?' || line[j] == '!') {
				j++
			}
			word := string(line[i:j])
			s := th.Text
			label := j < len(line) && line[j] == ':' && (j+1 >= len(line) || line[j+1] != ':')
			switch {
			case label:
				s = th.Modifier
			case rubyKeywords[word]:
				s = th.Keyword
			case soundWords[word]:
				s = style{fg: th.Text.fg, bg: th.Text.bg, bold: true}
			}
			for ; i < j; i++ {
				put(i, line[i], s)
			}
			if label {
				put(i, ':', th.Modifier)
				i++
			}
		default:
			put(i, r, th.Text)
			i++
		}
	}
}

// ---- track panes + step display --------------------------------------------------

func (a *App) drawTracks(f *frame, x, y, w, h int, v engine.View) {
	th := a.th
	names := map[string]bool{}
	if a.ed != nil {
		names = rubyvm.LiveLoopNames(a.ed.Text())
	}
	if len(v.Tracks) == 0 {
		f.text(x, y+2, "nothing playing", th.Muted, w)
		if a.seq != nil && a.seq.Waiting() {
			f.text(x, y+3, a.vm.Status(), th.Muted, w)
			f.text(x, y+4, "your code runs when Ruby is ready", th.Muted, w)
		} else {
			f.text(x, y+3, "put the cursor in a live_loop", th.Muted, w)
			f.text(x, y+4, "and press ^E", th.Muted, w)
		}
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
			f.text(x, y+1+i*block, "+"+txt.Count(len(v.Tracks)-i, "more track"), th.Muted, w)
			break
		}
		yy := y + 1 + i*block
		quiet := t.Mute || (anySolo && !t.Solo)

		f.text(x, yy, fmt.Sprintf("%d", t.ID), th.Muted, 0)
		nameW := 12
		f.text(x+2, yy, ":"+t.Name, th.Keyword, nameW)
		lx := x + 2 + nameW + 1
		// level bar (8 cells = 100%)
		n := int(t.Level*8 + 0.5)
		for k := 0; k < 8; k++ {
			if k < n {
				f.put(lx+k, yy, '▮', th.LevelOn)
			} else {
				f.put(lx+k, yy, '▯', th.LevelOff)
			}
		}
		cx := lx + 9
		// Everything after the level bar stops at the pane's edge.
		tag := func(s string, st style) {
			if cx < x+w {
				cx = f.text(cx, yy, s, st, x+w-cx) + 1
			}
		}
		tag(fmt.Sprintf("%3d%%", int(t.Level*100+0.5)), th.Muted)
		if t.Mute {
			tag("[MUTE]", th.MuteTag)
		}
		if t.Solo {
			tag("[SOLO]", th.SoloTag)
		}
		if t.Loop && !names[t.Name] {
			tag("not in code", th.Pending)
		}
		if src := soundName(t.Src); cx < x+w && src != "" {
			f.text(cx, yy, src, th.Muted, x+w-cx)
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
			if c == playCell && !t.Steps[c].On {
				ch = '◆'
			}
			f.put(sx, yy+1, ch, s)
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
	if a.prompt != nil && a.page == pagePlay { // off the Play page it is a pop-up
		x := f.text(1, y, "▸ "+a.prompt.label, th.Pane, 0)
		x = f.text(x, y, string(a.prompt.buf), style{fg: th.Toast.fg, bg: th.Toast.bg, bold: true}, w-x-1)
		*cx, *cy = x, y
		return
	}
	if a.status != "" {
		s, glyph, gs := th.Toast, "● ", style{fg: 114, bg: th.Base.bg, bold: true}
		if a.statusErr {
			s, glyph, gs = th.ToastErr, "✗ ", th.ToastErr
		}
		f.text(1, y, glyph, gs, 0)
		msg := a.status
		if !a.statusAt.IsZero() {
			msg = a.statusAt.Format("15:04:05") + "  " + msg
		}
		f.text(3, y, msg, s, w-4)
	}
	// Otherwise the line is empty: each page's keys are on the Help page.
}

// ---- transport bar (the pinned run bar) ------------------------------------------------

func (a *App) drawTransport(f *frame, y, w int, v engine.View) {
	th := a.th
	f.fill(0, y, w, 1, th.Bar)
	x := 1
	if v.Playing {
		x = f.text(x, y, "▶ play", style{fg: 114, bg: th.Bar.bg, bold: true}, 0)
	} else {
		x = f.text(x, y, "■ stop", th.Bar, 0)
	}
	x = f.text(x+2, y, fmt.Sprintf("%.0f bpm", v.BPM), th.Bar, 0)
	x = f.text(x+2, y, fmt.Sprintf("bar %d", v.Cycle+1), th.Bar, 0)
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
		x = f.text(x, y, fmt.Sprintf("[● REC %02d:%02d]", secs/60, secs%60), th.Rec, 0) + 1
	} else {
		x = f.text(x, y, "rec off", th.BarKey, 0) + 1
	}
	if v.Hushed {
		x = f.text(x+1, y, "[STOPPED]", th.Hush, 0) + 1
	}
	if v.Pending > 0 {
		x = f.text(x+1, y, "[next cycle]", th.Pending, 0) + 1
	}
	if label := a.pendingLabel(); label != "" {
		x = f.text(x+1, y, label, th.Pending, 0) + 1
	}
	if a.seq != nil && a.seq.Quantise() {
		x = f.text(x+1, y, "next bar", th.Pending, 0) + 1
	}
	if v.Underruns > 0 {
		x = f.text(x+1, y, fmt.Sprintf("xruns %d", v.Underruns), th.BarKey, 0)
	}
	hint := "^E run  ^G stop  ^P play  ^R rec  ^S/^O set  ^Q quit"
	if hx := w - len([]rune(hint)) - 1; hx > x+2 {
		f.text(hx, y, hint, th.BarKey, 0)
	}
	_ = strings.TrimSpace
}

// drawLog is the session log in the right-hand pane: errors, puts and cues,
// with the time they happened. It does not expire and it is not a dialog.
func (a *App) drawLog(f *frame, x, y, w, h int) {
	th := a.th
	if len(a.log) == 0 {
		f.text(x, y+2, "nothing yet", th.Muted, w)
		f.text(x, y+3, "errors, puts and cue stay here", th.Muted, w)
		return
	}
	visible := h - 1
	if visible < 1 {
		return
	}
	if a.logOff > len(a.log)-1 {
		a.logOff = len(a.log) - 1
	}
	bottom := len(a.log) - 1 - a.logOff
	top := bottom - visible + 1
	if top < 0 {
		top = 0
	}
	row := 0
	for i := top; i <= bottom && row < visible; i++ {
		e := a.log[i]
		yy := y + 1 + row
		row++
		s := th.Text
		if e.Kind == "err" {
			s = style{fg: th.ErrRow.fg, bg: th.Base.bg, bold: true}
		} else if e.Kind == "cue" {
			s = th.Muted
		}
		f.text(x, yy, e.At.Format("15:04:05"), th.Muted, 8)
		f.text(x+9, yy, e.Text, s, w-9)
	}
}

// soundName shows what a track last played in readable form.
func soundName(word string) string {
	switch {
	case word == "":
		return ""
	case strings.HasPrefix(word, "@"):
		return "sample"
	}
	if n, err := strconv.Atoi(word); err == nil && n >= 0 && n < 128 {
		return fmt.Sprintf("note %s%d", [12]string{"c", "cs", "d", "eb", "e", "f", "fs", "g", "gs", "a", "bb", "b"}[n%12], n/12-1)
	}
	return word
}

// sideLabel is the side box's title and the words beside it.
func (a *App) sideLabel(v engine.View) (string, string) {
	switch a.side {
	case sideRoll:
		return "Piano roll", "Esc r tracks"
	case sideLog:
		return "Log", "Alt+↑↓ scroll · Esc l tracks"
	}
	if n := len(v.Tracks); n > 0 {
		return "Tracks", fmt.Sprintf("%d playing · Esc k stops one", n)
	}
	return "Tracks", "Esc k stops one"
}
