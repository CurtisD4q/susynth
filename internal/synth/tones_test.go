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
	for _, w := range []string{"dsaw", "prophet", "blade", "bell", "piano", "hollow", "ambience", "rhodey", "horn"} {
		f, rms := pitchOf(t, w)
		semis := math.Abs(12 * math.Log2(f/want))
		if w == "horn" { // its sub-octave can set the period
			semis = math.Min(semis, math.Abs(semis-12))
		}
		if semis > 0.5 || rms < 0.02 || rms > 0.6 || math.IsNaN(rms) {
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
