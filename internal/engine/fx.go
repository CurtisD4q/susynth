package engine

// Global send effects: a stereo feedback delay and a Freeverb-style reverb.
// Tracks feed them with "# delay x" and "# room x"; tails keep ringing after
// the voices that fed them are gone.

type comb struct {
	buf   []float32
	i     int
	store float32
}

func (c *comb) process(in, fb, damp float32) float32 {
	out := c.buf[c.i]
	c.store = out*(1-damp) + c.store*damp
	c.buf[c.i] = in + c.store*fb
	if c.i++; c.i == len(c.buf) {
		c.i = 0
	}
	return out
}

type allpass struct {
	buf []float32
	i   int
}

func (a *allpass) process(in float32) float32 {
	b := a.buf[a.i]
	out := b - in
	a.buf[a.i] = in + b*0.5
	if a.i++; a.i == len(a.buf) {
		a.i = 0
	}
	return out
}

type fxBus struct {
	dl, dr []float32
	w      int
	combs  [2][4]comb
	aps    [2][2]allpass
}

func newFXBus() *fxBus {
	f := &fxBus{dl: make([]float32, 4*SampleRate), dr: make([]float32, 4*SampleRate)}
	scale := float64(SampleRate) / 44100
	lens := [4]int{1116, 1188, 1277, 1356}
	for ch := 0; ch < 2; ch++ {
		for k, n := range lens {
			f.combs[ch][k].buf = make([]float32, int(float64(n+ch*23)*scale))
		}
		for k, n := range [2]int{556, 441} {
			f.aps[ch][k].buf = make([]float32, int(float64(n+ch*23)*scale))
		}
	}
	return f
}

// process mixes the effect return for one block into out. dIn and rIn are the
// interleaved stereo sends; delaySamples/feedback/size are the bus settings.
func (f *fxBus) process(out, dIn, rIn []float32, delaySamples int, feedback, size float32) {
	if delaySamples < 1 {
		delaySamples = 1
	}
	if delaySamples >= len(f.dl) {
		delaySamples = len(f.dl) - 1
	}
	fb := 0.7 + 0.28*size
	for i := 0; i < len(out)/2; i++ {
		r := (f.w - delaySamples + len(f.dl)) % len(f.dl)
		tl, tr := f.dl[r], f.dr[r]
		f.dl[f.w] = dIn[i*2] + tl*feedback
		f.dr[f.w] = dIn[i*2+1] + tr*feedback
		if f.w++; f.w == len(f.dl) {
			f.w = 0
		}
		out[i*2] += tl
		out[i*2+1] += tr

		in := (rIn[i*2] + rIn[i*2+1]) * 0.5 * 0.15
		for ch := 0; ch < 2; ch++ {
			var sum float32
			for k := range f.combs[ch] {
				sum += f.combs[ch][k].process(in, fb, 0.3)
			}
			for k := range f.aps[ch] {
				sum = f.aps[ch][k].process(sum)
			}
			out[i*2+ch] += sum * 0.9
		}
	}
}
