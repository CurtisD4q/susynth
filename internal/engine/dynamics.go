package engine

import "math"

// Opt-in dynamics and colour: a compressor and a three-band EQ that tracks
// are routed through (with_fx :compressor / :eq around their code), and a
// stereo chorus fed by a send (with_fx :chorus). A set that uses none of
// them never touches them.

// ---- compressor ----------------------------------------------------------------

// CompSettings are the compressor's controls, named as in Sonic Pi.
type CompSettings struct {
	Threshold  float64 // level (mix units, 1 = full) where compression starts
	SlopeAbove float64 // output change per input change above it: 0.5 is 2:1
	Clamp      float64 // attack, seconds
	Relax      float64 // release, seconds
}

// DefaultComp is threshold 0.2, 2:1, 10 ms attack, 100 ms release.
var DefaultComp = CompSettings{Threshold: 0.2, SlopeAbove: 0.5, Clamp: 0.01, Relax: 0.1}

// compKnee is the width of the soft knee in dB: compression eases in
// across it instead of switching on at the threshold.
const compKnee = 6.0

// compressor is stereo-linked (both channels get the same gain, so the
// stereo image holds) and feed-forward. A peak detector (instant rise, slow
// fall) follows the level, so the gain does not wobble with each cycle of
// the waveform; the gain itself then moves at the attack and release times.
type compressor struct {
	set CompSettings
	env float64 // detected level
	gr  float64 // gain reduction in dB (<= 0), smoothed
}

// compHold is how slowly the detected level falls between peaks.
const compHold = 0.05

// curve is the static gain change in dB for an input level in dB.
func (c *compressor) curve(in float64) float64 {
	th := 20 * math.Log10(math.Max(c.set.Threshold, 1e-6))
	slope := math.Max(0.01, math.Min(c.set.SlopeAbove, 1))
	over := in - th
	switch {
	case over <= -compKnee/2:
		return 0
	case over >= compKnee/2:
		return over * (slope - 1)
	}
	x := over + compKnee/2 // inside the knee: a smooth quadratic blend
	return (slope - 1) * x * x / (2 * compKnee)
}

func (c *compressor) process(buf []float32) {
	att := math.Exp(-1 / (math.Max(c.set.Clamp, 1e-4) * SampleRate))
	rel := math.Exp(-1 / (math.Max(c.set.Relax, 1e-3) * SampleRate))
	hold := math.Exp(-1 / (compHold * SampleRate))
	for i := 0; i < len(buf); i += 2 {
		peak := math.Max(math.Abs(float64(buf[i])), math.Abs(float64(buf[i+1])))
		if c.env = c.env * hold; peak > c.env {
			c.env = peak
		}
		want := c.curve(20 * math.Log10(c.env+1e-12))
		k := rel
		if want < c.gr { // more reduction needed: the attack
			k = att
		}
		c.gr = want + (c.gr-want)*k
		g := float32(math.Pow(10, c.gr/20))
		buf[i] *= g
		buf[i+1] *= g
	}
}

// ---- EQ -------------------------------------------------------------------------

// EQSettings are the three bands: gains in dB, frequencies in Hz.
type EQSettings struct {
	LowDB, LowHz       float64 // low shelf
	MidDB, MidHz, MidQ float64 // bell
	HighDB, HighHz     float64 // high shelf
}

// DefaultEQ is flat, with the bands at 100 Hz, 1 kHz (Q 0.6) and 6 kHz.
var DefaultEQ = EQSettings{LowHz: 100, MidHz: 1000, MidQ: 0.6, HighHz: 6000}

// band is a zero-delay-feedback state-variable filter mixed into a shelf or
// bell (Simper's forms): stable, and its settings can change while it plays.
type band struct {
	g, k, m0, m1, m2 float32
	s                [2][2]float32 // channel, integrator
}

func (b *band) run(x float32, ch int) float32 {
	s := &b.s[ch]
	a1 := 1 / (1 + b.g*(b.g+b.k))
	a2 := b.g * a1
	a3 := b.g * a2
	v3 := x - s[1]
	v1 := a1*s[0] + a2*v3
	v2 := s[1] + a2*s[0] + a3*v3
	s[0] = 2*v1 - s[0] + flush - flush
	s[1] = 2*v2 - s[1] + flush - flush
	return b.m0*x + b.m1*v1 + b.m2*v2
}

func (b *band) shelf(low bool, db, hz float64) {
	a := math.Pow(10, db/40)
	g := math.Tan(math.Pi * math.Min(hz, SampleRate*0.45) / SampleRate)
	k := math.Sqrt2 // Q 0.707: no bump at the corner
	if low {
		b.g, b.k = float32(g/math.Sqrt(a)), float32(k)
		b.m0, b.m1, b.m2 = 1, float32(k*(a-1)), float32(a*a-1)
	} else {
		b.g, b.k = float32(g*math.Sqrt(a)), float32(k)
		b.m0, b.m1, b.m2 = float32(a*a), float32(k*(1-a)*a), float32(1-a*a)
	}
}

func (b *band) bell(db, hz, q float64) {
	a := math.Pow(10, db/40)
	b.g = float32(math.Tan(math.Pi * math.Min(hz, SampleRate*0.45) / SampleRate))
	b.k = float32(1 / (math.Max(q, 0.1) * a))
	b.m0, b.m1, b.m2 = 1, float32(1/(math.Max(q, 0.1)*a)*(a*a-1)), 0
}

type eq struct {
	set  EQSettings // aimed at
	now  EQSettings // gliding there, block by block
	b    [3]band
	init bool
}

// eqGlide is how far the gains move toward new settings each block.
const eqGlide = 0.3

func (q *eq) process(buf []float32) {
	if !q.init {
		q.now, q.init = q.set, true
	}
	glide := func(a *float64, b float64) {
		if *a += (b - *a) * eqGlide; math.Abs(b-*a) < 1e-3 {
			*a = b
		}
	}
	glide(&q.now.LowDB, q.set.LowDB)
	glide(&q.now.MidDB, q.set.MidDB)
	glide(&q.now.HighDB, q.set.HighDB)
	q.now.LowHz, q.now.MidHz, q.now.MidQ, q.now.HighHz = q.set.LowHz, q.set.MidHz, q.set.MidQ, q.set.HighHz
	q.b[0].shelf(true, q.now.LowDB, q.now.LowHz)
	q.b[1].bell(q.now.MidDB, q.now.MidHz, q.now.MidQ)
	q.b[2].shelf(false, q.now.HighDB, q.now.HighHz)
	for i := 0; i < len(buf); i += 2 {
		for ch := 0; ch < 2; ch++ {
			x := buf[i+ch]
			for k := range q.b {
				x = q.b[k].run(x, ch)
			}
			buf[i+ch] = x
		}
	}
}

// ---- chorus ---------------------------------------------------------------------

// ChorusSettings are the chorus's speed and depth.
type ChorusSettings struct {
	Rate  float64 // Hz
	Depth float64 // ms the voices drift either side of their centre
}

// DefaultChorus drifts 2.5 ms at 0.6 Hz.
var DefaultChorus = ChorusSettings{Rate: 0.6, Depth: 2.5}

const (
	chorusCentre = 0.015 // s: the voices sit ~15 ms behind the sound
	chorusBuf    = 4096
)

// chorus is two drifting copies per side, read from a short delay with
// Hermite interpolation; left and right drift out of phase, so the copies
// spread the sound wide. Only the copies come out (it is a send).
type chorus struct {
	set      ChorusSettings
	buf      [2][chorusBuf]float32
	w        int
	ph       float64 // LFO phase, radians
	silentIn int
	idle     bool
}

func (c *chorus) tap(ch int, d float64) float32 {
	p := float64(c.w) - d
	i := int(math.Floor(p))
	f := float32(p - float64(i))
	at := func(k int) float32 { return c.buf[ch][k&(chorusBuf-1)] }
	x0, x1, x2, x3 := at(i-1), at(i), at(i+1), at(i+2)
	c1 := 0.5 * (x2 - x0)
	c2 := x0 - 2.5*x1 + 2*x2 - 0.5*x3
	c3 := 0.5*(x3-x0) + 1.5*(x1-x2)
	return ((c3*f+c2)*f+c1)*f + x1
}

func (c *chorus) process(out, in []float32) {
	quiet := true
	for _, v := range in {
		if v != 0 {
			quiet = false
			break
		}
	}
	if quiet && c.idle {
		return
	}
	if quiet {
		c.silentIn += len(in) / 2
		if c.silentIn > chorusBuf {
			c.idle = true
			c.buf = [2][chorusBuf]float32{}
			return
		}
	} else {
		c.silentIn, c.idle = 0, false
	}
	rate := 2 * math.Pi * math.Max(0.01, math.Min(c.set.Rate, 10)) / SampleRate
	depth := math.Max(0, math.Min(c.set.Depth, 10)) / 1000 * SampleRate
	centre := chorusCentre * SampleRate
	for s := 0; s < len(out)/2; s++ {
		c.buf[0][c.w&(chorusBuf-1)] = in[s*2]
		c.buf[1][c.w&(chorusBuf-1)] = in[s*2+1]
		c.ph += rate
		if c.ph > 2*math.Pi {
			c.ph -= 2 * math.Pi
		}
		for ch := 0; ch < 2; ch++ {
			off := float64(ch) * math.Pi / 2 // left and right a quarter turn apart
			a := c.tap(ch, centre+depth*math.Sin(c.ph+off))
			b := c.tap(ch, centre*1.37+depth*math.Sin(c.ph+off+math.Pi))
			out[s*2+ch] += (a + b) * 0.5
		}
		c.w++
	}
}
