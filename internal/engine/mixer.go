package engine

import "math"

// The master mixer is Sonic Pi's: everything is summed, pushed by the drive
// (pre_amp) into a limiter, and then the volume (amp) fader sets how loud it
// comes out. It can also high-pass and low-pass the whole mix, fold it to
// mono or swap its channels, as set_mixer_control! does.

// Mixer is the master mixer's settings. The zero value is not the default:
// use DefaultMixer.
type Mixer struct {
	PreAmp        float64 // drive into the limiter; 1 is unity (set_drive! 0.5)
	Amp           float64 // the fader after the limiter, 0..1 (set_volume!)
	HPF           float64 // high-pass cutoff as a MIDI note; 0 is off
	LPF           float64 // low-pass cutoff as a MIDI note; 135.5 is off
	ForceMono     bool
	InvertStereo  bool
	LimiterBypass bool
}

// DefaultMixer is the mixer Sonic Pi starts with.
var DefaultMixer = Mixer{PreAmp: 1, Amp: 1, LPF: 135.5}

// DriveToPreAmp is Sonic Pi's drive mapping: 0 is a quarter, 0.5 unity, 1 four times.
func DriveToPreAmp(drive float64) float64 {
	return 0.25 * math.Pow(16, math.Max(0, math.Min(1, drive)))
}

// PreAmpToDrive is the inverse of DriveToPreAmp.
func PreAmpToDrive(pre float64) float64 {
	return math.Log(math.Max(pre, 1e-6)/0.25) / math.Log(16)
}

// MixerSet is a change to the mixer: only the keys present are changed.
type MixerSet map[string]float64

// Apply returns m with the change made, clamped as Sonic Pi clamps.
func (m Mixer) Apply(s MixerSet) Mixer {
	for k, v := range s {
		switch k {
		case "drive":
			m.PreAmp = DriveToPreAmp(v)
		case "pre_amp":
			m.PreAmp = math.Max(0, math.Min(v, 16))
		case "amp", "volume":
			m.Amp = math.Max(0, math.Min(v, 1))
		case "hpf":
			m.HPF = math.Max(0, math.Min(v, 135.5))
		case "hpf_bypass":
			if v != 0 {
				m.HPF = 0
			}
		case "lpf":
			m.LPF = math.Max(0, math.Min(v, 135.5))
		case "lpf_bypass":
			if v != 0 {
				m.LPF = 135.5
			}
		case "force_mono":
			m.ForceMono = v != 0
		case "invert_stereo":
			m.InvertStereo = v != 0
		case "limiter_bypass":
			m.LimiterBypass = v != 0
		case "reset":
			m = DefaultMixer
		}
	}
	return m
}

// mixerState is the mixer's running state.
type mixerState struct {
	set      Mixer
	pre, amp float32 // the gains, gliding to the settings (about 20 ms)
	started  bool
	hp, lp   [2]mixBiquad
	hpOn     bool
	lpOn     bool
	hpAt     float64
	lpAt     float64
	peak     float32 // the output peak of the last block
	limiting bool    // the limiter bent something in the last block
}

type mixBiquad struct{ b0, b1, b2, a1, a2, z1, z2 float64 }

func (q *mixBiquad) run(x float64) float64 {
	y := q.b0*x + q.z1
	q.z1 = q.b1*x - q.a1*y + q.z2
	q.z2 = q.b2*x - q.a2*y
	return y
}

func butter(hz float64, high bool) mixBiquad {
	w := 2 * math.Pi * math.Min(hz, SampleRate*0.49) / SampleRate
	cs, al := math.Cos(w), math.Sin(w)/math.Sqrt2
	a0 := 1 + al
	if high {
		return mixBiquad{b0: (1 + cs) / 2 / a0, b1: -(1 + cs) / a0, b2: (1 + cs) / 2 / a0, a1: -2 * cs / a0, a2: (1 - al) / a0}
	}
	return mixBiquad{b0: (1 - cs) / 2 / a0, b1: (1 - cs) / a0, b2: (1 - cs) / 2 / a0, a1: -2 * cs / a0, a2: (1 - al) / a0}
}

func newMixerState() mixerState { return mixerState{set: DefaultMixer} }

// process runs the mixer over the interleaved block in place.
func (s *mixerState) process(mix []float32) {
	m := s.set
	if !s.started {
		s.pre, s.amp, s.started = float32(m.PreAmp), float32(m.Amp), true
	}
	if m.HPF > 0 && (!s.hpOn || s.hpAt != m.HPF) {
		keep := s.hp
		c := butter(midiHz(m.HPF), true)
		s.hp = [2]mixBiquad{c, c}
		if s.hpOn {
			for i := range s.hp {
				s.hp[i].z1, s.hp[i].z2 = keep[i].z1, keep[i].z2
			}
		}
		s.hpOn, s.hpAt = true, m.HPF
	} else if m.HPF == 0 {
		s.hpOn = false
	}
	if m.LPF < 135 && (!s.lpOn || s.lpAt != m.LPF) {
		keep := s.lp
		c := butter(midiHz(m.LPF), false)
		s.lp = [2]mixBiquad{c, c}
		if s.lpOn {
			for i := range s.lp {
				s.lp[i].z1, s.lp[i].z2 = keep[i].z1, keep[i].z2
			}
		}
		s.lpOn, s.lpAt = true, m.LPF
	} else if m.LPF >= 135 {
		s.lpOn = false
	}
	const glide = 1 / (0.02 * SampleRate)
	var pk float32
	for i := 0; i+1 < len(mix); i += 2 {
		s.pre += (float32(m.PreAmp) - s.pre) * glide
		s.amp += (float32(m.Amp) - s.amp) * glide
		l, r := mix[i]*s.pre, mix[i+1]*s.pre
		if s.hpOn {
			l, r = float32(s.hp[0].run(float64(l))), float32(s.hp[1].run(float64(r)))
		}
		if s.lpOn {
			l, r = float32(s.lp[0].run(float64(l))), float32(s.lp[1].run(float64(r)))
		}
		if m.ForceMono {
			l = (l + r) / 2
			r = l
		}
		if m.InvertStereo {
			l, r = r, l
		}
		if m.LimiterBypass {
			l, r = clip1(l), clip1(r)
		} else {
			l, r = limit(l), limit(r)
		}
		mix[i], mix[i+1] = l*s.amp, r*s.amp
		if a := abs32(mix[i]); a > pk {
			pk = a
		}
		if a := abs32(mix[i+1]); a > pk {
			pk = a
		}
	}
	s.peak, s.limiting = pk*outScale, !m.LimiterBypass && pk > limitKnee*0.999
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// MixerLevel is the output peak (1 is full scale) and whether the limiter is working.
func (e *Engine) MixerLevel() (float32, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mixer.peak, e.mixer.limiting
}

func clip1(v float32) float32 {
	c := limitCeil * 1.0
	if v > c {
		return c
	}
	if v < -c {
		return -c
	}
	return v
}

func midiHz(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

// SetMixer changes the master mixer now.
func (e *Engine) SetMixer(s MixerSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mixer.set = e.mixer.set.Apply(s)
}

// Mixer returns the master mixer's settings.
func (e *Engine) Mixer() Mixer {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mixer.set
}
