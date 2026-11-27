package engine

// Global send effects: the echo (echo.go) and the reverb (reverb.go).
// Tracks feed them with "# delay x" and "# room x"; tails keep ringing after
// the voices that fed them are gone.

type fxBus struct {
	echo *echo
	rev  *reverb
}

func newFXBus() *fxBus { return &fxBus{echo: newEcho(), rev: newReverb()} }

// process mixes the effect returns for one block into out. dIn and rIn are
// the interleaved stereo sends.
func (f *fxBus) process(out, dIn, rIn []float32) {
	f.echo.process(out, dIn)
	f.rev.process(out, rIn)
}
