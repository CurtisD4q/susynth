package synth

import "math"

// Sonic Pi's sc808 drums, ported from their compiled SynthDefs. Each rings by
// itself (its own envelopes, in beats scaled to seconds by the caller) and
// ends when it has been quiet for a tenth of a second, as DetectSilence does.

func init() {
	for name, mk := range map[string]nativeMaker{
		"sc808_bassdrum":     newBassdrum,
		"sc808_snare":        newSnare,
		"sc808_clap":         newClap,
		"sc808_claves":       newClaves,
		"sc808_closed_hihat": newClosedHihat,
		"sc808_open_hihat":   newOpenHihat,
		"sc808_cowbell":      newCowbell,
		"sc808_cymbal":       newCymbal,
		"sc808_maracas":      newMaracas,
		"sc808_rimshot":      newRimshot,
		"sc808_tomlo":        tuned(40, 20, 0.4, 1.25, 1.125, -250),
		"sc808_tommid":       tuned(44, 16, 0.4, 1.3333, 1.125, -250),
		"sc808_tomhi":        tuned(52, 11, 0.4, 1.3333, 1.12121, -250),
		"sc808_congalo":      tuned(52, 18, 0.15, 1.33333, 1.12121, -250),
		"sc808_congamid":     tuned(57, 9, 0.15, 1.33333, 1.12121, -250),
		"sc808_congahi":      tuned(65, 6, 0.15, 1.22972, 1.08108, -250),
	} {
		natives[name] = mk
	}
}

// silence is DetectSilence: it is done once the signal has stayed under 0.0001
// for 0.1 s.
type silence struct{ n, need int }

func newSilence(sr float64) *silence { return &silence{need: int(0.1 * sr)} }

func (s *silence) feed(x float64) bool {
	if math.Abs(x) > 0.0001 {
		s.n = 0
	} else {
		s.n++
	}
	return s.n >= s.need
}

// drum builds a drum voice: gen makes the next sample.
func drum(v *Voice, gen func(t float64) float64) *native {
	sil := newSilence(v.sr)
	n := 0
	var last float64
	return &native{
		own: true,
		next: func() float64 {
			t := float64(n) / v.sr
			n++
			last = gen(t)
			return last
		},
		done: func() bool { return sil.feed(last) },
	}
}

// env is a SuperCollider envelope with one curve (shape 5), given as the
// starting level, then (level, time) pairs.
func env(curve float64, init float64, pairs ...float64) *curveEnv {
	e := &curveEnv{lv: []float64{init}, shape: 5, curve: curve}
	for i := 0; i+1 < len(pairs); i += 2 {
		e.lv = append(e.lv, pairs[i])
		e.tm = append(e.tm, pairs[i+1])
	}
	return e
}

// bHiPass is SuperCollider's BHiPass (rq is the reciprocal of Q).
func bHiPass(freq, rq, sr float64) biq {
	w := 2 * math.Pi * math.Min(freq, sr*0.49) / sr
	cs, al := math.Cos(w), math.Sin(w)*0.5*rq
	var q biq
	q.set((1+cs)/2, -(1 + cs), (1+cs)/2, 1+al, -2*cs, 1-al)
	return q
}

// bBandPass is SuperCollider's BBandPass (bw is the width in octaves).
func bBandPass(freq, bw, sr float64) biq {
	w := 2 * math.Pi * freq / sr
	sn := math.Sin(w)
	al := sn * math.Sinh(0.34657359*bw*w/sn)
	var q biq
	q.set(al, 0, -al, 1+al, -2*math.Cos(w), 1-al)
	return q
}

// hiShelf is SuperCollider's BHiShelf.
func hiShelf(freq, rs, db, sr float64) biq {
	a := math.Pow(10, db/40)
	w := 2 * math.Pi * freq / sr
	cs, sn := math.Cos(w), math.Sin(w)
	al := sn / 2 * math.Sqrt((a+1/a)*(1/rs-1)+2)
	sa := 2 * math.Sqrt(a) * al
	var q biq
	q.set(a*((a+1)+(a-1)*cs+sa), -2*a*((a-1)+(a+1)*cs), a*((a+1)+(a-1)*cs-sa),
		(a+1)-(a-1)*cs+sa, 2*((a-1)-(a+1)*cs), (a+1)-(a-1)*cs-sa)
	return q
}

// lfPulse is SuperCollider's LFPulse: 1 while the phase is under the width.
type lfPulse struct{ p float64 }

func (l *lfPulse) at(hz, width, sr float64) float64 {
	out := 0.0
	if l.p < width {
		out = 1
	}
	l.p += hz / sr
	l.p -= math.Floor(l.p)
	return out
}

// the six square waves every 808 cymbal and hat is built from
var hatFreqs = [6]float64{203.52, 366.31, 301.77, 518.19, 811.16, 538.75}

type hatBank struct{ p [6]lfPulse }

func (h *hatBank) sum(sr float64) float64 {
	var s float64
	for i := range h.p {
		s += h.p[i].at(hatFreqs[i], 0.5, sr)
	}
	return s
}

func newBassdrum(v *Voice, o func(string, float64) float64) *native {
	f := midicps(o("note", 34))
	click, decay, dc := o("click", 0.11), o("decay", 2), o("decay_curve", -7)
	att := o("attenuation", 1)
	e1 := env(dc, click, 1, 0, 0, decay)
	e2 := env(-230, click, 0.6, 0, 0, decay)
	fe1 := env(-14, f*7, f*1.35, 0.05/att, f, 0.6)
	fe2 := env(-10, f*7, f*1.35, 0.03/att, f, 0.6)
	var p1, p2, p3 float64
	hp := highpass2(350, v.sr)
	lim := newNormalizer(0.5, 0.01, v.sr)
	lim.limit = true
	return drum(v, func(t float64) float64 {
		fa, fb := fe1.at(t), fe2.at(t)
		p1 += fa / v.sr
		p2 += fa / v.sr
		p3 += fb / v.sr
		a := e1.at(t)
		tri := lfTriAt(p2 + 1.5708/4)
		x := math.Cos(2*math.Pi*p1)*a + tri*e2.at(t)*0.05
		x += hp.run(math.Cos(2*math.Pi*p3) * a * 2)
		return lim.run(x*2.5) * 2
	})
}

func newSnare(v *Voice, o func(string, float64) float64) *native {
	note := o("note", 65)
	fa, fb := midicps(note+o("detune", -11)), midicps(note)
	mix := clamp(o("mix", 0.7), 0, 1)
	tm := clamp(0.1-o("click", 0.999)/10, 0, 0.1)
	decay, dc := o("decay", 4.2), o("decay_curve", -115)
	e1 := env(dc, 0, 1, tm, 0, decay)
	e2 := env(-95, 0, 1, tm, 0, 0.8)
	hpf, lpf := highpass2(midicps(o("hpf", 93)), v.sr), lowpass2(midicps(o("lpf", 121)), v.sr)
	head := highpass2(o("head_hpf", 30), v.sr)
	var pa, pb float64
	return drum(v, func(t float64) float64 {
		pa += fa / v.sr
		pb += fb / v.sr
		x := (math.Cos(2*math.Pi*pa)*0.6 + math.Cos(2*math.Pi*pb)*0.7) * e2.at(t) * 2.5 * (1 - mix)
		x += lpf.run(hpf.run(white(v))) * e1.at(t) * mix
		return head.run(x * 2)
	})
}

func newClap(v *Voice, o func(string, float64) float64) *native {
	click, decay, dc, rev := o("click", 0.5), o("decay", 0.3), o("decay_curve", -160), o("rev", 1)
	e1 := env(dc, click, 1, 0, 0, decay)
	e2 := env(dc, 0, 0, 0.026, 1, 0, 0, 6)
	e3 := env(-9, 0, 1, 0.1, 0, 4)
	hz, lz := midicps(o("hpf", 71)), midicps(o("lpf", 84))
	h1, h2 := highpass2(hz, v.sr), highpass2(hz, v.sr)
	l2 := lowpass2(lz, v.sr)
	var bp scBPF
	return drum(v, func(t float64) float64 {
		n2 := white(v) * e2.at(t)
		x := (white(v)*e1.at(t)*1.4 + n2)
		x = bp.run(h1.run(x), lz, 0.5, v.sr)
		tail := l2.run(h2.run(white(v)*e3.at(t)*0.02)) * rev
		return x*1.5 + tail
	})
}

func newClaves(v *Voice, o func(string, float64) float64) *native {
	f := midicps(o("note", 99))
	e := env(o("decay_curve", -20), o("click", 1), 1, 0, 0, o("decay", 0.1))
	var p float64
	return drum(v, func(t float64) float64 {
		p += f / v.sr
		return math.Cos(2*math.Pi*p) * e.at(t)
	})
}

func newClosedHihat(v *Voice, o func(string, float64) float64) *native {
	e := env(o("decay_curve", -30), 0, 1, 0.005, 0, o("decay", 0.42))
	var bank hatBank
	var bp scBPF
	hp := highpass2(midicps(o("hpf", 121.252)), v.sr)
	lz := midicps(o("lpf", 121.059))
	bb, bh := bBandPass(8900, 0.8, v.sr), bHiPass(9000, 0.3, v.sr)
	pk := peakEQ(9700, 0.8, 0.7, v.sr)
	return drum(v, func(t float64) float64 {
		s := bank.sum(v.sr)
		x := hp.run(bp.run(s, lz, 1, v.sr)) + bh.run(bb.run(s))
		return pk.run(x) * e.at(t) * 12
	})
}

func newOpenHihat(v *Voice, o func(string, float64) float64) *native {
	decay, dc := o("decay", 0.5), o("decay_curve", -3)
	e1 := env(dc, 0, 1, 0.1, 0, decay)
	e2 := env(-150, 0, 1, 0, 0, decay*5)
	var bank hatBank
	var bp scBPF
	sh := lowShelf(990, 2, -3, v.sr)
	pk := peakEQ(7200, 0.5, 5, v.sr)
	h1, h2 := bHiPass(8100, 0.83666, v.sr), bHiPass(8100, 0.83666, v.sr)
	hs := hiShelf(9400, 1, 5, v.sr)
	cz := midicps(o("hpf", 118.551))
	lp := lowpass2(midicps(o("lpf", 107.213)), v.sr)
	return drum(v, func(t float64) float64 {
		x := bank.sum(v.sr) * 0.6
		x = hs.run(h2.run(h1.run(pk.run(bp.run(sh.run(x), cz, 1, v.sr)))))
		return lp.run(x*e1.at(t)*0.6+x*e2.at(t)) * 7
	})
}

func newCowbell(v *Voice, o func(string, float64) float64) *native {
	note := o("note", 79.5898)
	fa, fb := midicps(note+o("detune", -7.08445)), midicps(note)
	e1 := env(o("decay_curve", -90), 0, 1, 0.01, 0, o("decay", 9.5))
	e2 := env(-215, 0, 1, 0, 0, 1)
	hp, lp := highpass2(midicps(o("hpf", 59)), v.sr), lowpass2(midicps(o("lpf", 109)), v.sr)
	var pa, pb lfPulse
	return drum(v, func(t float64) float64 {
		s := pb.at(fb, 0.5, v.sr) + pa.at(fa, 0.5, v.sr)
		return lp.run(hp.run((s*e2.at(t)*6+s*e1.at(t))/6)) * 1
	})
}

func newCymbal(v *Voice, o func(string, float64) float64) *native {
	decay, dc, tone := o("decay", 2), o("decay_curve", -3), o("tone", 0.25)
	e1 := env(dc, 0, 1, 0.3, 0, decay)
	e2 := env(-5, 0, 0.6, 0.1, 0, decay*0.7)
	e3 := env(-120, 0, 0.3, 0.1, 0, decay*20)
	e4 := env(-150, 0, 1, 0, 0, decay*5)
	var bank hatBank
	a1, a3, a4 := lowShelf(2000, 1, 5, v.sr), peakEQ(2400, 0.5, 5, v.sr), bHiPass(1550, 0.7, v.sr)
	a5 := lowpass2(3000, v.sr)
	var abp, bbp, cbp scBPF
	b1, b3 := lowShelf(990, 2, -5, v.sr), peakEQ(7200, 0.5, 5, v.sr)
	b4, b5 := bHiPass(6800, 0.83666, v.sr), bHiPass(6800, 0.83666, v.sr)
	b6 := hiShelf(10000, 1, -4, v.sr)
	c1, c3 := lowShelf(990, 2, -15, v.sr), peakEQ(7400, 0.35, 10, v.sr)
	c4, c5 := bHiPass(10500, 0.894427, v.sr), bHiPass(10500, 0.894427, v.sr)
	out := lowpass2(4000, v.sr)
	return drum(v, func(t float64) float64 {
		s := bank.sum(v.sr) * 0.6
		a := a5.run(a4.run(a3.run(abp.run(a1.run(s), 3000, 1, v.sr))))
		b := b6.run(b5.run(b4.run(b3.run(bbp.run(b1.run(s), 7400, 1, v.sr)))))
		c := c5.run(c4.run(c3.run(cbp.run(c1.run(s), 6500, 1, v.sr)))) * 2
		x := a*e1.at(t)*tone*0.008 + b*e2.at(t)*0.3 + b*e3.at(t)*0.6 + c*e4.at(t)
		return out.run(x) * 12
	})
}

func newMaracas(v *Voice, o func(string, float64) float64) *native {
	e := env(o("decay_curve", -250), 0.3, 1, o("click", 0.027), 0, o("decay", 0.07))
	hp := highpass2(midicps(o("hpf", 113)), v.sr)
	return drum(v, func(t float64) float64 { return hp.run(white(v) * e.at(t)) })
}

func newRimshot(v *Voice, o func(string, float64) float64) *native {
	note := o("note", 92)
	fa, fb := midicps(note+o("detune", -22))*1.1, midicps(note)*1.1
	e := env(o("decay_curve", -42), o("click", 1), 1, 0.00272, 0, o("decay", 0.07))
	pk := peakEQ(464, 0.44, 8, v.sr)
	hp, lp := highpass2(midicps(o("hpf", 63)), v.sr), lowpass2(midicps(o("lpf", 118)), v.sr)
	var pa lfPulse
	var pb float64
	return drum(v, func(t float64) float64 {
		a := e.at(t)
		pb += fb / v.sr
		x := pa.at(fa, 0.8, v.sr)*a + lfTriAt(pb+0.25)*a + white(v)*a*0.46
		return lp.run(hp.run(pk.run(x)))
	})
}

// tuned makes a tom or conga: a sine that bends down to its note.
func tuned(note, decay, click, d1, d2, dc float64) nativeMaker {
	return func(v *Voice, o func(string, float64) float64) *native {
		f := midicps(o("note", note))
		fe := env(-4, f*o("detune1", d1), f*o("detune2", d2), 0.1, f, 0.5)
		e := env(o("decay_curve", dc), o("click", click), 1, 0, 0, o("decay", decay))
		var p float64
		return drum(v, func(t float64) float64 {
			p += fe.at(t) / v.sr
			return math.Cos(2*math.Pi*p) * e.at(t)
		})
	}
}

// drumNotes are the notes the tuned 808 drums play when the code names none.
var drumNotes = map[string]float64{"sc808_bassdrum": 34, "sc808_snare": 65, "sc808_claves": 99, "sc808_cowbell": 79.5898,
	"sc808_rimshot": 92, "sc808_tomlo": 40, "sc808_tommid": 44, "sc808_tomhi": 52,
	"sc808_congalo": 52, "sc808_congamid": 57, "sc808_congahi": 65}

// DefaultNote is the note a synth plays when none is given, if its design has
// one of its own.
func DefaultNote(wave string) (float64, bool) { n, ok := drumNotes[wave]; return n, ok }
