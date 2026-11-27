package engine

import (
	"math"
	"strings"
)

// Live-performance controls: timing changes to the beat grid, a low-pass
// filter on each track, the old set left by a mix (and its fade-out), and
// which source lines are sounding now.

// OldPrefix starts the names of the old set's tracks after a mix.
const OldPrefix = "old/"

// NextGrid is the first engine time, in seconds, at or after notBefore that
// falls on a multiple of step beats (1 for a beat, BeatsPerCycle for a bar).
func (e *Engine) NextGrid(step, notBefore float64) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.playing || e.bpm <= 0 || step <= 0 {
		return notBefore
	}
	s := int64(math.Ceil(notBefore * SampleRate))
	n := math.Ceil(e.beatAt(s)/step - 1e-9)
	for k := 0; k < 4; k++ {
		at := e.sampleAtBeat((n + float64(k)) * step)
		if at >= s {
			return float64(at) / SampleRate
		}
	}
	return notBefore
}

// SetDownbeat puts beat 0 of the bar grid at t seconds: the moment the first
// run starts, so later changes timed to the beat land with the music.
func (e *Engine) SetDownbeat(t float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.anchorS, e.anchorBeat = int64(math.Round(t*SampleRate)), 0
}

// SetBPMAt changes the tempo from t seconds on, so a use_bpm that runs on a
// beat moves the grid from that beat, not from whenever Ruby got there.
func (e *Engine) SetBPMAt(b, t float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b = math.Max(10, math.Min(600, b))
	s := int64(math.Round(t * SampleRate))
	if s < e.samples {
		s = e.samples
	}
	beat := e.beatAt(s)
	e.anchorS, e.anchorBeat = s, beat
	e.bpm = b
}

// RenameTrack moves a track to a new name, keeping its number, level, mute
// and history. A track already called to is replaced.
func (e *Engine) RenameTrack(from, to string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tracks[from]
	if !ok || from == to {
		return
	}
	if old, ok := e.tracks[to]; ok {
		for _, v := range e.voices {
			if old.id > 0 && v.Track == old.id {
				v.Kill()
			}
		}
		delete(e.tracks, to)
	}
	delete(e.tracks, from)
	t.name = to
	e.tracks[to] = t
	for i := range e.queue {
		if e.queue[i].Track == from {
			e.queue[i].Track = to
		}
	}
	for i := range e.roll {
		if e.roll[i].Name == from {
			e.roll[i].Name = to
		}
	}
}

// MuteNewTracks makes tracks that start while it is on start muted, so the
// parts of a set being mixed in can be brought in one at a time.
func (e *Engine) MuteNewTracks(on bool) {
	e.mu.Lock()
	e.muteNew = on
	e.mu.Unlock()
}

func isOld(name string) bool { return strings.HasPrefix(name, OldPrefix) }

// HasOld reports whether an old set is still playing.
func (e *Engine) HasOld() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tracks {
		if isOld(t.name) && t.refs > 0 {
			return true
		}
	}
	return false
}

// FadeOld fades the old set out over seconds. FadeDone reports when it has
// gone silent, so its loops can be stopped.
func (e *Engine) FadeOld(seconds float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fadeFrom = e.samples
	e.fadeLen = int64(math.Max(seconds, 0.05) * SampleRate)
	e.fadeDone = false
}

// FadeDone returns true once, when a fade started by FadeOld has finished.
func (e *Engine) FadeDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fadeLen > 0 && e.samples-e.fadeFrom >= e.fadeLen && !e.fadeDone {
		e.fadeDone = true
		return true
	}
	return false
}

// Fading reports how far through a fade of the old set it is (0..1), or -1.
func (e *Engine) Fading() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fadeLen <= 0 || e.fadeDone {
		return -1
	}
	return math.Min(1, float64(e.samples-e.fadeFrom)/float64(e.fadeLen))
}

// deckGain is the old set's fade, 1 for every other track.
func (e *Engine) deckGain(t *track, at int64) float32 {
	if e.fadeLen <= 0 || !isOld(t.name) {
		return 1
	}
	p := float64(at-e.fadeFrom) / float64(e.fadeLen)
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		return 0
	}
	// equal-power: the room does not dip in the middle of the fade
	return float32(math.Cos(p * math.Pi / 2))
}

// FilterSteps is how many steps the track filter has, from open to closed.
const FilterSteps = 24

// FilterHz is the cutoff for a filter step: 0 is open (no filter), and each
// step down is a fixed musical interval, to 90 Hz at the last.
func FilterHz(step int) float64 {
	if step <= 0 {
		return 0
	}
	if step > FilterSteps {
		step = FilterSteps
	}
	return 18000 * math.Pow(90.0/18000, float64(step)/FilterSteps)
}

// AdjustFilter moves a track's low-pass filter by d steps (down closes it)
// and returns the new cutoff in Hz (0 = open). It acts on notes already
// sounding, so it can be swept like a DJ mixer's filter.
func (e *Engine) AdjustFilter(id, d int) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.trackByID(id)
	if t == nil {
		return 0, false
	}
	t.fstep = max(0, min(FilterSteps, t.fstep-d))
	return FilterHz(t.fstep), true
}

// trackFilter is a stereo state-variable low-pass for one track's dry signal
// and its two effect sends.
type trackFilter struct {
	hz  float64          // smoothed cutoff
	st  [3][2][2]float64 // bus, channel, integrator
	buf [3][]float32
}

const openHz = 19000.0

func (f *trackFilter) bufs(n int) {
	for b := range f.buf {
		if cap(f.buf[b]) < n*2 {
			f.buf[b] = make([]float32, n*2)
		}
		f.buf[b] = f.buf[b][:n*2]
		clear(f.buf[b])
	}
}

// run filters the track's buffers towards target Hz (0 = open) and adds them
// to the outputs.
func (f *trackFilter) run(target float64, outs [3][]float32, n int) {
	if target <= 0 {
		target = openHz
	}
	if f.hz <= 0 {
		f.hz = openHz
	}
	// glide about 40 ms per block of 10 ms, so a sweep has no steps in it
	from := f.hz
	to := from * math.Pow(target/from, 0.25)
	if math.Abs(math.Log(target/from)) < 0.002 {
		to = target
	}
	f.hz = to
	const k = 1 / 0.9 // a little resonance, as on a DJ filter
	for i := 0; i < n; i++ {
		hz := from + (to-from)*float64(i)/float64(n)
		g := math.Tan(math.Pi * math.Min(hz, SampleRate*0.45) / SampleRate)
		a1 := 1 / (1 + g*(g+k))
		a2 := g * a1
		a3 := g * a2
		for b := 0; b < 3; b++ {
			for ch := 0; ch < 2; ch++ {
				s := &f.st[b][ch]
				v0 := float64(f.buf[b][i*2+ch])
				v3 := v0 - s[1]
				v1 := a1*s[0] + a2*v3
				v2 := s[1] + a2*s[0] + a3*v3
				s[0] = 2*v1 - s[0]
				s[1] = 2*v2 - s[1]
				outs[b][i*2+ch] += float32(v2)
			}
		}
	}
}

// active reports whether the filter is doing anything: closed, or still
// gliding open.
func (f *trackFilter) active(step int) bool {
	return step > 0 || (f.hz > 0 && f.hz < openHz*0.999)
}

// LitLine is a source line whose sound has just started.
type LitLine struct {
	Line, Run int
	Age       float64 // seconds since it was heard
}

// litKeep is how long a started sound stays listed for LitLines.
const litKeep = SampleRate

type litHit struct {
	at        int64
	line, run int
}

// LitLines lists the lines (with the run that played them) whose sounds
// started in the last window seconds of what is being heard. The old set's
// sounds are left out: their code is not in the editor.
func (e *Engine) LitLines(window float64) []LitLine {
	e.mu.Lock()
	defer e.mu.Unlock()
	heard := e.samples - e.lead
	from := heard - int64(window*SampleRate)
	var out []LitLine
	for _, h := range e.lit {
		if h.at >= from && h.at <= heard {
			out = append(out, LitLine{Line: h.line, Run: h.run, Age: float64(heard-h.at) / SampleRate})
		}
	}
	return out
}
