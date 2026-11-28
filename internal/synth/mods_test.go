package synth

import (
	"math"
	"testing"
)

func TestPitchShiftMod(t *testing.T) {
	const sr = 48000
	for _, c := range []struct{ semis, want float64 }{{12, 880}, {-12, 220}, {7, 440 * math.Pow(2, 7.0/12)}} {
		v, err := New("69", sr, 0, sr*2, Params{Gain: 1, Speed: 1, Wave: "sine", Note: 69, Release: 0.5,
			Mods: []Mod{{Kind: ModPitch, A: c.semis}}}, 3)
		if err != nil {
			t.Fatal(err)
		}
		x := make([]float64, sr)
		var sum float64
		for i := range x {
			l, _ := v.Next()
			x[i] = float64(l)
		}
		seg := x[sr/2 : sr/2+4800]
		for _, s := range seg {
			sum += s * s
		}
		best, lag := -1.0, 0
		for k := sr / 2000; k < sr/100; k++ {
			var cc float64
			for i := 0; i+k < len(seg); i++ {
				cc += seg[i] * seg[i+k]
			}
			if cc > best {
				best, lag = cc, k
			}
		}
		got := float64(sr) / float64(lag)
		if math.Abs(12*math.Log2(got/c.want)) > 0.4 || math.Sqrt(sum/float64(len(seg))) < 0.02 {
			t.Errorf("%+.0f semitones: %.1f Hz, want %.1f (level %.3f)", c.semis, got, c.want, math.Sqrt(sum/float64(len(seg))))
		}
	}
}
