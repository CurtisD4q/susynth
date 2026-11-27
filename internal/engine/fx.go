package engine

// Global send effects: the echo (echo.go) and the reverb (reverb.go).
// Tracks feed them with "# delay x" and "# room x"; tails keep ringing after
// the voices that fed them are gone.

type fxBus struct {
	echo *echo
	rev  *reverb
	cho  *chorus
}

func newFXBus() *fxBus {
	return &fxBus{echo: newEcho(), rev: newReverb(), cho: &chorus{set: DefaultChorus, idle: true}}
}

// process mixes the effect returns for one block into out. dIn, rIn and cIn
// are the interleaved stereo sends.
func (f *fxBus) process(out, dIn, rIn, cIn []float32) {
	f.echo.process(out, dIn)
	f.rev.process(out, rIn)
	f.cho.process(out, cIn)
}
