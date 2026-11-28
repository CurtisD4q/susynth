package synth

import "math"

// Voices modelled on Sonic Pi's own synth designs (etc/synthdefs in Sonic
// Pi), for the synths code uses most that used to share a plain wave:
// hollow, dark_ambience, prophet, blade, the bells, dsaw and piano. tone() sends these waves here.

type toneState struct {
	bq    [6]biquad
	g     [6]float32
	lp    [2]float32
	dcx   float32
	dcy   float32
	part  [8]float64 // partial phases
	pdec  [8]float64 // partial decay per frame
	pamp  [8]float64
	pfreq [8]float64
	pink  [3]float32
}

var extWaves = map[string]bool{
	"hollow": true, "ambience": true, "prophet": true, "blade": true, "bell": true, "dullbell": true,
	"dsaw": true, "dpulse": true, "dtri": true, "piano": true,
}

func (v *Voice) initExt() {
	x := &toneState{}
	v.ext = x
	f := v.freq
	switch v.wave {
	case "hollow": // noise through narrow band-passes at f, 2f, 4f (Sonic Pi: rq 0.01)
		for k, m := range [3]float64{1, 2, 4} {
			x.bq[k] = bandpass(f*m, f*m*0.012, v.sr)
		}
		x.g = [6]float32{1, 0.5, 0.125}
	case "ambience": // noise ringing resonators at f, an octave and two octaves up
		for k, st := range [3]float64{0, 12, 24} {
			fk := f * math.Pow(2, st/12)
			x.bq[k] = bandpass(fk, math.Max(1.5, fk*0.004), v.sr)
		}
		x.g = [6]float32{2, 1, 1}
	case "blade": // the violin formant bank after the saw (Roger Allen's design)
		x.bq[0] = bandpass(300, 300/3.5, v.sr)
		x.bq[1] = bandpass(700, 700/3.5, v.sr)
		x.bq[2] = bandpass(3000, 3000/2, v.sr)
	case "bell", "dullbell": // sine partials; higher ones quieter and shorter
		parts := []float64{1, 4, 6.8}
		if v.wave == "dullbell" {
			parts = []float64{0.56, 1.19, 3, 3.76}
		}
		prop := 1.0
		for k, p := range parts {
			x.pfreq[k] = f * p
			x.pamp[k] = prop / 2
			// each partial's envelope is the note's, shortened by its proportion
			x.pdec[k] = math.Exp(-1 / (math.Max(0.05, 1.2*prop) * v.sr))
			prop /= 2
		}
	case "piano": // a struck string: slightly stretched partials, the upper ones dying first
		n := 69 + 12*math.Log2(f/440)
		bright := 0.6
		for k := 0; k < 8; k++ {
			h := float64(k + 1)
			x.pfreq[k] = f * h * math.Sqrt(1+0.0004*h*h)
			x.pamp[k] = math.Pow(bright, float64(k)) / h
			life := math.Max(0.15, 3.5*math.Pow(2, -(n-48)/24)/h)
			x.pdec[k] = math.Exp(-1 / (life * v.sr))
			if x.pfreq[k] > v.sr*0.45 {
				x.pamp[k] = 0
			}
		}
	}
}

// noise3 gives white, pink-ish and brown noise from the voice's generator.
func (v *Voice) pinkNoise() float32 {
	w := v.noise()
	x := v.ext
	x.pink[0] = 0.99765*x.pink[0] + w*0.0990460
	x.pink[1] = 0.96300*x.pink[1] + w*0.2965164
	x.pink[2] = 0.57000*x.pink[2] + w*1.0526913
	return (x.pink[0] + x.pink[1] + x.pink[2] + w*0.1848) * 0.25
}

// blepPulse is a band-limited pulse of width w (0..1) at phase p.
func blepPulse(p, dt, w float64) float64 {
	q := p + (1 - w)
	q -= math.Floor(q)
	return (2*p - 1 - polyBLEP(p, dt)) - (2*q - 1 - polyBLEP(q, dt))
}

func (v *Voice) toneExt() float32 {
	if v.ext == nil {
		v.initExt()
	}
	x := v.ext
	f, sr := v.freq, v.sr
	t := float64(v.t) / sr
	var s float64
	switch v.wave {
	case "hollow":
		n := v.pinkNoise()
		for k := 0; k < 3; k++ {
			s += float64(x.bq[k].run(n) * x.g[k])
		}
		s *= 32
	case "ambience":
		n := v.pinkNoise()
		for k := 0; k < 3; k++ {
			s += float64(x.bq[k].run(n) * x.g[k])
		}
		s = math.Tanh(s*60) * 0.7
	case "prophet":
		// five pulses whose widths drift on slow LFOs, one an octave down
		dt := f / sr
		v.phase += dt
		v.phase -= math.Floor(v.phase)
		v.phase2 += dt / 2
		v.phase2 -= math.Floor(v.phase2)
		tri := func(rate, off float64) float64 {
			p := rate*t + off
			p -= math.Floor(p)
			return 1 - 4*math.Abs(p-0.5)
		}
		w1 := 0.1 / (1.2 + math.Sin(2*math.Pi*t))
		w2 := 0.8 * (1.2 + math.Sin(2*math.Pi*0.3*t) + 0.7) / 2
		w3 := 0.8 * (1.2 + tri(0.4, 0)) / 2
		w4 := 0.8 * (1.2 + tri(0.4, 0.19)) / 2
		w5 := 0.8 * (1.2 + tri(2.1, 0)) / 2
		cl := func(w float64) float64 { return math.Max(0.02, math.Min(0.98, w)) }
		s = blepPulse(v.phase, dt, cl(w1)) + blepPulse(v.phase, dt, cl(w2)) + blepPulse(v.phase, dt, cl(w3)) +
			blepPulse(v.phase, dt, cl(w4)) + 0.5*blepPulse(v.phase2, dt/2, cl(w5))
		s *= 0.25
		s = s * math.Abs(s) // Sonic Pi squares the mix before its filter
		// leak DC
		y := float32(s) - x.dcx + 0.995*x.dcy
		x.dcx, x.dcy = float32(s), y
		s = float64(y) * 1.0
	case "blade":
		// a saw with vibrato that fades in after half a second
		depth := v.so.vibDepth * math.Max(0, math.Min(1, (t-v.so.vibDelay)/math.Max(v.so.vibOnset, 1e-3)))
		fv := f * math.Pow(2, depth*math.Sin(2*math.Pi*v.so.vibRate*t)/12)
		dt := fv / sr
		v.phase += dt
		v.phase -= math.Floor(v.phase)
		raw := float32(2*v.phase - 1 - polyBLEP(v.phase, dt))
		for k := 0; k < 3; k++ {
			s += float64(x.bq[k].run(raw))
		}
		s *= 2.1
	case "bell", "dullbell":
		for k := 0; k < 4 && x.pfreq[k] > 0; k++ {
			x.part[k] += x.pfreq[k] / sr
			x.part[k] -= math.Floor(x.part[k])
			s += x.pamp[k] * math.Sin(2*math.Pi*x.part[k])
			x.pamp[k] *= x.pdec[k]
		}
		s *= 1.8
	case "dsaw", "dpulse", "dtri": // two oscillators detune semitones apart
		dt1, dt2 := f/sr, f*math.Pow(2, v.so.detune/12)/sr
		v.phase += dt1
		v.phase -= math.Floor(v.phase)
		v.phase2 += dt2
		v.phase2 -= math.Floor(v.phase2)
		var a, b float64
		switch v.wave {
		case "dpulse":
			a, b = blepPulse(v.phase, dt1, 0.5), blepPulse(v.phase2, dt2, 0.5)
		case "dtri":
			a, b = 4*math.Abs(v.phase-0.5)-1, 4*math.Abs(v.phase2-0.5)-1
		default:
			a = 2*v.phase - 1 - polyBLEP(v.phase, dt1)
			b = 2*v.phase2 - 1 - polyBLEP(v.phase2, dt2)
		}
		x.lp[0] += 0.3 * (float32(a+b)*0.5 - x.lp[0])
		s = float64(x.lp[0]) * 1.2
	case "piano":
		for k := 0; k < 8; k++ {
			if x.pamp[k] == 0 {
				continue
			}
			x.part[k] += x.pfreq[k] / sr
			x.part[k] -= math.Floor(x.part[k])
			s += x.pamp[k] * math.Sin(2*math.Pi*x.part[k])
			x.pamp[k] *= x.pdec[k]
		}
		if t < 0.004 { // the hammer
			s += float64(v.noise()) * 0.3 * (1 - t/0.004)
		}
		s *= 0.8
	}
	return float32(s)
}
