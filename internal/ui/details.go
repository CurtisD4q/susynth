package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	txt "github.com/CurtisD4q/pmin-base/text"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/rubyvm"
)

// Details.
//
// On a wide window the Sets and Recordings pages show, beside the list, a
// Details box for the row under the cursor: for a set, its live loops and
// the start of its code; for a recording, how long it is and where it is.

// detailsMinW is the narrowest window that has room for a Details box.
const detailsMinW = 100

// setPreview is a set's code, read once and kept until the set changes.
type setPreview struct {
	key   string
	lines []string
	loops []string
}

// hasDetails reports whether this page shows a Details box at width w.
func (a *App) hasDetails(w int) bool {
	if w < detailsMinW {
		return false
	}
	switch a.page {
	case pageSets:
		return len(a.ps.sets) > 0
	case pageRecs:
		return len(a.ps.recs) > 0
	}
	return false
}

// previewSet reads the set under the cursor, unless it is the one already
// read: the window redraws many times a second.
func (a *App) previewSet(s setInfo) setPreview {
	key := s.name + "\x00" + s.mod.String()
	if a.ps.preview.key == key {
		return a.ps.preview
	}
	p := setPreview{key: key}
	if path, err := setPath(s.name); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			text := string(b)
			p.lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
			for name := range rubyvm.LiveLoopNames(text) {
				p.loops = append(p.loops, ":"+name)
			}
			sort.Strings(p.loops)
		}
	}
	a.ps.preview = p
	return p
}

// drawDetails draws the row under the cursor in full, from row y+1, in the
// box area at x, w wide and h tall.
func (a *App) drawDetails(f *frame, x, y, w, h int) {
	th := a.th
	bold := style{fg: th.Text.fg, bg: th.Base.bg, bold: true}
	row := y + 1
	line := func(s string, st style) {
		if row < y+h {
			f.text(x, row, s, st, w)
			row++
		}
	}
	switch a.page {
	case pageSets:
		if a.ps.sel >= len(a.ps.sets) {
			return
		}
		s := a.ps.sets[a.ps.sel]
		p := a.previewSet(s)
		line(s.name, bold)
		line(fmt.Sprintf("%s · modified %s", txt.Count(s.lines, "line"), s.mod.Format("2006-01-02 15:04")), th.Muted)
		if len(p.loops) > 0 {
			line("live loops: "+strings.Join(p.loops, " "), th.Muted)
		}
		if s.name == a.setName {
			if a.dirty {
				line("open in the editor, with unsaved changes", th.Pending)
			} else {
				line("open in the editor", th.Muted)
			}
		}
		row++
		for i, l := range p.lines {
			if row >= y+h {
				break
			}
			if row == y+h-1 && i < len(p.lines)-1 {
				f.text(x, row, "…", th.Muted, w)
				break
			}
			a.drawCode(f, x, row, []rune(l), 0, w, th.Text)
			row++
		}
	case pageRecs:
		if a.ps.sel >= len(a.ps.recs) {
			return
		}
		r := a.ps.recs[a.ps.sel]
		line(r.name, bold)
		kind := "MIDI notes: open it in a DAW or a notation program"
		if strings.HasSuffix(r.name, ".wav") {
			s := int(r.dur.Seconds())
			line(fmt.Sprintf("%d:%02d · %s", s/60, s%60, txt.Bytes(r.size)), th.Muted)
			kind = fmt.Sprintf("WAV audio, 16-bit stereo at %d Hz", engine.SampleRate)
		} else {
			line(txt.Bytes(r.size), th.Muted)
		}
		line("recorded "+r.mod.Format("2006-01-02 15:04"), th.Muted)
		row++
		line(kind, th.Text)
		row++
		line(r.path, th.Muted)
	}
}
