package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	mint "github.com/CurtisD4q/pmin-kit"
	"github.com/CurtisD4q/pmin-kit/theme"

	"github.com/susynth/susynth/internal/engine"
)

// susynth as a Mint program.
//
// Each page is a Mint screen. Mint draws the trail along the top, the page
// icons down the left and the pages pop-up (m), each pane's title and
// rule, and the footer: the transport as badges, then the keys. susynth
// draws inside the panes, and takes every key first (Screen.Grab), so
// typing in the editor never reaches Mint's own keys; Esc then a key is its
// way to everything else, as before. Off the editor, m is left to Mint, as
// in any other Mint program; in the editor, where m is typed, it is Esc m.

// pageIcons are the pages' icons, in page order.
var pageIcons = []string{"▶", "≡", "●", "♪", "⚙", "?", "◈", "▮"}

// app is the Mint program: one page per item, no launch menu.
func (a *App) app() *mint.App {
	app := &mint.App{
		Title:       "◇ susynth",
		Crumb:       "◇ susynth",
		NoFlags:     true,
		NoHome:      true,
		NoThemeItem: true,
		Start:       int(a.page) + 1,
		Context:     a.context,
		Status:      a.badges,
	}
	for i := range pageNames {
		p := page(i)
		app.Items = append(app.Items, mint.HomeItem{
			Icon:  pageIcons[i],
			Label: pageNames[i],
			Open:  func() *mint.Screen { return a.screen(p) },
		})
	}
	return app
}

// screen is page p as a Mint screen. Opening it makes it the page shown.
func (a *App) screen(p page) *mint.Screen {
	a.setPage(p)
	hz := a.cfg.FrameHz
	if hz <= 0 {
		hz = 30
	}
	s := &mint.Screen{
		Boxed: true,
		Every: time.Second / time.Duration(hz),
		Grab:  a.grab,
		Poll:  a.poll,
		Hints: a.hints(p),

		CanLeave: a.canLeave,
	}
	switch p {
	case pagePlay:
		s.TopTitle = "Editor"
		s.TopInfo = func() string { return "^E run block · Esc-Enter run all" }
		s.TopWidth = 0.58
		if a.th.CellW > 2 { // projector themes: give the step display more room
			s.TopWidth = 0.45
		}
		s.Draw = a.paneDraw(a.drawEditorPane)
		side := &mint.Pane{Beside: true, Width: 1 - s.TopWidth, Draw: a.paneDraw(a.drawSidePane)}
		side.Info = func() string { _, info := a.sideLabel(a.eng.Snapshot()); return info }
		s.Panes = []*mint.Pane{side}
		s.Refresh = func(*mint.Screen) { side.Title, _ = a.sideLabel(a.eng.Snapshot()) }
	case pageMixer:
		a.mixerScreen(s)
	default:
		a.listScreen(s, p)
	}
	a.scr = s
	return s
}

// canLeave is asked before Mint closes a screen. Only quitting loses the
// editor: moving between pages keeps the code, so that is not asked about.
func (a *App) canLeave(c *mint.Ctx) bool {
	if !a.quit || !a.dirty {
		return true
	}
	if c.Confirm("Quit without saving?", "The code in the editor has changes that are not saved. ^S saves them.") {
		return true
	}
	a.quit = false
	return false
}

// setPage makes p the page shown, with its lists read afresh.
func (a *App) setPage(p page) {
	if p == a.page && a.ps.closed != nil {
		return // already shown: keep the cursor
	}
	a.page = p
	a.ps = pageState{closed: a.ps.closed}
	switch p {
	case pageSets:
		a.refreshSets()
	case pageRecs:
		a.refreshRecs()
	}
}

// gotoPage shows page p. While a key is being handled Mint opens it once
// the key is done, which calls screen and so setPage.
func (a *App) gotoPage(p page) {
	if p == a.page {
		return
	}
	if a.ctx != nil {
		a.ctx.Page(int(p))
		return
	}
	a.setPage(p)
}

// grab takes every key while a susynth page is shown.
func (a *App) grab(c *mint.Ctx, mk *mint.Key) bool {
	k, ok := keyFrom(mk)
	if !ok {
		return true // clicks do nothing: the focus stays where susynth has it
	}
	if a.page != pagePlay && a.prompt == nil && a.awaiting == "" && !a.escArmed() && mintMoves(k) {
		return false // Mint moves the cursor, folds groups, runs Enter and opens the pages
	}
	a.ctx = c
	defer func() { a.ctx = nil }()
	if a.scr != nil {
		a.syncSel(a.scr)
		if a.page == pageSounds && k.kind == kRune && k.r == 'i' && !k.alt {
			if it, ok := a.selectedSound(a.scr); ok {
				a.insertExample(it)
			}
			return true
		}
	}
	before := a.ps.sel
	a.handleKey(k)
	a.followMove(k, before)
	if a.quit {
		c.Quit()
	}
	return true
}

// followMove keeps Mint's cursor on a set that [ or ] moved up or down the
// set list.
func (a *App) followMove(k key, before int) {
	if a.page == pagePerform && a.scr != nil && k.kind == kRune && !k.alt && (k.r == '[' || k.r == ']') && before >= 0 {
		a.scr.MoveCursor(a.ps.sel - before)
	}
}

// mintMoves reports whether Mint handles k on a list page: moving, folding,
// Enter, and m for the pages pop-up.
func mintMoves(k key) bool {
	if k.alt {
		return false
	}
	switch k.kind {
	case kUp, kDown, kPgUp, kPgDn, kHome, kEnd, kEnter, kLeft, kRight:
		return true
	case kRune:
		return k.r == ' ' || k.r == 'm'
	}
	return false
}

// escArmed reports whether Esc was pressed a moment ago, so the next key is
// an Esc shortcut.
func (a *App) escArmed() bool { return !a.escAt.IsZero() && time.Since(a.escAt) < escWindow }

// poll runs before each frame: what Ruby said, a failed audio output, and
// the end of the Esc window.
func (a *App) poll(*mint.Screen) bool {
	select {
	case err := <-a.audioFail:
		a.say("audio output stopped ("+err.Error()+") — switched to silent; Esc a picks another", true)
		a.startAudio("none")
	default:
	}
	a.pollSeq()
	if a.eng.FadeDone() {
		a.seq.StopOld()
		a.say("the old set has faded out and stopped", false)
	}
	if !a.escAt.IsZero() && time.Since(a.escAt) >= escWindow {
		a.escAt = time.Time{}
	}
	return true
}

// context is what susynth is working on, after the page in the trail.
func (a *App) context() string {
	set := a.setName
	if set == "" {
		set = "untitled"
	}
	if a.dirty {
		set += " •"
	}
	return set + " · " + a.sinkName()
}

// badges are the transport, on the footer's upper row.
func (a *App) badges() []mint.Badge {
	v := a.eng.Snapshot()
	var out []mint.Badge
	if v.Playing {
		out = append(out, mint.Badge{Text: "play", Kind: mint.BadgeOK})
	} else {
		out = append(out, mint.Badge{Text: "stop", Kind: mint.BadgeIdle})
	}
	beats := ""
	for b := 1; b <= engine.BeatsPerCycle; b++ {
		if v.Playing && b == v.Beat {
			beats += "●"
		} else {
			beats += "○"
		}
	}
	out = append(out, mint.Badge{Text: fmt.Sprintf("%.0f bpm · bar %d · %s", v.BPM, v.Cycle+1, beats), Kind: mint.BadgeInfo})
	if v.Recording {
		secs := int(v.RecTime.Seconds())
		out = append(out, mint.Badge{Text: fmt.Sprintf("rec %02d:%02d", secs/60, secs%60), Kind: mint.BadgeError})
	} else {
		out = append(out, mint.Badge{Text: "rec off", Kind: mint.BadgeIdle})
	}
	if v.Hushed {
		out = append(out, mint.Badge{Text: "stopped", Kind: mint.BadgeWarn})
	}
	if v.Pending > 0 {
		out = append(out, mint.Badge{Text: "next cycle", Kind: mint.BadgeWarn})
	}
	if label := a.pendingLabel(); label != "" {
		out = append(out, mint.Badge{Text: label, Kind: mint.BadgeWarn})
	}
	if a.seq != nil && a.seq.ChangeOn() != "beat" {
		out = append(out, mint.Badge{Text: "changes: next " + a.seq.ChangeOn(), Kind: mint.BadgeIdle})
	}
	if f := a.eng.Fading(); f >= 0 {
		out = append(out, mint.Badge{Text: fmt.Sprintf("old set fading %d%%", int(f*100)), Kind: mint.BadgeWarn})
	} else if a.eng.HasOld() {
		out = append(out, mint.Badge{Text: "old set playing", Kind: mint.BadgeWarn})
	}
	if v.Underruns > 0 {
		out = append(out, mint.Badge{Text: fmt.Sprintf("xruns %d", v.Underruns), Kind: mint.BadgeIdle})
	}
	return out
}

// hints are the keys on the footer's lower row.
func (a *App) hints(p page) string {
	transport := "^E run   ^G stop   ^P play   ^R rec   ^S/^O set   Esc m pages   ^Q quit"
	switch p {
	case pageSets:
		return "↑↓ select   ⏎ load   a set list   n new   d delete   m pages   ^Q quit"
	case pageRecs:
		return "↑↓ select   ⏎ path   d delete   m pages   ^Q quit"
	case pageSounds:
		return "↑↓ select   ⏎ preview   ←→ fold   i insert   m pages   ^Q quit"
	case pageSettings:
		return "↑↓ select   ←→ change   ⏎ edit   m pages   ^Q quit"
	case pageMixer:
		return "↑↓ select   ←→ change   ⏎ reset   m pages   ^Q quit"
	case pageHelp:
		return "↑↓ PgUp PgDn scroll   m pages   ^Q quit"
	case pagePerform:
		return "⏎ go live   i mix in   Esc n/p next/previous   [ ] move   d remove   m pages"
	}
	return transport
}

// paneDraw is a Mint pane drawn by one of susynth's drawing functions. The
// function draws rows 1 to h of a frame w wide (row 0 is where its title
// was) and returns where the cursor goes, or -1.
func (a *App) paneDraw(draw func(f *frame, w, h int) (int, int)) func(*mint.Paint, bool) {
	return func(p *mint.Paint, focused bool) {
		w, h := p.Size()
		f := newFrame(w, h+1, a.th.Base)
		cx, cy := draw(f, w, h)
		for y := 1; y <= h; y++ {
			for x := 0; x < w; x++ {
				c := f.cells[y*w+x]
				p.SetInk(x, y-1, c.r, inkOf(c.s))
			}
		}
		if cx >= 0 && cy >= 1 {
			p.SetCursor(cx, cy-1)
		}
	}
}

func inkOf(s style) mint.Ink {
	k := mint.Ink{Bold: s.bold, Dim: s.dim, Reverse: s.rev}
	if s.fg >= 0 {
		k.Fg = mint.Xterm(int(s.fg))
	}
	if s.bg >= 0 {
		k.Bg = mint.Xterm(int(s.bg))
	}
	return k
}

// drawEditorPane is the code, with a question being typed (^S, ^O, Esc x)
// on its last row: on the Play page nothing covers the code.
func (a *App) drawEditorPane(f *frame, w, h int) (int, int) {
	if a.prompt == nil {
		return a.drawEditor(f, 0, 0, w, h)
	}
	a.drawEditor(f, 0, 0, w, h-1)
	th := a.th
	y := h
	f.fill(0, y, w, 1, th.CursorRow)
	x := f.text(1, y, "▸ "+a.prompt.label, style{fg: th.Pane.fg, bg: th.CursorRow.bg, bold: true}, 0)
	x = f.text(x, y, string(a.prompt.buf), style{fg: th.Toast.fg, bg: th.CursorRow.bg, bold: true}, w-x-1)
	return x, y
}

// drawSidePane is the pane beside the code: tracks, the piano roll or the log.
func (a *App) drawSidePane(f *frame, w, h int) (int, int) {
	v := a.eng.Snapshot()
	switch a.side {
	case sideRoll:
		a.drawRoll(f, 0, 0, w, h, v)
	case sideLog:
		a.drawLog(f, 0, 0, w, h)
	default:
		a.drawTracks(f, 0, 0, w, h, v)
	}
	return -1, -1
}

// usePalette gives Mint's parts (the trail, the titles, the footer) the
// theme's colours, unless the person has chosen a Mint theme.
func (a *App) usePalette() {
	if os.Getenv("MINT_PALETTE") != "" || theme.Current() != "default" {
		return
	}
	th := a.th
	spec := fmt.Sprintf("accent=%s,bright=%s,text=%s,muted=%s,on=%s,amber=%s,selbg=%s,err=%s",
		hexOf(th.Pane.fg), hexOf(th.StepOn.fg), hexOf(th.Text.fg), hexOf(th.Muted.fg),
		hexOf(114), hexOf(th.Pending.fg), hexOf(th.CursorRow.bg), hexOf(th.ToastErr.fg))
	if p, err := theme.FromSpec(theme.Load("default"), spec); err == nil {
		mint.SetPalette(p)
	}
}

// hexOf is xterm colour n as #rrggbb.
func hexOf(n int16) string {
	var r, g, b int
	switch {
	case n < 0:
		r, g, b = 220, 220, 220
	case n < 16:
		base := [16][3]int{{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16}, {36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229},
			{102, 102, 102}, {241, 76, 76}, {35, 209, 139}, {245, 245, 67}, {59, 142, 234}, {214, 112, 214}, {41, 184, 219}, {255, 255, 255}}
		r, g, b = base[n][0], base[n][1], base[n][2]
	case n < 232:
		v := [6]int{0, 95, 135, 175, 215, 255}
		i := int(n) - 16
		r, g, b = v[i/36], v[(i/6)%6], v[i%6]
	default:
		r = 8 + (int(n)-232)*10
		g, b = r, r
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// copyToSystem puts text on the system clipboard with OSC 52, which many
// terminals support (kitty, WezTerm, iTerm2, Alacritty, foot, Windows
// Terminal…). Terminals without it ignore the sequence.
func copyToSystem(text string) {
	if len(text) > 100000 || !mint.Interactive() {
		return
	}
	os.Stdout.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
}

var _ = strings.TrimSpace
