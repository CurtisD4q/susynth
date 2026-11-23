// Package ui is the terminal front end: editor, track panes, step display,
// transport bar and toasts. It lays out the screen the way Mint apps do
// (title, one divider, panes side by side, pinned bar). The screen code is
// its own, because susynth also runs on Windows and redraws many times a
// second; the small helpers (wording, clipping, cleaning text, checking
// paths) come from pmin-base.
package ui

import (
	"github.com/CurtisD4q/pmin-base/clean"
	txt "github.com/CurtisD4q/pmin-base/text"
	"github.com/CurtisD4q/pmin-base/values"

	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/susynth/susynth/internal/audio"
	"github.com/susynth/susynth/internal/config"
	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/live"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/rubyvm"
)

// Options come from the command line.
type Options struct {
	File  string // optional .rb set to open
	Sink  string // overrides config when non-empty
	Theme string // overrides config when non-empty
}

const starter = `# susynth: Sonic Pi code, running in real Ruby.
# ^E runs the block under the cursor, Esc-Enter runs everything.
# ^G stops all loops. ^Q quits.
# Esc then 1-7 changes page (the number is on the tab). Esc 6 is help.

use_bpm 120

live_loop :drums do
  sample :bd_haus
  sleep 0.5
  sample :drum_snare_hard, amp: 0.8
  sleep 0.5
end

live_loop :hats do
  sync :drums
  8.times { sample :drum_cymbal_closed, amp: rrand(0.3, 0.6); sleep 0.125 }
end

live_loop :bass do
  sync :drums
  use_synth :tb303
  play (ring :e2, :e2, :g2, :a2).tick, release: 0.2, cutoff: rrand(70, 110)
  sleep 0.25
end

# melody: edit the notes and press ^E again while it plays
live_loop :melody do
  sync :drums
  use_synth :pluck
  with_fx :reverb, mix: 0.4 do
    play scale(:e4, :minor_pentatonic).choose, amp: 0.6
  end
  sleep 0.5
end
`

type prompt struct {
	label string
	buf   []rune
	kind  string // "save" | "load"
}

// App is the whole UI state.
type App struct {
	term *Term
	cfg  config.Config
	eng  *engine.Engine
	ed   *Editor
	th   *Theme

	sink      audio.Sink
	cancel    context.CancelFunc
	runDone   chan struct{}
	audioFail chan error

	setName string
	dirty   bool
	quitAsk bool

	errs     []codeErr
	lineSnap []string // buffer lines when the markers were last aligned

	vm        *rubyvm.VM
	seq       *live.Seq
	seqCancel context.CancelFunc
	waitToast bool
	clipboard string // what ^C or ^X copied last
	side      sidePane
	escAt     time.Time
	escFrom   page
	laterMsgs chan laterMsg
	marks     map[int]bool

	log       []logLine // session log: errors, puts, cues, what you did
	logOff    int       // lines above the newest; 0 keeps the latest in view
	status    string    // the message row; it does not expire
	statusErr bool
	statusAt  time.Time

	prompt   *prompt
	awaiting string // "mute", "solo" or "stop" while a track number is expected
	awaitBuf string

	page page
	ps   pageState

	outs         []engineOutput
	setlist      []string
	live         int       // set list index currently live, -1 = none
	discardUntil time.Time // a second go-live inside this window discards unsaved edits

	recFile string
	quit    bool
}

// Run starts the interactive UI.
func Run(o Options) error {
	cfg, first := config.Load()
	if o.Sink != "" {
		cfg.Sink = o.Sink
	}
	if o.Theme != "" {
		cfg.Theme = o.Theme
	}
	term, err := openTerm()
	if err != nil {
		return err
	}
	defer term.Close()

	a := &App{term: term, cfg: cfg, eng: engine.New(), th: themeByName(cfg.Theme),
		marks: map[int]bool{}, audioFail: make(chan error, 1), live: -1, laterMsgs: make(chan laterMsg, 8)}
	a.eng.SetBPM(cfg.BPM)
	// Ruby boots in the background; the editor is usable straight away
	a.vm = rubyvm.Start(paths.CacheDir())
	a.seq = live.New(a.vm, a.eng)
	a.seq.SetQuantise(cfg.StartOnBar)
	sctx, scancel := context.WithCancel(context.Background())
	a.seqCancel = scancel
	a.seq.Start(sctx)
	a.ps.closed = map[string]bool{"Samples": true, "Drum synths": true}
	a.loadInitial(o, first)
	a.loadSetlist()
	a.startAudio(cfg.Sink)
	if first && !a.statusErr {
		a.say("welcome! ^E runs the block under the cursor — audio: "+a.sink.Name(), false)
	}
	a.reloadSamples(false)
	defer a.shutdown()
	return a.loop()
}

func (a *App) loadInitial(o Options, first bool) {
	text := starter
	switch {
	case o.File != "":
		if b, err := os.ReadFile(paths.Expand(o.File)); err == nil {
			text = string(b)
			a.setName = strings.TrimSuffix(filepath.Base(o.File), ".rb")
		} else {
			a.setName = strings.TrimSuffix(filepath.Base(o.File), ".rb")
		}
	case !first && a.cfg.AutoLoad && a.cfg.Set != "":
		// The name comes from config.json, which anything could have
		// written: only read a set that is inside the sets folder.
		if p := filepath.Join(paths.SetsDir(), a.cfg.Set+".rb"); !values.Within(paths.SetsDir(), p) {
			a.say("not loading "+clean.Line(a.cfg.Set)+": it is outside the sets folder", true)
		} else if b, err := os.ReadFile(p); err == nil {
			text, a.setName = string(b), a.cfg.Set
		}
	}
	a.ed = newEditor(text)
	a.lineSnap = a.ed.Lines()
	// start on the first real statement so ^E does something straight away
	for i := range a.ed.lines {
		if t := strings.TrimSpace(a.ed.Line(i)); t != "" && !strings.HasPrefix(t, "#") {
			a.ed.row = i
			break
		}
	}
}

func (a *App) shutdown() {
	if d, _ := a.eng.StopRecording(); d > 0 {
		_ = d
	}
	a.seqCancel()
	a.vm.Close()
	a.eng.SetOutputs(nil)
	for _, o := range a.outs {
		o.Close()
	}
	a.stopAudio()
	a.cfg.Theme = a.th.Name
	a.cfg.BPM = a.eng.BPM()
	a.cfg.Set = a.setName
	_ = a.cfg.Save()
}

func (a *App) say(msg string, isErr bool) {
	kind := "info"
	if isErr {
		kind = "err"
	}
	a.addLog(kind, msg, 0)
}

// addLog keeps one session line. Cues are recorded but do not replace the
// message row, so a stream of cues cannot hide an error.
func (a *App) addLog(kind, text string, line int) {
	a.log = append(a.log, logLine{At: time.Now(), Kind: kind, Text: text, Line: line})
	if len(a.log) > 400 {
		a.log = append([]logLine(nil), a.log[len(a.log)-400:]...)
	}
	if kind != "cue" {
		a.status, a.statusErr, a.statusAt = text, kind == "err", time.Now()
	}
}

// ---- audio ----------------------------------------------------------------

func (a *App) startAudio(name string) {
	a.stopAudio()
	s, err := audio.Open(name)
	if err != nil {
		a.say("no sound: "+err.Error()+" — running silent; ^R still records", true)
		s, _ = audio.Open("none")
	}
	a.sink = s
	defer a.applyOutputs()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.runDone = make(chan struct{})
	done, eng := a.runDone, a.eng
	go func() {
		defer close(done)
		if err := eng.Run(ctx, s.Write, s.Latency()); err != nil && ctx.Err() == nil {
			select {
			case a.audioFail <- err:
			default:
			}
		}
	}()
}

func (a *App) stopAudio() {
	if a.cancel != nil {
		a.cancel()
		<-a.runDone
		a.cancel = nil
	}
	if a.sink != nil {
		a.sink.Close()
		a.sink = nil
	}
}

func (a *App) cycleAudio() { a.stepAudio(1) }

// ---- main loop --------------------------------------------------------------

func (a *App) loop() error {
	keys := make(chan key, 64)
	go readKeys(keys)
	winch := make(chan os.Signal, 1)
	watchResize(winch)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGHUP)
	hz := a.cfg.FrameHz
	tick := time.NewTicker(time.Second / time.Duration(hz))
	defer tick.Stop()
	var prev []string
	w, h := a.term.size()
	for !a.quit {
		select {
		case k := <-keys:
			a.handleKey(k)
		case <-winch:
			w, h = a.term.size()
			prev = nil
		case <-term:
			return nil
		case err := <-a.audioFail:
			a.say("audio output stopped ("+err.Error()+") — switched to silent; Esc a picks another", true)
			a.startAudio("none")
		case <-tick.C:
		}
		a.pollSeq()
		if !a.escAt.IsZero() && time.Since(a.escAt) >= escWindow {
			a.escAt = time.Time{}
		}
		if a.cfg.FrameHz != hz {
			hz = a.cfg.FrameHz
			tick.Reset(time.Second / time.Duration(hz))
		}
		prev = a.render(w, h, prev)
	}
	return nil
}

func (a *App) render(w, h int, prev []string) []string {
	f, cx, cy := a.draw(w, h)
	rows := f.rows()
	t := a.term
	for i, r := range rows {
		if i < len(prev) && prev[i] == r {
			continue
		}
		t.write(moveTo(i, 0) + r)
	}
	if cx >= 0 {
		t.write("\x1b[0m" + moveTo(cy, cx) + "\x1b[?25h")
	} else {
		t.write("\x1b[?25l")
	}
	t.flush()
	return rows
}

// ---- keys -------------------------------------------------------------------

var loopRe = regexp.MustCompile(`^\s*live_loop\s+:?"?([A-Za-z_][A-Za-z0-9_]*)`)

// trackOnCursor finds the live_loop the cursor is in and its track number.
func (a *App) trackOnCursor() (int, string, bool) {
	for i := a.ed.row; i >= 0; i-- {
		if m := loopRe.FindStringSubmatch(a.ed.Line(i)); m != nil {
			for _, t := range a.eng.Snapshot().Tracks {
				if t.Name == m[1] {
					return t.ID, t.Name, true
				}
			}
			return 0, m[1], false
		}
	}
	return 0, "", false
}

// escWindow is how long after Esc the next key counts as an Esc shortcut.
const escWindow = 1500 * time.Millisecond

func (a *App) handleKey(k key) {
	if a.prompt != nil {
		a.escAt = time.Time{}
		a.awaiting, a.awaitBuf = "", ""
		a.promptKey(k)
		return
	}
	if a.awaiting != "" {
		a.trackCommand(k)
		return
	}
	// "Esc, then a key" shortcuts: a person can't press the second key within
	// the few milliseconds that mark an Alt+key, so a lone Esc arms the next
	// key for a moment. The transport shows Esc… while it is armed. Esc goes
	// back to Play; it does not clear the log or an error.
	from := a.page // the page a page-cycling key counts from
	if k.kind == kEsc && !k.alt {
		a.escAt, a.escFrom = time.Now(), a.page
	} else if !a.escAt.IsZero() {
		armed := time.Since(a.escAt) < escWindow
		a.escAt = time.Time{}
		if armed && !k.alt && a.side == sideLog && isLogScroll(k) {
			a.scrollLog(k, 8)
			return
		}
		if armed && !k.alt && (k.kind == kRune || k.kind == kEnter) {
			k.alt = true
			from = a.escFrom // Esc already took us to Play; cycle from where we were
		}
	}
	if k.kind != kCtrl || k.r != 'q' {
		a.quitAsk = false
	}
	if k.kind == kFn {
		// Optional alias. SSH, tmux, laptop Fn layers and desktop F-key grabs
		// often swallow these; Esc then 1-7 is the binding the tabs show.
		if p, ok := pageByNumber('0' + k.r); ok {
			a.gotoPage(p)
		}
		return
	}
	if k.alt && k.kind == kRune {
		if p, ok := pageByNumber(k.r); ok {
			a.gotoPage(p)
			return
		}
		if step := pageStep(k.r); step != 0 {
			a.stepPage(from, step)
			return
		}
	}
	if k.alt {
		a.altKey(k)
		return
	}
	if a.page != pagePlay {
		// transport keys work everywhere; the rest belongs to the page
		if k.kind == kCtrl && strings.ContainsRune("gprq", k.r) {
			a.ctrlKey(k.r)
			return
		}
		a.pageKey(k)
		return
	}
	a.editorKey(k)
}

// editorKey handles typing, moving, selecting and clipboard keys in the editor.
func (a *App) editorKey(k key) {
	ed := a.ed
	// moving: Shift extends the selection, anything else drops it
	move := func(f func()) {
		if k.shift {
			ed.Mark()
		} else {
			ed.Unmark()
		}
		f()
		ed.BreakRun()
	}
	edit := func(kind string, f func()) {
		ed.Checkpoint(kind)
		f()
		a.edited()
	}
	switch k.kind {
	case kCtrl:
		switch k.r {
		case 'a':
			ed.SelectAll()
		case 'c':
			a.copySelection(false)
		case 'x':
			a.copySelection(true)
		case 'v':
			if a.clipboard == "" {
				a.say("nothing copied yet — select with Shift+arrows and ^C (or paste with your terminal's paste key)", false)
				return
			}
			edit("", func() { ed.InsertText(a.clipboard) })
		case 'z':
			if ed.Undo() {
				a.edited()
			} else {
				a.say("nothing to undo", false)
			}
		case 'y':
			if ed.Redo() {
				a.edited()
			} else {
				a.say("nothing to redo", false)
			}
		case 'k':
			edit("", func() { ed.Unmark(); ed.KillLine() })
		default:
			a.ctrlKey(k.r)
		}
	case kPaste:
		if k.text != "" {
			edit("", func() { ed.InsertText(k.text) })
		}
	case kEsc:
		ed.Unmark()
	case kRune:
		kind := "type"
		if k.r == ' ' {
			kind = "space"
		}
		if _, _, sel := ed.Selection(); sel {
			kind = ""
		}
		edit(kind, func() { ed.DeleteSelection(); ed.Insert(k.r) })
	case kTab:
		edit("", func() { ed.DeleteSelection(); ed.Insert(' '); ed.Insert(' ') })
	case kEnter:
		edit("", func() { ed.DeleteSelection(); ed.Newline() })
	case kBackspace:
		if _, _, sel := ed.Selection(); sel {
			edit("", func() { ed.DeleteSelection() })
		} else {
			edit("back", func() { ed.Unmark(); ed.Backspace() })
		}
	case kDelete:
		if _, _, sel := ed.Selection(); sel {
			edit("", func() { ed.DeleteSelection() })
		} else {
			edit("del", func() { ed.Unmark(); ed.Delete() })
		}
	case kUp:
		move(func() { ed.Vert(-1) })
	case kDown:
		move(func() { ed.Vert(1) })
	case kLeft:
		move(ed.Left)
	case kRight:
		move(ed.Right)
	case kHome:
		move(ed.Home)
	case kEnd:
		move(ed.End)
	case kPgUp:
		move(func() { ed.Vert(-10) })
	case kPgDn:
		move(func() { ed.Vert(10) })
	}
}

// copySelection copies (or cuts) the selection, or the current line when
// nothing is selected, to susynth's clipboard and the system clipboard.
func (a *App) copySelection(cut bool) {
	ed := a.ed
	text := ed.SelectedText()
	whole := text == ""
	if whole {
		text = ed.Line(ed.row) + "\n"
	}
	a.clipboard = text
	a.term.copyToSystem(text)
	n := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
	what := txt.Count(n, "line")
	if !whole && n == 1 {
		what = txt.Count(len([]rune(text)), "character")
	}
	if !cut {
		ed.Unmark()
		a.say("copied "+what, false)
		return
	}
	ed.Checkpoint("")
	if whole {
		ed.KillLine()
	} else {
		ed.DeleteSelection()
	}
	a.edited()
	a.say("cut "+what+" — ^V pastes, ^Z undoes", false)
}

func (a *App) edited() {
	a.dirty = true
	lines := a.ed.Lines()
	if a.lineSnap != nil && len(lines) != len(a.lineSnap) {
		a.errs = remapErrs(a.errs, a.lineSnap, lines)
		a.marks = remapMarks(a.marks, a.lineSnap, lines)
	}
	a.lineSnap = lines
}

func (a *App) altKey(k key) {
	if isLogScroll(k) {
		if a.page == pagePlay && a.side == sideLog {
			a.scrollLog(k, 8)
			return
		}
		// Alt+arrows are not a shortcut. Treat them as plain movement, which is
		// what they did before the log learned to scroll.
		k.alt = false
		if a.page == pagePlay {
			a.editorKey(k)
		} else {
			a.pageKey(k)
		}
		return
	}
	switch {
	case k.kind == kEnter:
		a.eval(true)
	case k.kind != kRune:
	case k.r >= '1' && k.r <= '9':
		// A digit on its own used to mute. That caught people who were still
		// in the Esc window. Mute and stop now ask for a number on purpose.
		a.say("tracks: Esc u then a number mutes, Esc k then a number stops, Esc s solos", false)
	case k.r == 'u':
		a.awaiting, a.awaitBuf = "mute", ""
		a.say("mute: track number (Enter confirms)", false)
	case k.r == 'k':
		a.awaiting, a.awaitBuf = "stop", ""
		a.say("stop: track number, or Enter for the loop under the cursor", false)
	case k.r == 's':
		a.awaiting = "solo"
		a.say("solo: press a track number (0 clears)", false)
	case k.r == '-' || k.r == '=' || k.r == '+' || k.r == '_':
		id, name, ok := a.trackOnCursor()
		if name == "" {
			a.say("put the cursor inside a live_loop to change its level", true)
			return
		}
		if !ok {
			a.say(":"+name+" is not playing — ^E runs it", true)
			return
		}
		d := 0.1
		if k.r == '-' || k.r == '_' {
			d = -0.1
		}
		if v, ok := a.eng.AdjustLevel(id, d); ok {
			a.say(fmt.Sprintf(":%s level %d%%", name, int(v*100+0.5)), false)
		}
	case k.r == ',' || k.r == '.' || k.r == '<' || k.r == '>':
		a.say("tempo comes from your code: change use_bpm and press ^E", false)
	case k.r == 'l':
		if a.page != pagePlay {
			a.gotoPage(pagePlay)
		}
		if a.side == sideLog {
			a.side = sideTracks
			a.say("tracks — Esc l shows the log", false)
		} else {
			a.side = sideLog
			a.logOff = 0
			a.say("log — Alt+↑↓ scrolls, Esc l goes back to tracks", false)
		}
	case k.r == 'r':
		if a.page != pagePlay {
			a.gotoPage(pagePlay)
		}
		if a.side == sideRoll {
			a.side = sideTracks
			a.say("tracks — Esc r shows the piano roll, Esc l the log", false)
		} else {
			a.side = sideRoll
			a.say("piano roll — Esc r goes back to Tracks", false)
		}
	case k.r == 'x':
		a.startPrompt("midi", "export MIDI, seconds (1-600): ", "30")
	case k.r == 'n':
		a.liveStep(1)
	case k.r == 'p':
		a.liveStep(-1)
	case k.r == 't':
		a.th = nextTheme(a.th)
		a.say("theme: "+a.th.Name, false)
	case k.r == 'a':
		a.cycleAudio()
	}
}

func (a *App) ctrlKey(r rune) {
	switch r {
	case 'e':
		a.eval(false)
	case 'g':
		a.seq.Stop(true)
		a.marks = map[int]bool{}
		a.say("stopped every loop (^E or ^P to start again)", false)
	case 'p':
		if a.seq.Running() || a.eng.Playing() {
			a.seq.Stop(false)
			a.marks = map[int]bool{}
			a.say("stopped", false)
		} else {
			a.eval(true)
		}
	case 'r':
		a.toggleRecord()
	case 's':
		a.startPrompt("save", "save set as: ", a.setName)
	case 'o':
		a.startPrompt("load", "load set: ", "")
		a.say("sets: "+a.listSets(), false)
	case 'l':
		a.errs = nil
		a.say("error markers cleared — the log keeps them (Esc l)", false)
	case 'q':
		if a.dirty && !a.quitAsk {
			a.quitAsk = true
			a.say("unsaved changes — ^S saves, ^Q again quits anyway", true)
			return
		}
		a.quit = true
	}
}

// ---- eval -------------------------------------------------------------------

// codeErr is an error to mark in the editor; line is 0-based, -1 = none.
type codeErr struct {
	Line int
	Text string
}

func (e codeErr) String() string {
	if e.Line < 0 {
		return e.Text
	}
	return fmt.Sprintf("line %d: %s", e.Line+1, e.Text)
}

// eval sends code to Ruby: the block around the cursor, or everything.
func (a *App) eval(all bool) {
	cursor := a.ed.row + 1
	if all {
		cursor = 0
	}
	a.errs = nil
	a.seq.Run(a.ed.Text(), cursor)
	if !a.vm.IsReady() {
		if err := a.vm.Err(); err != nil {
			return
		}
		a.waitToast = true
		a.say("Ruby is still starting ("+a.vm.Status()+") — your code runs the moment it's ready", false)
	}
}

type laterMsg struct {
	text  string
	isErr bool
}

// later shows a message from a background job on the next frame.
func (a *App) later(text string, isErr bool) {
	select {
	case a.laterMsgs <- laterMsg{text, isErr}:
	default:
	}
}

// pollSeq shows what Ruby reported since the last frame.
func (a *App) pollSeq() {
	for len(a.laterMsgs) > 0 {
		m := <-a.laterMsgs
		a.say(m.text, m.isErr)
	}
	if a.waitToast && a.vm.IsReady() && !a.seq.Waiting() {
		a.waitToast = false
	}
	sawErr := false
	for _, n := range a.seq.Notes() {
		switch n.Kind {
		case "err":
			e := codeErr{Line: n.Line - 1, Text: n.Text}
			a.errs = append(a.errs, e)
			if len(a.errs) > 20 {
				a.errs = a.errs[len(a.errs)-20:]
			}
			a.addLog("err", e.String(), n.Line)
			sawErr = true
		case "log":
			a.addLog("puts", n.Text, 0)
		case "cue":
			a.addLog("cue", n.Text, 0)
		case "ran":
			if n.First == 0 {
				continue
			}
			for i := n.First - 1; i <= n.Last-1; i++ {
				a.marks[i] = true
			}
			msg := fmt.Sprintf("running lines %d–%d", n.First, n.Last)
			if n.Quantised {
				msg += " on the next bar"
			}
			a.addLog("info", msg, 0)
		}
	}
	if sawErr {
		// an error in the same batch stays on the message row
		for i := len(a.log) - 1; i >= 0; i-- {
			if a.log[i].Kind == "err" {
				a.status, a.statusErr, a.statusAt = a.log[i].Text, true, a.log[i].At
				break
			}
		}
	}
}

// ---- sets & recording --------------------------------------------------------

func (a *App) startPrompt(kind, label, initial string) {
	a.prompt = &prompt{label: label, buf: []rune(initial), kind: kind}
}

func (a *App) promptKey(k key) {
	p := a.prompt
	switch {
	case k.kind == kEsc || (k.kind == kCtrl && k.r == 'c'):
		a.prompt = nil
		a.say("cancelled", false)
	case k.kind == kEnter && !k.alt:
		name := strings.TrimSpace(string(p.buf))
		a.prompt = nil
		switch p.kind {
		case "oscaddr":
			if err := validAddr(name); err != nil {
				a.say(err.Error(), true)
				return
			}
			a.cfg.OSCAddr = name
			a.applyOutputs()
			a.saveConfig()
			return
		case "mididev":
			a.cfg.MidiDev = name
			a.applyOutputs()
			a.saveConfig()
			if a.cfg.MidiDev != "" {
				a.say("MIDI output: "+a.cfg.MidiDev, false)
			}
			return
		}
		if name == "" {
			if p.kind == "load" {
				a.say("sets: "+a.listSets(), false)
			}
			return
		}
		switch p.kind {
		case "save":
			a.saveSet(name)
		case "midi":
			var n int
			if _, err := fmt.Sscanf(name, "%d", &n); err != nil || n < 1 || n > 600 {
				a.say("seconds must be a number from 1 to 600", true)
				return
			}
			a.exportMIDI(n)
		default:
			a.loadSet(name)
		}
	case k.kind == kBackspace:
		if len(p.buf) > 0 {
			p.buf = p.buf[:len(p.buf)-1]
		}
	case k.kind == kRune && !k.alt:
		p.buf = append(p.buf, k.r)
	case k.kind == kPaste:
		p.buf = append(p.buf, []rune(strings.TrimSpace(strings.SplitN(k.text, "\n", 2)[0]))...)
	}
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,60}$`)

func setPath(name string) (string, error) {
	name = strings.TrimSuffix(name, ".rb")
	if !nameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("use letters, digits, - _ . and spaces in set names")
	}
	return filepath.Join(paths.SetsDir(), name+".rb"), nil
}

func (a *App) saveSet(name string) {
	p, err := setPath(name)
	if err == nil {
		_, err = paths.Ensure(paths.SetsDir())
	}
	if err == nil {
		err = os.WriteFile(p, []byte(a.ed.Text()), 0o644)
	}
	if err != nil {
		a.say("save failed: "+err.Error(), true)
		return
	}
	a.setName, a.dirty = strings.TrimSuffix(name, ".rb"), false
	a.say("saved "+p, false)
}

func (a *App) loadSet(name string) {
	p, err := setPath(name)
	var b []byte
	if err == nil {
		b, err = os.ReadFile(p)
	}
	if err != nil {
		a.say("load failed: "+err.Error(), true)
		return
	}
	a.ed.SetText(string(b))
	a.lineSnap = a.ed.Lines()
	a.setName, a.dirty = strings.TrimSuffix(name, ".rb"), false
	a.errs, a.marks = nil, map[int]bool{}
	a.say("loaded "+a.setName+" — ^E runs a block, Esc-Enter runs everything", false)
}

func (a *App) listSets() string {
	ents, _ := os.ReadDir(paths.SetsDir())
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".rb"); ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "(none saved yet)"
	}
	return strings.Join(names, "  ")
}

func (a *App) toggleRecord() {
	if a.eng.Snapshot().Recording {
		d, err := a.eng.StopRecording()
		if err != nil {
			a.say("recording failed: "+err.Error(), true)
			return
		}
		a.say(fmt.Sprintf("saved %s (%.0fs)", a.recFile, d.Seconds()), false)
		return
	}
	if _, err := paths.Ensure(paths.RecordingsDir()); err != nil {
		a.say("cannot create recordings folder: "+err.Error(), true)
		return
	}
	a.recFile = filepath.Join(paths.RecordingsDir(), "set-"+time.Now().Format("20060102-150405")+".wav")
	if err := a.eng.StartRecording(a.recFile); err != nil {
		a.say("cannot record: "+err.Error(), true)
		return
	}
	a.say("recording → "+a.recFile, false)
}
