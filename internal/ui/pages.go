package ui

import (
	txt "github.com/CurtisD4q/pmin-base/text"
	mint "github.com/CurtisD4q/pmin-kit"

	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/rubyvm"
	"github.com/susynth/susynth/internal/synth"
)

type page int

const (
	pagePlay page = iota
	pageSets
	pageRecs
	pageSounds
	pageSettings
	pageHelp
	pagePerform
	numPages
)

var pageNames = [numPages]string{"Play", "Sets", "Recordings", "Sounds", "Settings", "Help", "Perform"}

// pageStep reports +1 or -1 for a next/previous page key, otherwise 0.
// Esc f / Esc b (and Alt+f / Alt+b) are the pair to use: f and b are
// unshifted on Latin keyboards, and a terminal sends Alt+letter as ESC
// plus that letter. Esc ] / Esc [ and Esc M are older aliases (Esc m opens
// the pages pop-up). Alt+[ never arrives; ESC [ starts a cursor sequence.
func pageStep(r rune) int {
	switch r {
	case 'f', 'F', ']':
		return 1
	case 'b', 'B', '[', 'M':
		return -1
	default:
		return 0
	}
}

type setInfo struct {
	name          string
	mod           time.Time
	lines, tracks int
}

type recInfo struct {
	name, path string
	mod        time.Time
	size       int64
	dur        time.Duration
}

type pageState struct {
	sel, top int
	sets     []setInfo
	recs     []recInfo
	armed    string          // item armed for deletion (press d twice)
	closed   map[string]bool // Sounds groups that are folded up
	preview  setPreview      // the set shown in the Details box
}

// stepPage moves from a page by step, wrapping around the ends.
// from is where the key counts from, which can differ from a.page: Esc
// returns to Play before the following key arrives, and a cycle still
// starts on the page Esc was pressed on.
func (a *App) stepPage(from page, step int) {
	n := int(numPages)
	i := (int(from) + step) % n
	if i < 0 {
		i += n
	}
	a.gotoPage(page(i))
}

func (a *App) refreshSets() {
	a.ps.sets = nil
	ents, _ := os.ReadDir(paths.SetsDir())
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".rb") // as ^S saves them
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		si := setInfo{name: name, mod: info.ModTime()}
		if b, err := os.ReadFile(filepath.Join(paths.SetsDir(), e.Name())); err == nil {
			si.lines = strings.Count(string(b), "\n")
			si.tracks = len(rubyvm.LiveLoopNames(string(b)))
		}
		a.ps.sets = append(a.ps.sets, si)
	}
	sort.Slice(a.ps.sets, func(i, j int) bool { return a.ps.sets[i].mod.After(a.ps.sets[j].mod) })
}

func (a *App) refreshRecs() {
	a.ps.recs = nil
	ents, _ := os.ReadDir(paths.RecordingsDir())
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".wav") && !strings.HasSuffix(e.Name(), ".mid") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		r := recInfo{name: e.Name(), path: filepath.Join(paths.RecordingsDir(), e.Name()), mod: info.ModTime(), size: info.Size()}
		if r.size > 44 && strings.HasSuffix(r.name, ".wav") {
			r.dur = time.Duration(r.size-44) * time.Second / (engine.SampleRate * 4)
		}
		a.ps.recs = append(a.ps.recs, r)
	}
	sort.Slice(a.ps.recs, func(i, j int) bool { return a.ps.recs[i].mod.After(a.ps.recs[j].mod) })
}

// ---- sounds reference ------------------------------------------------------------

type soundItem struct {
	name, group, desc, example string
	word                       string // what Enter auditions ("" = nothing to preview)
	params                     synth.Params
}

var fxItems = []struct{ name, desc, example string }{
	{"reverb", "room sound; mix: and room: (also :gverb)", "with_fx :reverb, mix: 0.5 do\n  play :e4\nend"},
	{"echo", "repeats; phase: (beats) decay: mix: (also :ping_pong)", "with_fx :echo, phase: 0.375 do\n  play :e4\nend"},
	{"lpf", "low-pass filter; cutoff: as a note (also :rlpf :nlpf)", "with_fx :lpf, cutoff: 80 do\n  play :e2\nend"},
	{"hpf", "high-pass filter; cutoff: (also :rhpf :nhpf)", "with_fx :hpf, cutoff: 90 do\n  sample :bd_haus\nend"},
	{"bitcrusher", "lo-fi; bits:", "with_fx :bitcrusher, bits: 4 do\n  sample :drum_snare_hard\nend"},
	{"distortion", "drive; distort: 0..1 (also :krush :tanh)", "with_fx :distortion, distort: 0.6 do\n  play :e2\nend"},
	{"level", "volume; amp:", "with_fx :level, amp: 0.5 do\n  play :e4\nend"},
	{"pan", "stereo position; pan: -1..1", "with_fx :pan, pan: -0.8 do\n  play :e4\nend"},
}

// sounds lists everything on the Sounds page, in group order.
func (a *App) sounds() []soundItem {
	var out []soundItem
	for _, sy := range synth.SonicSynths {
		it := soundItem{name: sy.Name, desc: sy.Desc}
		if sy.Drum != "" {
			it.group, it.word = "Drum synths", sy.Drum
			it.example = "synth :" + sy.Name
		} else {
			it.group, it.word = "Synths", "c3"
			it.params = synth.Params{Wave: sy.Wave, Release: 0.6}
			it.example = "use_synth :" + sy.Name + "\nplay :e3, release: 0.5"
		}
		out = append(out, it)
	}
	for _, sm := range synth.SonicSamples {
		out = append(out, soundItem{name: sm.Name, group: "Samples", desc: "played by susynth's " + sm.Drum,
			example: "sample :" + sm.Name, word: sm.Drum})
	}
	for _, fx := range fxItems {
		out = append(out, soundItem{name: fx.name, group: "Effects", desc: fx.desc, example: fx.example})
	}
	for _, b := range synth.Banks() {
		out = append(out, soundItem{name: b.Name, group: "Sample banks",
			desc:    fmt.Sprintf("%s, %.1fs — sample :%s, 0 / 1 / … picks a file", txt.Count(len(b.Samples), "file"), b.Seconds(), b.Name),
			example: fmt.Sprintf("sample :%s, 0", b.Name), word: b.Name + ":0"})
	}
	return out
}

// soundGroups orders the Sounds page's drop-downs.
var soundGroups = []struct{ name, blurb string }{
	{"Synths", "use_synth :name, then play notes; each plays the nearest susynth voice"},
	{"Drum synths", "Sonic Pi's sc808 drums: synth :sc808_bassdrum"},
	{"Samples", "Sonic Pi sample names played by susynth's own drums"},
	{"Effects", "with_fx :name do … end; other Sonic Pi effects are skipped with a note"},
	{"Sample banks", "your WAV folders: sample :bank, n — or sample :file for one file"},
}

func groupOf(it soundItem) string { return it.group }

func blurbOf(g string) string {
	for _, x := range soundGroups {
		if x.name == g {
			return x.blurb
		}
	}
	return ""
}

// soundRow is one line of the Sounds page: a group header or a sound.
type soundRow struct {
	header bool
	group  string
	count  int
	item   soundItem
}

// soundRows is what is on screen: every group's header, then its sounds
// unless the group is folded up.
func (a *App) soundRows() []soundRow {
	list := a.sounds()
	var out []soundRow
	for _, g := range soundGroups {
		var items []soundItem
		for _, it := range list {
			if groupOf(it) == g.name {
				items = append(items, it)
			}
		}
		if len(items) == 0 && g.name == "Sample banks" {
			out = append(out, soundRow{header: true, group: g.name})
			continue
		}
		out = append(out, soundRow{header: true, group: g.name, count: len(items)})
		if a.ps.closed[g.name] {
			continue
		}
		for _, it := range items {
			out = append(out, soundRow{group: g.name, item: it})
		}
	}
	return out
}

func (a *App) toggleGroup(g string, open bool) {
	if a.ps.closed == nil {
		a.ps.closed = map[string]bool{}
	}
	a.ps.closed[g] = !open
}

// ---- keys ---------------------------------------------------------------------

func moveSel(sel, n int, k key) int {
	switch k.kind {
	case kUp:
		sel--
	case kDown:
		sel++
	case kPgUp:
		sel -= 10
	case kPgDn:
		sel += 10
	case kHome:
		sel = 0
	case kEnd:
		sel = n - 1
	}
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		sel = 0
	}
	return sel
}

// insertExample adds a sound's example to the end of the set and goes to
// the Play page to run it.
func (a *App) insertExample(it soundItem) {
	a.ed.row = len(a.ed.lines) - 1
	a.ed.End()
	a.ed.Newline()
	a.ed.Newline()
	for _, r := range it.example {
		if r == '\n' {
			a.ed.Newline()
			continue
		}
		a.ed.Insert(r)
	}
	a.edited()
	a.say("inserted at the end of the set — ^E to play it", false)
	a.gotoPage(pagePlay)
}

// sure asks before deleting name: a Mint pop-up, or, with no screen to
// show one on (the tests), a second press of the same key.
func (a *App) sure(armed, name, question, again string) bool {
	if a.inMint {
		return mint.Confirm(question, "It cannot be brought back.")
	}
	if armed != name {
		a.ps.armed = name
		a.say(again, true)
		return false
	}
	return true
}

func (a *App) pageKey(k key) {
	armed := a.ps.armed
	a.ps.armed = ""
	if k.kind == kEsc {
		a.gotoPage(pagePlay)
		return
	}
	isRune := func(r rune) bool { return k.kind == kRune && !k.alt && k.r == r }
	switch a.page {
	case pageSets:
		a.ps.sel = moveSel(a.ps.sel, len(a.ps.sets), k)
		switch {
		case k.kind == kEnter && a.ps.sel < len(a.ps.sets):
			a.loadSet(a.ps.sets[a.ps.sel].name)
			if !a.statusErr {
				a.gotoPage(pagePlay)
			}
		case isRune('a') && a.ps.sel < len(a.ps.sets):
			a.addToSetlist(a.ps.sets[a.ps.sel].name)
		case isRune('n'):
			a.ed.SetText(starter)
			a.lineSnap = a.ed.Lines()
			a.setName, a.dirty = "", false
			a.errs, a.marks = nil, map[int]bool{}
			a.say("new set — starter text loaded", false)
			a.gotoPage(pagePlay)
		case isRune('d') && a.ps.sel < len(a.ps.sets):
			name := a.ps.sets[a.ps.sel].name
			if !a.sure(armed, name, "Delete set "+name+"?", "press d again to delete set \""+name+"\"") {
				return
			}
			if p, err := setPath(name); err == nil && os.Remove(p) == nil {
				a.say("deleted set "+name, false)
				if a.setName == name {
					a.dirty = true
				}
			} else {
				a.say("could not delete "+name, true)
			}
			a.refreshSets()
			a.ps.sel = moveSel(a.ps.sel, len(a.ps.sets), key{})
		}
	case pageRecs:
		a.ps.sel = moveSel(a.ps.sel, len(a.ps.recs), k)
		switch {
		case k.kind == kEnter && a.ps.sel < len(a.ps.recs):
			a.say(a.ps.recs[a.ps.sel].path, false)
		case isRune('d') && a.ps.sel < len(a.ps.recs):
			r := a.ps.recs[a.ps.sel]
			if !a.sure(armed, r.name, "Delete "+r.name+"?", "press d again to delete "+r.name) {
				return
			}
			if os.Remove(r.path) == nil {
				a.say("deleted "+r.name, false)
			} else {
				a.say("could not delete "+r.name, true)
			}
			a.refreshRecs()
			a.ps.sel = moveSel(a.ps.sel, len(a.ps.recs), key{})
		}
	case pageSounds:
		rows := a.soundRows()
		a.ps.sel = moveSel(a.ps.sel, len(rows), k)
		row := rows[a.ps.sel]
		if row.header {
			open := !a.ps.closed[row.group]
			switch {
			case k.kind == kEnter || isRune(' '):
				a.toggleGroup(row.group, !open)
			case k.kind == kRight:
				a.toggleGroup(row.group, true)
			case k.kind == kLeft:
				a.toggleGroup(row.group, false)
			}
			return
		}
		it := row.item
		switch {
		case k.kind == kLeft: // fold the group and land on its header
			a.toggleGroup(row.group, false)
			for i, r := range a.soundRows() {
				if r.header && r.group == row.group {
					a.ps.sel = i
				}
			}
		case k.kind == kEnter || isRune(' '):
			if it.word == "" {
				a.say("effects change other sounds — i inserts an example to try", false)
			} else if err := a.eng.Audition(it.word, it.params, engine.SampleRate/3); err != nil {
				a.say(err.Error(), true)
			}
		case isRune('i'):
			a.insertExample(it)
		}
	case pageSettings:
		a.ps.sel = moveSel(a.ps.sel, len(settingRows), k)
		switch k.kind {
		case kLeft:
			a.changeSetting(a.ps.sel, -1, false)
		case kRight:
			a.changeSetting(a.ps.sel, 1, false)
		case kEnter:
			a.changeSetting(a.ps.sel, 1, true)
		}
	case pageHelp:
		a.ps.top = moveSel(a.ps.top, len(helpLines), k)
	case pagePerform:
		if a.ps.sel < 0 { // the cursor is on Now or Next
			if isRune('d') || isRune('[') || isRune(']') {
				a.say("move to a set in the list first", false)
			}
			return
		}
		a.ps.sel = moveSel(a.ps.sel, len(a.setlist), k)
		switch {
		case k.kind == kEnter:
			a.goLive(a.ps.sel)
		case isRune('i'):
			a.mixIn(a.ps.sel)
		case isRune('d') && a.ps.sel < len(a.setlist):
			a.removeFromSetlist(a.ps.sel)
			a.ps.sel = moveSel(a.ps.sel, len(a.setlist), key{})
		case isRune('['):
			a.ps.sel = a.moveInSetlist(a.ps.sel, -1)
		case isRune(']'):
			a.ps.sel = a.moveInSetlist(a.ps.sel, 1)
		}
	}
}

// ---- drawing ----------------------------------------------------------------------

func pad(s string, n int) string {
	s = txt.Clip(s, n)
	return s + strings.Repeat(" ", n-len([]rune(s)))
}

type helpLine struct {
	head bool
	key  string
	text string
}

var helpLines = []helpLine{
	{head: true, text: "PAGES"},
	{key: "m", text: "the pages pop-up beside the icons: ↑↓ choose, Enter open, m or Esc close"},
	{text: "In the editor on Play, m types the letter: press Esc m there."},
	{key: "Esc f  /  Esc b", text: "next / previous page"},
	{key: "Esc", text: "from any page, back to Play"},
	{text: "For Esc keys, press Esc, then the key within 1.5 s, or hold Alt."},
	{head: true, text: "KEYS ON EACH PAGE"},
	{key: "Sets", text: "↑↓ select · Enter load · a add to the set list · n new · d delete (asks first)"},
	{key: "Recordings", text: "↑↓ select · Enter show the path · d delete (asks first)"},
	{key: "Sounds", text: "↑↓ select · ←→ fold a group · Enter preview or fold · i insert the example"},
	{key: "Settings", text: "↑↓ select · ←→ change · Enter edit or reload where it says so"},
	{key: "Help", text: "↑↓ PgUp PgDn scroll"},
	{key: "Perform", text: "Enter go live · i mix in · Esc n / Esc p next / previous · [ ] reorder · d remove"},
	{text: ""},
	{head: true, text: "PLAYING"},
	{key: "^E", text: "run the block under the cursor (the whole live_loop; ┊ marks it)"},
	{key: "Esc Enter", text: "run the whole buffer, like Sonic Pi's Run"},
	{key: "^G", text: "stop every live_loop and thread, like Sonic Pi's Stop"},
	{key: "^P", text: "stop, or run the whole buffer again"},
	{key: "^R", text: "start / stop recording a WAV"},
	{key: "^S  ^O", text: "save / load a set by name (sets are .rb files)"},
	{key: "Esc u  n", text: "mute / unmute track n — a digit alone does nothing"},
	{key: "Esc k  n", text: "stop that track (Enter stops the loop under the cursor)"},
	{key: "Esc s  n", text: "solo track n (0 clears)"},
	{key: "Esc l", text: "session log (errors, puts, cue) in the right pane; Alt+↑↓ scrolls"},
	{key: "Esc - / =", text: "level of the live_loop the cursor is in"},
	{key: "Esc , / .", text: "close / open a filter on the live_loop the cursor is in (Esc < / > in big steps)"},
	{key: "Ctrl+↑↓", text: "nudge the number under the cursor; while playing it is heard on the next beat"},
	{text: "Shift+Ctrl+↑↓ moves it 10×. Esc ↑ / Esc ↓ do the same where Ctrl+↑↓ is taken (macOS)."},
	{key: "Esc r", text: "switch the right-hand pane between Tracks and the piano roll"},
	{key: "Esc t / a", text: "next theme / next audio output"},
	{key: "Esc x", text: "export the set as a MIDI file (runs silently in a second sandbox)"},
	{key: "Esc n / p", text: "perform: next / previous set in the set list"},
	{key: "Esc i", text: "mix in the next set: it starts muted on the next bar, beside what plays"},
	{key: "Esc o", text: "fade out the old set over 2 bars, then stop it"},
	{key: "^Q", text: "quit (asks first if there are unsaved changes)"},
	{text: ""},
	{head: true, text: "EDITING"},
	{key: "Shift + arrows", text: "select text (also Shift+Home/End/PgUp/PgDn); ^A selects everything"},
	{key: "^C  ^X  ^V", text: "copy / cut / paste; with nothing selected, copy or cut the whole line"},
	{key: "terminal paste", text: "Ctrl+Shift+V (Linux) or Cmd+V (macOS) pastes from other apps"},
	{key: "Backspace  Delete", text: "delete the selection (or one character)"},
	{key: "^Z  ^Y", text: "undo / redo"},
	{key: "^K", text: "delete the current line"},
	{text: "Copies also go to the system clipboard in terminals that support it (OSC 52)."},
	{text: ""},
	{head: true, text: "THE LANGUAGE  (Sonic Pi, running in real Ruby 3.4)"},
	{key: "live_loop :name do", text: "a loop that runs forever; re-run it to change it on its next pass"},
	{key: "  play :e3", text: "a note: :c4 :fs3 :eb2 or MIDI 60; chords: play chord(:e3, :minor)"},
	{key: "  sample :bd_haus", text: "a sample (see the Sounds page); sample :mybank, 2 for your own"},
	{key: "  sleep 0.5", text: "wait, in beats; every loop must sleep"},
	{key: "end", text: ""},
	{key: "use_bpm 120", text: "tempo (Sonic Pi starts at 60); with_bpm 90 do … end"},
	{key: "use_synth :prophet", text: "the synth for play; with_synth :pluck do … end"},
	{key: "amp: pan: release:", text: "options: attack: decay: sustain: release: cutoff: rate:"},
	{key: "with_fx :reverb do", text: "effects: reverb echo ping_pong lpf rlpf hpf bitcrusher distortion compressor eq chorus level pan"},
	{key: "sync :drums", text: "wait for the next pass of :drums (live_loops cue their name)"},
	{key: "in_thread do", text: "run something alongside; at [0, 1.5] do … end schedules it"},
	{key: "stop", text: "end the current loop or thread"},
	{text: ""},
	{head: true, text: "RINGS, RANDOMNESS, MUSIC THEORY"},
	{key: "(ring 1, 2, 3).tick", text: "step through a ring each pass; .look, .choose, .shuffle"},
	{key: "spread(3, 8)", text: "Euclidean rhythm as a ring of true/false"},
	{key: "scale(:e3, :minor)", text: "also :major :dorian :minor_pentatonic :blues_minor …"},
	{key: "chord(:e3, :m7)", text: "also :major :minor :dom7 :sus4 :dim; invert: 1"},
	{key: "rrand(0.5, 1)", text: "rrand_i choose dice one_in rand — repeatable, like Sonic Pi"},
	{key: "define :riff do |n|", text: "your own functions; or plain Ruby def, each, map, hashes…"},
	{key: "puts x", text: "prints x to the log (Esc l); cue :name does too"},
	{text: ""},
	{head: true, text: "HOW IT RUNS"},
	{text: "Your code runs in real Ruby inside a sandbox with no network or file access."},
	{text: "Sonic Pi's synths, samples and effects are played by susynth's own sounds, so"},
	{text: "the names work but the timbre is susynth's. MIDI and OSC calls in code are"},
	{text: "skipped: use the Settings page to send notes to MIDI devices or SuperDirt instead."},
	{text: ""},
	{head: true, text: "WHEN SOMETHING GOES WRONG"},
	{text: "A syntax error runs nothing, and a red row under the editor names the line."},
	{text: "An error inside a live_loop stops that loop only; the others keep playing."},
	{text: "A loop that never sleeps stops, with a \"did not sleep\" error. The others"},
	{text: "keep playing. A loop you delete is marked \"not in code\"; Esc k stops it."},
	{text: "Errors stay in the log for the session. Inserting a line moves their marker."},
}

// pageInfo is the few words set into a page's box beside its title.
func (a *App) pageInfo() string {
	switch a.page {
	case pageSets:
		return fmt.Sprintf("%d saved · %s", len(a.ps.sets), paths.SetsDir())
	case pageRecs:
		return fmt.Sprintf("%d recorded · %s", len(a.ps.recs), paths.RecordingsDir())
	case pageSounds:
		return "Sonic Pi names, played by susynth's own synthesised sounds · ←→ fold a group"
	case pageSettings:
		return "saved automatically"
	case pagePerform:
		return "set list · " + txt.Count(len(a.setlist), "set")
	case pageHelp:
		return "↑↓ PgUp PgDn scroll"
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
