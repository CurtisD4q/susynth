package synth

import "math"

// gverb: Sonic Pi runs SuperCollider's GVerb on each input channel and
// cross-fades the two stereo results by spread. This one is our own
// feedback-delay-network reverb tuned to GVerb's decay and level; it is not
// GVerb's own algorithm.
//
// Its decay time, damping, early-reflection burst and levels were fitted to
// impulse responses measured from scsynth's GVerb (the early burst as the
// energy distribution of those responses, not GVerb's own tap table).

type gverbCore struct {
	lines [8][]float64
	pos   [8]int
	lp    [8]float64
	early []float64
	ew    int
	etaps []int
	esign []float64
	bw    float64
	lpIn  float64
	gain  [8]float64
	damp  float64
}

var gverbLens = [8]float64{0.0297, 0.0371, 0.0411, 0.0437, 0.0533, 0.0611, 0.0673, 0.0739}

func newGverbCore(sr, room, release, damp float64) *gverbCore {
	g := &gverbCore{}
	scale := math.Pow(math.Max(room, 1)/10, gvExp)
	rt := gverbRT(release)
	for i := range g.lines {
		n := int(gverbLens[i] * scale * sr)
		if n < 8 {
			n = 8
		}
		g.lines[i] = make([]float64, n)
		g.gain[i] = math.Pow(10, -3*float64(n)/(rt*sr))
	}
	// the early reflections: a dense, decaying burst whose length follows the
	// room (the density of GVerb's, measured against scsynth)
	rs := sr / 48000 * math.Max(room, 1)
	last := int(float64(gverbEarlyPos[len(gverbEarlyPos)-1])*rs) + 2
	g.early = make([]float64, last+1)
	g.etaps = make([]int, len(gverbEarlyPos))
	g.esign = make([]float64, len(gverbEarlyPos))
	seed := uint32(2463534242)
	for i, p := range gverbEarlyPos {
		g.etaps[i] = int(float64(p)*rs + 0.5)
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		g.esign[i] = gverbEarlyAmp
		if seed&0x10000 != 0 {
			g.esign[i] = -gverbEarlyAmp
		}
	}
	g.damp = clamp(1-gvDA*math.Pow((1-clamp(damp, 0, 1))/0.5, gvDB), 0, 0.99)
	return g
}

// gverbRT is the reverb time, which Sonic Pi passes straight on as release:.
func gverbRT(release float64) float64 { return math.Max(0.2, release) }

// run feeds one sample through the reverb: the early reflections and the
// tail, as one mono signal (Sonic Pi runs GVerb with spread 0).
func (g *gverbCore) run(x float64, ref, tail float64) float64 {
	g.lpIn += g.bw * (x - g.lpIn)
	x = g.lpIn
	g.early[g.ew] = x
	var e float64
	n := len(g.early)
	for i, t := range g.etaps {
		k := g.ew - t
		if k < 0 {
			k += n
		}
		e += g.esign[i] * g.early[k]
	}
	if g.ew++; g.ew == n {
		g.ew = 0
	}
	var out [8]float64
	var sum float64
	for i := range g.lines {
		out[i] = g.lines[i][g.pos[i]]
		sum += out[i]
	}
	sum *= 2.0 / 8
	for i := range g.lines {
		v := (out[i] - sum) * g.gain[i]
		g.lp[i] = (1-g.damp)*v + g.damp*g.lp[i]
		in := g.lp[i] + x*0.5
		g.lines[i][g.pos[i]] = in
		if g.pos[i]++; g.pos[i] == len(g.lines[i]) {
			g.pos[i] = 0
		}
	}
	var tl float64
	for i := 0; i < len(out); i += 2 {
		tl += out[i]
	}
	return ref*e + tail*gverbTail*tl
}

const gverbTail = 1.889

const gverbEarlyAmp = 0.096 // the early burst has the measured energy (3.3 for a unit impulse)

var gverbEarlyPos = [300]float32{
	13.98, 13.98, 21.88, 21.88, 21.88, 21.88, 24.56, 27.96, 32.46, 35.14,
	35.86, 41.28, 41.28, 41.94, 42.34, 42.34, 42.34, 42.34, 43.04, 49.18,
	49.18, 49.18, 49.18, 49.18, 49.84, 51.40, 51.86, 52.92, 53.62, 55.26,
	56.32, 57.02, 57.88, 57.88, 57.88, 57.88, 59.30, 59.30, 59.30, 59.30,
	59.76, 60.42, 63.10, 63.16, 63.50, 64.20, 65.84, 67.60, 68.46, 69.24,
	69.64, 69.64, 69.64, 69.64, 69.88, 70.28, 70.34, 71.86, 71.86, 73.28,
	73.28, 74.40, 76.48, 76.48, 77.14, 78.70, 78.70, 79.04, 79.76, 79.76,
	79.76, 79.76, 79.82, 80.22, 80.46, 81.58, 83.22, 83.62, 83.86, 84.32,
	85.18, 85.18, 85.18, 85.36, 86.60, 86.60, 86.60, 86.60, 87.00, 87.26,
	88.38, 89.28, 90.34, 90.34, 90.80, 91.04, 91.50, 92.68, 93.74, 93.74,
	94.44, 95.30, 95.30, 95.30, 95.30, 95.76, 96.54, 96.72, 96.94, 96.94,
	97.18, 97.60, 97.64, 99.16, 99.16, 99.86, 100.58, 100.92, 101.24, 101.70,
	103.26, 103.92, 104.78, 105.88, 105.88, 106.34, 106.66, 107.06, 107.06, 107.06,
	107.30, 107.52, 107.72, 108.18, 109.28, 109.74, 110.52, 110.92, 111.50, 111.82,
	112.48, 112.48, 113.38, 113.90, 113.90, 114.56, 115.36, 116.40, 116.78, 117.18,
	117.18, 117.64, 117.88, 118.34, 119.46, 120.32, 121.04, 121.28, 121.74, 122.60,
	122.60, 122.60, 122.60, 123.18, 123.72, 124.02, 124.24, 124.48, 124.94, 125.94,
	126.70, 127.16, 127.78, 128.22, 128.54, 129.34, 130.56, 131.16, 132.08, 132.72,
	133.06, 133.18, 133.84, 134.14, 134.36, 134.60, 135.02, 135.48, 136.58, 136.80,
	137.62, 138.08, 138.66, 139.14, 139.78, 140.44, 141.20, 141.86, 142.66, 143.32,
	143.76, 144.48, 144.48, 144.94, 145.60, 146.70, 147.16, 148.20, 148.60, 149.38,
	149.90, 150.04, 150.80, 151.32, 151.98, 153.10, 154.00, 154.60, 155.06, 155.52,
	156.42, 157.98, 158.70, 159.56, 160.02, 160.24, 160.68, 161.44, 161.90, 162.56,
	163.66, 164.54, 165.20, 165.96, 167.08, 168.56, 169.74, 170.40, 171.14, 171.78,
	172.44, 173.62, 174.66, 175.58, 176.34, 177.38, 178.52, 179.18, 180.74, 181.64,
	182.36, 183.42, 185.04, 186.22, 187.32, 187.98, 188.96, 190.20, 191.36, 192.48,
	193.48, 195.16, 196.50, 197.78, 198.86, 199.98, 201.88, 203.06, 204.50, 206.34,
	207.70, 209.06, 210.36, 212.58, 214.62, 216.60, 219.06, 220.02, 223.00, 225.00,
	227.10, 230.02, 232.84, 235.32, 237.82, 240.90, 244.36, 247.08, 251.06, 255.58,
	259.90, 265.56, 270.92, 277.18, 283.66, 292.58, 304.04, 318.08, 339.50, 385.20,
}

const (
	gvExp = 0.829 // how the delay lengths grow with the room size
	gvDA  = 0.3557
	gvDB  = 1.1844 // the damping filter's pole from damp:
)

func init() {
	type gv struct {
		c               [2]*gverbCore
		room, rel, damp float64
	}
	defFX("gverb", 3, map[string]float64{"spread": 0.5, "damp": 0.5, "pre_damp": 0.5, "dry": 1, "room": 10, "max_room": -1,
		"release": 3, "ref_level": 0.7, "tail_level": 0.5, "amp": 1},
		func(float64) any { return &gv{} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*gv)
			if s.c[0] == nil || s.room != f.g("room") || s.rel != f.g("release") || s.damp != f.g("damp") {
				s.room, s.rel, s.damp = f.g("room"), f.g("release"), f.g("damp")
				if s.c[0] == nil {
					for i := range s.c {
						s.c[i] = newGverbCore(f.sr, s.room, s.rel, s.damp)
					}
				} else { // a change of room, decay or damping: new gains, delay lines kept where they fit
					for i := range s.c {
						old := s.c[i]
						n := newGverbCore(f.sr, s.room, s.rel, s.damp)
						if len(old.lines[0]) == len(n.lines[0]) {
							for k := range n.lines {
								n.lines[k], n.pos[k], n.lp[k] = old.lines[k], old.pos[k], old.lp[k]
							}
							if len(old.early) == len(n.early) {
								n.early, n.ew = old.early, old.ew
							}
							n.lpIn = old.lpIn
						}
						s.c[i] = n
					}
				}
			}
			spr := linlin(clamp(f.g("spread"), 0, 1), 0, 1, -1, 0)
			th := (spr*0.5 + 0.5) * math.Pi / 2
			ca, cb := math.Cos(th), math.Sin(th) // XFade2(a, b, spr): a*cos + b*sin
			dry := f.g("dry")
			for i := 0; i+1 < len(in); i += 2 {
				xl, xr := float64(in[i]), float64(in[i+1])
				s.c[0].bw, s.c[1].bw = f.g("pre_damp"), f.g("pre_damp")
				ll := s.c[0].run(xl, f.g("ref_level"), f.g("tail_level")) + dry*xl
				rr := s.c[1].run(xr, f.g("ref_level"), f.g("tail_level")) + dry*xr
				lr, rl := ll, rr // spread 0: both outputs of a GVerb are the same signal
				a := f.g("amp")
				out[i] = float32(a * (ll*ca + rl*cb))
				out[i+1] = float32(a * (rr*ca + lr*cb))
			}
		})
}
