// Package ui is the terminal front end: editor, track panes, step display,
// transport bar and toasts. It lays out the screen the way Mint apps do
// (title, one divider, panes side by side, pinned bar) so it can move onto
// mint-kit unchanged once that is available.
package ui

import (
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
	"github.com/susynth/susynth/internal/lang"
	"github.com/susynth/susynth/internal/paths"
)

// Options come from the command line.
type Options struct {
	File  string // optional .syn file to open
	Sink  string // overrides config when non-empty
	Theme string // overrides config when non-empty
}

const starter = `-- susynth: write patterns, press ^E to play the block under the cursor.
-- Changes land on the next cycle. ^G hushes everything. ^Q quits.

bpm 124
d1 "bd*2 [~ bd] bd ~"
d2 "~ sn ~ [sn cp]" # gain 0.9
d3 "hh*8" # gain 0.5 # pan 0.3
d4 "c2 ~ [c2 g2] ~" # voice saw # gain 0.7

-- melody: edit the notes and press ^E again while it plays
d5 "<c4 e4 g4> [g4 e4]*2 ~ c5" # voice pluck # gain 0.6
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

	errs    []lang.Error
	errRows int // editor line count when errs were recorded
	marks   map[int]bool

	toast      string
	toastErr   bool
	toastUntil time.Time

	prompt   *prompt
	awaiting string // "solo" while waiting for a track digit

	page page
	ps   pageState

	outs         []engineOutput
	setlist      []string
	live         int       // set list index currently live, -1 = none
	discardUntil time.Time // a second go-live inside this window discards unsaved edits
	grid         *gridState

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
		marks: map[int]bool{}, audioFail: make(chan error, 1), live: -1}
	a.eng.SetBPM(cfg.BPM)
	a.loadInitial(o, first)
	a.loadSetlist()
	a.startAudio(cfg.Sink)
	if first && !a.toastErr {
		a.say("welcome! ^E plays the block under the cursor — audio: "+a.sink.Name(), false)
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
			a.setName = strings.TrimSuffix(filepath.Base(o.File), ".syn")
		} else {
			a.setName = strings.TrimSuffix(filepath.Base(o.File), ".syn")
		}
	case !first && a.cfg.AutoLoad && a.cfg.Set != "":
		if b, err := os.ReadFile(filepath.Join(paths.SetsDir(), a.cfg.Set+".syn")); err == nil {
			text, a.setName = string(b), a.cfg.Set
		}
	}
	a.ed = newEditor(text)
	// start on the first real statement so ^E does something straight away
	for i := range a.ed.lines {
		if t := strings.TrimSpace(a.ed.Line(i)); t != "" && !strings.HasPrefix(t, "--") && !strings.HasPrefix(t, "//") {
			a.ed.row = i
			break
		}
	}
}

func (a *App) shutdown() {
	if d, _ := a.eng.StopRecording(); d > 0 {
		_ = d
	}
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
	a.toast, a.toastErr = msg, isErr
	a.toastUntil = time.Now().Add(4 * time.Second)
	if isErr {
		a.toastUntil = time.Now().Add(7 * time.Second)
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

var trackRe = regexp.MustCompile(`^\s*d(\d+)\b`)

func (a *App) trackOnCursor() (int, bool) {
	m := trackRe.FindStringSubmatch(a.ed.Line(a.ed.row))
	if m == nil {
		return 0, false
	}
	var id int
	fmt.Sscanf(m[1], "%d", &id)
	return id, true
}

func (a *App) handleKey(k key) {
	if a.prompt != nil {
		a.promptKey(k)
		return
	}
	if a.awaiting == "solo" {
		a.awaiting = ""
		if k.kind == kRune && k.r >= '0' && k.r <= '9' {
			id := int(k.r - '0')
			if !a.eng.SetSolo(id) {
				a.say(fmt.Sprintf("no track d%d to solo", id), true)
			} else if id == 0 {
				a.say("solo cleared", false)
			}
		}
		return
	}
	if k.kind != kCtrl || k.r != 'q' {
		a.quitAsk = false
	}
	if k.kind == kFn {
		if k.r >= 1 && int(k.r) <= int(numPages) {
			a.gotoPage(page(k.r - 1))
		}
		return
	}
	if k.alt {
		if k.kind == kRune && k.r == 'm' {
			a.gotoPage((a.page + 1) % numPages)
			return
		}
		a.altKey(k)
		return
	}
	if a.page == pagePlay && a.grid != nil {
		if k.kind == kCtrl && strings.ContainsRune("gprq", k.r) {
			a.ctrlKey(k.r)
			return
		}
		a.gridKey(k)
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
	switch k.kind {
	case kCtrl:
		a.ctrlKey(k.r)
	case kEsc:
		a.toast = ""
	case kRune:
		a.ed.Insert(k.r)
		a.edited()
	case kTab:
		a.ed.Insert(' ')
		a.ed.Insert(' ')
		a.edited()
	case kEnter:
		a.ed.Newline()
		a.edited()
	case kBackspace:
		a.ed.Backspace()
		a.edited()
	case kDelete:
		a.ed.Delete()
		a.edited()
	case kUp:
		a.ed.Vert(-1)
	case kDown:
		a.ed.Vert(1)
	case kLeft:
		a.ed.Left()
	case kRight:
		a.ed.Right()
	case kHome:
		a.ed.Home()
	case kEnd:
		a.ed.End()
	case kPgUp:
		a.ed.Vert(-10)
	case kPgDn:
		a.ed.Vert(10)
	}
}

func (a *App) edited() {
	a.dirty = true
	if len(a.ed.lines) != a.errRows {
		a.errs, a.marks = nil, map[int]bool{} // rows moved: old markers would point at the wrong lines
	}
}

func (a *App) altKey(k key) {
	switch {
	case k.kind == kEnter:
		a.eval(true)
	case k.kind != kRune:
	case k.r >= '1' && k.r <= '9':
		id := int(k.r - '0')
		if !a.eng.ToggleMute(id) {
			a.say(fmt.Sprintf("no track d%d playing", id), true)
		}
	case k.r == 's':
		a.awaiting = "solo"
		a.say("solo: press a track number (0 clears)", false)
	case k.r == '-' || k.r == '=' || k.r == '+' || k.r == '_':
		id, ok := a.trackOnCursor()
		if !ok {
			a.say("put the cursor on a dN line to change its level", true)
			return
		}
		d := 0.1
		if k.r == '-' || k.r == '_' {
			d = -0.1
		}
		if v, ok := a.eng.AdjustLevel(id, d); ok {
			a.say(fmt.Sprintf("d%d level %d%%", id, int(v*100+0.5)), false)
		} else {
			a.say(fmt.Sprintf("d%d is not playing — eval it first", id), true)
		}
	case k.r == ',' || k.r == '.' || k.r == '<' || k.r == '>':
		d := map[rune]float64{',': -1, '.': 1, '<': -5, '>': 5}[k.r]
		a.eng.SetBPM(a.eng.BPM() + d)
		a.say(fmt.Sprintf("tempo %.0f BPM", a.eng.BPM()), false)
	case k.r == 'g':
		if a.page == pagePlay {
			a.startGrid()
		}
	case k.r == 'x':
		a.startPrompt("midi", "export MIDI, cycles (1-64): ", "8")
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
		a.eng.Hush()
		a.marks = map[int]bool{}
		a.say("hush — all tracks cleared (edit and ^E to bring them back)", false)
	case 'p':
		if a.eng.TogglePlay() {
			a.say("playing", false)
		} else {
			a.say("stopped", false)
		}
	case 'r':
		a.toggleRecord()
	case 's':
		a.startPrompt("save", "save set as: ", a.setName)
	case 'o':
		a.startPrompt("load", "load set: ", "")
		a.say("sets: "+a.listSets(), false)
	case 'k':
		a.ed.KillLine()
		a.edited()
	case 'l':
		a.errs = nil
		a.say("errors cleared", false)
	case 'c':
		a.say("^Q quits (^G is hush)", false)
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

func (a *App) eval(all bool) {
	start, end := 0, len(a.ed.lines)-1
	if !all {
		s, e, ok := a.ed.Block(a.ed.row)
		if !ok {
			a.say("nothing under the cursor — put it inside a block of lines", true)
			return
		}
		start, end = s, e
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		b.WriteString(a.ed.Line(i))
		b.WriteByte('\n')
	}
	prog := lang.Parse(b.String(), start)

	kept := a.errs[:0:0]
	for _, e := range a.errs {
		if e.Line < start || e.Line > end {
			kept = append(kept, e)
		}
	}
	a.errs, a.errRows = kept, len(a.ed.lines)

	if len(prog.Errors) > 0 {
		a.errs = append(a.errs, prog.Errors...)
		a.say(fmt.Sprintf("eval failed: %d error(s) — nothing changed, last good pattern keeps playing", len(prog.Errors)), true)
		return
	}
	if len(prog.Tracks) == 0 && len(prog.Commands) == 0 {
		a.say("nothing to evaluate in this block", true)
		return
	}
	for i := start; i <= end; i++ {
		delete(a.marks, i)
	}
	for _, t := range prog.Tracks {
		a.marks[t.Line] = true
	}
	for _, c := range prog.Commands {
		a.marks[c.Line] = true
	}
	wasPlaying := a.eng.Snapshot().Playing
	a.eng.Apply(prog, all)
	if !wasPlaying {
		a.eng.Play()
		a.say(fmt.Sprintf("playing — %d track(s)", len(prog.Tracks)), false)
		return
	}
	a.say("queued — lands on the next cycle", false)
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
			if _, err := fmt.Sscanf(name, "%d", &n); err != nil || n < 1 || n > 64 {
				a.say("cycles must be a number from 1 to 64", true)
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
	}
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,60}$`)

func setPath(name string) (string, error) {
	name = strings.TrimSuffix(name, ".syn")
	if !nameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("use letters, digits, - _ . and spaces in set names")
	}
	return filepath.Join(paths.SetsDir(), name+".syn"), nil
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
	a.setName, a.dirty = strings.TrimSuffix(name, ".syn"), false
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
	a.setName, a.dirty = strings.TrimSuffix(name, ".syn"), false
	a.errs, a.marks = nil, map[int]bool{}
	a.say("loaded "+a.setName+" — ^E to play a block, Esc-Enter evals everything", false)
}

func (a *App) listSets() string {
	ents, _ := os.ReadDir(paths.SetsDir())
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".syn"); ok {
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
