package synth

import "math"

// More of Sonic Pi's effects: distortions, filters, levels, modulators.

// simple makes an effect from its options (with defaults), a per-channel
// state and the sound it makes.
type simpleFX struct {
	sr   float64
	o    map[string]float64
	kill float64
	run  func(f *simpleFX, out, in []float32)
	st   any
}

func (f *simpleFX) Set(o map[string]float64) {
	for k, v := range o {
		f.o[k] = v
	}
}
func (f *simpleFX) KillDelay() float64        { return f.kill }
func (f *simpleFX) Process(out, in []float32) { f.run(f, out, in) }
func (f *simpleFX) g(k string) float64        { return f.o[k] }

func defFX(name string, kill float64, defaults map[string]float64, mk func(sr float64) any, run func(f *simpleFX, out, in []float32)) {
	fxMakers[name] = func(sr float64) FX {
		f := &simpleFX{sr: sr, o: map[string]float64{}, kill: kill, run: run}
		for k, v := range defaults {
			f.o[k] = v
		}
		if mk != nil {
			f.st = mk(sr)
		}
		return f
	}
}

func init() {
	defFX("level", 1, nil, nil, func(f *simpleFX, out, in []float32) { copy(out, in) })

	defFX("pan", 1, map[string]float64{"pan": 0, "amp": 1}, nil, func(f *simpleFX, out, in []float32) {
		for i := 0; i+1 < len(in); i += 2 {
			l, r := balance2(float64(in[i]), float64(in[i+1]), f.g("pan"))
			out[i], out[i+1] = float32(l), float32(r)
		}
	})

	defFX("mono", 1, map[string]float64{"pan": 0}, nil, func(f *simpleFX, out, in []float32) {
		for i := 0; i+1 < len(in); i += 2 {
			m := (float64(in[i]) + float64(in[i+1])) / 2
			l, r := balance2(m, m, f.g("pan"))
			out[i], out[i+1] = float32(l), float32(r)
		}
	})

	defFX("distortion", 1, map[string]float64{"distort": 0.5}, nil, func(f *simpleFX, out, in []float32) {
		d := clamp(f.g("distort"), -1, 0.99999)
		k := 2 * d / (1 - d)
		for i, v := range in {
			x := float64(v)
			out[i] = float32(x * (1 + k) / (1 + k*math.Abs(x)))
		}
	})

	defFX("krush", 1, map[string]float64{"gain": 5, "cutoff": 100, "res": 0},
		func(float64) any { return &krushSt{dc: [2]leakDC{{coef: 0.995}, {coef: 0.995}}} },
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*krushSt)
			g := f.g("gain")
			hz := midicps(f.g("cutoff"))
			rq := linlin(f.g("res"), 1, 0, 0, 1)
			stereo(out, in, func(ch int, x float64) float64 {
				a := math.Abs(x)
				s := a * a
				n := (s + g*a) / (s + a*(g-1) + 1)
				return st.dc[ch].run(st.f[ch].run(n, hz, rq, f.sr))
			})
		})

	defFX("tanh", 1, map[string]float64{"krunch": 5}, nil, func(f *simpleFX, out, in []float32) {
		k := f.g("krunch")
		if k == 0 {
			k = 0.0001
		}
		k *= 5
		for i, v := range in {
			out[i] = float32(math.Tanh(k*float64(v)) / k * (1 + k/8))
		}
	})

	type crush struct {
		count [2]float64
		held  [2]float64
		lp    [2]butter
	}
	defFX("bitcrusher", 1, map[string]float64{"sample_rate": 10000, "bits": 8, "cutoff": 0},
		func(sr float64) any { return &crush{} },
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*crush)
			rate, bits := f.g("sample_rate"), f.g("bits")
			m := math.Pow(2, math.Max(1, math.Min(bits, 31))-1)
			hz := midicps(f.g("cutoff"))
			stereo(out, in, func(ch int, x float64) float64 {
				st.count[ch] += rate
				if st.count[ch] >= f.sr {
					st.count[ch] -= f.sr
					st.held[ch] = (math.Floor(x*m) + 0.5) / m
				}
				y := st.held[ch]
				if f.g("cutoff") > 0 {
					y = st.lp[ch].low(y, hz, f.sr)
				}
				return y
			})
		})

	filt := func(name, freqKey string, def, defRes float64, hasRes, resConv, norm bool, run func(f *simpleFX, st *filtSt, ch int, x, hz, rq float64) float64) {
		d := map[string]float64{freqKey: def}
		if hasRes {
			d["res"] = defRes
		}
		defFX(name, 1, d, func(sr float64) any {
			s := &filtSt{}
			for i := range s.nz {
				s.nz[i] = newNormalizer(1, 0.01, sr)
			}
			return s
		}, func(f *simpleFX, out, in []float32) {
			st := f.st.(*filtSt)
			hz := midicps(f.g(freqKey))
			rq := 0.0
			if hasRes {
				rq = f.g("res")
				if resConv {
					rq = linlin(rq, 1, 0, 0, 1)
				}
			}
			stereo(out, in, func(ch int, x float64) float64 {
				y := run(f, st, ch, x, hz, rq)
				if norm {
					y = st.nz[ch].run(y)
				}
				return y
			})
		})
	}
	rlpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 {
		return s.rlpf[ch].run(x, hz, rq, f.sr)
	}
	rhpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 {
		return s.rhpf[ch].run(x, hz, rq, f.sr)
	}
	lpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 { return s.b[ch].low(x, hz, f.sr) }
	hpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 { return s.b[ch].high(x, hz, f.sr) }
	bpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 { return s.bp[ch].run(x, hz, rq, f.sr) }
	rbpf := func(f *simpleFX, s *filtSt, ch int, x, hz, rq float64) float64 { return s.rz[ch].run(x, hz, rq, f.sr) }
	filt("lpf", "cutoff", 100, 0, false, false, false, lpf)
	filt("nlpf", "cutoff", 100, 0, false, false, true, lpf)
	filt("hpf", "cutoff", 100, 0, false, false, false, hpf)
	filt("nhpf", "cutoff", 100, 0, false, false, true, hpf)
	filt("rlpf", "cutoff", 100, 0.5, true, true, false, rlpf)
	filt("nrlpf", "cutoff", 100, 0.5, true, true, true, rlpf)
	filt("rhpf", "cutoff", 100, 0.5, true, true, false, rhpf)
	filt("nrhpf", "cutoff", 100, 0.5, true, false, true, rhpf) // Sonic Pi leaves res as it is here
	filt("bpf", "centre", 100, 0.6, true, true, false, bpf)
	filt("nbpf", "centre", 100, 0.6, true, true, true, bpf)
	filt("rbpf", "centre", 100, 0.6, true, true, false, rbpf)
	filt("nrbpf", "centre", 100, 0.6, true, true, true, rbpf)

	defFX("normaliser", 1, map[string]float64{"level": 1},
		func(sr float64) any { return &[2]*normalizer{newNormalizer(1, 0.01, sr), newNormalizer(1, 0.01, sr)} },
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*[2]*normalizer)
			st[0].level, st[1].level = f.g("level"), f.g("level")
			stereo(out, in, func(ch int, x float64) float64 { return st[ch].run(x) })
		})

	type ring struct {
		p  float64
		lm [2]*normalizer
	}
	defFX("ring_mod", 1, map[string]float64{"freq": 30, "mod_amp": 1},
		func(sr float64) any {
			r := &ring{}
			for i := range r.lm {
				r.lm[i] = newNormalizer(1, 0.01, sr)
				r.lm[i].limit = true
			}
			return r
		},
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*ring)
			hz := midicps(f.g("freq"))
			for i := 0; i+1 < len(in); i += 2 {
				m := f.g("mod_amp") * math.Sin(2*math.Pi*st.p)
				st.p += hz / f.sr
				st.p -= math.Floor(st.p)
				for ch := 0; ch < 2; ch++ {
					x := float64(in[i+ch])
					out[i+ch] = float32(st.lm[ch].run(x*m + x))
				}
			}
		})
}

type krushSt struct {
	f  [2]scRLPF
	dc [2]leakDC
}

type filtSt struct {
	rlpf [2]scRLPF
	rhpf [2]scRHPF
	b    [2]butter
	bp   [2]scBPF
	rz   [2]scResonz
	nz   [2]*normalizer
}
