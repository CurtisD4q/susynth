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
	if lookupSample(name, 0) != nil {
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
	Crush    float64 // bit depth 1..16, 0 = off
	Shape    float64 // distortion 0..1
	Delay    float64 // send to the delay bus 0..1
	Room     float64 // send to the reverb bus 0..1
	Release  float64 // tone release in seconds, 0 = default
	Attack   float64 // tone attack in seconds, 0 = a 3 ms click guard
	Note     float64 // exact MIDI note for tones (fractions allowed), 0 = from the word
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
)

// Voice is one sounding note. Create with New; read with Next.
type Voice struct {
	Track               int
	SendDelay, SendRoom float32 // bus send levels, read by the engine mixer

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
	rel       int
	relStart  int
	killed    bool
	killStart int
	done      bool
	wave      string
	ks        []float32
	ksPos     int
	variant   float64

	// per-voice effects and sample playback
	lpA, hpA, shape float32
	lp1, lp2, hp1   float32
	crush           float32
	ph              [5]float64
	smp             *Sample
	pos, step       float64
	att             int
}

// New builds a voice for a sound word. delay is the number of frames before
// it starts sounding; gate is how long a tone is held (frames).
func New(word string, sr, delay, gate int, p Params, seed uint32) (*Voice, error) {
	name, variant := split(word)
	v := &Voice{sr: float64(sr), delay: delay, gate: gate, rng: seed*2654435761 + 12345, wave: p.Wave}
	v.speed = p.Speed
	if v.speed <= 0 {
		v.speed = 1
	}
	g := math.Max(0, math.Min(p.Gain, 4))
	pan := math.Max(-1, math.Min(1, p.Pan))
	ang := (pan + 1) * math.Pi / 4
	v.gl = float32(g * math.Cos(ang))
	v.gr = float32(g * math.Sin(ang))
	v.variant = 1 + 0.06*float64(variant)
	v.SendDelay, v.SendRoom = float32(p.Delay), float32(p.Room)
	cut := func(hz float64) float32 {
		hz = math.Max(20, math.Min(hz, float64(sr)*0.45))
		return float32(1 - math.Exp(-2*math.Pi*hz/float64(sr)))
	}
	if p.LPF > 0 {
		v.lpA = cut(p.LPF)
	}
	if p.HPF > 0 {
		v.hpA = cut(p.HPF)
	}
	if p.Crush > 0 {
		v.crush = float32(math.Pow(2, math.Max(1, math.Min(16, p.Crush))-1))
	}
	v.shape = float32(math.Max(0, math.Min(1, p.Shape)))

	if sm := lookupSample(name, variant); sm != nil {
		v.k = kSample
		v.smp = sm
		v.step = float64(sm.Rate) / float64(sr) * v.speed
		return v, nil
	}

	if d, ok := drumAlias[strings.ToLower(name)]; ok {
		v.k = map[string]kind{"bd": kBD, "sn": kSN, "cp": kCP, "hh": kHH, "oh": kOH, "rim": kRim, "tom": kTom, "cb": kCB}[d]
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
	v.att = int(0.003 * v.sr)
	if p.Attack > 0.003 {
		v.att = int(math.Min(p.Attack, 8) * v.sr)
	}
	if v.wave == "" {
		v.wave = "saw"
	}
	if v.gate < int(0.005*v.sr) {
		v.gate = int(0.005 * v.sr)
	}
	v.rel = int(0.08 * v.sr)
	if p.Release > 0 {
		v.rel = int(math.Max(0.005, math.Min(p.Release, 8)) * v.sr)
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
	if v.delay > 0 {
		v.delay--
		return 0, 0
	}
	s := v.sample()
	v.t++
	if v.lpA > 0 {
		v.lp1 += v.lpA * (s - v.lp1)
		v.lp2 += v.lpA * (v.lp1 - v.lp2)
		s = v.lp2
	}
	if v.hpA > 0 {
		v.hp1 += v.hpA * (s - v.hp1)
		s -= v.hp1
	}
	if v.crush > 0 {
		s = float32(math.Round(float64(s*v.crush))) / v.crush
	}
	if v.shape > 0 {
		s = float32(math.Tanh(float64(s)*(1+float64(v.shape)*10))) * 0.8
	}
	if v.killed {
		fade := 1 - float32(v.t-v.killStart)/(0.005*float32(v.sr))
		if fade <= 0 {
			v.done = true
			return 0, 0
		}
		s *= fade
	}
	return s * v.gl, s * v.gr
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
	if v.k == kSample {
		i := int(v.pos)
		if i+1 >= len(v.smp.Data) {
			v.done = true
			return 0
		}
		f := float32(v.pos - float64(i))
		out := v.smp.Data[i]*(1-f) + v.smp.Data[i+1]*f
		v.pos += v.step
		return out * 0.9
	}
	return v.tone()
}

func (v *Voice) tone() float32 {
	age := v.t
	// envelope: 3 ms attack, hold for gate, linear release.
	env := float32(1)
	if a := v.att; a > 0 && age < a {
		env = float32(age) / float32(a)
	}
	if age >= v.gate {
		r := float32(age-v.gate) / float32(v.rel)
		if r >= 1 {
			v.done = true
			return 0
		}
		env *= 1 - r
	}
	var s float32
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
		idx := 4 * float64(exp(float64(age)/v.sr*3))
		v.phase2 += v.freq * 2 * 2 * math.Pi / v.sr
		v.phase += v.freq*2*math.Pi/v.sr + 0
		s = float32(math.Sin(v.phase + idx*math.Sin(v.phase2)))
	case "supersaw":
		var acc float32
		for k, d := range [5]float64{-0.012, -0.006, 0, 0.006, 0.012} {
			v.ph[k] += v.freq * (1 + d) / v.sr
			v.ph[k] -= math.Floor(v.ph[k])
			acc += float32(2*v.ph[k] - 1)
		}
		v.lp += 0.3 * (acc/5 - v.lp)
		s = v.lp * 1.4
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
	default: // saw
		v.phase += v.freq / v.sr
		v.phase -= math.Floor(v.phase)
		raw := float32(2*v.phase - 1)
		v.lp += 0.3 * (raw - v.lp)
		s = v.lp
	}
	return s * env * 0.35
}
