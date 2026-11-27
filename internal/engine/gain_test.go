package engine

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/synth"
)

// held starts one long sine note (detuned so a block edge is never a zero
// crossing) and renders a quarter second.
func held(t *testing.T) (*Engine, []int16) {
	t.Helper()
	e := New()
	e.Play()
	e.Schedule(Hit{At: 0, Track: "s", Word: "a4", Gate: SampleRate * 3,
		Params: synth.Params{Wave: "sine", Note: 70.3, Gain: 1, Attack: 0.01}})
	var out []int16
	for i := 0; i < 25; i++ {
		out = append(out, e.Render(480)...)
	}
	return e, out
}

// largestStep is the biggest jump between neighbouring left samples.
func largestStep(x []int16) float64 {
	m := 0.0
	for i := 2; i < len(x); i += 2 {
		m = math.Max(m, math.Abs(float64(x[i])-float64(x[i-2])))
	}
	return m
}

func renderBlocks(e *Engine, n int) []int16 {
	var out []int16
	for i := 0; i < n; i++ {
		out = append(out, e.Render(480)...)
	}
	return out
}

// A gain change mid-note must not jump: the step between samples may grow
// only a little beyond the tone's own.
func TestTrackGainChangesDoNotClick(t *testing.T) {
	changes := map[string]func(e *Engine){
		"mute":  func(e *Engine) { e.ToggleMute(1) },
		"level": func(e *Engine) { e.AdjustLevel(1, -0.5) },
		"solo other": func(e *Engine) {
			e.ThreadStarted("other", true) // a second, silent loop
			e.SetSolo(2)
		},
	}
	for name, change := range changes {
		e, pre := held(t)
		steady := largestStep(pre[len(pre)/2:])
		change(e)
		post := renderBlocks(e, 4)
		edge := largestStep(append(pre[len(pre)-2:], post...))
		if edge > steady*1.3 {
			t.Errorf("%s: step %.0f at the change, %.0f steady: it clicks", name, edge, steady)
		}
	}
}

func TestMuteGoesSilentAndUnmuteComesBack(t *testing.T) {
	e, _ := held(t)
	e.ToggleMute(1)
	renderBlocks(e, 2)
	if p := peak(renderBlocks(e, 2)); p != 0 {
		t.Fatalf("muted track still sounds, peak %d", p)
	}
	e.ToggleMute(1)
	renderBlocks(e, 2)
	if p := peak(renderBlocks(e, 2)); p < 3000 {
		t.Fatalf("unmuted note did not come back, peak %d", p)
	}
}

// The first note of a new track starts at its full level: the glide is only
// for changes, not for attacks.
func TestNewTrackStartsAtFullGain(t *testing.T) {
	e := New()
	e.Play()
	e.Schedule(Hit{At: 0, Track: "k", Word: "bd", Params: synth.Params{Gain: 1}})
	a := e.Render(480)
	if p := peak(a[:96]); p < 1000 {
		t.Fatalf("kick attack softened: peak %d in its first 1 ms", p)
	}
}

// Stop fades notes out over a few milliseconds instead of cutting them.
func TestStopFadesInsteadOfCutting(t *testing.T) {
	e, pre := held(t)
	steady := largestStep(pre[len(pre)/2:])
	e.Stop()
	post := renderBlocks(e, 2)
	if edge := largestStep(append(pre[len(pre)-2:], post...)); edge > steady*1.3 {
		t.Fatalf("stop clicks: step %.0f, %.0f steady", edge, steady)
	}
	if p := peak(post[len(post)/2:]); p != 0 {
		t.Fatalf("notes still sounding 20 ms after stop, peak %d", p)
	}
}

func peak(x []int16) int {
	m := 0
	for _, v := range x {
		m = max(m, int(math.Abs(float64(v))))
	}
	return m
}
