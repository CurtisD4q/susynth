package synth

import "math"

// Effects driven by control-rate waves: slicer, wobble, panslicer, tremolo,
// and the compressor.

const krBlock = 64 // SuperCollider's control block, in samples

// ctlWave is the wave of Sonic Pi's slicer family: -1..1, held for each
// control block, starting where the options say.
type ctlWave struct {
	sr        float64
	phase     float64 // cycles, 0..1 for pulse/sine; the saw and triangle use their own scale
	tick      int
	val       float64
	started   bool
	rng       uint64
	coin      bool
	coinSet   bool
	lastCycle int
}

func newCtlWave(sr float64) *ctlWave { return &ctlWave{sr: sr, rng: 1} }

type waveOpts struct {
	phase, offset, width, probability, probPos float64
	wave, invert                               int
	seed                                       float64
}

func (w *ctlWave) rand01() float64 {
	w.rng ^= w.rng << 13
	w.rng ^= w.rng >> 7
	w.rng ^= w.rng << 17
	return float64(w.rng>>11) / (1 << 53)
}

// step advances n samples and returns the wave (-1..1) for them.
func (w *ctlWave) step(o waveOpts, n int) float64 {
	rate := 1 / math.Max(o.phase, 1e-4)
	if !w.started {
		w.started = true
		w.rng = uint64(o.seed)*2654435761 + 88172645463325252
		// the oscillators' own starting phases, in their own units
		switch o.wave {
		case 0, 2:
			w.phase = 2*o.offset + 1
		case 1:
			w.phase = o.offset
		case 4:
			w.phase = o.offset + 0.5
		default:
			w.phase = (o.offset + 0.25) * 2 * math.Pi
		}
		w.lastCycle = -1
	}
	var v float64
	dt := float64(n) / w.sr
	switch o.wave {
	case 0: // -1 * LFSaw
		v = -w.phase
		w.phase += 2 * rate * dt
		for w.phase >= 1 {
			w.phase -= 2
		}
	case 1: // LFPulse
		if w.phase < o.width {
			v = 1
		} else {
			v = -1
		}
		w.phase += rate * dt
		w.phase -= math.Floor(w.phase)
	case 2: // LFTri
		z := w.phase
		if z > 1 {
			z = 2 - z
		}
		v = z
		w.phase += 4 * rate * dt
		if w.phase >= 3 {
			w.phase -= 4
		}
	case 4: // LFCub: a sine made of cubics, period 2
		q := w.phase
		if q > 1.5 {
			q -= 2
		} else if q > 0.5 {
			q = 1 - q
		}
		v = 3*q - 4*q*q*q
		w.phase += 2 * rate * dt
		for w.phase >= 2 {
			w.phase -= 2
		}
	default: // SinOsc
		v = math.Sin(w.phase)
		w.phase += 2 * math.Pi * rate * dt
	}
	if o.invert > 0 {
		v = -v
	}
	if o.probability > 0 {
		// Impulse.kr(rate) fires at the start of each cycle: a new coin
		c := int(w.tickTime(n) * rate)
		if c != w.lastCycle {
			w.lastCycle = c
			w.coin = w.rand01() < clamp(o.probability, 0, 1)
		}
		if !w.coin {
			v = linlin(clamp(o.probPos, 0, 1), 0, 1, -1, 1)
		}
	}
	return v
}

func (w *ctlWave) tickTime(n int) float64 {
	t := float64(w.tick) / w.sr
	w.tick += n
	return t
}

// lagUD is Lag/LagUD at control rate.
type lagUD struct {
	y    float64
	init bool
}

func (l *lagUD) run(x, up, down, sr float64) float64 {
	if !l.init {
		l.init, l.y = true, x
		return x
	}
	t := down
	if x > l.y {
		t = up
	}
	if t <= 0 {
		l.y = x
		return x
	}
	b := math.Exp(math.Log(0.001) / (t * sr / krBlock))
	l.y = x + b*(l.y-x)
	return l.y
}

func lagOf(smooth float64) (float64, float64) { return smooth, smooth }

func init() {
	type slicer struct {
		w        *ctlWave
		up, down lagUD
		sm       lagUD
		amp      float64
		n        int
	}
	slicerDefaults := map[string]float64{"phase": 0.25, "amp_min": 0, "amp_max": 1, "pulse_width": 0.5,
		"smooth": 0, "smooth_up": 0, "smooth_down": 0, "probability": 0, "prob_pos": 0, "phase_offset": 0,
		"wave": 1, "invert_wave": 0, "seed": 0}
	opts := func(f *simpleFX) waveOpts {
		return waveOpts{phase: f.g("phase"), offset: f.g("phase_offset"), width: f.g("pulse_width"),
			probability: f.g("probability"), probPos: f.g("prob_pos"), wave: int(f.g("wave")), invert: int(f.g("invert_wave")), seed: f.g("seed")}
	}
	defFX("slicer", 1, slicerDefaults, func(sr float64) any { return &slicer{w: newCtlWave(sr)} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*slicer)
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					w := s.w.step(opts(f), krBlock)
					a := linlin(w, -1, 1, f.g("amp_min"), f.g("amp_max"))
					a = s.up.run(a, f.g("smooth_up"), f.g("smooth_down"), f.sr)
					s.amp = s.sm.run(a, f.g("smooth"), f.g("smooth"), f.sr)
				}
				s.n++
				out[i], out[i+1] = in[i]*float32(s.amp), in[i+1]*float32(s.amp)
			}
		})

	type wobble struct {
		w        *ctlWave
		up, down lagUD
		sm       lagUD
		hz       float64
		n        int
		lp       [2]scRLPF
		hp       [2]scRHPF
	}
	wobbleDefaults := map[string]float64{"phase": 0.5, "cutoff_min": 60, "cutoff_max": 120, "res": 0.8,
		"pulse_width": 0.5, "filter": 0, "smooth": 0, "smooth_up": 0, "smooth_down": 0, "phase_offset": 0,
		"wave": 0, "invert_wave": 0, "probability": 0, "prob_pos": 0, "seed": 0}
	defFX("wobble", 1, wobbleDefaults, func(sr float64) any { return &wobble{w: newCtlWave(sr), hz: 1000} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*wobble)
			rq := linlin(f.g("res"), 1, 0, 0, 1)
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					w := s.w.step(opts(f), krBlock)
					lo, hi := midicps(f.g("cutoff_min")), midicps(f.g("cutoff_max"))
					hz := lo * math.Pow(hi/lo, (w+1)/2) // lin-exp
					hz = s.up.run(hz, f.g("smooth_up"), f.g("smooth_down"), f.sr)
					s.hz = s.sm.run(hz, f.g("smooth"), f.g("smooth"), f.sr)
				}
				s.n++
				for ch := 0; ch < 2; ch++ {
					x := float64(in[i+ch])
					if f.g("filter") > 0 {
						out[i+ch] = float32(s.hp[ch].run(x, s.hz, rq, f.sr))
					} else {
						out[i+ch] = float32(s.lp[ch].run(x, s.hz, rq, f.sr))
					}
				}
			}
		})

	type panslicer struct {
		w        *ctlWave
		up, down lagUD
		sm       lagUD
		pan      float64
		n        int
	}
	panDefaults := map[string]float64{"phase": 0.25, "pan_min": -1, "pan_max": 1, "pulse_width": 0.5,
		"smooth": 0, "smooth_up": 0, "smooth_down": 0, "probability": 0, "prob_pos": 0, "phase_offset": 0,
		"wave": 1, "invert_wave": 0, "seed": 0, "amp": 1}
	defFX("panslicer", 1, panDefaults, func(sr float64) any { return &panslicer{w: newCtlWave(sr)} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*panslicer)
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					w := s.w.step(opts(f), krBlock)
					p := linlin(w, -1, 1, clamp(f.g("pan_min"), -1, 1), clamp(f.g("pan_max"), -1, 1))
					p = s.up.run(p, f.g("smooth_up"), f.g("smooth_down"), f.sr)
					s.pan = s.sm.run(p, f.g("smooth"), f.g("smooth"), f.sr)
				}
				s.n++
				l, r := balance2(float64(in[i]), float64(in[i+1]), s.pan)
				out[i], out[i+1] = float32(l), float32(r)
			}
		})

	type trem struct {
		w    *ctlWave
		n    int
		gain float64
	}
	defFX("tremolo", 1, map[string]float64{"phase": 4, "phase_offset": 0, "wave": 2, "invert_wave": 0, "pulse_width": 0.5, "depth": 0.5},
		func(sr float64) any { return &trem{w: newCtlWave(sr)} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*trem)
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					wo := opts(f)
					wo.invert = 0
					w := s.w.step(wo, krBlock) // -1..1 as the other slicers
					// Sonic Pi's tremolo maps each wave to 0..1, then (a quirk of its
					// definition) once more through (x+1)/2
					c0 := (w + 1) / 2
					if wo.wave == 0 {
						c0 = (1 - w) / 2
					}
					c := (c0 + 1) / 2
					if f.g("invert_wave") > 0 {
						c = 1 - c
					}
					s.gain = 1 - c*f.g("depth")
				}
				s.n++
				out[i], out[i+1] = in[i]*float32(s.gain), in[i+1]*float32(s.gain)
			}
		})

	type comp struct {
		prevMax, gain, nextGain float64
		n                       int
	}
	defFX("compressor", 1, map[string]float64{"threshold": 0.2, "clamp_time": 0.01, "slope_above": 0.5, "slope_below": 1, "relax_time": 0.01},
		func(float64) any { return &comp{gain: 1, nextGain: 1} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*comp)
			coef := func(t float64) float64 {
				if t <= 0 {
					return 0
				}
				return math.Exp(compLog1 / (t * f.sr))
			}
			cc, rc := coef(f.g("clamp_time")), coef(f.g("relax_time"))
			thr := f.g("threshold")
			for i := 0; i+1 < len(in); i += 2 {
				ctl := math.Abs((float64(in[i]) + float64(in[i+1])) / 2)
				if ctl < s.prevMax {
					ctl += (s.prevMax - ctl) * rc
				} else {
					ctl += (s.prevMax - ctl) * cc
				}
				s.prevMax = ctl
				if s.n%krBlock == 0 {
					s.gain = s.nextGain
					below, above := f.g("slope_below"), f.g("slope_above")
					switch {
					case ctl < thr && below != 1:
						s.nextGain = math.Min(math.Pow(ctl/thr, below-1), 100)
					case ctl >= thr && above != 1:
						s.nextGain = math.Pow(ctl/thr, above-1)
					default:
						s.nextGain = 1
					}
				}
				g := s.gain + (s.nextGain-s.gain)*float64(s.n%krBlock)/krBlock
				s.n++
				out[i], out[i+1] = float32(float64(in[i])*g), float32(float64(in[i+1])*g)
			}
		})
}

// compLog1 is the Compander's time constant (fitted against scsynth): its envelope follower reaches
// -60 dB of a step in the time asked.
var compLog1 = math.Log(0.001)
