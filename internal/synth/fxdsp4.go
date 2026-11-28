package synth

import "math"

// Octaver, flanger, whammy, ixi_techno.

// delayC is a delay line read with cubic interpolation (SuperCollider's
// DelayC and AllpassC).
type delayC struct {
	buf []float64
	w   int
}

func newDelayC(maxSec, sr float64) *delayC {
	n := 1
	for float64(n) < maxSec*sr+4 {
		n <<= 1
	}
	return &delayC{buf: make([]float64, n)}
}

func (d *delayC) at(k int) float64 { return d.buf[(k+len(d.buf)*4)&(len(d.buf)-1)] }

// read gives the sound dsamp samples back (before this sample is written).
func (d *delayC) read(dsamp float64) float64 {
	dsamp = clamp(dsamp, 1, float64(len(d.buf)-3))
	p := float64(d.w) - dsamp
	i := int(math.Floor(p))
	f := p - float64(i)
	x0, x1, x2, x3 := d.at(i-1), d.at(i), d.at(i+1), d.at(i+2)
	c1 := 0.5 * (x2 - x0)
	c2 := x0 - 2.5*x1 + 2*x2 - 0.5*x3
	c3 := 0.5*(x3-x0) + 1.5*(x1-x2)
	return ((c3*f+c2)*f+c1)*f + x1
}

func (d *delayC) write(x float64) {
	d.buf[d.w&(len(d.buf)-1)] = x
	d.w = (d.w + 1) & (len(d.buf) - 1)
}

func init() {
	type octaver struct {
		lp          [2]butter
		dc          [2]leakDC
		sub, subsb  [2]float64
		prev, prev2 [2]float64
	}
	defFX("octaver", 1, map[string]float64{"super_amp": 1, "sub_amp": 1, "subsub_amp": 1},
		func(float64) any {
			return &octaver{dc: [2]leakDC{{coef: 0.995}, {coef: 0.995}}}
		},
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*octaver)
			stereo(out, in, func(ch int, x float64) float64 {
				lp := s.lp[ch].low(x, 440, f.sr)
				super := 2 * s.dc[ch].run(math.Abs(lp))
				if lp > 0 && s.prev[ch] <= 0 { // ToggleFF flips on a rising trigger
					s.sub[ch] = 1 - s.sub[ch]
				}
				s.prev[ch] = lp
				if s.sub[ch] > 0 && s.prev2[ch] <= 0 {
					s.subsb[ch] = 1 - s.subsb[ch]
				}
				s.prev2[ch] = s.sub[ch]
				return super*f.g("super_amp") + lp*s.sub[ch]*f.g("sub_amp") + lp*s.subsb[ch]*f.g("subsub_amp")
			})
		})

	type ixi struct {
		phase, hz float64
		n         int
		rl        [2]scRLPF
	}
	defFX("ixi_techno", 1, map[string]float64{"phase": 4, "phase_offset": 0, "cutoff_min": 60, "cutoff_max": 120, "res": 0.8},
		func(float64) any { return &ixi{} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*ixi)
			rq := linlin(f.g("res"), 1, 0, 0, 1)
			if s.n == 0 {
				s.phase = (f.g("phase_offset") - 0.25) * 2 * math.Pi
			}
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					w := math.Sin(s.phase)
					lo, hi := midicps(f.g("cutoff_min")), midicps(f.g("cutoff_max"))
					s.hz = lo * math.Pow(hi/lo, (w+1)/2)
					s.phase += 2 * math.Pi * krBlock / f.sr / math.Max(f.g("phase"), 1e-4)
				}
				s.n++
				for ch := 0; ch < 2; ch++ {
					out[i+ch] = float32(s.rl[ch].run(float64(in[i+ch]), s.hz, rq, f.sr))
				}
			}
		})

	type flanger struct {
		w         *ctlWave
		d         [2]*delayC
		lim       [2]*normalizer
		local     [2][krBlock]float64 // LocalIn: what the effect made a control block ago
		cur, from [2]float64          // delay in samples, now and at the start of the block
		to        [2]float64
		n         int
	}
	defFX("flanger", 1, map[string]float64{"phase": 4, "phase_offset": 0, "wave": 4, "invert_wave": 0, "stereo_invert_wave": 0,
		"pulse_width": 0.5, "delay": 5, "max_delay": 20, "depth": 5, "feedback": 0, "decay": 2, "invert_flange": 0},
		func(sr float64) any {
			s := &flanger{w: newCtlWave(sr)}
			for i := 0; i < 2; i++ {
				s.d[i] = newDelayC(0.2, sr) // room for max_delay
				s.lim[i] = newNormalizer(1, 0.01, sr)
				s.lim[i].limit = true
			}
			return s
		},
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*flanger)
			fb := clamp(f.g("feedback"), 0, 1)
			for i := 0; i+1 < len(in); i += 2 {
				if s.n%krBlock == 0 {
					wo := waveOpts{phase: f.g("phase"), offset: f.g("phase_offset"), width: f.g("pulse_width"), wave: int(f.g("wave"))}
					w := s.w.step(wo, krBlock)
					c := (w + 1) / 2
					if f.g("invert_wave") > 0 {
						c = 1 - c
					}
					cl := c
					if f.g("stereo_invert_wave") > 0 {
						cl = 1 - c
					}
					for ch, cw := range [2]float64{cl, c} {
						t := math.Max(cw*f.g("depth")/1000+f.g("delay")/1000, 0)
						s.from[ch] = s.cur[ch]
						if s.n == 0 {
							s.from[ch] = t * f.sr
						}
						s.to[ch] = t * f.sr
					}
				}
				frac := float64(s.n%krBlock+1) / krBlock
				s.n++
				mul := 1.0
				if f.g("invert_flange") > 0 {
					mul = -1
				}
				for ch := 0; ch < 2; ch++ {
					s.cur[ch] = s.from[ch] + (s.to[ch]-s.from[ch])*frac
					x := float64(in[i+ch])
					li := (s.n - 1) % krBlock
					fbk := s.lim[ch].run(s.local[ch][li]*fb) + (1-fb)*x
					dl := math.Max(s.cur[ch], 1)
					// AllpassC: the feedback gain makes the tail last decay seconds
					dec := f.g("decay") / 1000
					g := 0.0
					if dec != 0 {
						g = math.Copysign(math.Pow(0.001, dl/f.sr/math.Abs(dec)), dec)
					}
					v := s.d[ch].read(dl)
					wr := fbk + g*v
					y := v - g*wr
					s.d[ch].write(wr)
					wet := x + mul*y
					s.local[ch][li] = wet
					out[i+ch] = float32(wet)
				}
			}
		})

	type whammy struct {
		d      [2]*delayC
		dc     [2]leakDC
		p1, p2 float64
	}
	defFX("whammy", 1, map[string]float64{"transpose": 12, "deltime": 0.05, "max_delay_time": 1, "grainsize": 0.075, "pre_amp": 1},
		func(sr float64) any {
			s := &whammy{dc: [2]leakDC{{coef: 0.995}, {coef: 0.995}}}
			for i := range s.d {
				s.d[i] = newDelayC(1.5, sr)
			}
			return s
		},
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*whammy)
			gs := math.Max(f.g("grainsize"), 0.001)
			gf := f.g("transpose") / gs
			for i := 0; i+1 < len(in); i += 2 {
				// LFSaw(gf, 1.0) for the left, LFSaw(gf, 0.0) for the right; SinOsc windows at -pi/2 and +pi/2
				for ch := 0; ch < 2; ch++ {
					var saw, win float64
					if ch == 0 {
						saw = s.p1 + 1
						if saw >= 1 {
							saw -= 2
						}
						win = math.Sin(s.p2 - math.Pi/2)
					} else {
						saw = s.p1
						win = math.Sin(s.p2 + math.Pi/2)
					}
					dt := saw*(-gs/2) + f.g("deltime")
					v := s.d[ch].read(dt * f.sr)
					s.d[ch].write(float64(in[i+ch]) * f.g("pre_amp"))
					out[i+ch] = float32(s.dc[ch].run(v * ((win + 1) / 2)))
				}
				s.p1 += 2 * gf / f.sr
				for s.p1 >= 1 {
					s.p1 -= 2
				}
				for s.p1 < -1 {
					s.p1 += 2
				}
				s.p2 += 2 * math.Pi * gf / f.sr
			}
		})
}
