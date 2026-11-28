package synth

import "math"

// Envelope is Sonic Pi's ADSR: the level rises over attack to attack_level,
// moves over decay to decay_level, over sustain to sustain_level, and over
// release back to silence. Every move ends exactly on its level, so the note
// ends at exactly zero. EnvTimes.Shape picks the curve of the moves the way
// Sonic Pi's env_curve: does; without it (the older voices) the attack is a
// straight ramp and the other moves are curved like natural decays.
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
	shape    int     // SuperCollider's: 1 linear, 2 exponential, 3 sine, 4 welch, 5 custom, 6 squared, 7 cubed
	curve    float64 // for shape 5
	old      bool    // the older curved fade (shape 0 voices), kept bit for bit
	k        float64 // per-sample factor of its exponential
}

// envCurve shapes the curved segments of the older voices: like
// SuperCollider's curve -4.
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
	// Shape is SuperCollider's envelope shape for every move (1 linear, 2
	// exponential, 3 sine, 4 welch, 5 custom with Curve, 6 squared, 7
	// cubed); 0 is the older mix of a straight attack and curved moves.
	Shape int
	Curve float64
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
	shape := func(i int) (int, float64) {
		if e.Shape != 0 {
			return e.Shape, e.Curve
		}
		if i == 1 || i == 3 {
			return 5, envCurve
		}
		return 1, 0
	}
	env.seg = [4]segment{
		{n: n(math.Max(e.Attack, minAttack)), from: 0, to: al},
		{n: n(e.Decay), from: al, to: dl},
		{n: n(e.Sustain), from: dl, to: sl},
		{n: n(math.Max(e.Release, minRelease)), from: sl, to: 0},
	}
	for i := range env.seg {
		s := &env.seg[i]
		s.shape, s.curve = shape(i)
		if e.Shape == 0 && (i == 1 || i == 3) && s.n > 0 {
			s.old, s.k = true, math.Exp(envCurve/float64(s.n))
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
	if s.old {
		v = s.from + (s.to-s.from)*(1-e.m)/(1-math.Exp(envCurve))
		e.m *= s.k
	} else {
		v = shapeAt(s.shape, s.curve, s.from, s.to, float64(e.t)/float64(s.n))
	}
	if e.t++; e.t >= s.n {
		e.i++
		e.start()
	}
	return float32(v)
}

// shapeAt is the level a SuperCollider envelope segment has pos (0..1) of the
// way from one level to the next.
func shapeAt(shape int, curve, from, to, pos float64) float64 {
	switch shape {
	case 2: // exponential
		a, b := math.Max(from, 1e-4), math.Max(to, 1e-4)
		return a * math.Pow(b/a, pos)
	case 3: // sine
		return from + (to-from)*(0.5-0.5*math.Cos(math.Pi*pos))
	case 4: // welch
		if from < to {
			return from + (to-from)*math.Sin(math.Pi/2*pos)
		}
		return to - (to-from)*math.Sin(math.Pi/2*(1-pos))
	case 5: // custom curve
		if math.Abs(curve) < 0.001 {
			return from + (to-from)*pos
		}
		return from + (to-from)*(1-math.Exp(curve*pos))/(1-math.Exp(curve))
	case 6: // squared
		a, b := math.Sqrt(math.Max(from, 0)), math.Sqrt(math.Max(to, 0))
		x := a + (b-a)*pos
		return x * x
	case 7: // cubed
		a, b := math.Cbrt(math.Max(from, 0)), math.Cbrt(math.Max(to, 0))
		x := a + (b-a)*pos
		return x * x * x
	}
	return from + (to-from)*pos
}
