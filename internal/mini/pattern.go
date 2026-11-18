// Package mini implements a Tidal-style mini-notation parser and pattern
// query engine.
package mini

import (
	"sort"

	"github.com/susynth/susynth/internal/frac"
)

// Event is one note on: it starts at Start (cycle time) and lasts until End.
type Event struct {
	Start, End frac.F
	Value      string
}

type node interface {
	// query returns events whose onset lies in [a, b).
	query(a, b frac.F) []Event
}

// Pattern is a parsed mini-notation string.
type Pattern struct {
	Source string
	root   node
}

// Query returns the events with onset in [a, b), ordered by onset.
func (p *Pattern) Query(a, b frac.F) []Event {
	evs := p.root.query(a, b)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Start.Lt(evs[j].Start) })
	return evs
}

func inRange(t, a, b frac.F) bool { return t.Ge(a) && t.Lt(b) }

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

type silence struct{}

func (silence) query(a, b frac.F) []Event { return nil }

type atom struct{ value string }

func (n atom) query(a, b frac.F) []Event {
	var out []Event
	for c := a.Ceil(); frac.Int(c).Lt(b); c++ {
		out = append(out, Event{frac.Int(c), frac.Int(c + 1), n.value})
	}
	return out
}

type step struct {
	n      node
	weight int64
}

type seq struct {
	steps []step
	total int64
}

func newSeq(steps []step) node {
	var t int64
	for _, s := range steps {
		t += s.weight
	}
	if t == 0 {
		return silence{}
	}
	return &seq{steps, t}
}

func (s *seq) query(a, b frac.F) []Event {
	var out []Event
	for c := a.Floor(); frac.Int(c).Lt(b); c++ {
		base := frac.Int(c)
		var cum int64
		for _, st := range s.steps {
			start := frac.New(cum, s.total)
			w := frac.New(st.weight, s.total)
			cum += st.weight
			slotA := base.Add(start)
			slotB := slotA.Add(w)
			if slotB.Le(a) || slotA.Ge(b) {
				continue
			}
			for _, ev := range st.n.query(base, base.Add(frac.Int(1))) {
				ev.Start = slotA.Add(ev.Start.Sub(base).Mul(w))
				ev.End = slotA.Add(ev.End.Sub(base).Mul(w))
				if inRange(ev.Start, a, b) {
					out = append(out, ev)
				}
			}
		}
	}
	return out
}

type stack struct{ kids []node }

func (s stack) query(a, b frac.F) []Event {
	var out []Event
	for _, k := range s.kids {
		out = append(out, k.query(a, b)...)
	}
	return out
}

// alt is <a b c>: one child per cycle.
type alt struct{ kids []node }

func (s alt) query(a, b frac.F) []Event {
	var out []Event
	n := int64(len(s.kids))
	for c := a.Floor(); frac.Int(c).Lt(b); c++ {
		i := c - floorDiv(c, n)*n
		k := floorDiv(c, n)
		shift := frac.Int(c - k)
		for _, ev := range s.kids[i].query(frac.Int(k), frac.Int(k+1)) {
			ev.Start = ev.Start.Add(shift)
			ev.End = ev.End.Add(shift)
			if inRange(ev.Start, a, b) {
				out = append(out, ev)
			}
		}
	}
	return out
}

// fast speeds the inner pattern up by factor (use <1 to slow down).
type fast struct {
	inner  node
	factor frac.F
}

func (f fast) query(a, b frac.F) []Event {
	evs := f.inner.query(a.Mul(f.factor), b.Mul(f.factor))
	for i := range evs {
		evs[i].Start = evs[i].Start.Div(f.factor)
		evs[i].End = evs[i].End.Div(f.factor)
	}
	return evs
}

// degrade drops each event with the given probability (deterministically).
type degrade struct {
	inner node
	prob  float64
	seed  uint32
}

func (d degrade) query(a, b frac.F) []Event {
	var out []Event
	for _, ev := range d.inner.query(a, b) {
		h := uint32(ev.Start.N)*2654435761 ^ uint32(ev.Start.D)*40503 ^ d.seed*2246822519
		h ^= h >> 15
		h *= 2246822519
		h ^= h >> 13
		if float64(h%10000)/10000 >= d.prob {
			out = append(out, ev)
		}
	}
	return out
}

// euclid returns the Euclidean rhythm of k onsets in n steps (a rotation of
// the Bjorklund pattern, e.g. (3,8) = x..x..x.).
func euclid(k, n int) []bool {
	if n <= 0 {
		return nil
	}
	if k > n {
		k = n
	}
	out := make([]bool, n)
	for i := 0; i < n; i++ {
		out[i] = (i*k)%n < k
	}
	return out
}
