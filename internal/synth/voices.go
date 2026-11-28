// Package synth contains the built-in drum and tone voices. Everything is
// synthesised: no samples, so no sample licences.
package synth

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var drumAlias = map[string]string{
	"bd": "bd", "kick": "bd",
	"sn": "sn", "snare": "sn",
	"cp": "cp", "clap": "cp",
	"hh": "hh", "hat": "hh", "ch": "hh",
	"oh": "oh", "openhat": "oh",
	"rim": "rim",
	"tom": "tom",
	"cb":  "cb", "cowbell": "cb",
	"clave": "clave", "claves": "clave",
	"conga": "conga",
	"cym":   "cym", "cymbal": "cym",
	"maracas": "maracas",
}

var noteBase = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}

// Drums lists the canonical drum names.
var Drums = []string{"bd", "sn", "cp", "hh", "oh", "rim", "tom", "cb"}

// Waves lists the tone voices usable with "# voice <name>".
var Waves = []string{"sine", "tri", "saw", "square", "pluck", "fm", "supersaw", "organ", "noise"}

// ParseNote turns "c4", "fs3", "eb2", "a#5" or "60" into a MIDI note number.
func ParseNote(w string) (int, bool) {
	w = strings.ToLower(w)
	if n, err := strconv.Atoi(w); err == nil {
		return n, n >= 0 && n <= 127
	}
	if len(w) < 2 {
		return 0, false
	}
	base, ok := noteBase[w[0]]
	if !ok {
		return 0, false
	}
	i := 1
	switch w[i] {
	case 's', '#':
		base++
		i++
	case 'b', 'f':
		base--
		i++
	}
	oct, err := strconv.Atoi(w[i:])
	if err != nil || oct < -1 || oct > 9 {
		return 0, false
	}
	n := base + (oct+1)*12
	return n, n >= 0 && n <= 127
}

// split removes a ":n" variant suffix.
func split(word string) (string, int) {
	if i := strings.IndexByte(word, ':'); i > 0 {
		v, _ := strconv.Atoi(word[i+1:])
		return word[:i], v
	}
	return word, 0
}

// Known reports whether the engine can play the word, for eval-time errors.
func Known(word string) error {
	name, _ := split(word)
	if findSample(name, 0) != nil {
		return nil
	}
	if _, ok := drumAlias[strings.ToLower(name)]; ok {
		return nil
	}
	if _, ok := ParseNote(name); ok {
		return nil
	}
	return fmt.Errorf("unknown sound %q (try bd sn cp hh oh rim tom cb a note like c3, or a sample bank)", word)
}

// Params shape one voice.
type Params struct {
	Gain  float64 // 0..2, default 1
	Pan   float64 // -1..1
	Speed float64 // pitch/time multiplier, default 1
	Wave  string  // tone voice for notes

	LPF, HPF float64 // filter cutoffs in Hz, 0 = off
	Res      float64 // low-pass resonance 0..1 (res:)
	HPRes    float64 // high-pass resonance 0..1 (with_fx :rhpf)
	Crush    float64 // bit depth 1..16, 0 = off
	Shape    float64 // distortion 0..1
	Delay    float64 // send to the delay bus 0..1
	Room     float64 // send to the reverb bus 0..1
	Chorus   float64 // send to the chorus bus 0..1

	// Comp and EQ route the note's track through the compressor or EQ.
	Comp, EQ bool
	Release  float64 // tone release in seconds, 0 = default
	Attack   float64 // tone attack in seconds, 0 = a 3 ms click guard
	Decay    float64 // tone decay in seconds (the gate holds attack+decay+sustain)

	// AttackLevel, DecayLevel and SustainLevel are the envelope's levels;
	// 0 means the default (see EnvTimes).
	AttackLevel, DecayLevel, SustainLevel float64
	// EnvShape and EnvCurve are the envelope's SuperCollider shape (see
	// EnvFor); 0 keeps the older mixed shape.
	EnvShape int
	EnvCurve float64

	// CutoffMin turns on the filter envelope (tb303): the low-pass cutoff
	// moves between CutoffMin and LPF (Hz) as CutoffEnv goes from 0 to 1.
	CutoffMin float64
	CutoffEnv EnvTimes
	Note      float64 // exact MIDI note for tones (fractions allowed), 0 = from the word

	// Samples: Start and Finish pick the part to play (fractions of the
	// sample; Finish 0 means the end, Finish before Start plays backwards),
	// and Reverse plays it backwards (a negative rate:).
	Start, Finish float64
	Reverse       bool
	// Held is how long (seconds) a sample plays before its release, when
	// sustain: was given; 0 plays to the end.
	Held float64

	// Opt holds synth-specific options by key (synthopts.go); a missing key
	// is the synth's default.
	Opt map[string]float64

	// Mods are the with_fx effects that run inside the voice (mods.go),
	// innermost first.
	Mods []Mod
}

type kind int

const (
	kBD kind = iota
	kSN
	kCP
	kHH
	kOH
	kRim
	kTom
	kCB
	kTone
	kSample
	kClave
	kConga
	kCym
	kMaracas
)

// Voice is one sounding note. Create with New; read with Next.
type Voice struct {
	Track               int
	ID                  int64   // the Ruby node that started it, for control (0: none)
	Bus                 int64   // the with_fx unit it plays into (0: its track)
	SendDelay, SendRoom float32 // bus send levels, read by the engine mixer
	SendChorus          float32
	Gain                float32 // the track gain the engine mixer last applied

	k         kind
	sr        float64
	delay     int
	t         int
	gl, gr    float32
	speed     float64
	phase     float64
	phase2    float64
	freq      float64
	rng       uint32
	prev      float32
	lp        float32
	gate      int
	env       Envelope
	fenv      Envelope // the filter envelope, when cutoffMin > 0
	cutoffMin float64
	cutoffMax float64
	res       float64
	killed    bool
	killStart int
	done      bool
	wave      string
	ks        []float32
	ksPos     int
	variant   float64

	// per-voice effects and sample playback
	shape     float32
	fl        [2]filterState // left (or mono), right
	crush     float32
	ph        [6]float64
	smp       *Sample
	chL, chR  []float32 // the sample's channels at the octave being played
	stereo    bool
	right     float32 // the right channel of the current stereo frame
	pos, step float64
	begin     int     // the first frame of the part being played
	end       int     // one past its last frame
	sAtk      float64 // sample envelope (frames): attack, release, held length (0: to the end)
	sRel      float64
	sGate     float64
	sEnv      bool

	// control (control.go): nil until a control touches the voice
	amp0, pan0, lpf0, res0 float64
	ctl                    *controlled
	pend                   []pendingCtl
	mods                   []*modState
	ext                    *toneState // tones.go
	so                     synthOpts  // synthopts.go
	nat                    *native    // native.go: a Sonic Pi design ported as it is
	trim                   float32    // this wave's level against Sonic Pi's (waveTrim)
}

type filterState struct{ lp, hp Filter }

// fastStep is the fastest a sample is read before playback moves to a
// half-rate octave copy. Reading at up to 1.2x keeps whatever could fold back
// above ~20.6 kHz, and moving down an octave still leaves at least ~13.7 kHz
// of the sample's top end, whatever the speed.
const fastStep = 1.2

// New builds a voice for a sound word. delay is the number of frames before
// it starts sounding; gate is how long a tone is held (frames).
func New(word string, sr, delay, gate int, p Params, seed uint32) (*Voice, error) {
	name, variant := split(word)
	v := &Voice{sr: float64(sr), delay: delay, gate: gate, rng: seed*2654435761 + 12345, wave: p.Wave}
	v.speed = p.Speed
	v.trim = 1
	if x, ok := waveTrim[p.Wave]; ok {
		v.trim = x
	}
	if v.speed <= 0 {
		v.speed = 1
	}
	g := math.Max(0, math.Min(p.Gain, 4))
	pan := math.Max(-1, math.Min(1, p.Pan))
	ang := (pan + 1) * math.Pi / 4
	v.gl = float32(g * math.Cos(ang))
	v.gr = float32(g * math.Sin(ang))
	v.amp0, v.pan0, v.lpf0, v.res0 = g, pan, p.LPF, p.Res
	v.variant = 1 + 0.06*float64(variant)
	v.SendDelay, v.SendRoom, v.SendChorus = float32(p.Delay), float32(p.Room), float32(p.Chorus)
	for ch := range v.fl {
		if p.LPF > 0 {
			v.fl[ch].lp = NewLowPass(p.LPF, p.Res, v.sr)
		}
		if p.HPF > 0 {
			v.fl[ch].hp = NewHighPass(p.HPF, p.HPRes, v.sr)
		}
	}
	if p.Crush > 0 {
		v.crush = float32(math.Pow(2, math.Max(1, math.Min(16, p.Crush))-1))
	}
	v.shape = float32(math.Max(0, math.Min(1, p.Shape)))
	if len(p.Mods) > 0 {
		v.AddMods(p.Mods)
	}

	if sm := lookupSample(name, variant); sm != nil {
		v.k = kSample
		v.smp = sm
		v.chL, v.chR = sm.Data, sm.Right
		v.step = float64(sm.Rate) / float64(sr) * v.speed
		for _, o := range sm.oct { // fast playback reads a band-limited copy
			if v.step <= fastStep {
				break
			}
			v.chL, v.chR = o.l, o.r
			v.step = float64(o.rate) / float64(sr) * v.speed
		}
		v.stereo = v.chR != nil
		n := len(v.chL)
		st, fi := p.Start, p.Finish
		if fi <= 0 {
			fi = 1
		}
		rev := p.Reverse
		if fi < st {
			st, fi, rev = fi, st, !rev
		}
		st, fi = math.Max(0, math.Min(1, st)), math.Max(0, math.Min(1, fi))
		v.begin, v.end = int(st*float64(n)), int(math.Ceil(fi*float64(n)))
		if v.end > n {
			v.end = n
		}
		v.pos = float64(v.begin)
		if rev {
			v.step = -v.step
			v.pos = float64(v.end - 2)
		}
		if p.Attack > 0 || p.Release > 0 || p.Held > 0 {
			v.sEnv = true
			v.sAtk, v.sRel, v.sGate = p.Attack*v.sr, p.Release*v.sr, p.Held*v.sr
		}
		return v, nil
	}

	if d, ok := drumAlias[strings.ToLower(name)]; ok {
		v.k = map[string]kind{"bd": kBD, "sn": kSN, "cp": kCP, "hh": kHH, "oh": kOH, "rim": kRim, "tom": kTom, "cb": kCB,
			"clave": kClave, "conga": kConga, "cym": kCym, "maracas": kMaracas}[d]
		return v, nil
	}
	n, ok := ParseNote(name)
	if !ok {
		return nil, fmt.Errorf("unknown sound %q", word)
	}
	v.k = kTone
	note := float64(n)
	if p.Note > 0 {
		note = p.Note
	}
	v.freq = 440 * math.Pow(2, (note-69)/12) * v.speed
	v.initOpts(p.Opt)
	if mk, ok := natives[v.wave]; ok {
		v.nat = mk(v, func(k string, d float64) float64 {
			if x, ok := p.Opt[k]; ok {
				return x
			}
			return d
		})
		v.stereo = v.nat.lr != nil
	}
	if v.wave == "" {
		v.wave = "saw"
	}
	rel := 0.08
	if p.Release > 0 {
		rel = p.Release
	}
	// the gate holds attack + decay + sustain; sustain is what is left
	sus := math.Max(0, float64(v.gate)/v.sr-math.Max(p.Attack, minAttack)-p.Decay)
	v.env = NewEnvelope(v.sr, EnvTimes{Attack: p.Attack, Decay: p.Decay, Sustain: sus, Release: rel,
		AttackLevel: p.AttackLevel, DecayLevel: p.DecayLevel, SustainLevel: p.SustainLevel,
		Shape: p.EnvShape, Curve: p.EnvCurve})
	if p.CutoffMin > 0 && p.LPF > p.CutoffMin {
		v.fenv = NewEnvelope(v.sr, p.CutoffEnv)
		v.cutoffMin, v.cutoffMax, v.res = p.CutoffMin, p.LPF, p.Res
		v.sweep()
	}
	if v.nat != nil && v.nat.done != nil { // a drum rings by itself until it goes quiet
		v.env = NewEnvelope(v.sr, EnvTimes{Sustain: 30, Release: 0.005, Shape: 1})
	}
	if v.wave == "pluck" {
		n := int(v.sr / v.freq)
		if n < 2 {
			n = 2
		}
		v.ks = make([]float32, n)
		for i := range v.ks {
			v.ks[i] = v.noise()
		}
	}
	return v, nil
}

func (v *Voice) noise() float32 {
	v.rng ^= v.rng << 13
	v.rng ^= v.rng >> 17
	v.rng ^= v.rng << 5
	return float32(v.rng)/float32(math.MaxUint32)*2 - 1
}

// Kill fades the voice out over a few milliseconds (hush / stop).
func (v *Voice) Kill() {
	if !v.killed {
		v.killed = true
		v.killStart = v.t
	}
}

// Done reports that the voice has finished and can be dropped.
func (v *Voice) Done() bool { return v.done }

// Next returns the next stereo frame.
func (v *Voice) Next() (float32, float32) {
	if v.pend != nil || v.ctl != nil {
		v.stepCtl()
	}
	if v.delay > 0 {
		v.delay--
		return 0, 0
	}
	s := v.sample()
	v.t++
	s = v.effects(s, &v.fl[0])
	var r float32
	if v.stereo {
		r = v.effects(v.right, &v.fl[1])
	}
	if v.killed {
		fade := 1 - float32(v.t-v.killStart)/(0.005*float32(v.sr))
		if fade <= 0 {
			v.done = true
			return 0, 0
		}
		s *= fade
		r *= fade
	}
	if v.stereo { // pan works as balance
		if v.mods != nil {
			return v.runMods(s*v.gl, r*v.gr)
		}
		return s * v.gl, r * v.gr
	}
	if v.mods != nil {
		return v.runMods(s*v.gl, s*v.gr)
	}
	return s * v.gl, s * v.gr
}

// effects runs the per-voice filters, crush and shape on one channel.
func (v *Voice) effects(s float32, f *filterState) float32 {
	if f.lp.Active() {
		s = f.lp.Process(s)
	}
	if f.hp.Active() {
		s = f.hp.Process(s)
	}
	if v.crush > 0 {
		s = float32(math.Round(float64(s*v.crush))) / v.crush
	}
	if v.shape > 0 {
		s = float32(math.Tanh(float64(s)*(1+float64(v.shape)*10))) * 0.8
	}
	return s
}

func exp(x float64) float32 { return float32(math.Exp(-x)) }

func (v *Voice) sample() float32 {
	tau := float64(v.t) / v.sr * v.speed // time in seconds, scaled by speed
	dt := 2 * math.Pi / v.sr
	switch v.k {
	case kBD:
		f := (45 + 120*math.Exp(-tau*28)) * v.variant * v.speed
		v.phase += f * dt
		s := float32(math.Sin(v.phase)) * exp(tau*8) * 0.95
		s += v.noise() * exp(tau*400) * 0.25
		if tau > 0.6 {
			v.done = true
		}
		return s
	case kSN:
		f := 185 * v.variant * v.speed
		v.phase += f * dt
		n := v.noise()
		hp := n - v.prev*0.6
		v.prev = n
		s := float32(math.Sin(v.phase))*exp(tau*28)*0.45 + hp*exp(tau*20)*0.6
		if tau > 0.4 {
			v.done = true
		}
		return s
	case kCP:
		n := v.noise()
		hp := n - v.prev*0.8
		v.prev = n
		env := float32(0)
		for _, b := range []float64{0, 0.011, 0.022} {
			if tau >= b {
				env += exp((tau-b)*160) * 0.5
			}
		}
		if tau >= 0.033 {
			env += exp((tau-0.033)*22) * 0.5
		}
		if tau > 0.45 {
			v.done = true
		}
		return hp * env * 0.9
	case kHH, kOH:
		n := v.noise()
		hp := n - v.prev
		v.prev = n
		decay, end := 75.0, 0.14
		if v.k == kOH {
			decay, end = 9.0, 0.7
		}
		if tau > end {
			v.done = true
		}
		return hp * 0.5 * exp(tau*decay)
	case kRim:
		v.phase += 1700 * v.speed * dt
		s := float32(math.Sin(v.phase))*exp(tau*110)*0.6 + v.noise()*exp(tau*300)*0.3
		if tau > 0.12 {
			v.done = true
		}
		return s
	case kTom:
		f := (90 + 90*math.Exp(-tau*12)) * v.variant * v.speed
		v.phase += f * dt
		if tau > 0.6 {
			v.done = true
		}
		return float32(math.Sin(v.phase)) * exp(tau*7) * 0.8
	case kCB:
		v.phase += 540 * v.speed * dt
		v.phase2 += 800 * v.speed * dt
		sq := func(p float64) float32 {
			if math.Sin(p) >= 0 {
				return 1
			}
			return -1
		}
		if tau > 0.45 {
			v.done = true
		}
		return (sq(v.phase) + sq(v.phase2)) * 0.2 * exp(tau*14)
	}
	switch v.k {
	case kClave: // the 808's claves: a short, bright wooden ring
		v.phase += 2500 * v.speed * dt
		if tau > 0.08 {
			v.done = true
		}
		return float32(math.Sin(v.phase)) * exp(tau*60) * 0.55
	case kConga: // a tuned skin that bends down a little as it rings
		f := 200 * v.variant * v.variant * (1 + 0.15*math.Exp(-tau*40)) * v.speed
		v.phase += f * dt
		if tau > 0.5 {
			v.done = true
		}
		return float32(math.Sin(v.phase)) * exp(tau*11) * 0.75
	case kCym: // six detuned square partials, high-passed: the 808's metal
		var m float32
		for k, fr := range [6]float64{205.3, 304.4, 369.6, 522.7, 540, 800} {
			v.ph[k] += fr * 1.7 * v.speed / v.sr
			v.ph[k] -= math.Floor(v.ph[k])
			if v.ph[k] < 0.5 {
				m++
			} else {
				m--
			}
		}
		hp := m - v.prev
		v.prev = m
		if tau > 1.6 {
			v.done = true
		}
		return hp * 0.12 * exp(tau*2.4)
	case kMaracas: // a quick burst of bright noise
		n := v.noise()
		hp := n - v.prev
		v.prev = n
		if tau > 0.06 {
			v.done = true
		}
		return hp * 0.4 * exp(tau*70)
	}
	if v.k == kSample {
		i := int(v.pos)
		if i+1 >= v.end || i < v.begin || v.pos < 0 {
			v.done = true
			return 0
		}
		var l, r float32
		if v.step == 1 { // a loaded sample at normal speed: no interpolation
			l = v.chL[i]
			if v.stereo {
				r = v.chR[i]
			}
		} else {
			f := float32(v.pos - float64(i))
			l = interpolate(v.chL, i, f)
			if v.stereo {
				r = interpolate(v.chR, i, f)
			}
		}
		v.pos += v.step
		if v.sEnv {
			e := v.sampleEnv()
			if e <= 0 && float64(v.t) > v.sAtk {
				v.done = true
			}
			l, r = l*e, r*e
		}
		v.right = r * ToneGain
		return l * ToneGain
	}
	return v.tone()
}

// ToneGain is the level every voice is played at. The engine trims each voice
// by 0.8 and the master stage by 28800/32768, so this makes a note come out
// at the level Sonic Pi's synth or sample has before its mixer (a full-scale
// sample peaks at full scale).
// waveTrim brings the modelled waves to the level of the real synth (measured
// by rendering both): the ported designs need none.
var waveTrim = map[string]float32{"blade": 0.324, "hollow": 0.188, "ambience": 0.069,
	"tri": 1.44, "dtri": 1.72, "fm": 1.33, "pluck": 1.4, "saw": 0.785, "prophet": 0.725, "square": 1.17, "dsaw": 1.25,
	"bell": 0.83, "dullbell": 0.83}

const ToneGain = 1 / (0.8 * 28800.0 / 32768)

// sampleEnv is the sample's own envelope at this frame: a linear attack, then
// a linear release either after the held length or ending with the sample.
func (v *Voice) sampleEnv() float32 {
	t := float64(v.t)
	e := 1.0
	if v.sAtk > 0 && t < v.sAtk {
		e = t / v.sAtk
	}
	if v.sGate > 0 {
		if t > v.sGate {
			if v.sRel <= 0 {
				return 0
			}
			e *= 1 - (t-v.sGate)/v.sRel
		}
	} else if v.sRel > 0 {
		left := float64(v.end-v.begin) / math.Abs(v.step)
		if rem := left - t; rem < v.sRel {
			e *= math.Max(0, rem/v.sRel)
		}
	}
	return float32(math.Max(0, e))
}

// polyBLEP smooths the jump of a ramp that wraps at phase 0, spreading it
// over the samples either side of the wrap so it has (almost) no energy above
// the Nyquist frequency to fold back as aliasing. Subtract it from 2t-1.
func polyBLEP(t, dt float64) float64 {
	dt = math.Min(math.Abs(dt), 0.5)
	switch {
	case dt == 0:
		return 0
	case t < dt:
		x := t / dt
		return x + x - x*x - 1
	case t > 1-dt:
		x := (t - 1) / dt
		return x*x + x + x + 1
	}
	return 0
}

// sweepEvery is how often (samples) the filter envelope moves the cutoff.
const sweepEvery = 16

// sweep sets the low-pass cutoff from the filter envelope: between the
// minimum and maximum cutoff, evenly in pitch.
func (v *Voice) sweep() {
	var e float64
	for i := 0; i < sweepEvery; i++ {
		e = float64(v.fenv.Next())
	}
	hz := v.cutoffMin * math.Pow(v.cutoffMax/v.cutoffMin, e)
	for ch := range v.fl {
		v.fl[ch].lp.Set(hz, v.res, v.sr)
		v.fl[ch].lp.Snap()
	}
}

func (v *Voice) tone() float32 {
	if v.so.mod == nil {
		return v.toneBody()
	}
	base := v.freq
	v.freq = base * math.Pow(2, v.so.mod.semis(float64(v.t)/v.sr)/12)
	s := v.toneBody()
	v.freq = base
	return s
}

func (v *Voice) toneBody() float32 {
	if v.env.Done() {
		v.done = true
		return 0
	}
	env := v.env.Next()
	if v.cutoffMin > 0 && v.t%sweepEvery == 0 && v.t > 0 {
		v.sweep()
	}
	if v.nat != nil {
		if v.nat.own {
			env = 1
		}
		if v.nat.lr != nil {
			l, r := v.nat.lr()
			v.right = float32(r) * env * ToneGain
			return float32(l) * env * ToneGain
		}
		x := v.nat.next()
		if v.nat.done != nil && v.nat.done() {
			v.done = true
		}
		return float32(x) * env * ToneGain
	}
	var s float32
	if extWaves[v.wave] {
		return v.toneExt() * env * ToneGain * v.trim * float32(v.so.velGain(v.wave))
	}
	switch v.wave {
	case "sine":
		v.phase += v.freq * 2 * math.Pi / v.sr
		s = float32(math.Sin(v.phase))
	case "tri":
		v.phase += v.freq / v.sr
		v.phase -= math.Floor(v.phase)
		s = float32(4*math.Abs(v.phase-0.5) - 1)
	case "square":
		v.phase += v.freq / v.sr
		v.phase -= math.Floor(v.phase)
		if v.phase < 0.5 {
			s = 0.6
		} else {
			s = -0.6
		}
		v.lp += 0.35 * (s - v.lp)
		s = v.lp
	case "fm":
		// Sonic Pi's fm: the modulator is the note divided by divisor, and it
		// swings the carrier by depth times the note's own frequency, shaped
		// by the envelope, which is an index of depth*divisor
		idx := v.so.depth * v.so.divisor * float64(env)
		v.phase2 += v.freq / v.so.divisor * 2 * math.Pi / v.sr
		v.phase += v.freq * 2 * math.Pi / v.sr
		s = float32(math.Sin(v.phase+idx*math.Sin(v.phase2))) * 0.8
	case "organ":
		v.phase += v.freq * 2 * math.Pi / v.sr
		for k, w := range [5]float64{1, 0.6, 0.4, 0.3, 0.2} {
			h := float64([5]int{1, 2, 3, 4, 6}[k])
			s += float32(w * math.Sin(v.phase*h))
		}
		s *= 0.4
	case "noise":
		a := float32(math.Min(1, v.freq*2*math.Pi/v.sr*4))
		v.lp += a * (v.noise() - v.lp)
		s = v.lp * 2.5
	case "pluck":
		i := v.ksPos
		j := (i + 1) % len(v.ks)
		s = v.ks[i]
		v.ks[i] = 0.5 * (v.ks[i] + v.ks[j]) * 0.997
		v.ksPos = j
	default: // saw, band-limited so high notes do not alias
		dt := v.freq / v.sr
		v.phase += dt
		v.phase -= math.Floor(v.phase)
		raw := float32(2*v.phase - 1 - polyBLEP(v.phase, dt))
		v.lp += 0.3 * (raw - v.lp)
		s = v.lp
	}
	return s * env * ToneGain * v.trim
}
