package engine

import (
	"math"
	"sort"
)

// Effect units are Sonic Pi's with_fx: an effect that reads a bus of its own
// and writes to the bus outside it. Notes and samples started inside the block
// are written to the unit's bus; units inside units feed their parent's bus.
// A unit outlives its block while its sounds ring, then for its kill delay.

// UnitOp is a change to the effect units, carried by a Hit.
type UnitOp struct {
	Op     string // "open", "close", "ctl" or "kill"
	ID     int64
	Parent int64 // open: the unit it sits inside (0: the track)
	Name   string
	Opts   map[string]float64
	BPM    float64 // open: the tempo its time options are in beats of
}

// fxProc is one kind of effect.
type fxProc interface {
	// set gives the effect options (at creation, and again by control).
	set(o map[string]float64)
	// levels is the weight of the unprocessed sound and of the effect's own in the output.
	levels() (dry, wet float32)
	// process adds the effect's own sound for the stereo block in to out.
	process(out, in []float32)
	// killDelay is how long the unit lives on after its sounds are gone, seconds.
	killDelay() float64
}

type fxMaker func(o map[string]float64, bpm float64) fxProc

var fxMakers = map[string]fxMaker{}

// FXUnitNames lists the effects that run as units, sorted.
func FXUnitNames() []string {
	var out []string
	for n := range fxMakers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HasUnit reports whether with_fx name runs as a unit.
func HasUnit(name string) bool { _, ok := fxMakers[name]; return ok }

type fxUnit struct {
	id, parent int64
	name       string
	track      string
	proc       fxProc
	preAmp     float64
	amp        float64
	in, wet    []float32
	closed     bool
	closeAt    int64
	lastBusy   int64
	dead       bool
	bpm        float64
}

func opt(o map[string]float64, k string, d float64) float64 {
	if v, ok := o[k]; ok {
		return v
	}
	return d
}

func (e *Engine) unitOp(h Hit) {
	op := h.Unit
	switch op.Op {
	case "open":
		mk, ok := fxMakers[op.Name]
		if !ok {
			return
		}
		bpm := op.BPM
		if bpm <= 0 {
			bpm = e.bpm
		}
		u := &fxUnit{id: op.ID, parent: op.Parent, name: op.Name, track: h.Track, proc: mk(op.Opts, bpm),
			preAmp: opt(op.Opts, "pre_amp", 1), amp: opt(op.Opts, "amp", 1), lastBusy: h.At, bpm: bpm}
		e.units = append(e.units, u)
		if e.unitIdx == nil {
			e.unitIdx = map[int64]*fxUnit{}
		}
		e.unitIdx[u.id] = u
	case "close":
		if u := e.unitIdx[op.ID]; u != nil {
			u.closed, u.closeAt = true, h.At
		}
	case "ctl":
		if u := e.unitIdx[op.ID]; u != nil {
			if v, ok := op.Opts["pre_amp"]; ok {
				u.preAmp = v
			}
			if v, ok := op.Opts["amp"]; ok {
				u.amp = v
			}
			u.proc.set(scaleBeats(op.Opts, u.bpm))
		}
	case "kill":
		if u := e.unitIdx[op.ID]; u != nil {
			e.dropUnit(u)
		}
	}
}

// scaleBeats gives the effect its time options in seconds.
func scaleBeats(o map[string]float64, bpm float64) map[string]float64 {
	out := make(map[string]float64, len(o))
	for k, v := range o {
		switch k {
		case "phase", "decay", "max_phase", "attack", "release":
			v = v * 60 / bpm
		}
		out[k] = v
	}
	return out
}

func (e *Engine) dropUnit(u *fxUnit) {
	u.dead = true
	delete(e.unitIdx, u.id)
}

// unitFor is the live unit a sound writes to, or nil.
func (e *Engine) unitFor(id int64) *fxUnit {
	if id == 0 {
		return nil
	}
	return e.unitIdx[id]
}

// buf returns the unit's input bus for a block of n frames (cleared).
func (u *fxUnit) buf(n int) []float32 {
	if len(u.in) != n*2 {
		u.in = make([]float32, n*2)
		u.wet = make([]float32, n*2)
	}
	return u.in
}

// runUnits runs every unit, the newest first (so a unit inside another feeds
// it before it runs), into mix or the unit around it, then frees the ones
// that are done.
func (e *Engine) runUnits(mix []float32, n int) {
	if len(e.units) == 0 {
		return
	}
	end := e.samples + int64(n)
	for i := len(e.units) - 1; i >= 0; i-- {
		u := e.units[i]
		if u.dead {
			continue
		}
		in := u.buf(n)
		out := mix
		if p := e.unitFor(u.parent); p != nil {
			out = p.buf(n)
		}
		if u.preAmp != 1 {
			for k := range in {
				in[k] *= float32(u.preAmp)
			}
		}
		clear(u.wet)
		u.proc.process(u.wet, in)
		dry, wet := u.proc.levels()
		amp := float32(u.amp)
		for k := range in {
			out[k] += (in[k]*dry + u.wet[k]*wet) * amp
		}
		clear(in)
	}
	live := e.units[:0]
	for _, u := range e.units {
		if !u.dead && e.unitDone(u, end) {
			e.dropUnit(u)
		}
		if !u.dead {
			live = append(live, u)
		}
	}
	e.units = live
}

// unitDone: its block has ended, nothing is playing into it, and it has
// waited out its kill delay.
func (e *Engine) unitDone(u *fxUnit, end int64) bool {
	if !u.closed || end < u.closeAt {
		return false
	}
	busy := false
	for _, v := range e.voices {
		if v.Bus == u.id && !v.Done() {
			busy = true
			break
		}
	}
	if !busy {
		for _, c := range e.units {
			if c.parent == u.id && !c.dead {
				busy = true
				break
			}
		}
	}
	if busy {
		u.lastBusy = end
		return false
	}
	return float64(end-u.lastBusy) >= u.proc.killDelay()*SampleRate
}

// killUnits frees the units of a track (all of them for "").
func (e *Engine) killUnits(track string) {
	for _, u := range e.units {
		if track == "" || u.track == track {
			e.dropUnit(u)
		}
	}
}

func pow10(x float64) float64 { return math.Pow(10, x) }
