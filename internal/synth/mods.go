package synth

import "math"

// Per-voice effects for Sonic Pi's with_fx effects that change over time or
// colour the sound: slicer, panslicer, tremolo, wobble, ixi_techno, flanger,
// the band-pass filters, band_eq, ring_mod, octaver, vowel and mono.
//
// susynth folds with_fx into each note, so these run inside every voice the
// effect covers. A beat-synced one (slicer, wobble…) reads its LFO from the
// effect's own start time (Origin), so every note under one with_fx moves in
// step, as if the effect were a single bus.

// ModKind is which effect a Mod is.
type ModKind int

const (
	ModNone ModKind = iota
	ModSlicer
	ModPanSlicer
	ModTremolo
	ModWobble // also ixi_techno, with a sine wave
	ModFlanger
	ModBandPass
	ModBandEQ
	ModRing
	ModOctaver
	ModVowel
	ModMono
)

// LFO waves, numbered as Sonic Pi numbers them.
const (
	WaveSaw = iota
	WavePulse
	WaveTri
	WaveSine
)

// Mod is one effect on a voice. Times are in seconds, notes are MIDI.
type Mod struct {
	Kind    ModKind
	Mix     float64 // 0 dry .. 1 all effect
	Phase   float64 // LFO cycle length
	Offset  float64 // LFO phase offset, 0..1
	Origin  float64 // how long the effect had run when the voice started
	Wave    int
	Width   float64 // pulse width, 0..1
	Invert  bool
	Smooth  float64 // how long the LFO takes to move (slicer edges)
	Prob    float64 // chance a slicer slice is skipped (goes to ProbPos)
	ProbAt  float64
	Seed    float64
	Min     float64 // the range the LFO moves over: amp, pan or cutoff
	Max     float64
	Res     float64
	Freq    float64 // centre / ring note
	DB      float64
	Depth   float64 // flanger depth and delay (ms), tremolo depth
	Delay   float64
	FB      float64
	A, B, C float64 // octaver: super, sub, subsub amps; vowel: sound, voice
}

type modState struct {
	m      Mod
	smooth float64
	lp, hp [2]Filter
	bq     [2][3]biquad // band_eq (one), vowel (three formants)
	gains  [3]float64
	line   [2][]float32 // flanger
	w      int
	flip   [2]int // octaver
	last   [2]float32
	dc     [2]float32
	lastP  int64
	skip   bool
}

// AddMods gives a voice its with_fx effects, innermost first.
func (v *Voice) AddMods(ms []Mod) {
	for _, m := range ms {
		st := &modState{m: m, lastP: -1}
		switch m.Kind {
		case ModWobble:
			for c := range st.lp {
				st.lp[c] = NewLowPass(midiToHz(m.Min), m.Res*0.9, v.sr)
			}
		case ModBandPass:
			centre := midiToHz(m.Freq)
			// res 0 is a wide band, 1 a narrow ringing one
			width := math.Pow(2, 1.6*(1-m.Res)+0.15)
			for c := range st.lp {
				st.lp[c] = NewLowPass(math.Min(centre*width, v.sr*0.45), m.Res*0.6, v.sr)
				st.hp[c] = NewHighPass(math.Max(centre/width, 10), m.Res*0.6, v.sr)
			}
			st.gains[0] = 1 + m.Res*1.5 // make up the level the band loses
		case ModBandEQ:
			for c := range st.bq {
				st.bq[c][0] = peaking(midiToHz(m.Freq), m.Res, m.DB, v.sr)
			}
		case ModFlanger:
			n := int(0.025*v.sr) + 2
			st.line = [2][]float32{make([]float32, n), make([]float32, n)}
		case ModVowel:
			f := vowelFormants(int(m.A), int(m.B))
			for c := range st.bq {
				for k := 0; k < 3; k++ {
					st.bq[c][k] = bandpass(f[k][0], f[k][1], v.sr)
				}
			}
			st.gains = [3]float64{1, f[1][2], f[2][2]}
		}
		v.mods = append(v.mods, st)
	}
}

func midiToHz(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

// lfo is the effect's LFO, 0..1, at this frame of the voice.
func (st *modState) lfo(t float64) float64 {
	m := &st.m
	ph := m.Phase
	if ph <= 0 {
		ph = 0.25
	}
	x := (t+m.Origin)/ph + m.Offset
	cycle := int64(math.Floor(x))
	pos := x - math.Floor(x)
	var y float64
	// the shapes and starting points of Sonic Pi's fx LFOs (fx.clj)
	switch m.Wave {
	case WaveSaw: // falls, starting half way down
		p := pos + 0.5
		y = 1 - (p - math.Floor(p))
	case WavePulse:
		if pos < m.Width {
			y = 1
		}
	case WaveTri: // starts at the top
		y = math.Abs(2*pos - 1)
	default: // sine, starting at the top
		y = 0.5 + 0.5*math.Cos(2*math.Pi*pos)
	}
	if m.Invert {
		y = 1 - y
	}
	if m.Prob > 0 {
		if cycle != st.lastP {
			st.lastP = cycle
			st.skip = hash01(cycle, m.Seed) < m.Prob
		}
		if st.skip {
			y = m.ProbAt
		}
	}
	return y
}

func hash01(n int64, seed float64) float64 {
	x := uint64(n)*0x9E3779B97F4A7C15 ^ math.Float64bits(seed)
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 29
	return float64(x>>11) / float64(1<<53)
}

// runMods runs the effects on one stereo frame.
func (v *Voice) runMods(l, r float32) (float32, float32) {
	t := float64(v.t) / v.sr
	for _, st := range v.mods {
		m := &st.m
		dl, dr := l, r
		switch m.Kind {
		case ModSlicer, ModTremolo:
			y := st.lfo(t)
			if m.Smooth > 0 {
				st.smooth += (y - st.smooth) * math.Min(1, 1/(m.Smooth*v.sr))
				y = st.smooth
			}
			g := float32(m.Min + (m.Max-m.Min)*y)
			l, r = l*g, r*g
		case ModPanSlicer:
			y := st.lfo(t)
			if m.Smooth > 0 {
				st.smooth += (y - st.smooth) * math.Min(1, 1/(m.Smooth*v.sr))
				y = st.smooth
			}
			pan := math.Max(-1, math.Min(1, m.Min+(m.Max-m.Min)*y))
			ang := (pan + 1) * math.Pi / 4
			mono := (l + r) * 0.5
			l, r = mono*float32(math.Cos(ang)*math.Sqrt2), mono*float32(math.Sin(ang)*math.Sqrt2)
		case ModWobble:
			if v.t%sweepEvery == 0 {
				hz := midiToHz(m.Min + (m.Max-m.Min)*st.lfo(t))
				for c := range st.lp {
					st.lp[c].Set(math.Min(hz, v.sr*0.45), m.Res*0.9, v.sr)
				}
			}
			l, r = st.lp[0].Process(l), st.lp[1].Process(r)
		case ModBandPass:
			g := float32(st.gains[0])
			l = st.hp[0].Process(st.lp[0].Process(l)) * g
			r = st.hp[1].Process(st.lp[1].Process(r)) * g
		case ModBandEQ:
			l, r = st.bq[0][0].run(l), st.bq[1][0].run(r)
		case ModFlanger:
			n := len(st.line[0])
			d := (m.Delay + m.Depth*st.lfo(t)) / 1000 * v.sr
			d = math.Max(1, math.Min(float64(n-2), d))
			out := [2]float32{}
			in := [2]float32{l, r}
			for c := 0; c < 2; c++ {
				pos := float64(st.w) - d
				for pos < 0 {
					pos += float64(n)
				}
				i := int(pos)
				f := float32(pos - float64(i))
				y := st.line[c][i]*(1-f) + st.line[c][(i+1)%n]*f
				st.line[c][st.w] = in[c] + y*float32(m.FB)
				out[c] = (in[c] + y) * 0.7
			}
			st.w = (st.w + 1) % n
			l, r = out[0], out[1]
		case ModRing:
			mod := float32(math.Sin(2 * math.Pi * midiToHz(m.Freq) * (t + m.Origin)))
			l, r = l*mod, r*mod
		case ModOctaver:
			in := [2]float32{l, r}
			out := [2]float32{}
			for c := 0; c < 2; c++ {
				x := in[c]
				if st.last[c] < 0 && x >= 0 {
					st.flip[c]++
				}
				st.last[c] = x
				up := float32(math.Abs(float64(x)))
				st.dc[c] += (up - st.dc[c]) * 0.001
				up -= st.dc[c]
				sub, subsub := up, up
				if st.flip[c]&1 == 1 {
					sub = -sub
				}
				if st.flip[c]&2 == 2 {
					subsub = -subsub
				}
				out[c] = x + float32(m.A)*up + float32(m.B)*sub + float32(m.C)*subsub
				out[c] *= 0.5
			}
			l, r = out[0], out[1]
		case ModVowel:
			in := [2]float32{l, r}
			out := [2]float32{}
			for c := 0; c < 2; c++ {
				for k := 0; k < 3; k++ {
					out[c] += st.bq[c][k].run(in[c]) * float32(st.gains[k])
				}
				out[c] *= 4 // the formants pass a small part of the sound
			}
			l, r = out[0], out[1]
		case ModMono:
			mono := (l + r) * 0.5
			l, r = mono, mono
		}
		if m.Mix < 1 {
			mx := float32(math.Max(0, m.Mix))
			l, r = dl+(l-dl)*mx, dr+(r-dr)*mx
		}
	}
	return l, r
}

// biquad is a direct-form-I second-order filter (RBJ cookbook).
type biquad struct {
	b0, b1, b2, a1, a2 float32
	x1, x2, y1, y2     float32
}

func (q *biquad) run(x float32) float32 {
	y := q.b0*x + q.b1*q.x1 + q.b2*q.x2 - q.a1*q.y1 - q.a2*q.y2
	if y > -1e-20 && y < 1e-20 {
		y = 0
	}
	q.x2, q.x1, q.y2, q.y1 = q.x1, x, q.y1, y
	return y
}

func norm(b0, b1, b2, a0, a1, a2 float64) biquad {
	return biquad{b0: float32(b0 / a0), b1: float32(b1 / a0), b2: float32(b2 / a0), a1: float32(a1 / a0), a2: float32(a2 / a0)}
}

// peaking boosts or cuts db around hz; res 0..1 narrows it.
func peaking(hz, res, db, sr float64) biquad {
	hz = math.Max(20, math.Min(hz, sr*0.45))
	q := 0.5 + res*8
	a := math.Pow(10, db/40)
	w := 2 * math.Pi * hz / sr
	al := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	return norm(1+al*a, -2*c, 1-al*a, 1+al/a, -2*c, 1-al/a)
}

// bandpass passes a band of bw Hz around hz at unity gain.
func bandpass(hz, bw, sr float64) biquad {
	hz = math.Max(20, math.Min(hz, sr*0.45))
	q := math.Max(0.5, hz/math.Max(bw, 1))
	w := 2 * math.Pi * hz / sr
	al := math.Sin(w) / (2 * q)
	c := math.Cos(w)
	return norm(al, 0, -al, 1+al, -2*c, 1-al)
}

// vowelFormants are the first three formants (Hz, bandwidth Hz, level) of a
// sung vowel: sound 1..5 is a e i o u, voice 0..4 soprano alto
// counter-tenor tenor bass.
func vowelFormants(sound, voice int) [3][3]float64 {
	table := [5][5][3][3]float64{
		{ // soprano
			{{800, 80, 1}, {1150, 90, 0.5}, {2900, 120, 0.25}},
			{{350, 60, 1}, {2000, 100, 0.1}, {2800, 120, 0.18}},
			{{270, 60, 1}, {2140, 90, 0.25}, {2950, 100, 0.05}},
			{{450, 40, 1}, {800, 80, 0.28}, {2830, 100, 0.08}},
			{{325, 50, 1}, {700, 60, 0.16}, {2700, 170, 0.02}},
		},
		{ // alto
			{{800, 80, 1}, {1150, 90, 0.63}, {2800, 120, 0.1}},
			{{400, 60, 1}, {1600, 80, 0.06}, {2700, 120, 0.03}},
			{{350, 50, 1}, {1700, 100, 0.1}, {2700, 120, 0.03}},
			{{450, 70, 1}, {800, 80, 0.35}, {2830, 100, 0.16}},
			{{325, 50, 1}, {700, 60, 0.25}, {2530, 170, 0.03}},
		},
		{ // counter-tenor
			{{660, 80, 1}, {1120, 90, 0.5}, {2750, 120, 0.45}},
			{{440, 70, 1}, {1800, 80, 0.2}, {2700, 100, 0.12}},
			{{270, 40, 1}, {1850, 90, 0.13}, {2900, 100, 0.13}},
			{{430, 40, 1}, {820, 80, 0.32}, {2700, 100, 0.06}},
			{{370, 40, 1}, {630, 60, 0.1}, {2750, 100, 0.03}},
		},
		{ // tenor
			{{650, 80, 1}, {1080, 90, 0.5}, {2650, 120, 0.45}},
			{{400, 70, 1}, {1700, 80, 0.2}, {2600, 100, 0.25}},
			{{290, 40, 1}, {1870, 90, 0.18}, {2800, 100, 0.12}},
			{{400, 40, 1}, {800, 80, 0.32}, {2600, 100, 0.25}},
			{{350, 40, 1}, {600, 60, 0.1}, {2700, 100, 0.14}},
		},
		{ // bass
			{{600, 60, 1}, {1040, 70, 0.45}, {2250, 110, 0.35}},
			{{400, 40, 1}, {1620, 80, 0.25}, {2400, 100, 0.35}},
			{{250, 60, 1}, {1750, 90, 0.03}, {2600, 100, 0.16}},
			{{400, 40, 1}, {750, 80, 0.28}, {2400, 100, 0.08}},
			{{350, 40, 1}, {600, 80, 0.1}, {2400, 100, 0.03}},
		},
	}
	sound = max(1, min(5, sound)) - 1
	voice = max(0, min(4, voice))
	return table[voice][sound]
}
