package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/live"
	"github.com/susynth/susynth/internal/midi"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/rubyvm"
	"github.com/susynth/susynth/internal/synth"
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

// goLive loads set list entry i, stops what is playing and runs the new set.
// Unsaved edits ask first.
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
	a.seq.Stop(false)
	a.eval(true)
	if !a.toastErr {
		a.say(fmt.Sprintf("LIVE %d/%d: %s", i+1, len(a.setlist), name), false)
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

// exportMIDI runs the editor's code in a second, silent Ruby sandbox for the
// given number of seconds and writes what it played as a Standard MIDI File.
// The music that is playing is not interrupted.
func (a *App) exportMIDI(seconds int) {
	if ns := a.seq.Check(a.ed.Text()); len(ns) > 0 {
		a.errs = nil
		for _, n := range ns {
			a.errs = append(a.errs, codeErr{Line: n.Line - 1, Text: n.Text})
		}
		a.errRows = len(a.ed.lines)
		a.say("cannot export: fix the syntax error first", true)
		return
	}
	code := a.ed.Text()
	a.say(fmt.Sprintf("exporting %d seconds of MIDI…", seconds), false)
	go func() {
		vm := rubyvm.Start(paths.CacheDir())
		defer vm.Close()
		vm.SetTables(synth.SynthNames(), synth.SampleNames())
		<-vm.Ready()
		msg, isErr := func() (string, bool) {
			if err := vm.Err(); err != nil {
				return "export failed: " + err.Error(), true
			}
			hits, bpm, ns, err := live.Collect(vm, code, float64(seconds))
			if err != nil {
				return "export failed: " + err.Error(), true
			}
			for _, n := range ns {
				if n.Kind == "err" {
					return "export stopped by an error: " + n.Text, true
				}
			}
			notes, names, skipped := midi.FromHits(hits, bpm)
			if len(notes) == 0 {
				return "nothing to export: no drum or note sounds", true
			}
			if _, err := paths.Ensure(paths.RecordingsDir()); err != nil {
				return "cannot create folder: " + err.Error(), true
			}
			path := filepath.Join(paths.RecordingsDir(), "set-"+time.Now().Format("20060102-150405")+".mid")
			f, err := os.Create(path)
			if err == nil {
				err = midi.WriteSMF(f, bpm, names, notes)
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}
			if err != nil {
				return "export failed: " + err.Error(), true
			}
			m := fmt.Sprintf("exported %d notes, %ds → %s", len(notes), seconds, path)
			if skipped > 0 {
				m += fmt.Sprintf(" (%d sample hits skipped)", skipped)
			}
			return m, false
		}()
		a.later(msg, isErr)
	}()
}
