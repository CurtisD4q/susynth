package live

import (
	"math"
	"os"
	"path/filepath"
	"strings"
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
	h, g, err := Translate(rubyvm.Msg{"ev", "0.500000", "bass", "7", "p", "tb303", "n=40.0,amp=0.8,pan=-0.5,a=0.1,r=0.25,cutoff=80.0,o_cutoff=80.0,room=0.4,rsize=0.7"})
	if err != nil {
		t.Fatal(err)
	}
	if h.At != 24000 || h.Track != "bass" || h.Line != 7 || h.Word != "40" || h.Params.Wave != "tb303" ||
		h.Params.Gain != 0.8 || h.Params.Pan != -0.5 || h.Params.Release != 0.25 || h.Params.Attack != 0.1 ||
		h.Gate != 4800 || h.Params.Opt["cutoff"] != 80 || h.Params.LPF != 0 || h.Params.Room != 0.4 || !g.RoomSet || g.RoomSize != 0.7 {
		t.Errorf("%+v %+v", h, g)
	}
	h, _, err = Translate(rubyvm.Msg{"ev", "1", "drums", "2", "s", "drum_tom_mid_hard", "amp=1,rate=-2"})
	if err != nil || !strings.HasSuffix(h.Word, "drum_tom_mid_hard") || h.Params.Speed != 2 || !h.Params.Reverse || h.Label != 'd' {
		t.Errorf("%+v %v", h, err)
	}
	h, _, err = Translate(rubyvm.Msg{"ev", "2", "x", "3", "c", "beep", "id=7,n=65.0,nsl=0.5"})
	if err != nil || h.Ctl == nil || h.Node != 7 || h.Ctl.Note != 65 || h.Ctl.NoteSlide != 0.5 || h.Ctl.Amp == h.Ctl.Amp {
		t.Errorf("control: %+v %v", h, err)
	}
	if _, _, err := Translate(rubyvm.Msg{"ev", "1", "x", "2", "s", "loop_no_such_sample", ""}); err == nil {
		t.Error("unknown sample should fail")
	}
}

// End to end: Ruby code → middleman → engine → audio, rendered offline.
func TestRenderSonicPiCode(t *testing.T) {
	v := vm(t)
	e := engine.New()
	var out []float32
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
`, 2.1, func(b []float32) error { out = append(out, b...); return nil })
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

// rms is in 16-bit steps, so the thresholds read as they did with int16.
func rms(buf []float32, from, to int) float64 {
	var s float64
	for i := from * 2; i < to*2 && i < len(buf); i++ {
		v := float64(buf[i]) * 32768
		s += v * v
	}
	return math.Sqrt(s / float64((to-from)*2))
}

// A live_loop that never sleeps must not restart Ruby or hush the other loops.
func TestStuckLoopLeavesTheOthersRunning(t *testing.T) {
	v := vm(t)
	e := engine.New()
	s := New(v, e)
	s.Run("live_loop :ok do\n  sample :bd_haus\n  sleep 1\nend\nlive_loop :bad do\n  loop do\n    1 + 1\n  end\nend\n", 0)
	if !s.Running() {
		t.Fatal("run did not start")
	}
	s.tick()
	if !s.Running() {
		t.Fatal("a stuck loop stopped every loop")
	}
	if e.Snapshot().Hushed {
		t.Fatal("the engine was silenced")
	}
	saw := false
	for _, n := range s.Notes() {
		if n.Kind == "err" && strings.Contains(n.Text, "did not sleep") && strings.Contains(n.Text, "bad") {
			saw = true
		}
	}
	if !saw {
		t.Fatal("no did-not-sleep error for :bad")
	}
}

// With changes on the next bar, a run while music is already playing waits
// for the bar. The first run still starts at once, because that starts the bar.
func TestQuantiseSecondRunLandsOnTheBar(t *testing.T) {
	v := vm(t)
	e := engine.New()
	e.SetBPM(60)
	s := New(v, e)
	s.SetChangeOn(OnBar)
	s.Run("use_bpm 60\nsample :bd_haus\n", 0)
	s.Run("sample :bd_haus\n", 0)
	s.tick() // the immediate hit is due now; the bar hit waits until Ruby is advanced to it
	var out []float32
	for i := 0; i < 450; i++ { // 4.5 s, one bar is 4 s
		out = append(out, e.Render(480)...)
		s.tick()
	}
	if rms(out, 1400, 4000) < 200 {
		t.Error("the first run should sound straight away")
	}
	if rms(out, 170000, 188000) > 40 {
		t.Errorf("second hit arrived before the bar (rms %.0f)", rms(out, 170000, 188000))
	}
	if rms(out, 192000, 196000) < 200 {
		t.Errorf("second hit was not on the bar (rms %.0f)", rms(out, 192000, 196000))
	}
	var quantised bool
	for _, n := range s.Notes() {
		if n.Kind == "ran" && n.When == OnBar {
			quantised = true
		}
		if n.Kind == "err" {
			t.Errorf("error: %+v", n)
		}
	}
	if !quantised {
		t.Error("the second run was not marked as waiting for the bar")
	}
}

// Real time: the sequencer keeps Ruby ahead of the audio clock, and a
// redefined loop takes over on the next beat.
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
	if len(tv) != 1 || tv[0].Name != "a" || !strings.HasSuffix(tv[0].Src, "drum_snare_hard") {
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

// res:, the resonant filter effects, reverb damp: and ping-pong all reach
// the engine from real Ruby code.
func TestRubyPassesFilterAndEffectSettings(t *testing.T) {
	v := vm(t)
	ms, err := v.Run(1, 0, 0, `
with_fx :rlpf, cutoff: 90, res: 0.7 do
  play 60, res: 0.3
end
play 62, res: 0.4
with_fx :rhpf, res: 0.6 do
  play 64
end
with_fx :reverb, damp: 0.8 do
  play 65
end
with_fx :ping_pong, phase: 0.5 do
  play 67
end
with_fx :echo do
  play 69
end
`)
	if err != nil {
		t.Fatal(err)
	}
	more, err := v.Advance(1)
	if err != nil {
		t.Fatal(err)
	}
	ms = append(ms, more...)
	var hs []engine.Hit
	var gs []Globals
	for _, m := range ms {
		if m.Kind() == "ev" {
			h, g, err := Translate(m)
			if err != nil {
				t.Fatal(err)
			}
			hs, gs = append(hs, h), append(gs, g)
		}
	}
	if len(hs) != 6 {
		t.Fatalf("%d notes", len(hs))
	}
	if hs[0].Params.Res != 0.7 || hs[1].Params.Res != 0.4 || hs[2].Params.HPRes != 0.6 || hs[2].Params.HPF == 0 {
		t.Errorf("filters: %+v / %+v / %+v", hs[0].Params, hs[1].Params, hs[2].Params)
	}
	if !gs[3].RoomSet || gs[3].RoomDamp != 0.8 {
		t.Errorf("reverb damp: %+v", gs[3])
	}
	if !gs[4].DelaySet || !gs[4].PingPong || !gs[5].DelaySet || gs[5].PingPong {
		t.Errorf("ping-pong: %+v, echo: %+v", gs[4], gs[5])
	}
}

// Envelope levels and tb303's filter defaults reach the engine from Ruby.
func TestRubyPassesEnvelopes(t *testing.T) {
	v := vm(t)
	ms, err := v.Run(1, 0, 0, `
use_bpm 60
play 60, attack: 0.1, decay: 0.2, sustain: 0.3, release: 0.4, attack_level: 0.8, sustain_level: 0.3
use_synth :tb303
play 40
play 40, cutoff: 90, cutoff_min: 50, cutoff_release: 0.2, res: 0.2
use_synth :saw
play 40
`)
	if err != nil {
		t.Fatal(err)
	}
	more, err := v.Advance(1)
	if err != nil {
		t.Fatal(err)
	}
	var ps []synth.Params
	var gates []int
	for _, m := range append(ms, more...) {
		if m.Kind() == "ev" {
			h, _, err := Translate(m)
			if err != nil {
				t.Fatal(err)
			}
			ps, gates = append(ps, h.Params), append(gates, h.Gate)
		}
	}
	if len(ps) != 4 {
		t.Fatalf("%d notes", len(ps))
	}
	a := ps[0]
	if a.Attack != 0.1 || a.Decay != 0.2 || a.Release != 0.4 || a.AttackLevel != 0.8 || a.SustainLevel != 0.3 ||
		a.DecayLevel != 0 || gates[0] != 28800 {
		t.Errorf("envelope: %+v gate %d", a, gates[0])
	}
	b := ps[1] // tb303 is a native voice: it has its own filter, and its defaults live there
	if b.Wave != "tb303" || b.LPF != 0 || b.CutoffMin != 0 || b.Opt["attack"] != 0.01 || b.EnvShape != 5 || b.EnvCurve != 2 {
		t.Errorf("tb303 defaults: %+v", b)
	}
	c := ps[2]
	if c.Opt["cutoff"] != 90 || c.Opt["cutoff_min"] != 50 || c.Opt["res"] != 0.2 || math.Abs(c.Opt["cutoff_release"]-0.2) > 1e-9 {
		t.Errorf("tb303 options: %+v", c)
	}
	if d := ps[3]; d.LPF != 0 || d.CutoffMin != 0 || d.Res != 0 {
		t.Errorf("saw should have no filter by default: %+v", d)
	}
}

// The compressor, EQ and chorus settings reach the engine from Ruby.
func TestRubyPassesDynamics(t *testing.T) {
	v := vm(t)
	ms, err := v.Run(1, 0, 0, `
with_fx :compressor, threshold: 0.1, slope_above: 0.3 do
  play 60
end
with_fx :eq, low_shelf: 3, mid: -2, mid_note: 81 do
  play 62
end
with_fx :chorus, mix: 0.4, rate: 1.2 do
  play 64
end
play 65
`)
	if err != nil {
		t.Fatal(err)
	}
	more, err := v.Advance(1)
	if err != nil {
		t.Fatal(err)
	}
	var hs []engine.Hit
	var gs []Globals
	for _, m := range append(ms, more...) {
		if m.Kind() == "ev" {
			h, g, err := Translate(m)
			if err != nil {
				t.Fatal(err)
			}
			hs, gs = append(hs, h), append(gs, g)
		}
	}
	if len(hs) != 4 {
		t.Fatalf("%d notes", len(hs))
	}
	if !hs[0].Params.Comp || !gs[0].CompSet || gs[0].Comp.Threshold != 0.1 || gs[0].Comp.SlopeAbove != 0.3 {
		t.Errorf("compressor: %+v %+v", hs[0].Params, gs[0])
	}
	if !hs[1].Params.EQ || gs[1].EQ.LowDB != 3 || gs[1].EQ.MidDB != -2 || math.Abs(gs[1].EQ.MidHz-midiHz(81)) > 0.01 {
		t.Errorf("eq: %+v", gs[1])
	}
	if hs[2].Params.Chorus != 0.4 || gs[2].Chorus.Rate != 1.2 {
		t.Errorf("chorus: %+v %+v", hs[2].Params, gs[2])
	}
	if p := hs[3].Params; p.Comp || p.EQ || p.Chorus != 0 {
		t.Errorf("a plain note picked up dynamics: %+v", p)
	}
}
