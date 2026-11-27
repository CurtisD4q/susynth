package synth

import (
	"math"
	"math/cmplx"
	"testing"
)

func fftInPlace(a []complex128) {
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

// aliasDB is how far the energy between the harmonics (below 20 kHz) sits
// under the harmonics themselves: aliasing lands between them.
func aliasDB(x []float64, f float64) float64 {
	const sr = 48000
	n := len(x)
	a := make([]complex128, n)
	for i := range a {
		a[i] = complex(x[i]*(0.5-0.5*math.Cos(2*math.Pi*float64(i)/float64(n))), 0)
	}
	fftInPlace(a)
	near := make([]bool, n/2)
	for k := 1.0; k*f < sr/2; k++ {
		c := int(math.Round(k * f * float64(n) / sr))
		for d := -5; d <= 5; d++ {
			if c+d > 0 && c+d < n/2 {
				near[c+d] = true
			}
		}
	}
	var harm, rest float64
	for k := 1; k < int(20000*float64(n)/sr); k++ {
		p := real(a[k])*real(a[k]) + imag(a[k])*imag(a[k])
		if near[k] {
			harm += p
		} else {
			rest += p
		}
	}
	return 10 * math.Log10(harm/rest)
}

func TestSawDoesNotAlias(t *testing.T) {
	// fractional notes so harmonics never land on an exact bin pattern
	for _, c := range []struct {
		note, want float64 // want: inharmonic energy at least this many dB down
	}{{60.37, 50}, {72.37, 45}, {84.37, 40}, {96.37, 30}} {
		f := 440 * math.Pow(2, (c.note-69)/12)
		got := aliasDB(renderTone(t, "saw", c.note), f)
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
