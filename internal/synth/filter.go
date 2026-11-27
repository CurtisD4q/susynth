package synth

import "math"

// Filter is a resonant filter built from zero-delay-feedback state-variable
// stages (the "TPT" form): stable at any cutoff and resonance, and safe to
// move every sample. The low-pass is 24 dB/octave (two stages, Butterworth
// at no resonance); the high-pass is 12 dB/octave (one stage). Resonance
// (0..1) lifts the response around the cutoff, from flat at 0 to a strong
// ringing peak near 1, short of self-oscillation.
//
// The cutoff glides in pitch (evenly per octave), each sample moving part of
// the way to the target (about 5 ms), so sweeps and jumps are smooth. Snap skips the glide, for a
// note's first sample.
type Filter struct {
	high   bool
	g, gT  float32 // tan(pi*fc/sr), now and target
	lg     float64 // log of g while gliding
	k      [2]float32
	kT     [2]float32
	s      [2][2]float32 // stage, integrator
	active bool
}

// Butterworth damping (1/Q) for the two stages of a 4-pole low-pass.
const (
	bwK1 = 1 / 0.5412
	bwK2 = 1 / 1.3066
)

// filterGlide is the per-sample approach of the cutoff and resonance to
// their targets (time constant ~5 ms at 48 kHz).
const filterGlide = 1.0 / 240

// NewLowPass and NewHighPass make a filter at cutoff hz (sr the sample rate)
// with resonance res, already settled there.
func NewLowPass(hz, res, sr float64) Filter  { return newFilter(false, hz, res, sr) }
func NewHighPass(hz, res, sr float64) Filter { return newFilter(true, hz, res, sr) }

func newFilter(high bool, hz, res, sr float64) Filter {
	f := Filter{high: high}
	f.Set(hz, res, sr)
	f.Snap()
	return f
}

// Set aims the filter at a new cutoff (Hz) and resonance (0..1).
func (f *Filter) Set(hz, res, sr float64) {
	hz = math.Max(10, math.Min(hz, sr*0.49))
	res = math.Max(0, math.Min(res, 1))
	f.gT = float32(math.Tan(math.Pi * hz / sr))
	// the resonant stage's Q rises from its Butterworth value toward ~25
	q := 1.3066 / (1 - 0.95*res)
	if f.high {
		q = 0.7071 / (1 - 0.95*res)
		f.kT = [2]float32{float32(1 / q), 0}
	} else {
		f.kT = [2]float32{bwK1, float32(1 / q)}
	}
	f.active = true
}

// Snap jumps to the target cutoff and resonance.
func (f *Filter) Snap() { f.g, f.k = f.gT, f.kT; f.lg = math.Log(float64(f.g)) }

// Settled reports whether the filter has reached its target.
func (f *Filter) Settled() bool { return f.g == f.gT && f.k == f.kT }

// Active reports whether Set has been called.
func (f *Filter) Active() bool { return f.active }

// stage runs one 2-pole state-variable stage and returns its low-pass and
// high-pass outputs.
func stage(s *[2]float32, x, g, k float32) (lp, hp float32) {
	a1 := 1 / (1 + g*(g+k))
	a2 := g * a1
	a3 := g * a2
	v3 := x - s[1]
	v1 := a1*s[0] + a2*v3
	v2 := s[1] + a2*s[0] + a3*v3
	s[0] = 2*v1 - s[0] + flushF - flushF
	s[1] = 2*v2 - s[1] + flushF - flushF
	return v2, x - k*v1 - v2
}

// flushF clears values too small to hear before they turn denormal (slow).
const flushF = 1e-18

// Process filters one sample.
func (f *Filter) Process(x float32) float32 {
	if f.g != f.gT || f.k != f.kT {
		f.lg += (math.Log(float64(f.gT)) - f.lg) * filterGlide
		f.g = float32(math.Exp(f.lg))
		for i := range f.k {
			f.k[i] += (f.kT[i] - f.k[i]) * filterGlide
		}
		if near(f.g, f.gT) && near(f.k[0], f.kT[0]) && near(f.k[1], f.kT[1]) {
			f.g, f.k = f.gT, f.kT
		}
	}
	if f.high {
		_, hp := stage(&f.s[0], x, f.g, f.k[0])
		return hp
	}
	lp, _ := stage(&f.s[0], x, f.g, f.k[0])
	lp, _ = stage(&f.s[1], lp, f.g, f.k[1])
	return lp
}

func near(a, b float32) bool {
	d := a - b
	return d < 1e-6*b+1e-9 && d > -1e-6*b-1e-9
}
