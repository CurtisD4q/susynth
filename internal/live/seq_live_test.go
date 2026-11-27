package live

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/susynth/susynth/internal/engine"
)

// clock renders and steps Ruby in 10 ms blocks until seconds of audio exist.
func clock(e *engine.Engine, s *Seq, seconds float64) {
	for e.Now() < seconds {
		e.Render(engine.SampleRate / 100)
		s.tick()
	}
}

// heard lists the notes the engine has started, as "seconds track sound".
func heard(e *engine.Engine) []string {
	r := e.Roll(1000, 0)
	var out []string
	for _, n := range r.Notes {
		if n.At > r.Now {
			continue
		}
		w := n.Drum
		if w == "" {
			w = fmt.Sprint(n.Note)
		}
		out = append(out, fmt.Sprintf("%.2f %s %s", float64(n.At)/engine.SampleRate, n.Name, w))
	}
	sort.Strings(out)
	return out
}

func TestChangeIsHeardOnTheNextBeat(t *testing.T) {
	v := vm(t)
	e := engine.New()
	s := New(v, e)
	s.Run("use_bpm 60\nlive_loop :a do\n  sample :bd_haus\n  sleep 1\nend\n", 0)
	clock(e, s, 1.3)
	s.Run("use_bpm 60\nlive_loop :a do\n  sample :drum_snare_hard\n  sleep 1\nend\n", 2)
	clock(e, s, 3.5)
	got := strings.Join(heard(e), "\n")
	// Ruby had already sent the note at 1.5 s or so; the next beat is 2 s.
	for _, w := range []string{"1.03 a bd", "2.03 a sn", "3.03 a sn"} { // the first run started at 0.03
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if strings.Contains(got, "2.03 a bd") {
		t.Errorf("the old loop played on the beat it handed over:\n%s", got)
	}
	var ran *Note
	for _, n := range s.Notes() {
		if n.Kind == "ran" && n.First == 2 {
			n := n
			ran = &n
		}
		if n.Kind == "err" {
			t.Error(n.Text)
		}
	}
	if ran == nil || ran.When != OnBeat {
		t.Errorf("ran note %+v", ran)
	}
}

func TestMixInKeepsTheOldSetAndMutesTheNew(t *testing.T) {
	v := vm(t)
	e := engine.New()
	s := New(v, e)
	s.Run("use_bpm 120\nlive_loop :drums do\n  sample :bd_haus\n  sleep 1\nend\n", 0)
	clock(e, s, 0.7)
	if _, err := s.MixIn("use_bpm 120\nlive_loop :drums do\n  sample :drum_snare_hard\n  sleep 1\nend\nlive_loop :hats do\n  sample :drum_cymbal_closed\n  sleep 0.5\nend\n"); err != nil {
		t.Fatal(err)
	}
	clock(e, s, 3)
	tv := e.Snapshot().Tracks
	byName := map[string]engine.TrackView{}
	for _, tr := range tv {
		byName[tr.Name] = tr
	}
	if !byName["old/drums"].Old || byName["old/drums"].Mute || !byName["drums"].Mute || !byName["hats"].Mute {
		t.Fatalf("tracks %+v", tv)
	}
	got := strings.Join(heard(e), "\n")
	// the new set starts on the next bar, 2 s at 120 bpm
	// (the first run started at 0.03, and the bars count from there)
	if !strings.Contains(got, "2.03 drums sn") || !strings.Contains(got, "2.03 old/drums bd") || strings.Contains(got, "1.53 drums sn") {
		t.Errorf("notes:\n%s", got)
	}
	s.StopOld()
	clock(e, s, 6.5)
	for _, n := range heard(e) {
		if strings.Contains(n, "old/") && n > "3.20" {
			t.Errorf("the old set played after it was stopped: %s", n)
		}
	}
	for _, tr := range e.Snapshot().Tracks {
		if tr.Old {
			t.Errorf("old track left after StopOld: %+v", tr)
		}
	}
}
