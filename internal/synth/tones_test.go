package synth

import (
	"math"
	"testing"
)

// pitchOf measures a voice's fundamental by autocorrelation.
func pitchOf(t *testing.T, wave string) (float64, float64) {
	t.Helper()
	sr := 48000
	v, err := New("52", sr, 0, sr, Params{Gain: 1, Speed: 1, Wave: wave, Note: 52, Release: 0.5}, 3)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, sr/2)
	var sum float64
	for i := range x {
		l, _ := v.Next()
		x[i] = float64(l)
		sum += x[i] * x[i]
	}
	seg := x[sr/10 : sr/10+6000]
	best, lag := -1.0, 0
	for k := sr / 1000; k < sr/50; k++ {
		var c float64
		for i := 0; i+k < len(seg); i++ {
			c += seg[i] * seg[i+k]
		}
		if c > best {
			best, lag = c, k
		}
	}
	return float64(sr) / float64(lag), math.Sqrt(sum / float64(len(x)))
}

func TestSonicPiVoices(t *testing.T) {
	want := 440 * math.Pow(2, (52.0-69)/12) // 164.8 Hz
	for _, w := range []string{"dsaw", "prophet", "blade", "bell", "piano", "hollow", "ambience"} {
		f, rms := pitchOf(t, w)
		semis := math.Abs(12 * math.Log2(f/want))
		if semis > 0.5 || rms < 0.02 || rms > 1.0 || math.IsNaN(rms) {
			t.Errorf("%s: %.1f Hz (want %.1f), level %.3f", w, f, want, rms)
		}
	}
}

func TestNewDrums(t *testing.T) {
	for _, d := range []string{"clave", "conga", "conga:8", "cym", "maracas"} {
		v, err := New(d, 48000, 0, 0, Params{Gain: 1, Speed: 1}, 1)
		if err != nil {
			t.Fatal(err)
		}
		n, peak := 0, float32(0)
		for !v.Done() && n < 48000*3 {
			l, _ := v.Next()
			peak = max(peak, l, -l)
			n++
		}
		if !v.Done() || peak < 0.05 || peak > 1 {
			t.Errorf("%s: %d frames, peak %v", d, n, peak)
		}
	}
}

func voiceWith(t *testing.T, wave string, opt map[string]float64) []float64 {
	t.Helper()
	sr := 48000
	v, err := New("52", sr, 0, sr, Params{Gain: 1, Speed: 1, Wave: wave, Note: 52, Release: 0.5, Opt: opt}, 3)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, sr/2)
	for i := range x {
		l, _ := v.Next()
		x[i] = float64(l)
	}
	return x
}

func rmsOf(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

// detune: moves the second oscillator, divisor: and depth: change fm's
// sound, and the mod_ sweep moves a note's pitch.
func TestSynthOptionsVoices(t *testing.T) {
	diff := func(a, b []float64) float64 {
		var s float64
		for i := range a {
			s += math.Abs(a[i] - b[i])
		}
		return s / float64(len(a))
	}
	for _, w := range []string{"dsaw", "dpulse", "dtri"} {
		if rms := rmsOf(voiceWith(t, w, nil)); rms < 0.02 || math.IsNaN(rms) {
			t.Errorf("%s: level %.3f", w, rms)
		}
		if d := diff(voiceWith(t, w, nil), voiceWith(t, w, map[string]float64{"det": 1})); d < 0.005 {
			t.Errorf("%s: detune: changed nothing (%.4f)", w, d)
		}
	}
	base := voiceWith(t, "fm", nil)
	if d := diff(base, voiceWith(t, "fm", map[string]float64{"div": 5, "dep": 3})); d < 0.01 {
		t.Errorf("fm: divisor/depth changed nothing (%.4f)", d)
	}
	if rms := rmsOf(base); rms < 0.02 || math.IsNaN(rms) {
		t.Errorf("fm level %.3f", rms)
	}
	// a pulse sweep of 12 semitones: the first half-period is an octave up
	m := &modOsc{rng: 12, period: 0.5, pw: 0.5, wave: 1}
	if m.semis(0.1) != 12 || m.semis(0.3) != 0 {
		t.Errorf("mod pulse: %v %v", m.semis(0.1), m.semis(0.3))
	}
	if d := diff(voiceWith(t, "saw", nil), voiceWith(t, "saw", map[string]float64{"mr": 7, "mp": 0.25})); d < 0.01 {
		t.Errorf("mod sweep changed nothing (%.4f)", d)
	}
}
