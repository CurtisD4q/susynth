// Package audiotest measures audio for tests: how clean a tone is, how much
// it distorts or aliases, whether a change clicks, how a reverb decays, and
// how a filter responds. Every measure works on mono float64 signals at any
// sample rate; Split turns interleaved stereo into two of them.
package audiotest

import (
	"math"
	"math/cmplx"
)

// DB converts a power ratio to decibels.
func DB(power float64) float64 { return 10 * math.Log10(power+1e-300) }

// Split separates interleaved stereo (float32, float64 or int16 samples).
func Split[T ~float32 | ~float64 | ~int16](x []T) (l, r []float64) {
	l, r = make([]float64, len(x)/2), make([]float64, len(x)/2)
	for i := range l {
		l[i], r[i] = float64(x[2*i]), float64(x[2*i+1])
	}
	return l, r
}

// Sine is n samples of a unit sine at hz.
func Sine(sr, hz float64, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * hz * float64(i) / sr)
	}
	return x
}

// RMS is the root-mean-square level of x.
func RMS(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(max(len(x), 1)))
}

// Peak is the largest absolute sample.
func Peak(x []float64) float64 {
	m := 0.0
	for _, v := range x {
		m = math.Max(m, math.Abs(v))
	}
	return m
}

// Spectrum is the power spectrum of the longest power-of-two prefix of x,
// under a 4-term Blackman-Harris window (sidelobes 92 dB down, so window
// leakage does not pass for distortion). Bin k is k*sr/(2*len) Hz.
func Spectrum(x []float64) []float64 {
	n := 1
	for n*2 <= len(x) {
		n *= 2
	}
	a := make([]complex128, n)
	for i := range a {
		p := 2 * math.Pi * float64(i) / float64(n)
		w := 0.35875 - 0.48829*math.Cos(p) + 0.14128*math.Cos(2*p) - 0.01168*math.Cos(3*p)
		a[i] = complex(x[i]*w, 0)
	}
	fft(a)
	out := make([]float64, n/2)
	for i := range out {
		out[i] = real(a[i])*real(a[i]) + imag(a[i])*imag(a[i])
	}
	return out
}

func fft(a []complex128) {
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

// halfWidth is how many bins either side of a tone count as the tone: the
// window's main lobe is 4 bins wide, the rest is margin.
const halfWidth = 12

func bin(p []float64, sr, hz float64) int { return int(math.Round(hz * float64(len(p)*2) / sr)) }

func band(p []float64, c int) float64 {
	var s float64
	for k := c - halfWidth; k <= c+halfWidth; k++ {
		if k > 0 && k < len(p) {
			s += p[k]
		}
	}
	return s
}

// Purity is how far everything except the tone at hz sits below it, in dB:
// noise, distortion and aliasing together.
func Purity(x []float64, sr, hz float64) float64 {
	p := Spectrum(x)
	c := bin(p, sr, hz)
	var sig, rest float64
	for k := 1; k < len(p); k++ {
		if k >= c-halfWidth && k <= c+halfWidth {
			sig += p[k]
		} else {
			rest += p[k]
		}
	}
	return DB(sig / rest)
}

// THD is the harmonic distortion of a tone at hz: the summed level of its
// harmonics below Nyquist over its fundamental, as an amplitude ratio.
func THD(x []float64, sr, hz float64) float64 {
	p := Spectrum(x)
	var h float64
	for k := 2.0; k*hz < sr/2-hz; k++ {
		h += band(p, bin(p, sr, k*hz))
	}
	return math.Sqrt(h / band(p, bin(p, sr, hz)))
}

// Inharmonic is how far the energy between the harmonics of f (below
// limitHz) sits under the harmonics themselves, in dB. A periodic tone that
// aliases puts its folded-back energy there.
func Inharmonic(x []float64, sr, f, limitHz float64) float64 {
	p := Spectrum(x)
	near := make([]bool, len(p))
	for k := 1.0; k*f < sr/2; k++ {
		c := bin(p, sr, k*f)
		for d := -halfWidth; d <= halfWidth; d++ {
			if c+d > 0 && c+d < len(p) {
				near[c+d] = true
			}
		}
	}
	var harm, rest float64
	for k := 1; k < bin(p, sr, limitHz) && k < len(p); k++ {
		if near[k] {
			harm += p[k]
		} else {
			rest += p[k]
		}
	}
	return DB(harm / rest)
}

// LargestStep is the biggest jump between neighbouring samples. A change
// that clicks makes a jump far larger than the signal's own.
func LargestStep(x []float64) float64 {
	m := 0.0
	for i := 1; i < len(x); i++ {
		m = math.Max(m, math.Abs(x[i]-x[i-1]))
	}
	return m
}

// ClickRatio compares the largest step across a change (the end of before
// joined to after) with the steady signal's own largest step. Near 1 means
// the change is smooth; several times more is an audible click.
func ClickRatio(before, after []float64) float64 {
	steady := LargestStep(before[len(before)/2:])
	edge := LargestStep(append(append([]float64{}, before[len(before)-2:]...), after...))
	return edge / math.Max(steady, 1e-12)
}

// GainDB is the level of out over in, in dB (for filter responses: feed a
// sine, compare the settled output).
func GainDB(in, out []float64) float64 { return 20 * math.Log10(RMS(out)/RMS(in)) }

// Correlation is the normalised correlation of two signals: 1 identical,
// 0 unrelated (a wide stereo tail), -1 inverted.
func Correlation(a, b []float64) float64 {
	var ab, aa, bb float64
	for i := range min(len(a), len(b)) {
		ab += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	return ab / math.Sqrt(aa*bb+1e-300)
}

// DecayTime is the RT60 of an impulse response: how long its energy takes to
// fall 60 dB, fitted on the backwards-integrated (Schroeder) curve between
// -5 and -35 dB so noise at the end does not skew it.
func DecayTime(x []float64, sr float64) float64 {
	e := make([]float64, len(x))
	var s float64
	for i := len(x) - 1; i >= 0; i-- {
		s += x[i] * x[i]
		e[i] = s
	}
	if s == 0 {
		return 0
	}
	t5, t35 := -1, -1
	for i, v := range e {
		d := DB(v / s)
		if t5 < 0 && d <= -5 {
			t5 = i
		}
		if d <= -35 {
			t35 = i
			break
		}
	}
	if t5 < 0 || t35 <= t5 {
		return math.Inf(1)
	}
	return float64(t35-t5) / sr * 2
}

// EchoDensity is the normalised echo density of x from start (seconds) over
// win seconds: the share of samples more than one standard deviation from
// zero, over what Gaussian noise gives (0.3173). A dense, smooth reverb
// tail reaches ~1; a sparse, fluttering tail stays well below.
func EchoDensity(x []float64, sr, start, win float64) float64 {
	a, b := int(start*sr), int((start+win)*sr)
	b = min(b, len(x))
	if b <= a {
		return 0
	}
	seg := x[a:b]
	sd := RMS(seg)
	n := 0
	for _, v := range seg {
		if math.Abs(v) > sd {
			n++
		}
	}
	return float64(n) / float64(len(seg)) / 0.3173
}

// Roughness is the standard deviation, in dB, of the level in bandHz-wide
// bands from lo to hi Hz. A smooth (non-ringing) spectrum scores low; one
// with resonant peaks scores high.
func Roughness(x []float64, sr, lo, hi, bandHz float64) float64 {
	p := Spectrum(x)
	var vals []float64
	for f := lo; f+bandHz <= hi; f += bandHz {
		var s float64
		for k := bin(p, sr, f); k < bin(p, sr, f+bandHz); k++ {
			s += p[k]
		}
		vals = append(vals, DB(s))
	}
	var m, v float64
	for _, x := range vals {
		m += x
	}
	m /= float64(len(vals))
	for _, x := range vals {
		v += (x - m) * (x - m)
	}
	return math.Sqrt(v / float64(len(vals)))
}

// NoiseFloor is the RMS level of x in dB relative to full scale 1.0.
func NoiseFloor(x []float64) float64 { return 20 * math.Log10(RMS(x)+1e-300) }
