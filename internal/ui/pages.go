package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/paths"
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
	armed    string // item armed for deletion (press d twice)
}

var trackLineRe = regexp.MustCompile(`(?m)^\s*d\d+\b`)

func (a *App) gotoPage(p page) {
	if p == a.page {
		return
	}
	a.page = p
	a.ps = pageState{}
	switch p {
	case pageSets:
		a.refreshSets()
	case pageRecs:
		a.refreshRecs()
	}
}

func (a *App) refreshSets() {
	a.ps.sets = nil
	ents, _ := os.ReadDir(paths.SetsDir())
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".syn")
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
			si.tracks = len(trackLineRe.FindAllString(string(b), -1))
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
	name, kind, desc, example string
	word, wave                string // what Enter auditions
}

var soundItems = []soundItem{
	{"bd", "drum", "kick: pitch-swept sine with a click", `d1 "bd*4"`, "bd", ""},
	{"sn", "drum", "snare: 185 Hz body plus filtered noise", `d2 "~ sn ~ sn"`, "sn", ""},
	{"cp", "drum", "clap: three noise bursts and a tail", `d2 "~ cp ~ cp"`, "cp", ""},
	{"hh", "drum", "closed hat (aliases: hat, ch)", `d3 "hh*8" # gain 0.5`, "hh", ""},
	{"oh", "drum", "open hat (alias: openhat)", `d4 "~ oh ~ oh"`, "oh", ""},
	{"rim", "drum", "rim shot: short 1.7 kHz ping", `d5 "~ rim ~ [rim rim]"`, "rim", ""},
	{"tom", "drum", "tom: falling sine, 90-180 Hz", `d5 "tom ~ tom:2 ~"`, "tom", ""},
	{"cb", "drum", "cowbell: two detuned squares", `d6 "cb ~ ~ cb"`, "cb", ""},
	{"sine", "tone", "pure sine; notes like c3 fs4 eb2 or MIDI 60", `d7 "c3 e3 g3 c4" # voice sine`, "c3", "sine"},
	{"tri", "tone", "triangle: soft and hollow", `d7 "<c3 f3 g3>/2" # voice tri`, "c3", "tri"},
	{"saw", "tone", "saw, low-passed: the default tone", `d7 "c2 ~ [c2 g2] ~" # voice saw`, "c2", "saw"},
	{"square", "tone", "square, low-passed: hollow bass/lead", `d7 "c2*2 eb2 g2" # voice square`, "c2", "square"},
	{"pluck", "tone", "Karplus-Strong plucked string", `d7 "c4 e4 g4 [b4 g4]" # voice pluck`, "c4", "pluck"},
	{"fm", "tone", "two-operator FM with a decaying bell attack", `d7 "c3 ~ g3 ~" # voice fm # room 0.3`, "c3", "fm"},
	{"supersaw", "tone", "five detuned saws: wide and thick", `d7 "<c3 eb3> ~ g3 ~" # voice supersaw # lpf 1500`, "c3", "supersaw"},
	{"organ", "tone", "additive organ: five harmonics", `d7 "c3 e3 g3 c4" # voice organ`, "c3", "organ"},
	{"noise", "tone", "filtered noise; the note sets the brightness", `d7 "c4 ~ ~ c5" # voice noise # rel 0.4`, "c4", "noise"},
}

// sounds is the built-in list followed by any sample banks that are loaded.
func (a *App) sounds() []soundItem {
	out := append([]soundItem(nil), soundItems...)
	for _, b := range synth.Banks() {
		out = append(out, soundItem{b.Name, "sample", fmt.Sprintf("%d file(s), %.1fs — use %s:0 %s:1 … to pick", len(b.Samples), b.Seconds(), b.Name, b.Name),
			fmt.Sprintf(`d8 "%s*4"`, b.Name), b.Name, ""})
	}
	return out
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
			if !a.toastErr {
				a.gotoPage(pagePlay)
			}
		case isRune('a') && a.ps.sel < len(a.ps.sets):
			a.addToSetlist(a.ps.sets[a.ps.sel].name)
		case isRune('n'):
			a.ed.SetText(starter)
			a.setName, a.dirty = "", false
			a.errs, a.marks = nil, map[int]bool{}
			a.say("new set — starter text loaded", false)
			a.gotoPage(pagePlay)
		case isRune('d') && a.ps.sel < len(a.ps.sets):
			name := a.ps.sets[a.ps.sel].name
			if armed != name {
				a.ps.armed = name
				a.say("press d again to delete set \""+name+"\"", true)
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
			if armed != r.name {
				a.ps.armed = r.name
				a.say("press d again to delete "+r.name, true)
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
		list := a.sounds()
		a.ps.sel = moveSel(a.ps.sel, len(list), k)
		it := list[a.ps.sel]
		switch {
		case k.kind == kEnter || isRune(' '):
			if err := a.eng.Audition(it.word, it.wave); err != nil {
				a.say(err.Error(), true)
			}
		case isRune('i'):
			a.ed.row = len(a.ed.lines) - 1
			a.ed.End()
			a.ed.Newline()
			a.ed.Newline()
			for _, r := range it.example {
				a.ed.Insert(r)
			}
			a.edited()
			a.say("inserted at the end of the set — ^E to play it", false)
			a.gotoPage(pagePlay)
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
		a.ps.sel = moveSel(a.ps.sel, len(a.setlist), k)
		switch {
		case k.kind == kEnter:
			a.goLive(a.ps.sel)
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
	r := []rune(s)
	if len(r) > n {
		if n < 1 {
			return ""
		}
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}

func (a *App) drawPage(f *frame, x, y, w, h int) {
	th := a.th
	title := strings.ToUpper(pageNames[a.page])
	f.text(x+1, y, title, th.Pane, 0)
	switch a.page {
	case pageSets:
		f.text(x+1+len(title)+2, y, fmt.Sprintf("%d saved · %s", len(a.ps.sets), paths.SetsDir()), th.Muted, w-len(title)-4)
		if len(a.ps.sets) == 0 {
			f.text(x+2, y+2, "no saved sets yet — press ^S on the Play page", th.Muted, w-3)
			return
		}
		rows := []string{pad("NAME", 28) + pad("TRACKS", 8) + pad("LINES", 8) + "MODIFIED"}
		for _, s := range a.ps.sets {
			cur := ""
			if s.name == a.setName {
				cur = " ●"
			}
			rows = append(rows, pad(s.name+cur, 28)+pad(fmt.Sprint(s.tracks), 8)+pad(fmt.Sprint(s.lines), 8)+s.mod.Format("2006-01-02 15:04"))
		}
		a.drawList(f, x, y+1, w, h-1, rows, a.ps.sel)
	case pageRecs:
		f.text(x+1+len(title)+2, y, fmt.Sprintf("%d recorded · %s", len(a.ps.recs), paths.RecordingsDir()), th.Muted, w-len(title)-4)
		if len(a.ps.recs) == 0 {
			f.text(x+2, y+2, "no recordings yet — press ^R while playing", th.Muted, w-3)
			return
		}
		rows := []string{pad("FILE", 32) + pad("LENGTH", 9) + pad("SIZE", 10) + "RECORDED"}
		for _, r := range a.ps.recs {
			s := int(r.dur.Seconds())
			length, size := fmt.Sprintf("%d:%02d", s/60, s%60), fmt.Sprintf("%.1f MB", float64(r.size)/1e6)
			if strings.HasSuffix(r.name, ".mid") {
				length, size = "midi", fmt.Sprintf("%.1f KB", float64(r.size)/1e3)
			}
			rows = append(rows, pad(r.name, 32)+pad(length, 9)+pad(size, 10)+r.mod.Format("2006-01-02 15:04"))
		}
		a.drawList(f, x, y+1, w, h-1, rows, a.ps.sel)
	case pageSounds:
		f.text(x+1+len(title)+2, y, "built-in sounds are synthesised (no licences); your sample banks follow", th.Muted, w-len(title)-4)
		rows := []string{pad("SOUND", 10) + pad("KIND", 7) + "DESCRIPTION"}
		list := a.sounds()
		for _, s := range list {
			rows = append(rows, pad(s.name, 10)+pad(s.kind, 7)+s.desc)
		}
		a.drawList(f, x, y+1, w, h-5, rows, a.ps.sel)
		it := list[a.ps.sel]
		f.hline(x+1, y+h-4, w-2, '─', th.Divider)
		f.text(x+2, y+h-3, "try it:", th.Muted, 0)
		f.text(x+10, y+h-3, it.example, th.String, w-12)
		f.text(x+2, y+h-2, "Enter plays a preview · i inserts this line into your set", th.Muted, w-3)
	case pageSettings:
		f.text(x+1+len(title)+2, y, "saved automatically", th.Muted, w-len(title)-4)
		step := 1
		if h >= 2*numSettings+5 {
			step = 2
		}
		for i, r := range settingRows {
			yy := y + 1 + i*step
			base := th.Text
			if i == a.ps.sel {
				f.fill(x, yy, w, 1, th.CursorRow)
				base = th.CursorRow
			}
			f.text(x+2, yy, r.label, base, 22)
			f.text(x+26, yy, "‹ "+pad(a.settingValue(i), 28)+" ›", style{fg: th.Keyword.fg, bg: base.bg, bold: true}, w-27)
		}
		f.hline(x+1, y+h-3, w-2, '─', th.Divider)
		f.text(x+2, y+h-2, settingRows[a.ps.sel].hint, th.Muted, w-3)
	case pagePerform:
		f.text(x+1+len(title)+2, y, fmt.Sprintf("set list · %d set(s)", len(a.setlist)), th.Muted, w-len(title)-4)
		if len(a.setlist) == 0 {
			f.text(x+2, y+2, "the set list is empty", th.Muted, w-3)
			f.text(x+2, y+3, "open F2 Sets, select a set and press a to add it", th.Muted, w-3)
			return
		}
		now, next := "—", "—"
		if a.live >= 0 && a.live < len(a.setlist) {
			now = a.setlist[a.live]
		}
		if n := a.live + 1; n >= 0 && n < len(a.setlist) {
			next = a.setlist[n]
		}
		f.text(x+2, y+2, "NOW ", th.Muted, 0)
		f.text(x+7, y+2, "▶ "+now, style{fg: th.StepOn.fg, bg: th.Base.bg, bold: true}, w-9)
		f.text(x+2, y+3, "NEXT", th.Muted, 0)
		f.text(x+7, y+3, next, th.Text, w-9)
		f.hline(x+1, y+5, w-2, '─', th.Divider)
		rows := []string{pad("#", 5) + pad("SET", 30) + "STATE"}
		for i, n := range a.setlist {
			state := ""
			if i == a.live {
				state = "● LIVE"
			}
			if _, err := os.Stat(filepath.Join(paths.SetsDir(), n+".syn")); err != nil {
				state += " (file missing)"
			}
			rows = append(rows, pad(fmt.Sprint(i+1), 5)+pad(n, 30)+state)
		}
		a.drawList(f, x, y+6, w, h-6, rows, a.ps.sel)
	case pageHelp:
		f.text(x+1+len(title)+2, y, "↑↓ PgUp PgDn scroll", th.Muted, w-len(title)-4)
		for i := 0; i < h-1; i++ {
			j := a.ps.top + i
			if j >= len(helpLines) {
				break
			}
			l := helpLines[j]
			switch {
			case l.head:
				f.text(x+2, y+1+i, l.text, th.Keyword, w-3)
			case l.key != "":
				f.text(x+2, y+1+i, pad(l.key, 18), th.String, 18)
				f.text(x+21, y+1+i, l.text, th.Text, w-22)
			default:
				f.text(x+2, y+1+i, l.text, th.Text, w-3)
			}
		}
	}
}

// drawList paints a header row (rows[0]) and the selectable rows beneath it.
func (a *App) drawList(f *frame, x, y, w, h int, rows []string, sel int) {
	th := a.th
	f.text(x+2, y, rows[0], th.Muted, w-3)
	visible := h - 1
	if visible < 1 {
		return
	}
	if sel < a.ps.top {
		a.ps.top = sel
	}
	if sel >= a.ps.top+visible {
		a.ps.top = sel - visible + 1
	}
	for i := 0; i < visible; i++ {
		j := a.ps.top + i
		if j >= len(rows)-1 {
			break
		}
		s := th.Text
		if j == sel {
			f.fill(x, y+1+i, w, 1, th.CursorRow)
			s = th.CursorRow
			f.put(x+1, y+1+i, '›', style{fg: th.Keyword.fg, bg: s.bg, bold: true})
		}
		f.text(x+2, y+1+i, rows[j+1], s, w-3)
	}
}

// pageHint is the key hint shown on the toast row when no toast is active.
func (a *App) pageHint() string {
	switch a.page {
	case pageSets:
		return "↑↓ select · Enter load · a add to set list · n new · d delete · Esc back"
	case pageRecs:
		return "↑↓ select · Enter show path · d delete · Esc back"
	case pageSounds:
		return "↑↓ select · Enter preview · i insert example · Esc back"
	case pageSettings:
		return "↑↓ select · ←→ change · Esc back"
	case pageHelp:
		return "↑↓ scroll · Esc back"
	case pagePerform:
		return "Enter go live · Esc n / Esc p next / previous · [ ] reorder · d remove · Esc back"
	}
	return "Esc+1-9 mute · Esc+s solo · Esc+-/= level · Esc+,/. tempo · Esc+t theme · F1-F7 pages"
}

type helpLine struct {
	head bool
	key  string
	text string
}

var helpLines = []helpLine{
	{head: true, text: "PAGES"},
	{key: "F1 … F7", text: "Play, Sets, Recordings, Sounds, Settings, Help, Perform"},
	{key: "Esc m", text: "next page (for keyboards without F-keys); Esc returns to Play"},
	{text: ""},
	{head: true, text: "PLAYING"},
	{key: "^E", text: "evaluate the block under the cursor; lands on the next cycle"},
	{key: "Esc Enter", text: "evaluate the whole buffer; tracks no longer in the text stop"},
	{key: "^G", text: "hush: clear every track and voice immediately"},
	{key: "^P", text: "play / stop the transport"},
	{key: "^R", text: "start / stop recording a WAV"},
	{key: "^S  ^O", text: "save / load a set by name"},
	{key: "Esc 1-9", text: "mute / unmute that track"},
	{key: "Esc s  n", text: "solo track n (0 clears)"},
	{key: "Esc - / =", text: "level of the track on the cursor line"},
	{key: "Esc , / .", text: "tempo -1 / +1 BPM (< > for 5)"},
	{key: "Esc t / a", text: "next theme / next audio output"},
	{key: "Esc g", text: "grid editor for the track on the cursor line (see below)"},
	{key: "Esc x", text: "export the set as a MIDI file"},
	{key: "Esc n / p", text: "perform: next / previous set in the set list, on the beat"},
	{key: "^Q", text: "quit (asks once if there are unsaved changes)"},
	{text: ""},
	{head: true, text: "STATEMENTS  (one per line, -- starts a comment)"},
	{key: `d1 "bd sn"`, text: "a track: d1 … d16, pattern in quotes"},
	{key: "# gain 0.8", text: "level 0..2        # pan -0.3  position -1..1"},
	{key: "# speed 1.2", text: "pitch and time   # voice saw  sine tri saw square pluck"},
	{key: "bpm 128", text: "tempo (20..400)"},
	{key: "mute n / unmute n", text: "silence or restore a track"},
	{key: "solo n / unsolo", text: "hear only one track / all again"},
	{key: "level n 0.7", text: "track level 0..1.5"},
	{key: "stop n / hush", text: "remove one track / everything"},
	{key: "delaytime 0.75", text: "global delay time in beats; delayfb 0.5 feedback; roomsize 0.7"},
	{text: ""},
	{head: true, text: "EFFECTS AND VOICES  (modifiers after the pattern)"},
	{key: "# lpf 800", text: "low-pass cutoff Hz        # hpf 200  high-pass cutoff"},
	{key: "# crush 6", text: "bit crusher (bits)        # shape 0.4  distortion 0..1"},
	{key: "# delay 0.4", text: "send to the echo bus      # room 0.3  send to the reverb"},
	{key: "# rel 0.5", text: "tone release in seconds"},
	{key: "# voice fm", text: "also supersaw organ noise (and sine tri saw square pluck)"},
	{text: ""},
	{head: true, text: "PATTERN FUNCTIONS  (chain with $, outermost first)"},
	{key: `d1 $ fast 2 $ "bd sn"`, text: "fast n · slow n · rev · palindrome · iter n · ply n"},
	{key: "every 4 (fast 2)", text: "apply a function on every 4th cycle: every 3 rev"},
	{key: "degradeBy 0.3", text: "randomly drop 30%   rotL 0.25 / rotR 0.25 shift"},
	{text: ""},
	{head: true, text: "SAMPLES, OSC, MIDI"},
	{text: "Put .wav files in sub-folders of the samples folder (see F5); the folder name"},
	{text: "becomes a sound: kick/ → \"kick\", \"kick:2\" picks the 3rd file. They override"},
	{text: "built-in drums of the same name. F5 also turns on OSC to SuperDirt and live"},
	{text: "MIDI out (a raw device such as /dev/snd/midiC1D0) with clock."},
	{text: ""},
	{head: true, text: "MINI-NOTATION  (inside the quotes)"},
	{key: "a b c", text: "steps share the cycle equally"},
	{key: "~", text: "rest"},
	{key: "[a b]", text: "group: fits in one step"},
	{key: "a*2   a/2", text: "twice as fast / half as fast"},
	{key: "<a b c>", text: "one per cycle, in turn"},
	{key: "a, b", text: "layers played together (inside [ ])"},
	{key: "a!3   a ! ", text: "repeat the step"},
	{key: "a@3   a _ _", text: "make the step 3 times as long"},
	{key: "a?   a?0.3", text: "randomly drop (same every time); optional chance"},
	{key: "a(3,8)", text: "Euclidean rhythm; a(3,8,2) rotates it"},
	{key: "bd:2", text: "variant: slightly different pitch"},
	{text: ""},
	{head: true, text: "WHEN SOMETHING GOES WRONG"},
	{text: "A bad eval changes nothing. The last good pattern keeps playing and a red row"},
	{text: "under the editor says which line and column. Fix it and press ^E again."},
}
