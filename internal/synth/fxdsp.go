package synth

import (
	"math"
	"sort"
)

// Effects as Sonic Pi's compiled synthdefs make them. Each one takes the
// stereo sound its unit gathered (already scaled by pre_mix) and gives back
// its own sound; the unit around it does pre_amp, pre_mix, the equal-power
// mix with the unprocessed sound, and amp, as Sonic Pi's def-fx wrapper does.

// FX is one effect. Time options reach it in seconds.
type FX interface {
	// Set gives it options (at creation, and again by control): only the
	// options present change.
	Set(o map[string]float64)
	// Process writes the effect's sound for the interleaved stereo block in to
	// out (which it overwrites).
	Process(out, in []float32)
	// KillDelay is how long the effect lives on after its sounds are gone, in seconds.
	KillDelay() float64
}

type fxMaker func(sr float64) FX

var fxMakers = map[string]fxMaker{}

// fxDefaultMix is the mix of effects that override Sonic Pi's default of 1.
var fxDefaultMix = map[string]float64{"reverb": 0.4}

// NewFX makes the effect name at sample rate sr, or nil.
func NewFX(name string, sr float64, o map[string]float64) FX {
	mk, ok := fxMakers[name]
	if !ok {
		return nil
	}
	f := mk(sr)
	f.Set(o)
	return f
}

// FXUnits lists the effects that run as units, sorted.
func FXUnits() []string {
	out := make([]string, 0, len(fxMakers))
	for n := range fxMakers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// FXDefaultMix is the mix an effect has unless the program says.
func FXDefaultMix(name string) float64 {
	if m, ok := fxDefaultMix[name]; ok {
		return m
	}
	return 1
}

// upd sets *p to o[k] when the option is there.
func upd(o map[string]float64, k string, p *float64) {
	if v, ok := o[k]; ok {
		*p = v
	}
}

// ---- shared pieces ----

// scHPF / scLPF: SuperCollider's 2-pole Butterworth filters.
type butter struct{ x1, x2, y1, y2 float64 }

func (b *butter) low(x, freq, sr float64) float64 {
	pf := math.Min(freq, sr*0.49) * math.Pi / sr
	c := 1 / math.Tan(pf)
	c2 := c * c
	s := math.Sqrt2 * c
	a0 := 1 / (1 + s + c2)
	b1 := 2 * (1 - c2) * a0
	b2 := (1 - s + c2) * a0
	y := a0*(x+2*b.x1+b.x2) - b1*b.y1 - b2*b.y2
	b.x2, b.x1, b.y2, b.y1 = b.x1, x, b.y1, y
	return b.fin(y)
}

func (b *butter) high(x, freq, sr float64) float64 {
	pf := math.Min(freq, sr*0.49) * math.Pi / sr
	c := math.Tan(pf)
	c2 := c * c
	s := math.Sqrt2 * c
	a0 := 1 / (1 + s + c2)
	b1 := 2 * (c2 - 1) * a0
	b2 := (1 - s + c2) * a0
	y := a0*(x-2*b.x1+b.x2) - b1*b.y1 - b2*b.y2
	b.x2, b.x1, b.y2, b.y1 = b.x1, x, b.y1, y
	return b.fin(y)
}

func (b *butter) fin(y float64) float64 {
	if math.IsNaN(y) || math.IsInf(y, 0) {
		*b = butter{}
		return 0
	}
	return y
}

// scRHPF is SuperCollider's RHPF.
type scRHPF struct{ y1, y2 float64 }

func (f *scRHPF) run(in, freq, rq, sr float64) float64 {
	pf := math.Min(freq, sr*0.49) * 2 * math.Pi / sr
	q := math.Max(0.001, rq)
	d := math.Tan(pf * q * 0.5)
	c := (1 - d) / (1 + d)
	b1 := (1 + c) * math.Cos(pf)
	b2 := -c
	a0 := (1 + c + b1) * 0.25
	y0 := a0*in + b1*f.y1 + b2*f.y2
	out := y0 - 2*f.y1 + f.y2
	if math.IsNaN(out) || math.IsInf(out, 0) {
		*f = scRHPF{}
		return 0
	}
	f.y2, f.y1 = f.y1, y0
	return out
}

// scResonz is SuperCollider's Resonz.
type scResonz struct{ y1, y2 float64 }

func (f *scResonz) run(in, freq, rq, sr float64) float64 {
	pf := math.Min(freq, sr*0.49) * 2 * math.Pi / sr
	b := pf * math.Max(rq, 0.001)
	r := 1 - b*0.5
	twoR := 2 * r
	r2 := r * r
	cost := twoR * math.Cos(pf) / (1 + r2)
	b1 := twoR * cost
	b2 := -r2
	a0 := (1 - r2) * 0.5
	y0 := in + b1*f.y1 + b2*f.y2
	out := a0 * (y0 - f.y2)
	if math.IsNaN(out) || math.IsInf(out, 0) {
		*f = scResonz{}
		return 0
	}
	f.y2, f.y1 = f.y1, y0
	return out
}

// combN is SuperCollider's CombN: a feedback comb with no interpolation.
type combN struct {
	buf []float64
	w   int
}

func newCombN(maxSec, sr float64) *combN {
	n := 1
	for float64(n) < maxSec*sr+1 {
		n <<= 1
	}
	return &combN{buf: make([]float64, n)}
}

func (c *combN) run(x, delaySec, decay, sr float64) float64 {
	d := math.Max(1, math.Min(delaySec*sr, float64(len(c.buf)-1)))
	r := (c.w - int(d)) & (len(c.buf) - 1)
	y := c.buf[r]
	fb := 0.0
	if decay != 0 && delaySec > 0 {
		fb = math.Copysign(math.Pow(0.001, delaySec/math.Abs(decay)), decay)
	}
	c.buf[c.w] = x + fb*y
	c.w = (c.w + 1) & (len(c.buf) - 1)
	return y
}

// balance2 is SuperCollider's Balance2 for one stereo pair: equal power.
func balance2(l, r, pos float64) (float64, float64) {
	p := clamp(pos, -1, 1)*0.5 + 0.5
	return l * math.Cos(p*math.Pi/2), r * math.Sin(p*math.Pi/2)
}

// stereo runs f over an interleaved block, a channel at a time.
func stereo(out, in []float32, f func(ch int, x float64) float64) {
	for i := 0; i+1 < len(in); i += 2 {
		out[i] = float32(f(0, float64(in[i])))
		out[i+1] = float32(f(1, float64(in[i+1])))
	}
}

// ---- reverb ----

type fvStereo struct {
	comb [2][8]fvComb
	allp [2][4]fvAllp
	mix  float64
	room float64
	damp float64
	fb   float64
	d    float64
}

type reverbFX struct{ z fvStereo }

func init() {
	fxMakers["reverb"] = func(sr float64) FX {
		r := &reverbFX{}
		r.z.mix, r.z.room, r.z.damp = 0.4, 0.6, 0.5
		k := 1.0 // SuperCollider's FreeVerb2 does not scale its delay lengths with the sample rate
		_ = sr
		for ch := 0; ch < 2; ch++ {
			for i, n := range [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617} {
				r.z.comb[ch][i].buf = make([]float64, int(float64(n+23*ch)*k))
			}
			for i, n := range [4]int{556, 441, 341, 225} {
				r.z.allp[ch][i].buf = make([]float64, int(float64(n+23*ch)*k))
			}
		}
		return r
	}
}

func (r *reverbFX) Set(o map[string]float64) {
	upd(o, "mix", &r.z.mix)
	upd(o, "room", &r.z.room)
	upd(o, "damp", &r.z.damp)
	r.z.fb = clamp(r.z.room, 0, 1)*0.28 + 0.7
	r.z.d = clamp(r.z.damp, 0, 1) * 0.4
}

func (r *reverbFX) KillDelay() float64 { return math.Min(r.z.room*10+1, 11) }

func (r *reverbFX) Process(out, in []float32) {
	z := &r.z
	mix := clamp(z.mix, 0, 1)
	for i := 0; i+1 < len(in); i += 2 {
		inL, inR := float64(in[i]), float64(in[i+1])
		x := (inL + inR) * 0.015
		var o [2]float64
		for ch := 0; ch < 2; ch++ {
			var sum float64
			for k := range z.comb[ch] {
				c := &z.comb[ch][k]
				y := c.buf[c.i]
				c.store = y*(1-z.d) + c.store*z.d
				c.buf[c.i] = x + c.store*z.fb
				if c.i++; c.i == len(c.buf) {
					c.i = 0
				}
				sum += y
			}
			for k := range z.allp[ch] {
				a := &z.allp[ch][k]
				b := a.buf[a.i]
				y := -sum + b
				a.buf[a.i] = sum + b*0.5
				if a.i++; a.i == len(a.buf) {
					a.i = 0
				}
				sum = y
			}
			o[ch] = sum
		}
		out[i] = float32(inL*(1-mix) + o[0]*mix)
		out[i+1] = float32(inR*(1-mix) + o[1]*mix)
	}
}

// ---- echo and ping_pong ----

type echoFX struct {
	sr                float64
	phase, decay, max float64
	comb              [2]*combN
}

func init() {
	fxMakers["echo"] = func(sr float64) FX { return &echoFX{sr: sr, phase: 0.25, decay: 2, max: 2} }
}

func (e *echoFX) Set(o map[string]float64) {
	upd(o, "phase", &e.phase)
	upd(o, "decay", &e.decay)
	if v, ok := o["max_phase"]; ok {
		e.max = v
	}
	if e.comb[0] == nil {
		e.max = math.Max(e.max, e.phase)
		e.comb = [2]*combN{newCombN(e.max, e.sr), newCombN(e.max, e.sr)}
	}
}
func (e *echoFX) KillDelay() float64 { return e.decay }
func (e *echoFX) Process(out, in []float32) {
	stereo(out, in, func(ch int, x float64) float64 {
		return x + e.comb[ch].run(x, e.phase, e.decay, e.sr)
	})
}

type pingPongFX struct {
	sr               float64
	phase, fb, start float64
	max              float64
	bufL, bufR       []float32
	w                int
}

func init() {
	fxMakers["ping_pong"] = func(sr float64) FX {
		return &pingPongFX{sr: sr, phase: 0.25, fb: 0.5, start: 1, max: 1}
	}
}

func (p *pingPongFX) Set(o map[string]float64) {
	upd(o, "phase", &p.phase)
	upd(o, "feedback", &p.fb)
	upd(o, "pan_start", &p.start)
	upd(o, "max_phase", &p.max)
	if p.bufL == nil {
		p.max = math.Max(p.max, p.phase)
		n := int(p.sr*p.max) + 2
		p.bufL, p.bufR = make([]float32, n), make([]float32, n)
	}
}

func (p *pingPongFX) KillDelay() float64 {
	fb := clamp(p.fb, 0.01, 0.99)
	return math.Log(0.01) / math.Log(fb) * p.phase
}

func (p *pingPongFX) Process(out, in []float32) {
	n := len(p.bufL)
	d := int(clamp(p.phase*p.sr, 1, float64(n-1)))
	fb := float32(clamp(p.fb, -1, 1))
	for i := 0; i+1 < len(in); i += 2 {
		l, r := balance2(float64(in[i]), float64(in[i+1]), -clamp(p.start, -1, 1))
		rd := (p.w - d + n) % n
		dl, dr := p.bufL[rd], p.bufR[rd]
		// each side's echo goes to the other
		p.bufL[p.w] = float32(r) + fb*dr
		p.bufR[p.w] = float32(l) + fb*dl
		p.w = (p.w + 1) % n
		out[i] = dl + in[i]
		out[i+1] = dr + in[i+1]
	}
}
