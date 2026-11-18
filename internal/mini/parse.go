package mini

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/susynth/susynth/internal/frac"
)

// ParseError reports a problem at a byte offset in the pattern string.
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string { return fmt.Sprintf("col %d: %s", e.Pos+1, e.Msg) }

// Parse compiles a mini-notation string. validate, if non-nil, is called for
// every sound word; returning an error rejects the pattern.
func Parse(src string, validate func(word string) error) (*Pattern, error) {
	p := &parser{s: src, validate: validate}
	root, err := p.stack(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i < len(p.s) {
		return nil, &ParseError{p.i, fmt.Sprintf("unexpected %q", p.s[p.i])}
	}
	return &Pattern{Source: src, root: root}, nil
}

type parser struct {
	s        string
	i        int
	validate func(string) error
	seed     uint32
}

func (p *parser) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '#' || c == ':' || c == '.' || c == '-'
}

// stack parses sequences separated by commas.
func (p *parser) stack(depth int) (node, error) {
	var kids []node
	for {
		s, err := p.sequence(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, s)
		p.ws()
		if p.peek() == ',' {
			p.i++
			continue
		}
		break
	}
	if len(kids) == 1 {
		return kids[0], nil
	}
	return stack{kids}, nil
}

func (p *parser) sequence(depth int) (node, error) {
	steps, err := p.steps(depth)
	if err != nil {
		return nil, err
	}
	return newSeq(steps), nil
}

func (p *parser) steps(depth int) ([]step, error) {
	if depth > 16 {
		return nil, &ParseError{p.i, "nested too deeply"}
	}
	var steps []step
	for {
		p.ws()
		c := p.peek()
		if c == 0 || c == ']' || c == '>' || c == ',' {
			return steps, nil
		}
		switch c {
		case '_':
			if p.i+1 >= len(p.s) || !isWordByte(p.s[p.i+1]) {
				if len(steps) == 0 {
					return nil, &ParseError{p.i, "'_' needs a step before it"}
				}
				steps[len(steps)-1].weight++
				p.i++
				continue
			}
		case '!':
			if p.i+1 >= len(p.s) || p.s[p.i+1] == ' ' || p.s[p.i+1] == ']' || p.s[p.i+1] == '>' {
				if len(steps) == 0 {
					return nil, &ParseError{p.i, "'!' needs a step before it"}
				}
				steps = append(steps, steps[len(steps)-1])
				p.i++
				continue
			}
		}
		st, err := p.stepWithMods(depth)
		if err != nil {
			return nil, err
		}
		steps = append(steps, st...)
	}
}

func (p *parser) term(depth int) (node, error) {
	c := p.peek()
	switch {
	case c == '~':
		p.i++
		return silence{}, nil
	case c == '[':
		open := p.i
		p.i++
		n, err := p.stack(depth + 1)
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.peek() != ']' {
			return nil, &ParseError{open, "unclosed '['"}
		}
		p.i++
		return n, nil
	case c == '<':
		open := p.i
		p.i++
		var kids []node
		for {
			steps, err := p.steps(depth + 1)
			if err != nil {
				return nil, err
			}
			for _, st := range steps {
				for w := int64(0); w < st.weight; w++ {
					kids = append(kids, st.n)
				}
			}
			p.ws()
			if p.peek() == ',' { // <a b, c d> : stacked alternations
				p.i++
				continue
			}
			break
		}
		if p.peek() != '>' {
			return nil, &ParseError{open, "unclosed '<'"}
		}
		p.i++
		if len(kids) == 0 {
			return silence{}, nil
		}
		return alt{kids}, nil
	case isWordByte(c):
		start := p.i
		for p.i < len(p.s) && isWordByte(p.s[p.i]) {
			p.i++
		}
		w := p.s[start:p.i]
		if p.validate != nil {
			if err := p.validate(w); err != nil {
				return nil, &ParseError{start, err.Error()}
			}
		}
		return atom{w}, nil
	}
	return nil, &ParseError{p.i, fmt.Sprintf("unexpected %q", c)}
}

func (p *parser) number() (frac.F, error) {
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
		p.i++
	}
	if start == p.i {
		return frac.F{}, &ParseError{start, "expected a number"}
	}
	f, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil || f <= 0 || f > 1024 {
		return frac.F{}, &ParseError{start, "number out of range"}
	}
	if !strings.Contains(p.s[start:p.i], ".") {
		n, _ := strconv.ParseInt(p.s[start:p.i], 10, 64)
		return frac.Int(n), nil
	}
	return frac.FromFloat(f), nil
}

func (p *parser) integer() (int, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	n, err := strconv.Atoi(p.s[start:p.i])
	if err != nil {
		return 0, &ParseError{start, "expected an integer"}
	}
	return n, nil
}

func (p *parser) stepWithMods(depth int) ([]step, error) {
	n, err := p.term(depth)
	if err != nil {
		return nil, err
	}
	weight := int64(1)
	copies := 1
	for {
		switch p.peek() {
		case '*', '/':
			op := p.peek()
			p.i++
			f, err := p.number()
			if err != nil {
				return nil, err
			}
			if op == '/' {
				f = frac.Int(1).Div(f)
			}
			n = fast{n, f}
		case '!':
			p.i++
			if c := p.peek(); c >= '0' && c <= '9' {
				k, err := p.integer()
				if err != nil || k < 1 || k > 64 {
					return nil, &ParseError{p.i, "repeat count out of range"}
				}
				copies = k
			} else {
				copies++
			}
		case '@':
			p.i++
			k, err := p.integer()
			if err != nil || k < 1 || k > 64 {
				return nil, &ParseError{p.i, "weight out of range"}
			}
			weight = int64(k)
		case '?':
			p.i++
			prob := 0.5
			if c := p.peek(); c >= '0' && c <= '9' || c == '.' {
				start := p.i
				for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
					p.i++
				}
				f, err := strconv.ParseFloat(p.s[start:p.i], 64)
				if err != nil || f < 0 || f > 1 {
					return nil, &ParseError{start, "probability must be 0..1"}
				}
				prob = f
			}
			p.seed++
			n = degrade{n, prob, p.seed}
		case '(':
			open := p.i
			p.i++
			p.ws()
			k, err := p.integer()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.peek() != ',' {
				return nil, &ParseError{p.i, "expected ',' in euclid (k,n)"}
			}
			p.i++
			p.ws()
			steps, err := p.integer()
			if err != nil {
				return nil, err
			}
			p.ws()
			rot := 0
			if p.peek() == ',' {
				p.i++
				p.ws()
				if rot, err = p.integer(); err != nil {
					return nil, err
				}
				p.ws()
			}
			if p.peek() != ')' {
				return nil, &ParseError{open, "unclosed '('"}
			}
			p.i++
			if steps < 1 || steps > 64 || k < 0 {
				return nil, &ParseError{open, "euclid steps out of range"}
			}
			bits := euclid(k, steps)
			sts := make([]step, steps)
			for i := range sts {
				if bits[((i+rot)%steps+steps)%steps] {
					sts[i] = step{n, 1}
				} else {
					sts[i] = step{silence{}, 1}
				}
			}
			n = newSeq(sts)
		default:
			out := make([]step, copies)
			for i := range out {
				out[i] = step{n, weight}
			}
			return out, nil
		}
	}
}
