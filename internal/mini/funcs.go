package mini

import (
	"errors"

	"github.com/susynth/susynth/internal/frac"
)

// Pattern transformers: each returns a new Pattern, the receiver is unchanged.

func (p *Pattern) wrap(n node) *Pattern { return &Pattern{Source: p.Source, root: n} }

func cyclesIn(a, b frac.F) (int64, int64) { return a.Floor(), b.Ceil() }

// Fast speeds the pattern up by f (f > 0).
func (p *Pattern) Fast(f float64) (*Pattern, error) {
	if f <= 0 || f > 1024 {
		return nil, errors.New("speed must be between 0 and 1024")
	}
	return p.wrap(fast{p.root, frac.FromFloat(f)}), nil
}

// Slow stretches the pattern by f (f > 0).
func (p *Pattern) Slow(f float64) (*Pattern, error) {
	if f <= 0 || f > 1024 {
		return nil, errors.New("slowdown must be between 0 and 1024")
	}
	return p.Fast(1 / f)
}

// reverseCycle mirrors events inside each cycle.
type reverseNode struct{ inner node }

func (r reverseNode) query(a, b frac.F) []Event {
	var out []Event
	lo, hi := cyclesIn(a, b)
	for c := lo; c < hi; c++ {
		out = append(out, mirror(r.inner.query(frac.Int(c), frac.Int(c+1)), c, a, b)...)
	}
	return out
}

func mirror(evs []Event, c int64, a, b frac.F) []Event {
	var out []Event
	sum := frac.Int(2*c + 1)
	for _, e := range evs {
		ne := Event{Start: sum.Sub(e.End), End: sum.Sub(e.Start), Value: e.Value}
		if inRange(ne.Start, a, b) {
			out = append(out, ne)
		}
	}
	return out
}

// Rev plays each cycle backwards.
func (p *Pattern) Rev() *Pattern { return p.wrap(reverseNode{p.root}) }

type palindromeNode struct{ inner node }

func (n palindromeNode) query(a, b frac.F) []Event {
	var out []Event
	lo, hi := cyclesIn(a, b)
	for c := lo; c < hi; c++ {
		k := floorDiv(c, 2)
		var evs []Event
		for _, e := range n.inner.query(frac.Int(k), frac.Int(k+1)) {
			sh := frac.Int(c - k)
			e.Start, e.End = e.Start.Add(sh), e.End.Add(sh)
			evs = append(evs, e)
		}
		if c%2 != 0 {
			out = append(out, mirror(evs, c, a, b)...)
			continue
		}
		for _, e := range evs {
			if inRange(e.Start, a, b) {
				out = append(out, e)
			}
		}
	}
	return out
}

// Palindrome plays forwards, then backwards, alternating each cycle.
func (p *Pattern) Palindrome() *Pattern { return p.wrap(palindromeNode{p.root}) }

type shiftNode struct {
	inner node
	by    frac.F // positive = earlier (rotate left)
}

func (n shiftNode) query(a, b frac.F) []Event {
	evs := n.inner.query(a.Add(n.by), b.Add(n.by))
	for i := range evs {
		evs[i].Start, evs[i].End = evs[i].Start.Sub(n.by), evs[i].End.Sub(n.by)
	}
	return evs
}

// Rot shifts the pattern left by x cycles (negative = right).
func (p *Pattern) Rot(x float64) *Pattern { return p.wrap(shiftNode{p.root, frac.FromFloat(x)}) }

type iterNode struct {
	inner node
	n     int64
}

func (n iterNode) query(a, b frac.F) []Event {
	var out []Event
	lo, hi := cyclesIn(a, b)
	for c := lo; c < hi; c++ {
		sh := frac.New(c-floorDiv(c, n.n)*n.n, n.n)
		for _, e := range (shiftNode{n.inner, sh}).query(frac.Int(c), frac.Int(c+1)) {
			if inRange(e.Start, a, b) {
				out = append(out, e)
			}
		}
	}
	return out
}

// Iter shifts the pattern by 1/n more each cycle, returning after n cycles.
func (p *Pattern) Iter(n int) (*Pattern, error) {
	if n < 1 || n > 64 {
		return nil, errors.New("iter needs 1..64")
	}
	return p.wrap(iterNode{p.root, int64(n)}), nil
}

type everyNode struct {
	inner, applied node
	n              int64
}

func (e everyNode) query(a, b frac.F) []Event {
	var out []Event
	lo, hi := cyclesIn(a, b)
	for c := lo; c < hi; c++ {
		src := e.inner
		if c-floorDiv(c, e.n)*e.n == 0 {
			src = e.applied
		}
		for _, ev := range src.query(frac.Int(c), frac.Int(c+1)) {
			if inRange(ev.Start, a, b) {
				out = append(out, ev)
			}
		}
	}
	return out
}

// Every applies f on every nth cycle (cycles 0, n, 2n, ...).
func (p *Pattern) Every(n int, f func(*Pattern) (*Pattern, error)) (*Pattern, error) {
	if n < 1 || n > 64 {
		return nil, errors.New("every needs 1..64")
	}
	q, err := f(p)
	if err != nil {
		return nil, err
	}
	return p.wrap(everyNode{p.root, q.root, int64(n)}), nil
}

// DegradeBy randomly (but repeatably) drops events with probability prob.
func (p *Pattern) DegradeBy(prob float64) (*Pattern, error) {
	if prob < 0 || prob > 1 {
		return nil, errors.New("probability must be 0..1")
	}
	return p.wrap(degrade{p.root, prob, 977}), nil
}

type plyNode struct {
	inner node
	n     int64
}

func (n plyNode) query(a, b frac.F) []Event {
	var out []Event
	for _, e := range n.inner.query(a.Sub(frac.Int(1)), b) {
		d := e.End.Sub(e.Start).Div(frac.Int(n.n))
		for i := int64(0); i < n.n; i++ {
			s := e.Start.Add(d.Mul(frac.Int(i)))
			if inRange(s, a, b) {
				out = append(out, Event{s, s.Add(d), e.Value})
			}
		}
	}
	return out
}

// Ply repeats every event n times within its own length.
func (p *Pattern) Ply(n int) (*Pattern, error) {
	if n < 1 || n > 32 {
		return nil, errors.New("ply needs 1..32")
	}
	return p.wrap(plyNode{p.root, int64(n)}), nil
}
