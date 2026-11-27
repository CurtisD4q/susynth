package synth

import "math"

// Envelope is Sonic Pi's ADSR: the level rises over attack to attack_level,
// moves over decay to decay_level, over sustain to sustain_level, and over
// release back to silence. The attack is a straight ramp; the other moves
// are curved like natural decays (fast at first, then easing in), so a
// release sounds like a fade, not a cut. Every move ends exactly on its
// level, so the note ends at exactly zero.
type Envelope struct {
	seg  [4]segment
	i    int // segment playing
	t    int // samples into it
	m    float64
	done bool
}

type segment struct {
	n        int
	from, to float64
	curved   bool
	k        float64 // per-sample factor of the curve's exponential
}

// envCurve shapes the curved segments: like SuperCollider's curve -4.
const envCurve = -4.0

// Shortest attack and release: anything quicker clicks.
const (
	minAttack  = 0.003
	minRelease = 0.005
)

// EnvTimes are an envelope's times in seconds and its levels. A level of 0
// means its default: 1 for attack and sustain, and the sustain level for
// decay (as in Sonic Pi). Use a tiny value for a level that really is 0.
type EnvTimes struct {
	Attack, Decay, Sustain, Release       float64
	AttackLevel, DecayLevel, SustainLevel float64
}

// NewEnvelope builds an envelope at sample rate sr.
func NewEnvelope(sr float64, e EnvTimes) Envelope {
	al, sl := orOne(e.AttackLevel), orOne(e.SustainLevel)
	dl := e.DecayLevel
	if dl == 0 {
		dl = sl
	}
	n := func(s float64) int { return int(math.Round(math.Max(0, math.Min(s, 60)) * sr)) }
	var env Envelope
	env.seg = [4]segment{
		{n: n(math.Max(e.Attack, minAttack)), from: 0, to: al},
		{n: n(e.Decay), from: al, to: dl, curved: true},
		{n: n(e.Sustain), from: dl, to: sl},
		{n: n(math.Max(e.Release, minRelease)), from: sl, to: 0, curved: true},
	}
	for i := range env.seg {
		s := &env.seg[i]
		if s.n > 0 {
			s.k = math.Exp(envCurve / float64(s.n))
		}
	}
	env.start()
	return env
}

func orOne(v float64) float64 {
	if v == 0 {
		return 1
	}
	return math.Max(0, v)
}

// start moves to the next segment with any length.
func (e *Envelope) start() {
	for e.i < len(e.seg) && e.seg[e.i].n == 0 {
		e.i++
	}
	e.t, e.m = 0, 1
	if e.i == len(e.seg) {
		e.done = true
	}
}

// Length is the envelope's whole length in samples.
func (e *Envelope) Length() int {
	n := 0
	for _, s := range e.seg {
		n += s.n
	}
	return n
}

// Done reports that the release has finished.
func (e *Envelope) Done() bool { return e.done }

// Next returns the level for this sample and moves on one.
func (e *Envelope) Next() float32 {
	if e.done {
		return 0
	}
	s := &e.seg[e.i]
	var v float64
	if s.curved {
		// SuperCollider's curve: from + (to-from) * (1-e^(c*p)) / (1-e^c)
		v = s.from + (s.to-s.from)*(1-e.m)/(1-math.Exp(envCurve))
		e.m *= s.k
	} else {
		v = s.from + (s.to-s.from)*float64(e.t)/float64(s.n)
	}
	if e.t++; e.t >= s.n {
		e.i++
		e.start()
	}
	return float32(v)
}
