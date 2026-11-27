package synth

import "math"

// Control changes a sound that is already playing, the way Sonic Pi's
// control does. A NaN value leaves that setting alone; a slide is how long,
// in seconds, the change takes (a negative slide keeps the voice's own).
type Control struct {
	Note, Amp, Pan, Cutoff, Rate                          float64
	NoteSlide, AmpSlide, PanSlide, CutoffSlide, RateSlide float64
	Kill                                                  bool
}

// NoControl is a Control that changes nothing; set the fields you want.
func NoControl() Control {
	n := math.NaN()
	return Control{Note: n, Amp: n, Pan: n, Cutoff: n, Rate: n,
		NoteSlide: -1, AmpSlide: -1, PanSlide: -1, CutoffSlide: -1, RateSlide: -1}
}

// glide moves a value to a target in a straight line over a number of frames.
type glide struct {
	cur, to, step float64
	n             int
}

func (g *glide) set(to float64, frames int) {
	if frames <= 0 {
		g.cur, g.to, g.n = to, to, 0
		return
	}
	g.to, g.n, g.step = to, frames, (to-g.cur)/float64(frames)
}

func (g *glide) next() float64 {
	if g.n > 0 {
		g.cur += g.step
		if g.n--; g.n == 0 {
			g.cur = g.to
		}
	}
	return g.cur
}

func (g *glide) moving() bool { return g.n > 0 }

// controlled is the state of a voice that control has touched: its note,
// level, pan, cutoff (as a MIDI note) and sample rate, each able to glide,
// and the slide times each change takes unless the control says otherwise.
type controlled struct {
	note, amp, pan, cutoff, rate glide
	slide                        [5]float64 // note, amp, pan, cutoff, rate (seconds)
	step0                        float64    // the sample step at rate 1
	hasFilter                    bool
}

type pendingCtl struct {
	wait int
	c    Control
}

// Control schedules a change wait frames from now (counted the way Next
// counts them, so before the voice starts the change waits with it).
func (v *Voice) Control(c Control, wait int) {
	if wait < 0 {
		wait = 0
	}
	v.pend = append(v.pend, pendingCtl{wait, c})
}

// SetSlides sets the slide times a voice starts with (note_slide: and so on
// on play), used by later controls that don't give their own.
func (v *Voice) SetSlides(note, amp, pan, cutoff, rate float64) {
	v.initCtl()
	v.ctl.slide = [5]float64{note, amp, pan, cutoff, rate}
}

func (v *Voice) initCtl() {
	if v.ctl != nil {
		return
	}
	c := &controlled{}
	c.note.cur = 69 + 12*math.Log2(math.Max(v.freq, 1e-9)/440/math.Max(v.speed, 1e-9))
	c.amp.cur = v.amp0
	c.pan.cur = v.pan0
	c.rate.cur = 1
	c.step0 = v.step
	if v.fl[0].lp.Active() {
		c.hasFilter = true
		c.cutoff.cur = 69 + 12*math.Log2(v.lpf0/440)
	} else {
		c.cutoff.cur = 130
	}
	v.ctl = c
}

func (v *Voice) applyCtl(c Control) {
	if c.Kill {
		v.Kill()
		return
	}
	v.initCtl()
	k := v.ctl
	frames := func(s float64, i int) int {
		if s < 0 {
			s = k.slide[i]
		} else {
			k.slide[i] = s
		}
		return int(s * v.sr)
	}
	if !math.IsNaN(c.Note) {
		k.note.set(c.Note, frames(c.NoteSlide, 0))
	} else if c.NoteSlide >= 0 {
		k.slide[0] = c.NoteSlide
	}
	if !math.IsNaN(c.Amp) {
		k.amp.set(math.Max(0, math.Min(c.Amp, 4)), frames(c.AmpSlide, 1))
	}
	if !math.IsNaN(c.Pan) {
		k.pan.set(math.Max(-1, math.Min(1, c.Pan)), frames(c.PanSlide, 2))
	}
	if !math.IsNaN(c.Cutoff) {
		if !k.hasFilter {
			for ch := range v.fl {
				v.fl[ch].lp = NewLowPass(440*math.Pow(2, (k.cutoff.cur-69)/12), v.res0, v.sr)
			}
			k.hasFilter = true
		}
		k.cutoff.set(math.Max(0, math.Min(c.Cutoff, 130)), frames(c.CutoffSlide, 3))
	}
	if !math.IsNaN(c.Rate) {
		k.rate.set(c.Rate, frames(c.RateSlide, 4))
	}
	v.ctlUpdate(true)
}

// ctlUpdate moves the glides on one frame and sets the voice from them.
func (v *Voice) ctlUpdate(force bool) {
	k := v.ctl
	if force || k.note.moving() {
		v.freq = 440 * math.Pow(2, (k.note.next()-69)/12) * v.speed
	}
	if force || k.amp.moving() || k.pan.moving() {
		g, pan := k.amp.next(), k.pan.next()
		ang := (pan + 1) * math.Pi / 4
		v.gl, v.gr = float32(g*math.Cos(ang)), float32(g*math.Sin(ang))
	}
	if k.hasFilter && (force || k.cutoff.moving() && v.t%sweepEvery == 0) {
		if !force {
			for i := 0; i < sweepEvery-1; i++ {
				k.cutoff.next()
			}
		}
		hz := 440 * math.Pow(2, (k.cutoff.next()-69)/12)
		for ch := range v.fl {
			v.fl[ch].lp.Set(math.Min(hz, v.sr*0.45), v.res0, v.sr)
		}
	}
	if v.k == kSample && (force || k.rate.moving()) {
		r := k.rate.next()
		v.step = k.step0 * math.Abs(r)
	}
}

// stepCtl runs pending controls and glides for the frame about to play.
func (v *Voice) stepCtl() {
	if len(v.pend) > 0 {
		keep := v.pend[:0]
		for _, p := range v.pend {
			if p.wait <= 0 {
				v.applyCtl(p.c)
			} else {
				p.wait--
				keep = append(keep, p)
			}
		}
		v.pend = keep
	}
	if v.ctl != nil {
		v.ctlUpdate(false)
	}
}
