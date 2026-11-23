package ui

import (
	txt "github.com/CurtisD4q/pmin-base/text"

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

// pageByNumber maps the digit drawn on a tab to that page. '1' is Play.
func pageByNumber(r rune) (page, bool) {
	i := int(r - '1')
	if i < 0 || i >= int(numPages) {
		return 0, false
	}
	return page(i), true
}

// pageStep reports +1 or -1 for a next/previous page key, otherwise 0.
// Esc f / Esc b (and Alt+f / Alt+b) are the pair to use: f and b are
// unshifted on Latin keyboards, and a terminal sends Alt+letter as ESC
// plus that letter. Esc ] / Esc [ and Esc m / Esc M are older aliases.
// Alt+[ never arrives; ESC [ starts a cursor sequence.
func pageStep(r rune) int {
	switch r {
	case 'f', 'F', ']', 'm':
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
}

var trackLineRe = regexp.MustCompile(`(?m)^\s*d\d+\b`)

func (a *App) gotoPage(p page) {
	if p == a.page {
		return
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
	s = txt.Clip(s, n)
	return s + strings.Repeat(" ", n-len([]rune(s)))
}

func (a *App) drawPage(f *frame, x, y, w, h int) {
	th := a.th
	title := pageNames[a.page]
	f.text(x+1, y, "▸ "+title, th.Pane, 0)
	switch a.page {
	case pageSets:
		f.text(x+1+len(title)+2+2, y, fmt.Sprintf("%d saved · %s", len(a.ps.sets), paths.SetsDir()), th.Muted, w-len(title)+2-4)
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
		f.text(x+1+len(title)+2+2, y, fmt.Sprintf("%d recorded · %s", len(a.ps.recs), paths.RecordingsDir()), th.Muted, w-len(title)+2-4)
		if len(a.ps.recs) == 0 {
			f.text(x+2, y+2, "no recordings yet — press ^R while playing", th.Muted, w-3)
			return
		}
		rows := []string{pad("FILE", 32) + pad("LENGTH", 9) + pad("SIZE", 10) + "RECORDED"}
		for _, r := range a.ps.recs {
			s := int(r.dur.Seconds())
			length, size := fmt.Sprintf("%d:%02d", s/60, s%60), txt.Bytes(r.size)
			if strings.HasSuffix(r.name, ".mid") {
				length = "midi"
			}
			rows = append(rows, pad(r.name, 32)+pad(length, 9)+pad(size, 10)+r.mod.Format("2006-01-02 15:04"))
		}
		a.drawList(f, x, y+1, w, h-1, rows, a.ps.sel)
	case pageSounds:
		f.text(x+1+len(title)+2+2, y, "Sonic Pi names, played by susynth's own synthesised sounds; ←→ fold a group", th.Muted, w-len(title)-6)
		rows := a.soundRows()
		if a.ps.sel >= len(rows) {
			a.ps.sel = len(rows) - 1
		}
		visible := h - 5
		if visible < 1 {
			return
		}
		if a.ps.sel < a.ps.top {
			a.ps.top = a.ps.sel
		}
		if a.ps.sel >= a.ps.top+visible {
			a.ps.top = a.ps.sel - visible + 1
		}
		for i := 0; i < visible && a.ps.top+i < len(rows); i++ {
			j := a.ps.top + i
			r := rows[j]
			yy := y + 1 + i
			base := th.Text
			if j == a.ps.sel {
				f.fill(x, yy, w, 1, th.CursorRow)
				base = th.CursorRow
			}
			if r.header {
				arrow := "▾"
				if a.ps.closed[r.group] {
					arrow = "▸"
				}
				hs := style{fg: th.Pane.fg, bg: base.bg, bold: true}
				f.text(x+1, yy, arrow+" "+r.group, hs, 0)
				note := fmt.Sprintf("%d", r.count)
				if r.group == "Sample banks" && r.count == 0 {
					note = "none loaded — put .wav files in " + paths.SamplesDir()
				}
				f.text(x+3+len(r.group)+2, yy, note, th.Muted.over(base), w-len(r.group)-6)
				continue
			}
			f.text(x+4, yy, pad(r.item.name, 20), style{fg: th.Keyword.fg, bg: base.bg, bold: true}, 20)
			f.text(x+25, yy, r.item.desc, base, w-26)
		}
		f.hline(x+1, y+h-4, w-2, '─', th.Divider)
		if r := rows[a.ps.sel]; !r.header {
			f.text(x+2, y+h-3, "try it:", th.Muted, 0)
			f.text(x+10, y+h-3, strings.ReplaceAll(r.item.example, "\n", " ⏎ "), th.String, w-12)
			f.text(x+2, y+h-2, "Enter plays a preview · i inserts this line into your set", th.Muted, w-3)
		} else {
			f.text(x+2, y+h-3, blurbOf(r.group), th.Muted, w-3)
			f.text(x+2, y+h-2, "Enter or ←→ opens and folds this group", th.Muted, w-3)
		}
	case pageSettings:
		f.text(x+1+len(title)+2+2, y, "saved automatically", th.Muted, w-len(title)+2-4)
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
		f.text(x+1+len(title)+2+2, y, "set list · "+txt.Count(len(a.setlist), "set"), th.Muted, w-len(title)+2-4)
		if len(a.setlist) == 0 {
			f.text(x+2, y+2, "the set list is empty", th.Muted, w-3)
			f.text(x+2, y+3, "open Esc 2 Sets, select a set and press a to add it", th.Muted, w-3)
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
		f.text(x+1+len(title)+2+2, y, "↑↓ PgUp PgDn scroll", th.Muted, w-len(title)+2-4)
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
				f.text(x+2, y+1+i, pad(l.key, 22), th.String, 22)
				f.text(x+25, y+1+i, l.text, th.Text, w-26)
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
		return "↑↓ select · ←→ fold group · Enter preview/toggle · i insert example · Esc back"
	case pageSettings:
		return "↑↓ select · ←→ change · Esc back"
	case pageHelp:
		return "↑↓ scroll · Esc back"
	case pagePerform:
		return "Enter go live · Esc n / Esc p next / previous · [ ] reorder · d remove · Esc back"
	}
	return "Shift+arrows select · ^C ^X ^V · Esc u mute · Esc k stop · Esc l log · Esc 6 help"
}

type helpLine struct {
	head bool
	key  string
	text string
}

var helpLines = []helpLine{
	{head: true, text: "PAGES"},
	{key: "Esc 1 … Esc 7", text: "Play, Sets, Recordings, Sounds, Settings, Help, Perform"},
	{key: "Esc f  /  Esc b", text: "next / previous page"},
	{key: "Esc", text: "from any page, back to Play"},
	{text: "The number is on each tab. Press Esc, then the key within 1.5 s, or hold Alt."},
	{text: "F1-F7 still open those pages when the terminal sends function keys."},
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
	{key: "Esc r", text: "switch the right-hand pane between Tracks and the piano roll"},
	{key: "Esc t / a", text: "next theme / next audio output"},
	{key: "Esc x", text: "export the set as a MIDI file (runs silently in a second sandbox)"},
	{key: "Esc n / p", text: "perform: next / previous set in the set list"},
	{key: "^Q", text: "quit (asks once if there are unsaved changes)"},
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
	{key: "  sample :bd_haus", text: "a sample (see Esc 4 Sounds); sample :mybank, 2 for your own"},
	{key: "  sleep 0.5", text: "wait, in beats; every loop must sleep"},
	{key: "end", text: ""},
	{key: "use_bpm 120", text: "tempo (Sonic Pi starts at 60); with_bpm 90 do … end"},
	{key: "use_synth :prophet", text: "the synth for play; with_synth :pluck do … end"},
	{key: "amp: pan: release:", text: "options: attack: decay: sustain: release: cutoff: rate:"},
	{key: "with_fx :reverb do", text: "effects: reverb echo lpf hpf bitcrusher distortion level pan"},
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
	{text: "skipped: use Esc 5 Settings to send notes to MIDI devices or SuperDirt instead."},
	{text: ""},
	{head: true, text: "WHEN SOMETHING GOES WRONG"},
	{text: "A syntax error runs nothing, and a red row under the editor names the line."},
	{text: "An error inside a live_loop stops that loop only; the others keep playing."},
	{text: "A loop that never sleeps stops, with a \"did not sleep\" error. The others"},
	{text: "keep playing. A loop you delete is marked \"not in code\"; Esc k stops it."},
	{text: "Errors stay in the log for the session. Inserting a line moves their marker."},
}
