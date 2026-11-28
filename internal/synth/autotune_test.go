package synth

import (
	"math"
	"math/cmplx"
	"testing"
)

// freqOf finds the strongest partial of x (at sr) by a coarse DFT scan.
func freqOf(x []float64, sr float64) float64 {
	best, bf := 0.0, 0.0
	for f := 80.0; f < 1200; f += 0.5 {
		var s complex128
		for i, v := range x {
			s += complex(v, 0) * cmplx.Exp(complex(0, -2*math.Pi*f*float64(i)/sr))
		}
		if m := cmplx.Abs(s); m > best {
			best, bf = m, f
		}
	}
	return bf
}

func TestAutotuner(t *testing.T) {
	const sr = 48000.0
	run := func(f0 float64, opts map[string]float64) []float64 {
		fx := fxMakers["autotuner"](sr).(*simpleFX)
		for k, v := range opts {
			fx.o[k] = v
		}
		n := int(sr * 1.5)
		in := make([]float32, n*2)
		for i := 0; i < n; i++ {
			// a sawtooth-ish voice: harmonics keep the pitch clear
			var v float64
			for h := 1; h <= 6; h++ {
				v += math.Sin(2*math.Pi*f0*float64(h)*float64(i)/sr) / float64(h)
			}
			in[2*i], in[2*i+1] = float32(0.2*v), float32(0.2*v)
		}
		out := make([]float32, n*2)
		fx.Process(out, in)
		y := make([]float64, 0, n/2)
		for i := n / 2; i < n; i += 1 {
			y = append(y, float64(out[2*i]))
		}
		return y
	}
	// each pitch moves to the nearest semitone: 226 Hz to A3, 240 Hz to B3
	for _, c := range []struct{ in, want float64 }{{226, 220}, {240, 246.94}, {262, 261.63}} {
		y := run(c.in, nil)
		if got := freqOf(y[:12000], sr); math.Abs(got-c.want) > 1.2 {
			t.Errorf("%.0f Hz came out at %.1f Hz, want %.2f", c.in, got, c.want)
		}
	}
	// note: snaps to a given pitch; strength 0 leaves it
	if got := freqOf(run(226, map[string]float64{"note": 57})[:12000], sr); math.Abs(got-220) > 1.2 {
		t.Errorf("note: 57 gave %.1f Hz, want 220", got)
	}
	if got := freqOf(run(226, map[string]float64{"strength": 0})[:12000], sr); math.Abs(got-226) > 1.2 {
		t.Errorf("strength 0 gave %.1f Hz, want 226", got)
	}
	// the level stays about the same
	y := run(226, nil)
	var e float64
	for _, v := range y {
		e += v * v
	}
	var e0 float64
	for _, v := range run(226, map[string]float64{"strength": 0}) {
		e0 += v * v
	}
	if r := math.Sqrt(e / e0); r < 0.7 || r > 1.4 {
		t.Errorf("level ratio %.2f", r)
	}
}
