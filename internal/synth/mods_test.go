package synth

import (
	"math"
	"testing"
)

// rmsWindows plays a voice and returns its RMS level in windows of w frames.
func rmsWindows(v *Voice, n, w int) []float64 {
	var out []float64
	var sum float64
	for i := 1; i <= n; i++ {
		l, r := v.Next()
		sum += float64(l*l + r*r)
		if i%w == 0 {
			out = append(out, math.Sqrt(sum/float64(w)))
			sum = 0
		}
	}
	return out
}

func TestSlicerGatesOnTheBeat(t *testing.T) {
	sr := 48000
	p := Params{Gain: 1, Speed: 1, Wave: "saw", Note: 48, Release: 0.1,
		Mods: []Mod{{Kind: ModSlicer, Mix: 1, Phase: 0.25, Wave: WavePulse, Width: 0.5, Min: 0, Max: 1}}}
	v, _ := New("48", sr, 0, sr, p, 1)
	lv := rmsWindows(v, sr/2, sr/16) // 8 windows of 62.5 ms; a slice is two: on, on, off, off…
	for i, x := range lv {
		on := i%4 < 2
		if on && x < 0.1 || !on && x > 0.01 {
			t.Errorf("window %d level %.4f (on: %v) %v", i, x, on, lv)
		}
	}
	// a voice that starts half a slice into the effect is in step with it
	p.Mods[0].Origin = 0.125
	v, _ = New("48", sr, 0, sr, p, 1)
	if lv := rmsWindows(v, sr/4, sr/16); lv[0] > 0.01 || lv[1] > 0.01 || lv[2] < 0.1 {
		t.Errorf("origin ignored: %v", lv)
	}
}

func TestLFOShapes(t *testing.T) {
	st := &modState{m: Mod{Phase: 1, Wave: WaveSine, Width: 0.5}}
	if y := st.lfo(0); math.Abs(y-1) > 1e-9 {
		t.Errorf("sine starts at %v, want the top", y)
	}
	st.m.Wave = WaveTri
	if y := st.lfo(0.5); math.Abs(y) > 1e-9 {
		t.Errorf("tri at half way %v, want the bottom", y)
	}
	st.m.Wave = WaveSaw
	if a, b := st.lfo(0.1), st.lfo(0.3); b >= a {
		t.Errorf("saw rises: %v then %v", a, b)
	}
	a := st.lfo(0.1)
	st.m.Invert = true
	if y := st.lfo(0.1); math.Abs(y-(1-a)) > 1e-9 {
		t.Errorf("inverted saw %v, plain %v", y, a)
	}
}

func TestEffectsAreStable(t *testing.T) {
	sr := 48000
	for _, m := range []Mod{
		{Kind: ModPanSlicer, Mix: 1, Phase: 0.25, Wave: WavePulse, Width: 0.5, Min: -1, Max: 1},
		{Kind: ModTremolo, Mix: 1, Phase: 1, Wave: WaveTri, Min: 0.5, Max: 1},
		{Kind: ModWobble, Mix: 1, Phase: 0.25, Wave: WaveSaw, Min: 60, Max: 120, Res: 0.8},
		{Kind: ModFlanger, Mix: 1, Phase: 2, Wave: WaveSine, Delay: 5, Depth: 5, FB: 0.9},
		{Kind: ModBandPass, Mix: 1, Freq: 100, Res: 0.9},
		{Kind: ModBandEQ, Mix: 1, Freq: 100, Res: 0.6, DB: 12},
		{Kind: ModRing, Mix: 1, Freq: 30},
		{Kind: ModOctaver, Mix: 1, A: 1, B: 1, C: 1},
		{Kind: ModVowel, Mix: 1, A: 3, B: 4},
		{Kind: ModMono, Mix: 0.5},
	} {
		v, _ := New("48", sr, 0, sr, Params{Gain: 1, Speed: 1, Wave: "saw", Note: 48, Release: 0.1, Mods: []Mod{m}}, 1)
		lv := rmsWindows(v, sr, sr/4)
		for _, x := range lv[:3] {
			if math.IsNaN(x) || x > 2 || x < 1e-4 {
				t.Errorf("kind %d: levels %v", m.Kind, lv)
				break
			}
		}
	}
}
