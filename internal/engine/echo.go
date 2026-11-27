package engine

import "math"

// The echo is a stereo delay whose time never jumps: a small change glides
// there, and a big one crossfades from the old time to the new over 50 ms
// (no pitch sweep, no click). Its feedback path
// rolls off treble and deep bass, so each repeat is a little darker and
// thinner than the one before, as real echoes are. The first repeat is the
// sound itself. In ping-pong mode the repeats alternate left and right.

const (
	echoMax     = 4 * SampleRate
	echoGlide   = 1.0 / (0.08 * SampleRate) // ~80 ms to reach a new time or feedback
	echoNudge   = 48                        // time changes up to 1 ms glide; bigger ones crossfade
	echoXfade   = 1.0 / (0.05 * SampleRate) // a crossfade takes 50 ms
	echoLowHz   = 5500                      // repeats lose treble above this ...
	echoHighHz  = 70                        // ... and rumble below this
	echoScanGap = 10                        // blocks between idle checks of the buffer
)

var (
	echoLPA = float32(1 - math.Exp(-2*math.Pi*echoLowHz/SampleRate))
	echoHPA = float32(1 - math.Exp(-2*math.Pi*echoHighHz/SampleRate))
)

type echo struct {
	buf      [2][]float32
	w        int
	d, dT    float64 // delay in samples: now, and where it is heading
	d2, xf   float64 // during a crossfade: the new time, and progress 0..1 (-1 none)
	fb, fbT  float32
	pingPong bool
	lp, hp   [2]float32

	silentIn, sinceScan int
	idle                bool
}

func newEcho() *echo {
	e := &echo{buf: [2][]float32{make([]float32, echoMax), make([]float32, echoMax)}, xf: -1}
	e.set(0.25, 0.45, false)
	e.d, e.fb = e.dT, e.fbT
	return e
}

// set aims the echo at a time (seconds), feedback (0..0.95) and mode.
func (e *echo) set(seconds, feedback float64, pingPong bool) {
	e.dT = math.Max(1, math.Min(seconds*SampleRate, echoMax-4))
	e.fbT = float32(math.Max(0, math.Min(feedback, 0.95)))
	e.pingPong = pingPong
}

// read returns channel ch d samples back from the write point, with 4-point
// (Hermite) interpolation: a gliding time reads between samples.
func (e *echo) read(ch int, d float64) float32 {
	b := e.buf[ch]
	p := float64(e.w) - d
	if p < 0 {
		p += echoMax
	}
	i := int(p)
	f := float32(p - float64(i))
	at := func(k int) float32 { return b[(k%echoMax+echoMax)%echoMax] }
	x0, x1, x2, x3 := at(i-1), at(i), at(i+1), at(i+2)
	c1 := 0.5 * (x2 - x0)
	c2 := x0 - 2.5*x1 + 2*x2 - 0.5*x3
	c3 := 0.5*(x3-x0) + 1.5*(x1-x2)
	return ((c3*f+c2)*f+c1)*f + x1
}

// process adds the echo of the stereo send in (interleaved) to out.
func (e *echo) process(out, in []float32) {
	quiet := true
	for _, v := range in {
		if v != 0 {
			quiet = false
			break
		}
	}
	if quiet && e.idle {
		return
	}
	if quiet {
		e.silentIn += len(in) / 2
	} else {
		e.silentIn, e.idle = 0, false
	}
	for s := 0; s < len(out)/2; s++ {
		e.fb += (e.fbT - e.fb) * float32(echoGlide)
		if e.xf < 0 {
			if math.Abs(e.dT-e.d) <= echoNudge {
				e.d += (e.dT - e.d) * echoGlide
			} else {
				e.d2, e.xf = e.dT, 0
			}
		}
		var a, b float32 = 1, 0 // equal-power weights of the old and new time
		if e.xf >= 0 {
			if e.xf += echoXfade; e.xf >= 1 {
				e.d, e.xf = e.d2, -1
			} else {
				a, b = float32(math.Cos(e.xf*math.Pi/2)), float32(math.Sin(e.xf*math.Pi/2))
			}
		}
		var tap, rep [2]float32
		for ch := 0; ch < 2; ch++ {
			tap[ch] = e.read(ch, e.d)
			if b != 0 {
				tap[ch] = tap[ch]*a + e.read(ch, e.d2)*b
			}
			e.lp[ch] += echoLPA * (tap[ch] - e.lp[ch])
			e.hp[ch] += echoHPA * (e.lp[ch] - e.hp[ch])
			rep[ch] = (e.lp[ch] - e.hp[ch]) * e.fb
		}
		inL, inR := in[s*2], in[s*2+1]
		if e.pingPong {
			e.buf[0][e.w] = (inL+inR)*0.5 + rep[1] + flush - flush
			e.buf[1][e.w] = rep[0] + flush - flush
		} else {
			e.buf[0][e.w] = inL + rep[0] + flush - flush
			e.buf[1][e.w] = inR + rep[1] + flush - flush
		}
		if e.w++; e.w == echoMax {
			e.w = 0
		}
		out[s*2] += tap[0]
		out[s*2+1] += tap[1]
	}
	// Once nothing has been sent for longer than the delay, everything
	// still to come is already in the buffer: if all of it is below the
	// floor, the echo has died away.
	if e.silentIn > int(e.d)+len(in) {
		if e.sinceScan++; e.sinceScan >= echoScanGap {
			e.sinceScan = 0
			var loud float32
			for _, b := range e.buf {
				for _, v := range b {
					loud = max(loud, v, -v)
				}
			}
			if loud < idleFloor {
				e.idle = true
				clear(e.buf[0])
				clear(e.buf[1])
				e.lp, e.hp = [2]float32{}, [2]float32{}
			}
		}
	}
}
