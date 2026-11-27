package synth

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

// renderTone plays a held note and returns the mono signal after its attack.
func renderTone(t *testing.T, wave string, note float64) []float64 {
	t.Helper()
	const sr, n = 48000, 65536
	v, err := New("c4", sr, 0, sr*3, Params{Wave: wave, Gain: 1, Note: note}, 1)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, sr/4+n)
	for i := range x {
		l, r := v.Next()
		x[i] = float64(l + r)
	}
	return x[sr/4:]
}

func TestSawDoesNotAlias(t *testing.T) {
	// fractional notes so harmonics never land on an exact bin pattern
	for _, c := range []struct {
		note, want float64 // want: inharmonic energy at least this many dB down
	}{{60.37, 50}, {72.37, 45}, {84.37, 40}, {96.37, 30}} {
		f := 440 * math.Pow(2, (c.note-69)/12)
		got := audiotest.Inharmonic(renderTone(t, "saw", c.note), 48000, f, 20000)
		t.Logf("saw %.0f Hz: aliasing %.1f dB down", f, got)
		if got < c.want {
			t.Errorf("saw %.0f Hz: aliasing only %.1f dB down, want %.0f", f, got, c.want)
		}
	}
}

// The saw keeps its level and character: only the folded-back energy goes.
func TestSawKeepsItsLevel(t *testing.T) {
	x := renderTone(t, "saw", 48)
	var s float64
	for _, v := range x {
		s += v * v
	}
	rms := math.Sqrt(s / float64(len(x)))
	// the old naive saw measured 0.279 here
	if rms < 0.27 || rms > 0.29 {
		t.Fatalf("saw C3 rms %.3f, want about 0.28 (as before)", rms)
	}
}
