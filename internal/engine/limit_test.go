package engine

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/susynth/susynth/internal/synth"
)

func TestLimiterIsLinearBelowTheKneeAndNeverClips(t *testing.T) {
	for _, v := range []float32{0, 0.001, 0.1, 0.5, limitKnee} {
		if got := limit(v); got != v || limit(-v) != -v {
			t.Fatalf("limit(%v) = %v: not linear below the knee", v, got)
		}
	}
	prev := float32(0)
	for x := float32(0); x < 100; x += 0.001 {
		y := limit(x)
		if y < prev || y > limitCeil {
			t.Fatalf("limit(%v) = %v: must rise and stay under %v", x, y, limitCeil)
		}
		prev = y
	}
	// no corner at the knee: the slope just above it is still 1
	d := (limit(limitKnee+1e-3) - limitKnee) / 1e-3
	if d < 0.99 {
		t.Fatalf("slope just above the knee %.3f, want ~1", d)
	}
	if math.Round(float64(limitCeil)*masterScale) > 32767 {
		t.Fatal("ceiling overflows int16")
	}
}

// harmonics renders a held 1.5 kHz sine at gain g and returns its harmonic
// distortion as a fraction of the fundamental.
func harmonics(g float64) float64 {
	const f = 1500.0
	e := New()
	e.Play()
	e.Schedule(Hit{At: 0, Track: "s", Word: "a4", Gate: SampleRate * 3,
		Params: synth.Params{Wave: "sine", Note: 69 + 12*math.Log2(f/440), Gain: g, Attack: 0.01}})
	var l []float64
	for i := 0; i < 200; i++ {
		for k, v := range e.Render(480) {
			if k%2 == 0 {
				l = append(l, float64(v))
			}
		}
	}
	const n = 65536
	l = l[8192 : 8192+n]
	a := make([]complex128, n)
	for i := range a {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/n)
		a[i] = complex(l[i]*w, 0)
	}
	dft(a)
	band := func(hz float64) float64 {
		c := int(math.Round(hz * n / SampleRate))
		var s float64
		for k := c - 4; k <= c+4; k++ {
			if k > 0 && k < n/2 {
				s += real(a[k])*real(a[k]) + imag(a[k])*imag(a[k])
			}
		}
		return s
	}
	var h float64
	for k := 2.0; k*f < SampleRate/2; k++ {
		h += band(k * f)
	}
	return math.Sqrt(h / band(f))
}

// dft is an in-place radix-2 FFT.
func dft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wn := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := a[i+k], a[i+k+l/2]*wn
				a[i+k], a[i+k+l/2] = u+v, u-v
				wn *= w
			}
		}
	}
}

// Normal levels pass clean; only a very loud mix is bent at all.
func TestMasterStageDoesNotColourNormalLevels(t *testing.T) {
	for _, c := range []struct{ gain, most float64 }{{0.5, 0.0005}, {1, 0.0005}, {2, 0.0005}, {4, 0.01}} {
		if d := harmonics(c.gain); d > c.most {
			t.Errorf("gain %.1f: %.3f%% harmonic distortion, want under %.2f%%", c.gain, d*100, c.most*100)
		} else {
			t.Logf("gain %.1f: %.4f%% harmonic distortion", c.gain, d*100)
		}
	}
}
