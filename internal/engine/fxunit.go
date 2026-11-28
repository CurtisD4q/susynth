package engine

import (
	"math"

	"github.com/susynth/susynth/internal/synth"
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

// FXUnitNames lists the effects that run as units, sorted.
func FXUnitNames() []string { return synth.FXUnits() }

// HasUnit reports whether with_fx name runs as a unit.
func HasUnit(name string) bool { return synth.NewFX(name, SampleRate, nil) != nil }

type fxUnit struct {
	id, parent int64
	name       string
	track      string
	proc       synth.FX
	preAmp     float64
	amp        float64
	preMix     float64
	mix        float64
	in, wet    []float32
	closed     bool
	closeAt    int64
	lastBusy   int64
	dead       bool
	bpm        float64
	dry        []float32
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
		bpm := op.BPM
		if bpm <= 0 {
			bpm = e.bpm
		}
		proc := synth.NewFX(op.Name, SampleRate, scaleBeats(op.Opts, bpm))
		if proc == nil {
			return
		}
		u := &fxUnit{id: op.ID, parent: op.Parent, name: op.Name, track: h.Track, proc: proc,
			preAmp: opt(op.Opts, "pre_amp", 1), amp: opt(op.Opts, "amp", 1),
			preMix: clampF(opt(op.Opts, "pre_mix", 1), 0, 1), mix: clampF(opt(op.Opts, "mix", synth.FXDefaultMix(op.Name)), 0, 1),
			lastBusy: h.At, bpm: bpm}
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
			if v, ok := op.Opts["pre_mix"]; ok {
				u.preMix = clampF(v, 0, 1)
			}
			if v, ok := op.Opts["mix"]; ok {
				u.mix = clampF(v, 0, 1)
			}
			u.proc.Set(scaleBeats(op.Opts, u.bpm))
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
		// Sonic Pi's def-fx: the effect hears pre_mix of the sound, the rest
		// goes round it, and an equal-power cross-fade (mix) joins the
		// unprocessed sound to the result.
		pm := float32(u.preMix)
		// The effects run at Sonic Pi's levels (the mix inside the engine is
		// 1/outScale louder, and the master brings it back).
		up := float32(1 / spLevel)
		if cap(u.dry) < len(in) {
			u.dry = make([]float32, len(in))
		}
		dry := u.dry[:len(in)]
		for k := range in {
			dry[k] = in[k] * pm * float32(spLevel)
		}
		u.proc.Process(u.wet, dry)
		th := u.mix * math.Pi / 2
		cd, cw, amp := float32(math.Cos(th)), float32(math.Sin(th)), float32(u.amp)
		by := 1 - pm
		for k := range in {
			w := u.wet[k]*up + in[k]*by
			out[k] += (in[k]*cd + w*cw) * amp
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
	return float64(end-u.lastBusy) >= u.proc.KillDelay()*SampleRate
}

// killUnits frees the units of a track (all of them for "").
func (e *Engine) killUnits(track string) {
	for _, u := range e.units {
		if track == "" || u.track == track {
			e.dropUnit(u)
		}
	}
}

// spLevel is how Sonic Pi's levels sit against the engine's own.
const spLevel = float64(outScale)

func clampF(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
