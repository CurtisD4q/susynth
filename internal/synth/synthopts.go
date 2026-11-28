package synth

import "math"

// Synth-specific options (Sonic Pi's detune:, divisor:, depth:, vel:,
// vibrato_*: and the mod_ family), carried in Params.Opt by key.

type synthOpts struct {
	detune, divisor, depth                float64
	vibRate, vibDepth, vibDelay, vibOnset float64
	vel                                   float64 // 0: not given
	mod                                   *modOsc
}

// modOsc is the oscillator the mod_ synths sweep their note with: it moves
// the note between itself and rng semitones above it.
type modOsc struct {
	rng, period, off, pw float64
	wave                 int
	invert               bool
}

// semis is how far above the note the sweep is at time t (seconds).
func (m *modOsc) semis(t float64) float64 {
	x := t/m.period + m.off
	x -= math.Floor(x)
	var c float64 // -1..1
	switch m.wave {
	case 0: // saw, falling
		c = 1 - 2*x
	case 2: // triangle, starting high
		c = 4*math.Abs(x-0.5) - 1
		c = -c
	case 3: // sine, starting high
		c = math.Cos(2 * math.Pi * x)
	default: // pulse, starting high
		if x < m.pw {
			c = 1
		} else {
			c = -1
		}
	}
	if m.invert {
		c = -c
	}
	return (c + 1) / 2 * m.rng
}

func (v *Voice) initOpts(o map[string]float64) {
	get := func(k string, d float64) float64 {
		if x, ok := o[k]; ok {
			return x
		}
		return d
	}
	so := &v.so
	so.detune, so.divisor, so.depth = get("det", 0.1), math.Max(get("div", 2), 1e-3), get("dep", 1)
	so.vibRate, so.vibDepth, so.vibDelay, so.vibOnset = get("vr", 6), get("vd", 0.15), get("vdl", 0.5), get("von", 0.1)
	so.vel = get("vel", 0)
	if r := get("mr", 0); r != 0 && get("mp", 0) > 0 {
		so.mod = &modOsc{rng: r, period: o["mp"], off: get("moff", 0), pw: get("mpw", 0.5),
			wave: int(get("mw", 1)), invert: get("minv", 0) > 0}
	}
}

// velGain scales the piano and rhodey with vel: (Sonic Pi's default is 0.2).
func (s *synthOpts) velGain(wave string) float64 {
	if s.vel <= 0 || (wave != "piano" && wave != "rhodey") {
		return 1
	}
	return math.Min(2, math.Max(0.2, (0.5+s.vel)/0.7))
}
