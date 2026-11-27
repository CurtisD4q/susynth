package engine

import "math"

// The reverb is a feedback delay network: eight delay lines whose outputs
// are mixed back into their inputs through a lossless (Householder) matrix,
// so every echo spawns eight more and the tail thickens fast into a smooth
// wash. Each line loses level at a rate set by the decay time, and loses
// treble faster than bass (damping), the way air and soft walls do. Two of
// the lines drift slowly in length, which breaks up the metallic ringing a
// fixed network has. Left and right read the lines with different signs, so
// the tail is wide.

const (
	fdnLines = 8
	fdnMod   = 6.0  // samples of slow length drift on the moving lines
	fdnGlide = 4e-4 // per-sample approach to new decay settings (~50 ms)
	flush    = 1e-18
)

// fdnLengths are the line lengths in ms: mutually prime in samples, spread
// so their echoes do not line up.
var fdnLengths = [fdnLines]float64{31.3, 37.9, 41.9, 47.3, 53.3, 59.9, 67.1, 73.7}

// fdnOutL and fdnOutR are orthogonal sign patterns, so the two outputs are
// uncorrelated while carrying the same energy.
var (
	fdnOutL = [fdnLines]float32{1, -1, 1, -1, 1, -1, 1, -1}
	fdnOutR = [fdnLines]float32{1, 1, -1, -1, 1, 1, -1, -1}
)

type fdnLine struct {
	buf    []float32
	w      int
	length float64 // in samples
	g, a   float32 // loop gain and damping pole, gliding to gT, aT
	gT, aT float32
	lp     float32
	lfo    float64 // phase of the length drift, radians
	rate   float64 // radians per sample, 0 for a fixed line
}

// read returns the line's output d samples ago (fractional, interpolated).
func (l *fdnLine) read(d float64) float32 {
	p := float64(l.w) - d
	n := len(l.buf)
	for p < 0 {
		p += float64(n)
	}
	i := int(p)
	f := float32(p - float64(i))
	j := i + 1
	if j == n {
		j = 0
	}
	return l.buf[i] + (l.buf[j]-l.buf[i])*f
}

// allpass is a Schroeder allpass used to smear the input before the network.
type diffuser struct {
	buf []float32
	i   int
}

func (a *diffuser) run(x float32) float32 {
	const k = 0.6
	d := a.buf[a.i]
	y := d - k*x
	a.buf[a.i] = x + k*y
	if a.i++; a.i == len(a.buf) {
		a.i = 0
	}
	return y
}

type reverb struct {
	lines  [fdnLines]fdnLine
	pre    [2][]float32 // pre-delay per channel
	preW   int
	diff   [2][4]diffuser
	inGain float32
	size   float64
	damp   float64

	// idle: nothing is sending and the tail has died away, so the network
	// is skipped. silentIn counts samples since the last non-zero input.
	silentIn int
	idle     bool
}

// idleFloor is the output level below which a tail counts as gone (~-180 dB).
const idleFloor = 1e-9

// preDelay is the gap before the tail starts: it keeps the dry attack clear.
const preDelay = 0.012

// reverbGain makes the default room (size 0.6, damp 0.5) about as loud as
// the reverb it replaced, so sets keep their balance.
const reverbGain = 0.29

func newReverb() *reverb {
	r := &reverb{inGain: reverbGain}
	for i := range r.lines {
		l := &r.lines[i]
		l.length = math.Round(fdnLengths[i] * SampleRate / 1000)
		l.buf = make([]float32, int(l.length+fdnMod)+4)
		if i%4 == 1 { // lines 1 and 5 drift, at unrelated slow rates
			l.rate = 2 * math.Pi * (0.31 + 0.23*float64(i)) / SampleRate
		}
	}
	n := int(preDelay * SampleRate)
	r.pre = [2][]float32{make([]float32, n), make([]float32, n)}
	for ch := range r.diff {
		for k, ms := range [4]float64{1.9, 4.7, 7.3, 11.3} {
			r.diff[ch][k].buf = make([]float32, int((ms+float64(ch)*0.9)*SampleRate/1000))
		}
	}
	r.set(0.6, 0.5)
	for i := range r.lines {
		r.lines[i].g, r.lines[i].a = r.lines[i].gT, r.lines[i].aT
	}
	return r
}

// DecayTime is the reverb's RT60 in seconds for a room size 0..1: from a
// small room (0.35 s) to a hall (4.2 s).
func DecayTime(size float64) float64 {
	return 0.35 * math.Pow(12, math.Max(0, math.Min(1, size)))
}

// set aims the network at a room size and damping (both 0..1). Damping is
// how much faster treble dies than bass: at 0 they decay together, at 1
// treble lasts about a seventh as long.
func (r *reverb) set(size, damp float64) {
	size, damp = math.Max(0, math.Min(1, size)), math.Max(0, math.Min(1, damp))
	if size == r.size && damp == r.damp && r.lines[0].gT != 0 {
		return
	}
	r.size, r.damp = size, damp
	rt := DecayTime(size)
	hf := rt * (1 - 0.85*damp)
	for i := range r.lines {
		l := &r.lines[i]
		g := math.Pow(10, -3*l.length/(SampleRate*rt))
		gh := math.Pow(10, -3*l.length/(SampleRate*hf))
		q := gh / g // the damping filter's gain at Nyquist
		l.gT, l.aT = float32(g), float32((1-q)/(1+q))
	}
}

// process adds the reverb of the stereo send in (interleaved) to out.
func (r *reverb) process(out, in []float32) {
	quiet := true
	for _, v := range in {
		if v != 0 {
			quiet = false
			break
		}
	}
	if quiet && r.idle {
		return
	}
	if quiet {
		r.silentIn += len(in) / 2
	} else {
		r.silentIn, r.idle = 0, false
	}
	var x [fdnLines]float32
	var loud float32
	for s := 0; s < len(out)/2; s++ {
		// pre-delay and diffusion, per channel
		var inp [2]float32
		for ch := 0; ch < 2; ch++ {
			d := r.pre[ch][r.preW]
			r.pre[ch][r.preW] = in[s*2+ch] * r.inGain
			for k := range r.diff[ch] {
				d = r.diff[ch][k].run(d)
			}
			inp[ch] = d
		}
		if r.preW++; r.preW == len(r.pre[0]) {
			r.preW = 0
		}
		// read every line through its loss and damping
		var sum, oL, oR float32
		for i := range r.lines {
			l := &r.lines[i]
			l.g += (l.gT - l.g) * fdnGlide
			l.a += (l.aT - l.a) * fdnGlide
			d := l.length
			if l.rate != 0 {
				l.lfo += l.rate
				if l.lfo > 2*math.Pi {
					l.lfo -= 2 * math.Pi
				}
				d += fdnMod * math.Sin(l.lfo)
			}
			v := l.read(d)
			l.lp = v*(1-l.a) + l.lp*l.a + flush - flush
			x[i] = l.lp * l.g
			sum += x[i]
			oL += x[i] * fdnOutL[i]
			oR += x[i] * fdnOutR[i]
		}
		// Householder feedback, plus the input (left to even lines, right to odd)
		h := sum * (2.0 / fdnLines)
		for i := range r.lines {
			l := &r.lines[i]
			l.buf[l.w] = x[i] - h + inp[i&1]
			if l.w++; l.w == len(l.buf) {
				l.w = 0
			}
		}
		out[s*2] += oL
		out[s*2+1] += oR
		loud = max(loud, oL, -oL, oR, -oR)
	}
	// once the input has been silent longer than it takes to reach the
	// lines, a tail this quiet can only fade further
	if r.silentIn > r.inputSpan() && loud < idleFloor {
		r.idle = true
		for i := range r.lines {
			clear(r.lines[i].buf)
			r.lines[i].lp = 0
		}
	}
}

// inputSpan is how many samples input takes to pass the pre-delay and
// diffusers into the network.
func (r *reverb) inputSpan() int {
	n := len(r.pre[0])
	for _, d := range r.diff[0] {
		n += len(d.buf)
	}
	return n + 64
}
