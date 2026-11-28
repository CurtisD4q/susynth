package synth

import (
	"math"
	"sort"
)

// autotuner: tracks the pitch of its input and retunes it to the nearest
// semitone (or to note:), keeping the formants. Our own design: a normalised
// autocorrelation pitch tracker, then pitch-synchronous granular synthesis.
// Grains of grains_period input cycles, read from a phase-locked point of
// the cycle, are overlapped at the retuned pitch's period; reading them at
// formant_ratio moves the formants without moving the pitch.

const (
	atDecim  = 4                     // the tracker works at a quarter of the sample rate
	atHop    = 480                   // and looks for a pitch 100 times a second
	atMinHz  = 60.0                  // the lowest pitch it looks for
	atMaxHz  = 4000.0                // and the highest
	atAmp    = 0.01                  // below this level the pitch is held
	atPeak   = 0.5                   // the autocorrelation peak that counts as a pitch
	atMedian = 7                     // of the last 7 detections
	atRing   = 1 << 19               // input history, samples
	atWin    = 768                   // tracker window, at the decimated rate
	atBuf    = atWin + 250 + atHop/4 // decimated history kept
)

type atGrain struct {
	pos, step float64
	n, length float64
}

type autotune struct {
	sr float64

	ring [atRing]float32
	w    int

	// pitch tracking
	dec     [atBuf]float64
	dw      int
	dacc    float64
	dn      int
	hop     int
	hist    []float64
	f0      float64
	lagged  float64 // the correction in semitones, after retune's lag
	thIn    float64 // phase of the input cycle, 0..1
	thOut   float64 // phase of the output cycle
	jit     float64
	grains  []atGrain
	rng     uint64
	started bool
}

func (a *autotune) rand() float64 {
	a.rng ^= a.rng << 13
	a.rng ^= a.rng >> 7
	a.rng ^= a.rng << 17
	return float64(a.rng>>11) / (1 << 53)
}

// track looks for the pitch of the latest stretch of input.
func (a *autotune) track() {
	rate := a.sr / atDecim
	maxLag := int(rate/atMinHz) + 1
	minLag := int(rate / atMaxHz)
	if minLag < 2 {
		minLag = 2
	}
	// the last atWin+maxLag decimated samples, oldest first
	n := atWin + maxLag + 1
	x := make([]float64, n)
	var e float64
	for i := range x {
		x[i] = a.dec[((a.dw-n+i)%atBuf+atBuf)%atBuf]
	}
	for i := n - atWin; i < n; i++ {
		e += x[i] * x[i]
	}
	if math.Sqrt(e/atWin) < atAmp*0.5 { // below the level threshold: hold the last pitch
		return
	}
	r := make([]float64, maxLag+2)
	e0 := e
	cur := x[n-atWin:]
	for lag := minLag - 1; lag <= maxLag+1 && lag < len(r); lag++ {
		var c, e1 float64
		old := x[n-atWin-lag : n-lag]
		for i := 0; i < atWin; i++ {
			c += cur[i] * old[i]
			e1 += old[i] * old[i]
		}
		if e1 > 0 && e0 > 0 {
			r[lag] = c / math.Sqrt(e0*e1)
		}
	}
	best := 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		if r[lag] > best {
			best = r[lag]
		}
	}
	if best < atPeak {
		return
	}
	// the first peak close to the best one avoids octave errors
	pick := 0
	for lag := minLag; lag <= maxLag; lag++ {
		if r[lag] >= 0.9*best && r[lag] >= r[lag-1] && r[lag] >= r[lag+1] {
			pick = lag
			break
		}
	}
	if pick == 0 {
		return
	}
	tau := float64(pick)
	if d := r[pick-1] - 2*r[pick] + r[pick+1]; d < 0 {
		tau += 0.5 * (r[pick-1] - r[pick+1]) / d
	}
	hz := rate / tau
	a.hist = append(a.hist, hz)
	if len(a.hist) > atMedian {
		a.hist = a.hist[1:]
	}
	s := append([]float64(nil), a.hist...)
	sort.Float64s(s)
	a.f0 = s[len(s)/2]
}

func init() {
	defFX("autotuner", 1, map[string]float64{"note": 0, "formant_ratio": 1, "min_freq": 10, "max_formant_ratio": 10,
		"grains_period": 2, "transpose": 0, "retune": 0, "strength": 1, "time_dispersion": 0},
		func(sr float64) any { return &autotune{sr: sr, f0: 440, rng: 88172645463325252} },
		func(f *simpleFX, out, in []float32) {
			a := f.st.(*autotune)
			sr := f.sr
			gp := math.Max(f.g("grains_period"), 0.1)
			maxFR := math.Max(f.g("max_formant_ratio"), 0.01)
			fr := clamp(f.g("formant_ratio"), 1/maxFR, maxFR)
			trans := math.Pow(2, f.g("transpose")/12)
			strength := clamp(f.g("strength"), 0, 1)
			retune := math.Max(f.g("retune"), 0)
			td := f.g("time_dispersion")
			lagc := 0.0
			if retune > 0 {
				lagc = math.Exp(math.Log(0.001) / (retune * sr))
			}
			for i := 0; i+1 < len(in); i += 2 {
				m := float64(in[i]) + float64(in[i+1])
				a.ring[a.w&(atRing-1)] = float32(m)
				a.w++
				// decimate for the tracker
				a.dacc += m
				if a.dn++; a.dn == atDecim {
					a.dec[a.dw%atBuf] = a.dacc / atDecim
					a.dw++
					a.dacc, a.dn = 0, 0
				}
				if a.hop++; a.hop >= atHop {
					a.hop = 0
					a.track()
				}
				// the correction: to the nearest semitone, or to note:
				midi := 12*math.Log2(a.f0/440) + 69
				target := math.Round(midi)
				if nt := f.g("note"); nt > 0 {
					target = nt
				}
				shift := (target - midi) * strength
				a.lagged = shift + lagc*(a.lagged-shift)
				ratio := math.Pow(2, a.lagged/12) * trans
				fout := a.f0 * ratio

				tin := sr / a.f0
				a.thIn += a.f0 / sr
				a.thIn -= math.Floor(a.thIn)
				if !a.started {
					a.started = true
					a.jit = 1
				}
				a.thOut += fout / sr / a.jit
				if a.thOut >= 1 {
					a.thOut -= math.Floor(a.thOut)
					a.jit = 1 + td*(2*a.rand()-1)
					if a.jit < 0.1 {
						a.jit = 0.1
					}
					length := gp * tin
					cycles := math.Ceil(gp * math.Max(fr, 1))
					start := float64(a.w) - (a.thIn+cycles)*tin
					if start > 0 && len(a.grains) < 96 {
						a.grains = append(a.grains, atGrain{pos: start, step: fr, length: length})
					}
				}
				var y float64
				live := a.grains[:0]
				for _, g := range a.grains {
					if g.n >= g.length {
						continue
					}
					w := 0.5 * (1 - math.Cos(2*math.Pi*g.n/g.length))
					p0 := math.Floor(g.pos)
					fp := g.pos - p0
					k := int(p0)
					v := float64(a.ring[k&(atRing-1)])*(1-fp) + float64(a.ring[(k+1)&(atRing-1)])*fp
					y += w * v
					g.pos += g.step
					g.n++
					live = append(live, g)
				}
				a.grains = live
				y *= 2 / (gp * ratio)
				out[i], out[i+1] = float32(y), float32(y)
			}
		})
}
