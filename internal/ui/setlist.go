package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/lang"
	"github.com/susynth/susynth/internal/midi"
	"github.com/susynth/susynth/internal/paths"
	"path/filepath"
)

// ---- set list (performance mode) -----------------------------------------------------

func (a *App) loadSetlist() {
	a.setlist = nil
	b, err := os.ReadFile(paths.SetlistFile())
	if err != nil {
		return
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			a.setlist = append(a.setlist, l)
		}
	}
}

func (a *App) saveSetlist() {
	if _, err := paths.Ensure(paths.ConfigDir()); err == nil {
		err = os.WriteFile(paths.SetlistFile(), []byte(strings.Join(a.setlist, "\n")+"\n"), 0o644)
		if err != nil {
			a.say("could not save the set list: "+err.Error(), true)
		}
	}
}

func (a *App) addToSetlist(name string) {
	a.setlist = append(a.setlist, name)
	a.saveSetlist()
	a.say(fmt.Sprintf("added \"%s\" to the set list (position %d) — F7 to perform", name, len(a.setlist)), false)
}

func (a *App) removeFromSetlist(i int) {
	if i < 0 || i >= len(a.setlist) {
		return
	}
	a.setlist = append(a.setlist[:i], a.setlist[i+1:]...)
	if a.live == i {
		a.live = -1
	} else if a.live > i {
		a.live--
	}
	a.saveSetlist()
}

func (a *App) moveInSetlist(i, d int) int {
	j := i + d
	if i < 0 || j < 0 || i >= len(a.setlist) || j >= len(a.setlist) {
		return i
	}
	a.setlist[i], a.setlist[j] = a.setlist[j], a.setlist[i]
	switch a.live {
	case i:
		a.live = j
	case j:
		a.live = i
	}
	a.saveSetlist()
	return j
}

// goLive loads set list entry i and evaluates the whole set; it lands on the
// next cycle, so the changeover is on the beat. Unsaved edits ask first.
func (a *App) goLive(i int) {
	if i < 0 || i >= len(a.setlist) {
		return
	}
	if a.dirty && time.Now().After(a.discardUntil) {
		a.discardUntil = time.Now().Add(4 * time.Second)
		a.say("unsaved changes in the editor — repeat to discard them and go live (or ^S first)", true)
		return
	}
	a.discardUntil = time.Time{}
	name := a.setlist[i]
	a.loadSet(name)
	if a.toastErr {
		return
	}
	a.live = i
	a.eval(true)
	if !a.toastErr {
		a.say(fmt.Sprintf("LIVE %d/%d: %s — lands on the next cycle", i+1, len(a.setlist), name), false)
	}
}

// liveStep moves to the next (+1) or previous (-1) set in the list.
func (a *App) liveStep(d int) {
	if len(a.setlist) == 0 {
		a.say("the set list is empty — add sets on F2 with 'a'", true)
		return
	}
	i := a.live + d
	if a.live < 0 {
		i = 0
		if d < 0 {
			i = len(a.setlist) - 1
		}
	}
	if i < 0 || i >= len(a.setlist) {
		a.say("that is the end of the set list", true)
		return
	}
	a.goLive(i)
}

// ---- MIDI file export ------------------------------------------------------------------

// exportMIDI writes the editor's tracks as a Standard MIDI File.
func (a *App) exportMIDI(cycles int) {
	prog := lang.Parse(a.ed.Text(), 0)
	if len(prog.Errors) > 0 {
		a.errs = append([]lang.Error(nil), prog.Errors...)
		a.errRows = len(a.ed.lines)
		a.say(fmt.Sprintf("cannot export: %d error(s) in the set — fix them first", len(prog.Errors)), true)
		return
	}
	notes, names, skipped := midi.FromProgram(prog, cycles)
	if len(notes) == 0 {
		a.say("nothing to export: no tracks with drum or note sounds", true)
		return
	}
	if _, err := paths.Ensure(paths.RecordingsDir()); err != nil {
		a.say("cannot create folder: "+err.Error(), true)
		return
	}
	path := filepath.Join(paths.RecordingsDir(), "set-"+time.Now().Format("20060102-150405")+".mid")
	f, err := os.Create(path)
	if err == nil {
		err = midi.WriteSMF(f, midi.BPMOf(prog, a.eng.BPM()), names, notes)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		a.say("export failed: "+err.Error(), true)
		return
	}
	msg := fmt.Sprintf("exported %d notes, %d cycles → %s", len(notes), cycles, path)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d sample hits skipped)", skipped)
	}
	a.say(msg, false)
}
