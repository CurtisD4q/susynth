package synth

import "math"

// Native voices are Sonic Pi's own synth designs ported unit generator by
// unit generator from the compiled SynthDefs (etc/synthdefs in Sonic Pi),
// down to the filter formulas SuperCollider uses, so a note is built the
// way Sonic Pi builds it. The amplitude envelope, pan and amp are the
// voice's own (voices.go); a native voice makes the signal before them and
// reads its options (cutoff:, res:, detune:...) from Params.Opt by their
// Sonic Pi names.

// native is one running native voice.
type native struct {
	next func() float64        // the next sample, before the amplitude envelope
	lr   func() (l, r float64) // for a stereo synth, in place of next
	cut  func(midi float64)    // control cutoff: (a MIDI note); nil if the synth has none
	cur  func() float64        // the cutoff now, as a MIDI note
	own  bool                  // the synth applies its amplitude envelope itself
}

// nativeMaker builds a native voice. o reads an option by Sonic Pi's name,
// giving d when the note did not set it.
type nativeMaker func(v *Voice, o func(string, float64) float64) *native

var natives = map[string]nativeMaker{}

// IsNative reports whether a synth wave is a native voice, which has its own
// filter and reads Sonic Pi's option names.
func IsNative(wave string) bool { _, ok := natives[wave]; return ok }

func midicps(m float64) float64 { return 440 * math.Pow(2, (m-69)/12) }

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

// rqOf is Sonic Pi's res: (0..1, more is more resonant) as the filter's rq.
func rqOf(res float64) float64 { return 1 - clamp(res, 0, 1) }

// linlin maps x from a..b onto c..d.
func linlin(x, a, b, c, d float64) float64 { return c + (x-a)/(b-a)*(d-c) }

// scRLPF is SuperCollider's RLPF: a resonant two-pole low-pass.
type scRLPF struct {
	y1, y2, fr, rq, a0, b1, b2 float64
}

func (f *scRLPF) run(in, freq, rq, sr float64) float64 {
	if freq != f.fr || rq != f.rq {
		f.fr, f.rq = freq, rq
		pf := math.Min(freq, sr*0.49) * 2 * math.Pi / sr
		q := math.Max(0.001, rq)
		d := math.Tan(pf * q * 0.5)
		c := (1 - d) / (1 + d)
		f.b1 = (1 + c) * math.Cos(pf)
		f.b2 = -c
		f.a0 = (1 + c - f.b1) * 0.25
	}
	y0 := f.a0*in + f.b1*f.y1 + f.b2*f.y2
	out := y0 + 2*f.y1 + f.y2
	if math.IsNaN(out) || math.IsInf(out, 0) {
		f.y1, f.y2, out = 0, 0, 0
		return 0
	}
	f.y2, f.y1 = f.y1, y0
	return out
}

// biq is a biquad (transposed direct form II).
type biq struct{ b0, b1, b2, a1, a2, z1, z2 float64 }

func (q *biq) run(x float64) float64 {
	y := q.b0*x + q.z1
	q.z1 = q.b1*x - q.a1*y + q.z2
	q.z2 = q.b2*x - q.a2*y
	return y
}

func (q *biq) set(b0, b1, b2, a0, a1, a2 float64) {
	q.b0, q.b1, q.b2, q.a1, q.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

// lowShelf is SuperCollider's BLowShelf (rs is the shelf slope).
func lowShelf(freq, rs, db, sr float64) biq {
	a := math.Pow(10, db/40)
	w := 2 * math.Pi * freq / sr
	cs, sn := math.Cos(w), math.Sin(w)
	al := sn / 2 * math.Sqrt((a+1/a)*(1/rs-1)+2)
	sa := 2 * math.Sqrt(a) * al
	var q biq
	q.set(a*((a+1)-(a-1)*cs+sa), 2*a*((a-1)-(a+1)*cs), a*((a+1)-(a-1)*cs-sa),
		(a+1)+(a-1)*cs+sa, -2*((a-1)+(a+1)*cs), (a+1)+(a-1)*cs-sa)
	return q
}

// peakEQ is SuperCollider's BPeakEQ (rq is the reciprocal of Q).
func peakEQ(freq, rq, db, sr float64) biq {
	a := math.Pow(10, db/40)
	w := 2 * math.Pi * freq / sr
	cs, al := math.Cos(w), math.Sin(w)*rq/2
	var q biq
	q.set(1+al*a, -2*cs, 1-al*a, 1+al/a, -2*cs, 1-al/a)
	return q
}

// lowpass2 is SuperCollider's LPF: a two-pole Butterworth low-pass.
func lowpass2(freq, sr float64) biq {
	w := 2 * math.Pi * math.Min(freq, sr*0.49) / sr
	cs, al := math.Cos(w), math.Sin(w)/math.Sqrt2
	var q biq
	q.set((1-cs)/2, 1-cs, (1-cs)/2, 1+al, -2*cs, 1-al)
	return q
}

// leakDC is SuperCollider's LeakDC.
type leakDC struct{ x1, y1, coef float64 }

func (l *leakDC) run(x float64) float64 {
	y := x - l.x1 + l.coef*l.y1
	l.x1, l.y1 = x, y
	return y
}

// normalizer is SuperCollider's Normalizer: it delays the sound by dur and
// scales it so the loudest sample of the neighbouring blocks reaches level.
type normalizer struct {
	level     float64
	n, pos    int
	prev, cur []float64
	prevMax   float64
	curMax    float64
	g0, g1    float64
	primed    bool
}

func newNormalizer(level, dur, sr float64) *normalizer {
	n := int(math.Max(2, dur*sr))
	return &normalizer{level: level, n: n, prev: make([]float64, n), cur: make([]float64, n), g0: 1, g1: 1}
}

func (z *normalizer) run(x float64) float64 {
	out := z.prev[z.pos] * (z.g0 + (z.g1-z.g0)*float64(z.pos)/float64(z.n))
	z.cur[z.pos] = x
	z.curMax = math.Max(z.curMax, math.Abs(x))
	z.pos++
	if z.pos == z.n {
		z.pos = 0
		z.prev, z.cur = z.cur, z.prev
		z.prevMax = z.curMax
		z.curMax = 0
		z.g0 = z.g1
		z.g1 = z.level / math.Max(z.prevMax, 1e-5)
		if !z.primed {
			z.g0, z.primed = z.g1, true
		}
	}
	return out
}

// curveEnv is a SuperCollider envelope held at its last level: breakpoints
// of (level, time) moved with a shape and curve.
type curveEnv struct {
	lv, tm []float64
	shape  int
	curve  float64
}

func (e *curveEnv) at(t float64) float64 {
	cur := e.lv[0]
	prev := e.lv[0]
	acc := 0.0
	for i := 1; i < len(e.lv); i++ {
		d := e.tm[i-1]
		if t < acc+d {
			return shapeAt(e.shape, e.curve, prev, e.lv[i], (t-acc)/d)
		}
		acc += d
		prev = e.lv[i]
		cur = prev
	}
	return cur
}

// SuperCollider's low-frequency oscillators, from a phase in 0..1.
func lfTriAt(p float64) float64 { // starts at 0, rising
	p -= math.Floor(p)
	switch {
	case p < 0.25:
		return 4 * p
	case p < 0.75:
		return 2 - 4*p
	}
	return 4*p - 4
}

func lfSawAt(p float64) float64 { // starts at 0, rising to 1, then wraps to -1
	p += 0.5
	p -= math.Floor(p)
	return 2*p - 1
}

// sawBL is SuperCollider's Saw at phase p (0..1) and phase step dt:
// band-limited, and half the size of an LFSaw (measured against scsynth).
func sawBL(p, dt float64) float64 { return 0.5 * (2*p - 1 - polyBLEP(p, dt)) }

// pulseBL is SuperCollider's Pulse of width w: band-limited, with no DC, and
// half the size of an LFPulse.
func pulseBL(p, dt, w float64) float64 { return 0.5 * blepPulse(p, dt, w) }

// osc is a phase accumulator.
type osc struct{ p float64 }

func (o *osc) step(dt float64) float64 {
	o.p += dt
	o.p -= math.Floor(o.p)
	return o.p
}

func init() {
	natives["bass_foundation"] = newBassFoundation
	natives["bass_highend"] = newBassHighend
	natives["winwood_lead"] = newWinwoodLead
	natives["zawa"] = newZawa
	natives["supersaw"] = newSupersaw
	natives["subpulse"] = newSubpulse
	natives["chiplead"] = newChiplead
	natives["chipbass"] = newChipbass
	natives["tb303"] = newTB303
	natives["hoover"] = newHoover
	natives["tech_saws"] = newTechSaws
	natives["growl"] = newGrowl
	for name, gen := range map[string]struct {
		mk   func(v *Voice) func() float64
		gain float64
	}{
		"sp_noise": {whiteSrc, 0.9}, "sp_pnoise": {pinkSrc, 3}, "sp_bnoise": {brownSrc, 1.2},
		"sp_gnoise": {graySrc, 1}, "sp_cnoise": {clipSrc, 0.6},
	} {
		natives[name] = noiseSynth(gen.mk, gen.gain)
	}
	natives["chipnoise"] = newChipnoise
	natives["kalimba"] = newKalimba
	natives["gabberkick"] = newGabberkick
	natives["rhodey"] = newRhodey
	natives["rodeo"] = newRodeo
	natives["organ_tonewheel"] = newOrgan
}

// sinKr is a sine of rate hz and a phase offset, read at time t.
func sinKr(t, hz, ph float64) float64 { return math.Sin(2*math.Pi*hz*t + ph) }

// lfPar is SuperCollider's LFPar from a phase in 0..1 (starting at 1).
func lfParAt(p float64) float64 {
	p -= math.Floor(p)
	z := 4 * p
	switch {
	case z < 1:
		return 1 - z*z
	case z < 3:
		z -= 2
		return z*z - 1
	}
	z -= 4
	return 1 - z*z
}

// delayC is a delay line read with linear interpolation (SuperCollider's
// DelayL/DelayC/CombC with no feedback).
type delayLine struct {
	buf []float64
	pos int
}

func newDelay(maxSec, sr float64) *delayLine {
	return &delayLine{buf: make([]float64, int(maxSec*sr)+4)}
}

func (d *delayLine) run(x, sec, sr float64) float64 {
	n := len(d.buf)
	d.buf[d.pos] = x
	dl := clamp(sec*sr, 1, float64(n-2))
	i := math.Floor(dl)
	f := dl - i
	a := d.buf[((d.pos-int(i))%n+n)%n]
	b := d.buf[((d.pos-int(i)-1)%n+n)%n]
	d.pos = (d.pos + 1) % n
	return a + (b-a)*f
}

// pinkKellet is a pink noise source (Paul Kellet's filter).
type pinkKellet struct{ b [3]float64 }

func (p *pinkKellet) next(w float64) float64 {
	p.b[0] = 0.99765*p.b[0] + w*0.0990460
	p.b[1] = 0.96300*p.b[1] + w*0.2965164
	p.b[2] = 0.57000*p.b[2] + w*1.0526913
	return (p.b[0] + p.b[1] + p.b[2] + w*0.1848) * 0.25
}

// white is a uniform -1..1 source for a native voice.
func white(v *Voice) float64 { return float64(v.noise()) }

// rand01 is a uniform 0..1 value (SuperCollider's Rand, taken once per note).
func rand01(v *Voice) float64 { return (white(v) + 1) / 2 }

// bassFilterEnv is the filter envelope bass_foundation and bass_highend
// share: Env.adsr(0, 0.5, 0.2, 0.2) held at its sustain.
func bassFilterEnv(t float64) float64 {
	if t >= 0.5 {
		return 0.2
	}
	return shapeAt(5, -4, 1, 0.2, t/0.5)
}

// cutoffHolder keeps a native voice's cutoff (a MIDI note) so control can
// move it.
type cutoffHolder struct{ midi float64 }

func (c *cutoffHolder) hz() float64 { return midicps(c.midi) }

func newBassFoundation(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 83)}
	rq := rqOf(o("res", 0.5))
	var f scRLPF
	var sx osc
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			x := sawBL(sx.step(v.freq/v.sr), v.freq/v.sr)
			return f.run(x, cut.hz()*bassFilterEnv(t)+100, rq, v.sr)
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newBassHighend(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 102)}
	rq := rqOf(o("res", 0.9))
	drive := o("drive", 2)
	var f scRLPF
	var s [3]osc
	shelf := lowShelf(300, 1, -12, v.sr)
	peak := peakEQ(1600, 1, -6, v.sr)
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			var x float64
			for i, m := range [3]float64{0.25, 1, 1.5} {
				dt := v.freq * m / v.sr
				x += sawBL(s[i].step(dt), dt) * [3]float64{0.5, 0.4, 0.1}[i]
			}
			y := f.run(x, cut.hz()*bassFilterEnv(t)+100, rq, v.sr)
			ab := math.Abs(y)
			y = y * (ab + drive) / ((y*y+drive-1)*ab + 1)
			y = peak.run(shelf.run(y))
			return y * 2
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newWinwoodLead(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 119)}
	rq := rqOf(o("res", 0.2))
	width, rate := o("lfo_width", 0.01), o("lfo_rate", 8)
	ratio, length := o("ramp_ratio", 0.5), math.Max(o("ramp_length", 0.2), 1e-4)
	var f scRLPF
	var p1, p2, lfo osc
	shelf := lowShelf(351, 1, -9, v.sr)
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			ramp := 1.0
			if t < length {
				ramp = shapeAt(5, -4, 0, 1, t/length)
			}
			ramp = ratio*ramp + (1 - ratio)
			l := lfTriAt(lfo.step(rate / v.sr))
			f1 := v.freq * ramp * (1 + width*l)
			f2 := f1 * 1.001
			x := (pulseBL(p1.step(f1/v.sr), f1/v.sr, 0.2) + pulseBL(p2.step(f2/v.sr), f2/v.sr, 0.19)) * 0.5
			return shelf.run(f.run(x, cut.hz(), rq, v.sr))
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newZawa(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 100)}
	rq := rqOf(o("res", 0.9))
	phase := math.Max(o("phase", 1), 1e-3) // seconds (the Ruby side turns beats into seconds)
	po := o("phase_offset", 0)
	wave, rng := int(o("wave", 3)), o("range", 24)
	disable, invert, pw := o("disable_wave", 0) > 0, o("invert_wave", 0) > 0, o("pulse_width", 0.5)
	var f scRLPF
	var lf osc
	var sync, saw float64 // SyncSaw phases in -1..1
	n := 0
	return &native{
		next: func() float64 {
			n++
			p := lf.step(1 / phase / v.sr)
			var c float64
			switch wave {
			case 0: // -LFSaw, starting at phase 2*offset+1
				c = -(math.Mod(2*po+1+2*p+1, 2) - 1)
			case 1: // LFPulse
				if math.Mod(po+p, 1) < pw {
					c = 1
				} else {
					c = -1
				}
			case 2: // LFTri, starting at phase 2*offset+1
				x := math.Mod(2*po+1+4*p+1, 4) - 1
				if x < 1 {
					c = x
				} else {
					c = 2 - x
				}
			default:
				c = math.Sin(2*math.Pi*p + (po+0.25)*2*math.Pi)
			}
			if invert {
				c = -c
			}
			note := 69 + 12*math.Log2(v.freq/440)
			sf := midicps(note + rng)
			if !disable {
				sf = midicps(linlin(c, -1, 1, note, note+rng))
			}
			// SyncSaw: a saw at sf whose phase restarts every period of the note
			f1, f2 := 2*v.freq/v.sr, 2*sf/v.sr
			z := saw
			saw += f2
			if saw >= 1 {
				saw -= 2
			}
			sync += f1
			if sync >= 1 {
				sync -= 2
				saw = (sync+1)*f2/f1 - 1
			}
			return f.run(z, cut.hz(), rq, v.sr) * 0.5
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newSupersaw(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 130)}
	rq := rqOf(o("res", 0.7))
	var f scRLPF
	var main osc
	var sh [4]osc
	dc := leakDC{coef: 0.995}
	nz := newNormalizer(1, 0.01, v.sr)
	rates := [4]float64{4, 7, 5, 2}
	return &native{
		next: func() float64 {
			in := lfSawAt(main.step(v.freq / v.sr))
			var sum float64
			for i := range sh {
				c := 0.0
				if in > lfSawAt(sh[i].step(rates[i]/v.sr)) {
					c = 1
				}
				sum += in - c
			}
			x := dc.run((sum - in) * 0.25)
			return nz.run(f.run(x, cut.hz(), rq, v.sr)) * 0.9
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newSubpulse(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 100)}
	pw, sub, det := o("pulse_width", 0.5), o("sub_amp", 1), o("sub_detune", -12)
	var p, s osc
	var lpq biq
	last := -1.0
	nz := newNormalizer(1, 0.01, v.sr)
	return &native{
		next: func() float64 {
			dt := v.freq / v.sr
			x := pulseBL(p.step(dt), dt, pw)
			x += sub * math.Sin(2*math.Pi*s.step(v.freq*math.Pow(2, det/12)/v.sr))
			if hz := cut.hz(); hz != last {
				keep := lpq
				lpq = lowpass2(hz, v.sr)
				lpq.z1, lpq.z2 = keep.z1, keep.z2
				last = hz
			}
			return nz.run(lpq.run(x)) * 0.8
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newChiplead(v *Voice, o func(string, float64) float64) *native {
	w := [3]float64{0.125, 0.25, 0.5}[int(clamp(o("width", 0), 0, 2))]
	var p osc
	return &native{
		next: func() float64 {
			dt := v.freq / v.sr
			x := pulseBL(p.step(dt), dt, w)
			if a := math.Abs(x); a > 0.5 { // softclip
				x = (a - 0.25) / x
			}
			return x * 0.8
		},
	}
}

func newChipbass(v *Voice, o func(string, float64) float64) *native {
	// a 32-step triangle, 16 levels, stepped at 32 times the note
	var p osc
	step := 0
	return &native{
		next: func() float64 {
			ph := p.p
			if p.step(32*v.freq/v.sr) < ph { // wrapped: the next step
				step = (step + 1) % 32
			}
			i := step
			if i > 15 {
				i = 31 - i
			}
			return linlin(float64(i), 0, 15, -1, 1)
		},
	}
}

func newTB303(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 120)}
	cmin := o("cutoff_min", 30)
	rq := rqOf(o("res", 0.9))
	wave, pw := int(o("wave", 0)), o("pulse_width", 0.5)
	att, dec, sus, rel := o("attack", 0.01), o("decay", 0), o("sustain", 0), o("release", 1)
	al, sl := o("attack_level", 1), o("sustain_level", 1)
	dl := o("decay_level", -1)
	if dl < 0 {
		dl = sl
	}
	pick := func(k string, d float64) float64 {
		if x := o(k, -1); x >= 0 {
			return x
		}
		return d
	}
	cat, cdc, csu, cre := pick("cutoff_attack", att), pick("cutoff_decay", dec), pick("cutoff_sustain", sus), pick("cutoff_release", rel)
	cal, csl := o("cutoff_attack_level", 1), o("cutoff_sustain_level", 1)
	cdl := o("cutoff_decay_level", -1)
	if cdl < 0 {
		cdl = csl
	}
	cc := o("cutoff_env_curve", 2)
	fe := &curveEnv{lv: []float64{0, cal, cdl, csl, 0}, tm: []float64{cat, cdc, csu, cre}, shape: 5, curve: cc}
	var f scRLPF
	var p osc
	n := 0
	_ = att + dec + sus + rel + al + dl
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			dt := v.freq / v.sr
			var x float64
			switch wave {
			case 1:
				x = pulseBL(p.step(dt), dt, pw)
			case 2:
				x = 2 * lfTriAt(p.step(dt))
			default:
				x = sawBL(p.step(dt), dt)
			}
			return f.run(x, midicps(cmin)+fe.at(t)*cut.hz(), rq, v.sr)
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newHoover(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 130)}
	rq := rqOf(o("res", 0.1))
	pre := o("pre_amp", 10)
	// the three copies of the voice, each with its own vibrato and width wobble
	vibRate := [3]float64{2.94954, 2.98256, 2.9805}
	vibPh := [3]float64{3.02158, 4.7889, 2.75812}
	pwmRate := [3]float64{2.7397, 2.29176, 2.88545}
	sawMul := [3]float64{0.25, 0.5, 1}
	pulMul := [3]float64{0.5, 1, 2}
	var saw, pul, par [3]osc
	var eq1, eq2 [3]biq
	var dl [3]*delayLine
	var f [3]scRLPF
	for k := range dl {
		dl[k] = newDelay(0.005, v.sr)
		eq1[k] = peakEQ(6000, 1, 3, v.sr)
		eq2[k] = peakEQ(3500, 1, 6, v.sr)
	}
	chPh := [3]float64{math.Pi / 2, 3 * math.Pi / 2, math.Pi / 2}
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			var fr [3]float64
			var mix float64
			for j := 0; j < 3; j++ {
				s := sinKr(t, vibRate[j], vibPh[j])
				fr[j] = v.freq * 0.995 * math.Pow(1.005/0.995, (s+1)/2)
				a := saw[j].step(sawMul[j] * fr[j] / v.sr)
				pw := 0.375*sinKr(t, pwmRate[j], 0) + 0.5
				pp := pul[j].step(pulMul[j] * fr[j] / v.sr)
				pv := 0.0
				if pp < pw {
					pv = 1
				}
				mix += (a) * (1 - pv)
			}
			mix *= 0.1
			var out float64
			for k := 0; k < 3; k++ {
				x := (mix + lfParAt(par[k].step(0.25*fr[k]/v.sr))) * 0.1
				x = eq2[k].run(eq1[k].run(x))
				dt := sinKr(t, 3, chPh[k])*0.000833333 + 0.00416667
				x += 0.5 * dl[k].run(x, dt, v.sr)
				out += f[k].run(pre*x, cut.hz(), rq, v.sr)
			}
			return out * 2.5
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newGrowl(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 130)}
	rq := rqOf(o("res", 0.7))
	var sw, sn osc
	lp := lowpass2(440, v.sr)
	var f scRLPF
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			a := sawBL(sw.step(0.25*v.freq/v.sr), 0.25*v.freq/v.sr)
			b := math.Sin(2 * math.Pi * sn.step(1.01*v.freq/v.sr))
			x := lp.run((a + b) * 0.5)
			// PitchShift(window 0.5 s, ratio 1) is four overlapping grains that
			// fade in one quarter-window after another: the sound swells in
			return f.run(x*pitchShiftSwell(t, 0.5), cut.hz(), rq, v.sr) * 3
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

// freeVerb is SuperCollider's FreeVerb: Jezar's Freeverb, one channel.
type freeVerb struct {
	comb  [8]fvComb
	allp  [4]fvAllp
	mix   float64
	fb, d float64
}

type fvComb struct {
	buf   []float64
	i     int
	store float64
}

type fvAllp struct {
	buf []float64
	i   int
}

func newFreeVerb(sr, mix, room, damp float64) *freeVerb {
	z := &freeVerb{mix: clamp(mix, 0, 1)}
	k := sr / 44100
	for i, n := range [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617} {
		z.comb[i].buf = make([]float64, int(float64(n)*k))
	}
	for i, n := range [4]int{556, 441, 341, 225} {
		z.allp[i].buf = make([]float64, int(float64(n)*k))
	}
	z.fb = clamp(room, 0, 1)*0.28 + 0.7
	z.d = clamp(damp, 0, 1) * 0.4
	return z
}

func (z *freeVerb) run(x float64) float64 {
	in := x * 0.015
	var out float64
	for i := range z.comb {
		c := &z.comb[i]
		y := c.buf[c.i]
		c.store = y*(1-z.d) + c.store*z.d
		c.buf[c.i] = in + c.store*z.fb
		if c.i++; c.i == len(c.buf) {
			c.i = 0
		}
		out += y
	}
	for i := range z.allp {
		a := &z.allp[i]
		b := a.buf[a.i]
		y := -out + b
		a.buf[a.i] = out + b*0.5
		if a.i++; a.i == len(a.buf) {
			a.i = 0
		}
		out = y
	}
	return x*(1-z.mix) + out*z.mix*freeVerbWet
}

// freeVerbWet is the gain of the reverb's own signal (calibrated against
// scsynth).
const freeVerbWet = 1

func newTechSaws(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 130)}
	rq := rqOf(0) // Sonic Pi clips res with its limits the wrong way round (Clip(res, 1, 0)): the filter has no resonance
	if o("res", 0.7) < 0 {
		rq = 1
	}
	var ph [10]osc
	var dl [10]*delayLine
	var det, dly [10]float64
	pan := [5]float64{-1, -0.5, 0, 0.5, 1}
	for i := range ph {
		dl[i] = newDelay(0.005, v.sr)
		det[i] = 0.99 + 0.02*rand01(v)
		dly[i] = math.Min(0.0001+0.0099*rand01(v), 0.005)
	}
	var fl, fr scRLPF
	var pk pinkKellet
	var vl, vr = newFreeVerb(v.sr, 0.45, 1.8, 0.5), newFreeVerb(v.sr, 0.45, 1.8, 0.5)
	nl, nr := newNormalizer(1, 0.01, v.sr), newNormalizer(1, 0.01, v.sr)
	return &native{
		lr: func() (float64, float64) {
			var hl, hr, ll, lr float64
			for i := 0; i < 10; i++ {
				fq := v.freq * det[i]
				if i >= 5 {
					fq /= 2
				}
				x := dl[i].run(lfSawAt(ph[i].step(fq/v.sr)), dly[i], v.sr)
				a := (pan[i%5] + 1) * math.Pi / 4
				if i < 5 {
					hl += x * math.Cos(a)
					hr += x * math.Sin(a)
				} else {
					ll += x * math.Cos(a)
					lr += x * math.Sin(a)
				}
			}
			noise := 0.07 * pk.next(white(v))
			l := 0.65*hl*0.447214 + 0.85*ll*0.447214 + noise
			r := 0.65*hr*0.447214 + 0.85*lr*0.447214 + noise
			l, r = clamp(l, -0.55, 0.55), clamp(r, -0.55, 0.55)
			l = vl.run(nl.run(fl.run(l, cut.hz(), rq, v.sr)))
			r = vr.run(nr.run(fr.run(r, cut.hz(), rq, v.sr)))
			return l * 0.9, r * 0.9
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

// pitchShiftSwell is the gain SuperCollider's PitchShift gives a sound at
// ratio 1: four triangular grains a quarter window apart, the first one
// starting a quarter window in, so the sound fades in over a window.
func pitchShiftSwell(t, w float64) float64 {
	var g float64
	for i := 1; i <= 4; i++ {
		x := (t - float64(i)*w/4) / w
		if x < 0 {
			continue
		}
		x -= math.Floor(x)
		g += 1 - math.Abs(2*x-1)
	}
	return g * 0.5
}

// The noise synths filter one kind of noise with an RLPF and a fixed gain.
func noiseSynth(src func(v *Voice) func() float64, gain float64) nativeMaker {
	return func(v *Voice, o func(string, float64) float64) *native {
		cut := &cutoffHolder{o("cutoff", 110)}
		rq := rqOf(o("res", 0))
		gen := src(v)
		var f scRLPF
		return &native{
			next: func() float64 { return gain * f.run(gen(), cut.hz(), rq, v.sr) },
			cut:  func(m float64) { cut.midi = m },
			cur:  func() float64 { return cut.midi },
		}
	}
}

func whiteSrc(v *Voice) func() float64 { return func() float64 { return white(v) } }

func clipSrc(v *Voice) func() float64 {
	return func() float64 {
		if white(v) < 0 {
			return -1
		}
		return 1
	}
}

// brownSrc is SuperCollider's BrownNoise: a random walk bounced off +-1.
func brownSrc(v *Voice) func() float64 {
	z := 0.0
	return func() float64 {
		z += white(v) * 0.125
		if z > 1 {
			z = 2 - z
		} else if z < -1 {
			z = -2 - z
		}
		return z
	}
}

// graySrc is SuperCollider's GrayNoise: one random bit of a 32-bit number
// flips each sample.
func graySrc(v *Voice) func() float64 {
	var b int32
	return func() float64 {
		v.noise() // steps the generator
		b ^= int32(1) << ((v.rng >> 5) & 31)
		return float64(b) * 4.65661287308e-10
	}
}

// pinkSrc is pink noise at SuperCollider's level (measured against scsynth).
func pinkSrc(v *Voice) func() float64 {
	var p pinkKellet
	return func() float64 { return pinkScale * p.next(white(v)) }
}

const pinkScale = 0.385

func newChipnoise(v *Voice, o func(string, float64) float64) *native {
	bands := [16]float64{1, 2, 4, 5.34, 8, 10.68, 16, 19.03, 25.4, 32, 42.71, 64, 128, 256, 512, 1024}
	var ph float64
	val := 1.0
	return &native{
		next: func() float64 {
			f := 220 * bands[int(clamp(math.Floor(o("freq_band", 0)), 0, 15))]
			ph += f / v.sr
			if ph >= 1 {
				ph -= math.Floor(ph)
				val = 1
				if white(v) < 0 {
					val = -1
				}
			}
			x := val
			if a := math.Abs(x); a > 0.5 {
				x = (a - 0.25) / x
			}
			return x * 0.8
		},
	}
}

// adsrLin is Sonic Pi's straight-line envelope, for the synths that keep
// several of their own: times in seconds.
type adsrLin struct{ a, d, s, r, al, dl, sl float64 }

func adsrOf(o func(string, float64) float64, a, d, s, r, sl float64) adsrLin {
	p := adsrLin{a: o("attack", a), d: o("decay", d), s: o("sustain", s), r: o("release", r),
		al: o("attack_level", 1), sl: o("sustain_level", sl)}
	if p.dl = o("decay_level", -1); p.dl < 0 {
		p.dl = p.sl
	}
	return p
}

// at is the level t seconds in, with the decay stretched by dm.
func (p adsrLin) at(t, dm float64) float64 {
	d := p.d * dm
	seg := func(from, to, dur float64) (float64, bool) {
		if t >= dur {
			t -= dur
			return 0, false
		}
		if dur <= 0 {
			return to, true
		}
		return from + (to-from)*t/dur, true
	}
	if v, ok := seg(0, p.al, p.a); ok {
		return v
	}
	if v, ok := seg(p.al, p.dl, d); ok {
		return v
	}
	if v, ok := seg(p.dl, p.sl, p.s); ok {
		return v
	}
	if v, ok := seg(p.sl, 0, p.r); ok {
		return v
	}
	return 0
}

// highpass2 is SuperCollider's HPF.
func highpass2(freq, sr float64) biq {
	w := 2 * math.Pi * math.Min(freq, sr*0.49) / sr
	cs, al := math.Cos(w), math.Sin(w)/math.Sqrt2
	var q biq
	q.set((1+cs)/2, -(1 + cs), (1+cs)/2, 1+al, -2*cs, 1-al)
	return q
}

// scBPF is SuperCollider's BPF (rq is bandwidth over frequency).
type scBPF struct{ y1, y2 float64 }

func (b *scBPF) run(x, freq, rq, sr float64) float64 {
	pf := freq * 2 * math.Pi / sr
	pbw := rq * pf * 0.5
	c := 1 / math.Tan(pbw)
	d := 2 * math.Cos(pf)
	a0 := 1 / (1 + c)
	b1, b2 := c*d*a0, (1-c)*a0
	y0 := x + b1*b.y1 + b2*b.y2
	out := a0 * (y0 - b.y2)
	b.y2, b.y1 = b.y1, y0
	return out
}

// ringz is SuperCollider's Ringz: a resonator that rings for decay seconds.
type ringz struct{ y1, y2, b1, b2 float64 }

func newRingz(freq, decay, sr float64) *ringz {
	r := math.Exp(math.Log(0.001) / (decay * sr))
	twoR, r2 := 2*r, r*r
	cost := twoR * math.Cos(freq*2*math.Pi/sr) / (1 + r2)
	return &ringz{b1: twoR * cost, b2: -r2}
}

func (z *ringz) run(x float64) float64 {
	y0 := x + z.b1*z.y1 + z.b2*z.y2
	out := 0.5 * (y0 - z.y2)
	z.y2, z.y1 = z.y1, y0
	return out
}

func expRand(v *Voice, lo, hi float64) float64 {
	return math.Exp(math.Log(lo) + math.Log(hi/lo)*rand01(v))
}

func newKalimba(v *Voice, o func(string, float64) float64) *native {
	click := o("clickiness", 0.1)
	rel := 3 + rand01(v)
	var tone osc
	lp, hp := lowpass2(380, v.sr), highpass2(120, v.sr)
	var pk pinkKellet
	var bp scBPF
	rz := [3]*ringz{
		newRingz(240*expRand(v, 0.97, 1.02), 0.8, v.sr),
		newRingz(2020*expRand(v, 0.97, 1.02), 0.07, v.sr),
		newRingz(3151*expRand(v, 0.97, 1.02), 0.08, v.sr),
	}
	gain := [3]float64{0.354813, 1, 0.562341}
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			var e float64
			if t < 0.03 {
				e = shapeAt(5, -7, 0, 1, t/0.03)
			} else if t < 0.03+rel {
				e = shapeAt(5, -7, 1, 0, (t-0.03)/rel)
			}
			x := hp.run(lp.run(math.Sin(2*math.Pi*tone.step(v.freq/v.sr)) * e))
			var ce float64
			if t < 0.001 {
				ce = shapeAt(5, -4, 0, 1, t/0.001)
			} else if t < 0.011 {
				ce = shapeAt(5, -4, 1, 0, (t-0.001)/0.01)
			}
			in := bp.run(pk.next(white(v)), 6500, 0.1, v.sr) * ce
			var c float64
			for i, r := range rz {
				c += r.run(in) * gain[i]
			}
			return x*click + c*click*(1-click)
		},
	}
}

func newGabberkick(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 119)}
	rq := rqOf(o("res", 0.2))
	boost := o("boost", 8)
	s0, s1 := midicps(o("slope_start", 84)), midicps(o("slope_intermediate", 69))
	l1, l2 := math.Max(o("slope_length1", 0.015), 1e-4), math.Max(o("slope_length2", 0.1), 1e-4)
	var ph [3]osc
	var eq biq
	var f scRLPF
	dl := newDelay(0.01, v.sr)
	var lfo osc
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			var fr float64
			switch {
			case t < l1:
				fr = shapeAt(2, 0, s0, s1, t/l1)
			case t < l1+l2:
				fr = shapeAt(2, 0, s1, v.freq, (t-l1)/l2)
			default:
				fr = v.freq
			}
			var x float64
			for i, m := range [3]float64{0.99424, 1.00579, 1} {
				x += math.Sin(2 * math.Pi * ph[i].step(fr*m/v.sr))
			}
			x = math.Atan(math.Tanh(x*10) * 5.1)
			if n%16 == 0 { // XLine 2000 -> 100 over 0.3 s
				keep := eq
				ft := 100.0
				if t < 0.3 {
					ft = 2000 * math.Pow(100.0/2000, t/0.3)
				}
				eq = peakEQ(ft, 2, boost, v.sr)
				eq.z1, eq.z2 = keep.z1, keep.z2
			}
			n++
			x = eq.run(x)
			x += dl.run(x, 0.0005+0.0005*math.Sin(2*math.Pi*lfo.step(4/v.sr)), v.sr)
			x = f.run(x, cut.hz(), rq, v.sr) * 0.6
			return clamp(x, -1, 1) * 0.5
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

func newRhodey(v *Voice, o func(string, float64) float64) *native {
	env := adsrOf(o, 0.001, 1, 0, 1, 0)
	vel, mod, mix := o("vel", 0.8), o("mod_index", 0.2), o("mix", 0.2)
	lw, lr := o("lfo_width", 0.3), o("lfo_rate", 0.4)*12
	var p4, p3, p2, p1, lfo osc
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			f := v.freq * 2
			e1, e2, e3 := env.at(t, 1.25), env.at(t, 1), env.at(t, 1.5)
			osc4 := math.Sin(2*math.Pi*p4.step(f*0.5/v.sr)) * 2 * math.Pi * 2 * 0.535887 * mod * e3 * vel
			osc3 := math.Sin(2*math.Pi*p3.step(f/v.sr)+osc4) * e3 * vel
			osc2 := math.Sin(2*math.Pi*p2.step(f*15/v.sr)) * 2 * math.Pi * 0.108819 * e2 * vel
			osc1 := math.Sin(2*math.Pi*p1.step(f/v.sr)+osc2) * e1 * vel
			x := osc3*(1-mix) + osc1*mix
			return x * (math.Sin(2*math.Pi*lfo.step(lr/v.sr))*lw + 1)
		},
	}
}

func newRodeo(v *Voice, o func(string, float64) float64) *native {
	cut := &cutoffHolder{o("cutoff", 95)}
	rq := o("rq", 0.5)
	var p1, p2 osc
	var s [4][2]float64 // four biquads' two delays (x1, x2) and (y1, y2) kept below
	var x1, x2, y1, y2 [4]float64
	_ = s
	n := 0
	run := func(k int, x, a0, a1, b1, b2 float64) float64 {
		y := a0*x + a1*x1[k] + a0*x2[k] + b1*y1[k] + b2*y2[k]
		x2[k], x1[k] = x1[k], x
		y2[k], y1[k] = y1[k], y
		return y
	}
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			fe := 1.0 // the filter envelope: 1 falling to 0.8 over a second, squared
			if t < 1 {
				fe = shapeAt(5, -4, 1, 0.8, t)
			} else {
				fe = 0.8
			}
			fc := fe*fe*cut.hz() + 200 + v.freq
			w := 2 * math.Pi * math.Min(fc, v.sr*0.45) / v.sr
			cs, sn := math.Cos(w), math.Sin(w)
			al := sn * 0.5 * math.Sqrt(rq)
			r := 1 / (1 + al)
			a0, a1, b1, b2 := (1-cs)*0.5*r, (1-cs)*r, 2*cs*r, -(1-al)*r
			hi := 0.3 * pulseBL(p2.step(6.92429*v.freq/v.sr), 6.92429*v.freq/v.sr, 0.1)
			lo := 0.7 * pulseBL(p1.step(v.freq/v.sr), v.freq/v.sr, 0.2)
			a := run(1, run(0, lo, a0, a1, b1, b2), a0, a1, b1, b2)
			b := run(3, run(2, hi, a0, a1, b1, b2), a0, a1, b1, b2)
			return a + b
		},
		cut: func(m float64) { cut.midi = m },
		cur: func() float64 { return cut.midi },
	}
}

// shapedAdsr is a Sonic Pi envelope with every move on one SuperCollider
// shape, t seconds in.
func (p adsrLin) shaped(t float64, shape int) float64 {
	seg := func(from, to, dur float64) (float64, bool) {
		if t >= dur {
			t -= dur
			return 0, false
		}
		return shapeAt(shape, 0, from, to, t/dur), true
	}
	if v, ok := seg(0, p.al, p.a); ok {
		return v
	}
	if v, ok := seg(p.al, p.dl, p.d); ok {
		return v
	}
	if v, ok := seg(p.dl, p.sl, p.s); ok {
		return v
	}
	if v, ok := seg(p.sl, 0, p.r); ok {
		return v
	}
	return 0
}

// vibrato is SuperCollider's Vibrato: a sine on rate that wanders a little in
// rate and depth, which starts after delay and reaches full depth over onset.
type vibrato struct {
	v                     *Voice
	rate, depth, rvar, dv float64
	delay, onset, iphase  float64
	ph                    float64
	r0, r1, d0, d1        float64
	n                     int
}

func newVibrato(v *Voice, rate, depth, delay, onset, rvar, dvar, iphase float64) *vibrato {
	b := &vibrato{v: v, rate: rate, depth: depth, rvar: rvar, dv: dvar, delay: delay, onset: onset, iphase: iphase}
	b.r0, b.r1, b.d0, b.d1 = white(v), white(v), white(v), white(v)
	return b
}

// next is the modulation, -depth..depth, scaled by how far into the onset the note is.
func (b *vibrato) next(t float64) float64 {
	step := int(b.v.sr / 4) // the wander picks a new target four times a second
	if b.n%step == 0 {
		b.r0, b.r1 = b.r1, white(b.v)
		b.d0, b.d1 = b.d1, white(b.v)
	}
	f := float64(b.n%step) / float64(step)
	b.n++
	rate := b.rate * (1 + b.rvar*(b.r0+(b.r1-b.r0)*f))
	depth := b.depth * (1 + b.dv*(b.d0+(b.d1-b.d0)*f))
	b.ph += rate / b.v.sr
	ramp := 1.0
	if t < b.delay {
		ramp = 0
	} else if b.onset > 0 && t < b.delay+b.onset {
		ramp = (t - b.delay) / b.onset
	}
	return depth * ramp * math.Sin(2*math.Pi*b.ph+b.iphase)
}

func newOrgan(v *Voice, o func(string, float64) float64) *native {
	env := adsrOf(o, 0.01, 0, 1, 0.01, 1)
	bars := [9]float64{o("bass", 8), o("quint", 8), o("fundamental", 8), o("oct", 8), o("nazard", 0),
		o("blockflute", 0), o("tierce", 0), o("larigot", 0), o("sifflute", 0)}
	discs := [9]float64{0.5, 1.5, 1, 2, 3, 4, 5, 6, 8}
	rf := o("rs_freq", 6.7)
	lf := math.Max(linlin(rf, 0, 6.666667, -0.047619, 5.666667), 0)
	delay, onset, fv := o("rs_delay", 0), o("rs_onset", 0), o("rs_freq_var", 0.1)
	var htPh, hpPh float64
	if rf >= 1 { // Select.kr(rs_freq, [0, -pi]): index 0 for rs_freq < 1
		htPh, hpPh = -math.Pi, -math.Pi/2
	}
	lt := newVibrato(v, lf, o("rs_amplitude_depth", 0.2), delay, onset, fv, fv, 0)
	ht := newVibrato(v, rf, o("rs_amplitude_depth", 0.2), delay, onset, fv, fv, htPh)
	lp := newVibrato(v, lf, o("rs_pan_depth", 0.05), delay, onset, fv, fv, 0)
	hp := newVibrato(v, rf, o("rs_pan_depth", 0.05), delay, onset, fv, fv, hpPh)
	pv := newVibrato(v, rf, o("rs_pitch_depth", 0.008), delay, onset, fv, fv, 0)
	var ph [9]float64
	lo, hi := lowpass2(800, v.sr), highpass2(800, v.sr)
	n := 0
	panGain := func(pos float64) (float64, float64) { // Pan2's gains with the centre at 1
		pos = clamp(pos, -1, 1)
		a := (pos + 1) * math.Pi / 4
		return math.Cos(a) / math.Sqrt2 * 2, math.Sin(a) / math.Sqrt2 * 2
	}
	return &native{
		own: true,
		lr: func() (float64, float64) {
			t := float64(n) / v.sr
			n++
			eg := env.shaped(t, 3)
			pitch := v.freq * (1 + pv.next(t))
			var x float64
			for i := range ph {
				ph[i] += pitch * discs[i] / v.sr
				ph[i] -= math.Floor(ph[i])
				x += math.Sin(2*math.Pi*ph[i]) * eg * bars[i] / 144
			}
			x = x / (1 + math.Abs(x))
			l := lo.run(x) * (1 + lt.next(t))
			h := hi.run(x) * (1 + ht.next(t))
			ll, lr := panGain(lp.next(t))
			hl, hr := panGain(hp.next(t))
			L, R := l*ll+h*hl, l*lr+h*hr
			L, R = L/(1+math.Abs(L))*4, R/(1+math.Abs(R))*4
			return L, R
		},
	}
}

func init() { natives["piano"] = newPiano }

// newPiano plays Sonic Pi's piano (an MdaPiano) from measurements of it: the
// first sixteen partials, each with its own level and decay, stretched the way
// a string's are, for the note and velocity (pianodata.go).
func newPiano(v *Voice, o func(string, float64) float64) *native {
	vel := clamp(o("vel", 0.2), 0, 1)
	note := clamp(69+12*math.Log2(v.freq/440), pianoNotes[0], pianoNotes[9])
	// where the note falls among the measured ones
	ni := 0
	for ni < 8 && note > pianoNotes[ni+1] {
		ni++
	}
	nf := (note - pianoNotes[ni]) / (pianoNotes[ni+1] - pianoNotes[ni])
	vi := 0
	if vel > pianoVels[1] {
		vi = 1
	}
	vf := (vel - pianoVels[vi]) / (pianoVels[vi+1] - pianoVels[vi])
	vf = clamp(vf, -0.5, 1)
	mixLog := func(a, b, f float64) float64 {
		if a <= 0 || b <= 0 {
			return (1-f)*a + f*b
		}
		return math.Exp((1-f)*math.Log(a) + f*math.Log(b))
	}
	var a1, t1, a2, t2, frq [16]float64
	var ph [16]float64
	for k := 0; k < 16; k++ {
		var a, b, c, d, bb [2]float64
		for j := 0; j < 2; j++ {
			e0, e1 := &pianoFit[vi+j][ni], &pianoFit[vi+j][ni+1]
			a[j] = mixLog(e0.A1[k], e1.A1[k], nf)
			b[j] = (1-nf)*e0.T1[k] + nf*e1.T1[k]
			c[j] = mixLog(e0.A2[k], e1.A2[k], nf)
			d[j] = (1-nf)*e0.T2[k] + nf*e1.T2[k]
			bb[j] = (1-nf)*e0.B + nf*e1.B
		}
		a1[k] = mixLog(a[0], a[1], vf)
		t1[k] = math.Max((1-vf)*b[0]+vf*b[1], 0.02)
		a2[k] = mixLog(c[0], c[1], vf)
		t2[k] = math.Max((1-vf)*d[0]+vf*d[1], 0.02)
		frq[k] = float64(k+1) * math.Sqrt(1+((1-vf)*bb[0]+vf*bb[1])*float64((k+1)*(k+1)))
	}
	n := 0
	return &native{
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			var x float64
			for k := 0; k < 16; k++ {
				if a1[k] <= 0 && a2[k] <= 0 {
					continue
				}
				f := v.freq * frq[k]
				if f > v.sr*0.45 {
					continue
				}
				ph[k] += f / v.sr
				ph[k] -= math.Floor(ph[k])
				x += (a1[k]*math.Exp(-t/t1[k]) + a2[k]*math.Exp(-t/t2[k])) * math.Sin(2*math.Pi*ph[k])
			}
			return x
		},
	}
}
