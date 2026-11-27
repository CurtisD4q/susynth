package engine

// Global send effects: a stereo feedback delay and the reverb (reverb.go).
// Tracks feed them with "# delay x" and "# room x"; tails keep ringing after
// the voices that fed them are gone.

type fxBus struct {
	dl, dr []float32
	w      int
	rev    *reverb
}

func newFXBus() *fxBus {
	return &fxBus{dl: make([]float32, 4*SampleRate), dr: make([]float32, 4*SampleRate), rev: newReverb()}
}

// process mixes the effect return for one block into out. dIn and rIn are the
// interleaved stereo sends; delaySamples and feedback set the echo.
func (f *fxBus) process(out, dIn, rIn []float32, delaySamples int, feedback float32) {
	if delaySamples < 1 {
		delaySamples = 1
	}
	if delaySamples >= len(f.dl) {
		delaySamples = len(f.dl) - 1
	}
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
	}
	f.rev.process(out, rIn)
}
