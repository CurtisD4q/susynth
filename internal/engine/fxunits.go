package engine

import "math"

// The first effect units: the reverb and the two echoes, as the shared
// effects of before made into units.

func init() {
	fxMakers["reverb"] = newReverbUnit
	fxMakers["echo"] = newEchoUnit
	fxMakers["ping_pong"] = newPingPongUnit
}

type reverbUnit struct {
	r         *reverb
	mix, room float64
}

func newReverbUnit(o map[string]float64, _ float64) fxProc {
	u := &reverbUnit{r: newReverb()}
	u.set(o)
	u.r.size, u.r.damp = -1, -1 // so the first set takes
	u.r.set(opt(o, "room", 0.6), opt(o, "damp", 0.5))
	for i := range u.r.lines {
		u.r.lines[i].g, u.r.lines[i].a = u.r.lines[i].gT, u.r.lines[i].aT
	}
	return u
}

func (u *reverbUnit) set(o map[string]float64) {
	u.mix, u.room = opt(o, "mix", 0.4), opt(o, "room", 0.6)
	u.r.set(u.room, opt(o, "damp", 0.5))
}
func (u *reverbUnit) levels() (float32, float32) { return float32(1 - u.mix), float32(u.mix) }
func (u *reverbUnit) process(out, in []float32)  { u.r.process(out, in) }
func (u *reverbUnit) killDelay() float64         { return math.Min(u.room*10+1, 11) }

type echoUnit struct {
	e                 *echo
	mix, phase, decay float64
	bpm               float64
	pingPong          bool
	feedback          float64
	hasFeedback       bool
}

func newEchoUnit(o map[string]float64, bpm float64) fxProc {
	u := &echoUnit{e: newEcho(), bpm: bpm}
	u.set(o)
	u.e.d, u.e.fb = u.e.dT, u.e.fbT
	return u
}

func newPingPongUnit(o map[string]float64, bpm float64) fxProc {
	u := &echoUnit{e: newEcho(), bpm: bpm, pingPong: true}
	u.set(o)
	u.e.d, u.e.fb = u.e.dT, u.e.fbT
	return u
}

// set reads the options; time options arrive in seconds (the engine scales
// beats before it calls).
func (u *echoUnit) set(o map[string]float64) {
	u.mix = opt(o, "mix", 1)
	u.phase = math.Max(opt(o, "phase", 0.25*60/u.bpm), 0.001)
	u.decay = math.Max(opt(o, "decay", 2*60/u.bpm), 0.01)
	u.hasFeedback = false
	fb := pow10(-3 * u.phase / u.decay)
	if u.pingPong {
		fb = 0.5
	}
	if v, ok := o["feedback"]; ok {
		fb = v
	}
	u.feedback = fb
	u.e.set(u.phase, fb, u.pingPong)
}
func (u *echoUnit) levels() (float32, float32) { return 1, float32(u.mix) }
func (u *echoUnit) process(out, in []float32)  { u.e.process(out, in) }
func (u *echoUnit) killDelay() float64 {
	if u.pingPong {
		fb := math.Min(math.Max(u.feedback, 0.01), 0.99)
		return math.Log(0.01) / math.Log(fb) * u.phase
	}
	return u.decay
}
