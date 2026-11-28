package synth

import "math"

// A sample's own pitch: option. Sonic Pi's sample player shifts the pitch
// without changing the speed, so the voice does the same with a shifter of
// its own (the with_fx :pitch_shift effect is a unit, fxdsp6.go).

// ModKind is which effect a Mod is.
type ModKind int

const (
	ModNone  ModKind = iota
	ModPitch         // a sample's pitch: A semitones, without changing speed
)

// Mod is one effect on a voice.
type Mod struct {
	Kind  ModKind
	A     float64 // semitones
	Delay float64 // the shifter's window, milliseconds (0: 100)
}

type modState struct {
	m    Mod
	line [2][]float32
	w    int
	pp   float64 // where the first tap is in its window, 0..1
}

// AddMods gives a voice its effects.
func (v *Voice) AddMods(ms []Mod) {
	for _, m := range ms {
		st := &modState{m: m}
		if m.Kind == ModPitch {
			win := m.Delay / 1000
			if win <= 0 {
				win = 0.1
			}
			n := int(win*v.sr) + 4
			st.line = [2][]float32{make([]float32, n), make([]float32, n)}
		}
		v.mods = append(v.mods, st)
	}
}

// runMods runs the effects on one stereo frame.
func (v *Voice) runMods(l, r float32) (float32, float32) {
	for _, st := range v.mods {
		if st.m.Kind != ModPitch {
			continue
		}
		// Two taps on a delay line, half a window apart, each sweeping
		// through it at the rate that shifts the pitch, and faded in and
		// out so that their sum stays level.
		n := len(st.line[0])
		win := float64(n - 4)
		ratio := math.Pow(2, st.m.A/12)
		p1, p2 := st.pp, st.pp+0.5
		if p2 >= 1 {
			p2--
		}
		s1, s2 := math.Sin(math.Pi*p1), math.Sin(math.Pi*p2)
		g1, g2 := float32(s1*s1), float32(s2*s2)
		in := [2]float32{l, r}
		var out [2]float32
		for c := 0; c < 2; c++ {
			st.line[c][st.w] = in[c]
			tap := func(p float64) float32 {
				pos := float64(st.w) - p*win
				for pos < 0 {
					pos += float64(n)
				}
				i := int(pos)
				f := float32(pos - float64(i))
				return st.line[c][i%n]*(1-f) + st.line[c][(i+1)%n]*f
			}
			out[c] = tap(p1)*g1 + tap(p2)*g2
		}
		st.w = (st.w + 1) % n
		st.pp -= (ratio - 1) / win
		st.pp -= math.Floor(st.pp)
		l, r = out[0], out[1]
	}
	return l, r
}

// biquad is a direct-form-I second-order filter (RBJ cookbook).
type biquad struct {
	b0, b1, b2, a1, a2 float32
	x1, x2, y1, y2     float32
}

func (q *biquad) run(x float32) float32 {
	y := q.b0*x + q.b1*q.x1 + q.b2*q.x2 - q.a1*q.y1 - q.a2*q.y2
	if y > -1e-20 && y < 1e-20 {
		y = 0
	}
	q.x2, q.x1, q.y2, q.y1 = q.x1, x, q.y1, y
	return y
}

func norm(b0, b1, b2, a0, a1, a2 float64) biquad {
	return biquad{b0: float32(b0 / a0), b1: float32(b1 / a0), b2: float32(b2 / a0), a1: float32(a1 / a0), a2: float32(a2 / a0)}
}

// bandpass passes a band of bw Hz around hz at unity gain.
func bandpass(hz, bw, sr float64) biquad {
	hz = math.Max(20, math.Min(hz, sr*0.45))
	q := math.Max(0.5, hz/math.Max(bw, 1))
	w := 2 * math.Pi * hz / sr
	al := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	return norm(al, 0, -al, 1+al, -2*c, 1-al)
}
