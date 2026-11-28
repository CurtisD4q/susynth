package synth

import "math"

// More Sonic Pi synths ported from their compiled SynthDefs: prophet,
// hollow, blade, the bells and dsaw.

func init() {
	natives["prophet"] = newProphet
	natives["hollow"] = newHollow
	natives["blade"] = newBlade
	natives["dsaw"] = newDsaw
	natives["bell"] = newBellSynth([]bellPart{{1, 0.5, 1}, {4, 0.25, 0.5}, {6.8, 0.125, 0.25}})
	natives["dullbell"] = newBellSynth([]bellPart{{0.56, 0.5, 1}, {1.19, 0.25, 0.5}, {3, 0.125, 0.25}, {3.76, 0.0625, 0.125}})
}

// lfTriKr is SuperCollider's LFTri at phase p in cycles starting at the
// iphase (0..4) given.
func lfTriKr(p float64) float64 { return lfTriAt(p) }

func newProphet(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 110)}
	rq := 1 - o("res", 0.7)
	var p1, p5 osc
	nz := newNormalizer(1, 0.01, v.sr)
	lfn := newLFNoise1(0.2, v)
	var rl scRLPF
	var dc leakDC
	dc.coef = 0.995
	ph5 := 0.0
	n := 0
	return &native{
		own: true,
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			env := v.envNow
			dt := v.freq / v.sr
			c := p1.step(dt)
			w1 := 0.1 / (1.2 + math.Sin(2*math.Pi*t))
			w2 := 0.8 * (1.2 + math.Sin(2*math.Pi*0.3*t) + 0.7) / 2
			w3 := 0.8 * (1.2 + lfTriKr(0.4*t)) / 2
			w4 := 0.8 * (1.2 + lfTriKr(0.4*t+0.19/4)) / 2
			ph5 += (2 + lfn.next()) / v.sr
			w5 := 0.8 * (1.2 + lfTriKr(ph5)) / 2
			cl := func(w float64) float64 { return w }
			s := pulseBL(c, dt, cl(w1)) + pulseBL(c, dt, cl(w2)) + pulseBL(c, dt, cl(w3)) + pulseBL(c, dt, cl(w4))
			s += 0.5 * pulseBL(p5.step(dt/2), dt/2, cl(w5))
			// scsynth's Pulse drifts in phase as its width moves, which lowers the
			// summed peaks the Normalizer sees; this trim matches the level measured
			// from the compiled synthdef.
			s = nz.run(s*0.2) * 0.87
			y := rl.run(env*s*s, cut.hz(), rq, v.sr)
			return dc.run(1.5 * env * y)
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newHollow(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 90)}
	rq := 1 - o("res", 0.99)
	norm := o("norm", 0) != 0
	var src func() float64
	switch int(o("noise", 1)) {
	case 0:
		src = pinkSrc(v)
	case 2:
		w := whiteSrc(v)
		src = func() float64 { return 0.5 * w() }
	case 3:
		c := clipSrc(v)
		src = func() float64 { return 0.2 * c() }
	case 4:
		g := graySrc(v)
		src = func() float64 { return 0.2 * g() }
	default:
		b := brownSrc(v)
		src = func() float64 { return 0.4 * b() }
	}
	var bp [3]scBPF
	var lp butter
	nz := newNormalizer(1, 0.01, v.sr)
	mul := [3]float64{1, 2, 4}
	gain := [3]float64{1, 0.5, 0.125}
	return &native{
		own: true,
		next: func() float64 {
			env := v.envNow
			x := src() * env
			var s float64
			for k := range bp {
				s += gain[k] * bp[k].run(x, math.Min(v.freq*mul[k], v.sr*0.49), rq, v.sr)
			}
			y := lp.low(s, cut.hz(), v.sr)
			if norm {
				y = nz.run(y)
			}
			return 15 * env * y
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newBlade(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 100)}
	rate, depth := v.so.vibRate, v.so.vibDepth
	delay, onset := v.so.vibDelay, v.so.vibOnset
	var lp, hp butter
	var b1, b2, b3 scBPF
	var ph float64
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			// the vibrato fades in after its delay
			var ve float64
			switch {
			case t < delay:
			case t < delay+onset && onset > 0:
				ve = depth * (t - delay) / onset
			default:
				ve = depth
			}
			f := v.freq * math.Pow(2, ve*math.Sin(2*math.Pi*rate*t)/12)
			dt := f / v.sr
			ph += dt
			ph -= math.Floor(ph)
			x := lp.low(sawBL(ph, dt), cut.hz(), v.sr)
			s := b1.run(x, 300, 0.285714, v.sr) + b2.run(x, 700, 0.285714, v.sr) + b3.run(x, 3000, 0.5, v.sr)
			return 1.1 * 1.1 * hp.high(s, 30, v.sr)
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newDsaw(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 100)}
	det := v.so.detune
	var a, b osc
	var lp butter
	nz := newNormalizer(1, 0.01, v.sr)
	return &native{
		next: func() float64 {
			d1 := v.freq / v.sr
			d2 := d1 * math.Pow(2, det/12)
			s := sawBL(a.step(d1), d1) + sawBL(b.step(d2), d2)
			return 1.1 * nz.run(lp.low(0.5*s, cut.hz(), v.sr))
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

// bellPart is one sine of a bell: its frequency ratio, level and the share
// of the decay, sustain and release times it lasts for.
type bellPart struct{ ratio, level, mult float64 }

func newBellSynth(parts []bellPart) nativeMaker {
	return func(v *Voice, o func(string, float64) float64) *native {
		att, dec, sus, rel := o("attack", 0), o("decay", 0), o("sustain", 0), o("release", 1)
		al, sl := o("attack_level", 1), o("sustain_level", 1)
		ph := make([]osc, len(parts))
		n := 0
		return &native{
			own: true,
			next: func() float64 {
				t := float64(n) / v.sr
				n++
				var s float64
				for i, p := range parts {
					e := bellEnv(t, att, dec*p.mult, sus*p.mult, rel*p.mult, al, sl)
					if e == 0 {
						continue
					}
					s += p.level * e * math.Sin(2*math.Pi*ph[i].step(v.freq*p.ratio/v.sr))
				}
				return s
			},
		}
	}
}

// bellEnv is the linear envelope of one bell partial: 0 up to the attack
// level, down to the sustain level over the decay, held, then to 0.
func bellEnv(t, a, d, s, r, al, sl float64) float64 {
	if t < a {
		return al * t / a
	}
	t -= a
	if t < d {
		return al + (sl-al)*t/d
	}
	t -= d
	if t < s {
		return sl
	}
	t -= s
	if t < r {
		return sl * (1 - t/r)
	}
	return 0
}

// newAmbience is dark_ambience: quiet noise ringing three resonators (the
// note, +detune1 and +detune2 semitones), run through GVerb (room 70,
// reverb_time 100 by default, spread 15), soft-clipped, band-limited by an
// RLPF and balanced.
func newAmbience(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 110)}
	rq := 1 - o("res", 0.7)
	ring := o("ring", 0.2)
	var src func() float64
	switch int(o("noise", 0)) {
	case 1:
		b := brownSrc(v)
		src = func() float64 { return 0.002 * b() }
	case 2:
		w := whiteSrc(v)
		src = func() float64 { return 0.002 * w() }
	case 3:
		c := clipSrc(v)
		src = func() float64 { return 0.001 * c() }
	case 4:
		g := graySrc(v)
		src = func() float64 { return 0.001 * g() }
	default:
		p := pinkSrc(v)
		src = func() float64 { return 0.005 * p() }
	}
	d1, d2 := o("detune1", 12), o("detune2", 24)
	f := v.freq
	zs := [3]*ringz{
		newRingz(f, ring, v.sr),
		newRingz(f*math.Pow(2, d1/12), ring, v.sr),
		newRingz(f*math.Pow(2, d2/12), ring, v.sr),
	}
	room := clamp(o("room", 70), 0.1, 300)
	gv := newGverbCore(v.sr, room, o("reverb_time", 100), 0.5)
	gv.bw = 0.5
	var lpl, lpr scRLPF
	return &native{
		own: true,
		lr: func() (float64, float64) {
			env := v.envNow
			n := src()
			x := (2*zs[0].run(n) + zs[1].run(n) + zs[2].run(n)) * 0.25
			// GVerb takes the enveloped sound; its dry level of 1 adds it back
			in := env * x
			l, r := gv.step(in, 0.7, 0.5, true)
			l, r = l+in, r+in
			c := cut.hz()
			l = lpl.run(env*math.Tanh(l), c, rq, v.sr)
			r = lpr.run(env*math.Tanh(r), c, rq, v.sr)
			return l * 0.8, r * 0.8 // Balance2 at the centre (0.707), trimmed to the level measured against scsynth
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func init() { natives["ambience"] = newAmbience }
