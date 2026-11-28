package synth

import "math"

// gverb: Sonic Pi runs SuperCollider's GVerb on each input channel and
// cross-fades the two stereo results by spread. This one is our own
// feedback-delay-network reverb tuned to GVerb's decay and level; it is not
// GVerb's own algorithm.

type gverbCore struct {
	lines [8][]float64
	pos   [8]int
	lp    [8]float64
	early []float64
	ew    int
	taps  [8]int
	gain  [8]float64
	damp  float64
}

var gverbLens = [8]float64{0.0297, 0.0371, 0.0411, 0.0437, 0.0533, 0.0611, 0.0673, 0.0739}

func newGverbCore(sr, room, release, damp float64) *gverbCore {
	g := &gverbCore{}
	scale := math.Pow(math.Max(room, 1)/10, 0.6)
	rt := gverbRT(release)
	for i := range g.lines {
		n := int(gverbLens[i] * scale * sr)
		if n < 8 {
			n = 8
		}
		g.lines[i] = make([]float64, n)
		g.gain[i] = math.Pow(10, -3*float64(n)/(rt*sr))
	}
	g.early = make([]float64, int(0.12*sr*scale)+8)
	for i := range g.taps {
		g.taps[i] = int((0.006 + 0.0113*float64(i+1)*float64(i%3+1)/2) * scale * sr)
		if g.taps[i] >= len(g.early) {
			g.taps[i] = len(g.early) - 1
		}
	}
	g.damp = clamp(1-0.575*math.Pow((1-clamp(damp, 0, 1))/0.5, 1.7), 0, 0.99)
	return g
}

// gverbRT is the decay time the effect aims for, from Sonic Pi's release.
func gverbRT(release float64) float64 { return math.Max(0.2, 0.35*release+1.2) }

func (g *gverbCore) run(x float64, ref, tail float64) (l, r float64) {
	g.early[g.ew] = x
	var e [2]float64
	for i, t := range g.taps {
		v := g.early[(g.ew-t+len(g.early))%len(g.early)]
		if i%2 == 0 {
			e[0] += v / float64(1+i/2)
		} else {
			e[1] += v / float64(1+i/2)
		}
	}
	g.ew = (g.ew + 1) % len(g.early)
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
	var tl, tr float64
	for i := range out {
		if i%2 == 0 {
			tl += out[i]
		} else {
			tr += out[i]
		}
	}
	return ref*0.5*e[0] + tail*gverbTail*tl, ref*0.5*e[1] + tail*gverbTail*tr
}

const gverbTail = 2.7

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
							n.early, n.ew = old.early, old.ew
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
				ll, lr := s.c[0].run(xl, f.g("ref_level"), f.g("tail_level"))
				rl, rr := s.c[1].run(xr, f.g("ref_level"), f.g("tail_level"))
				ll, lr, rl, rr = ll+dry*xl, lr+dry*xl, rl+dry*xr, rr+dry*xr
				a := f.g("amp")
				out[i] = float32(a * (ll*ca + rl*cb))
				out[i+1] = float32(a * (rr*ca + lr*cb))
			}
		})
}
