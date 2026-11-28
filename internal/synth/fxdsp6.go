package synth

import "math"

// pitch_shift: SuperCollider's PitchShift, four overlapping grains read from
// a delay line at a different speed.

func init() {
	type grain struct {
		pos float64 // 0..1 through the grain
		d0  float64 // delay at its start, in samples
	}
	type shifter struct {
		d     [2]*delayC
		g     [4]grain
		cnt   int
		stage int
		rng   uint64
	}
	rnd := func(s *shifter) float64 {
		s.rng ^= s.rng << 13
		s.rng ^= s.rng >> 7
		s.rng ^= s.rng << 17
		return float64(s.rng>>11) / (1 << 53)
	}
	defFX("pitch_shift", 1, map[string]float64{"pitch": 0, "window_size": 0.2, "pitch_dis": 0, "time_dis": 0},
		func(sr float64) any {
			s := &shifter{rng: 1}
			for i := range s.d {
				s.d[i] = newDelayC(1.0, sr)
			}
			return s
		},
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*shifter)
			ratio := math.Pow(2, f.g("pitch")/12)
			win := math.Max(f.g("window_size"), 0.01) * f.sr
			for i := 0; i+1 < len(in); i += 2 {
				if s.cnt <= 0 {
					s.cnt = int(win / 4)
					k := s.stage & 3
					s.stage++
					d0 := math.Max(0, ratio-1) * win
					if ratio < 1 {
						d0 = 0
					}
					s.g[k] = grain{pos: 0, d0: d0 + f.g("time_dis")*win*rnd(s)}
				}
				s.cnt--
				var acc [2]float64
				for k := range s.g {
					g := &s.g[k]
					if g.pos >= 1 {
						continue
					}
					w := 1 - math.Abs(2*g.pos-1)
					delay := g.d0 - (ratio-1)*g.pos*win
					for ch := 0; ch < 2; ch++ {
						acc[ch] += w * s.d[ch].read(math.Max(delay, 1))
					}
					g.pos += 1 / win
				}
				for ch := 0; ch < 2; ch++ {
					s.d[ch].write(float64(in[i+ch]))
					out[i+ch] = float32(acc[ch] * pitchShiftGain)
				}
			}
		})
}

// pitchShiftGain brings the four triangle windows to unity.
const pitchShiftGain = 0.5
