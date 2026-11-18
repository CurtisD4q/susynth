package live

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/rubyvm"
	"github.com/susynth/susynth/internal/synth"
)

func vm(t *testing.T) *rubyvm.VM {
	t.Helper()
	v := rubyvm.Start(filepath.Join(os.TempDir(), "susynth-test-wasm"))
	v.SetTables(synth.SynthNames(), synth.SampleNames())
	select {
	case <-v.Ready():
	case <-time.After(5 * time.Minute):
		t.Fatal("Ruby did not start")
	}
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v
}

func TestTranslate(t *testing.T) {
	h, g, err := Translate(rubyvm.Msg{"ev", "0.500000", "bass", "7", "p", "tb303", "n=40.0,amp=0.8,pan=-0.5,a=0.1,r=0.25,cutoff=80.0,room=0.4,rsize=0.7"})
	if err != nil {
		t.Fatal(err)
	}
	if h.At != 24000 || h.Track != "bass" || h.Line != 7 || h.Word != "40" || h.Params.Wave != "saw" ||
		h.Params.Gain != 0.8 || h.Params.Pan != -0.5 || h.Params.Release != 0.25 || h.Params.Attack != 0.1 ||
		h.Gate != 4800 || math.Abs(h.Params.LPF-830.61) > 0.1 || h.Params.Room != 0.4 || !g.RoomSet || g.RoomSize != 0.7 {
		t.Errorf("%+v %+v", h, g)
	}
	h, _, err = Translate(rubyvm.Msg{"ev", "1", "drums", "2", "s", "drum_tom_mid_hard", "amp=1,rate=-2"})
	if err != nil || h.Word != "tom:4" || h.Params.Speed != 2 || h.Label != 't' {
		t.Errorf("%+v %v", h, err)
	}
	if _, _, err := Translate(rubyvm.Msg{"ev", "1", "x", "2", "s", "loop_amen", ""}); err == nil {
		t.Error("unknown sample should fail")
	}
}

// End to end: Ruby code → middleman → engine → audio, rendered offline.
func TestRenderSonicPiCode(t *testing.T) {
	v := vm(t)
	e := engine.New()
	var out []int16
	notes, err := Render(v, e, `
use_bpm 120
live_loop :drums do
  sample :bd_haus
  sleep 1
end
live_loop :bass do
  sync :drums
  use_synth :saw
  play :e2, release: 0.3
  sleep 1
end
`, 2.1, func(b []int16) error { out = append(out, b...); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Kind == "err" {
			t.Errorf("error: %+v", n)
		}
	}
	if e.BPM() != 120 {
		t.Errorf("bpm %v", e.BPM())
	}
	// beats at 0, 0.5 s, 1 s ...: loud right after each, quieter just before
	for _, beat := range []int{0, 24000, 48000, 72000} {
		if r := rms(out, beat+200, beat+2400); r < 300 {
			t.Errorf("beat at %d is quiet (%.0f)", beat, r)
		}
	}
}

func rms(buf []int16, from, to int) float64 {
	var s float64
	for i := from * 2; i < to*2 && i < len(buf); i++ {
		s += float64(buf[i]) * float64(buf[i])
	}
	return math.Sqrt(s / float64((to-from)*2))
}

// Real time: the sequencer keeps Ruby ahead of the audio clock, and a
// redefined loop takes over on its next pass.
func TestRealtimeRedefine(t *testing.T) {
	v := vm(t)
	e := engine.New()
	s := New(v, e)
	s.Run("live_loop :a do\n  sample :bd_haus\n  sleep 0.25\nend\n", 0)
	stopAt := time.Now().Add(1200 * time.Millisecond)
	words := map[string]int{}
	for time.Now().Before(stopAt) {
		e.Render(480)
		s.tick()
		time.Sleep(time.Millisecond)
		if time.Now().After(stopAt.Add(-700*time.Millisecond)) && words["redefined"] == 0 {
			words["redefined"] = 1
			s.Run("live_loop :a do\n  sample :drum_snare_hard\n  sleep 0.25\nend\n", 0)
		}
	}
	if !s.Running() {
		t.Fatal("not running")
	}
	tv := e.Snapshot().Tracks
	if len(tv) != 1 || tv[0].Name != "a" || tv[0].Src != "sn" {
		t.Errorf("tracks after redefine: %+v", tv)
	}
	for _, n := range s.Notes() {
		if n.Kind == "err" {
			t.Errorf("error: %+v", n)
		}
	}
	s.Stop(false)
	if len(e.Snapshot().Tracks) != 0 {
		t.Error("tracks left after stop")
	}
}
