package lang

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/susynth/susynth/internal/mini"
)

// tokenize splits on spaces but keeps (parenthesised groups) together.
func tokenize(s string) ([]string, error) {
	var toks []string
	depth, start := 0, -1
	for i, r := range s {
		switch {
		case r == '(':
			if depth == 0 && start < 0 {
				start = i
			}
			depth++
		case r == ')':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced ')'")
			}
		case (r == ' ' || r == '\t') && depth == 0:
			if start >= 0 {
				toks = append(toks, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if depth != 0 {
		return nil, errors.New("unclosed '('")
	}
	if start >= 0 {
		toks = append(toks, s[start:])
	}
	return toks, nil
}

type transform func(*mini.Pattern) (*mini.Pattern, error)

// parseFn turns e.g. ["every","4","(fast 2)"] into a transform.
func parseFn(t []string) (transform, error) {
	name := t[0]
	num := func() (float64, error) {
		if len(t) != 2 {
			return 0, fmt.Errorf("%s needs one number", name)
		}
		f, err := strconv.ParseFloat(t[1], 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", t[1])
		}
		return f, nil
	}
	noArg := func() error {
		if len(t) != 1 {
			return fmt.Errorf("%s takes no arguments", name)
		}
		return nil
	}
	switch name {
	case "fast", "slow", "iter", "ply", "degradeBy", "rotL", "rotR":
		f, err := num()
		if err != nil {
			return nil, err
		}
		switch name {
		case "fast":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Fast(f) }, nil
		case "slow":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Slow(f) }, nil
		case "iter":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Iter(int(f)) }, nil
		case "ply":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Ply(int(f)) }, nil
		case "degradeBy":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.DegradeBy(f) }, nil
		case "rotL":
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Rot(f), nil }, nil
		default:
			return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Rot(-f), nil }, nil
		}
	case "rev":
		if err := noArg(); err != nil {
			return nil, err
		}
		return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Rev(), nil }, nil
	case "palindrome":
		if err := noArg(); err != nil {
			return nil, err
		}
		return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Palindrome(), nil }, nil
	case "every":
		if len(t) < 3 {
			return nil, errors.New("every needs a count and a function: every 4 (fast 2)")
		}
		n, err := strconv.Atoi(t[1])
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", t[1])
		}
		inner := t[2:]
		if len(inner) == 1 && strings.HasPrefix(inner[0], "(") && strings.HasSuffix(inner[0], ")") {
			if inner, err = tokenize(inner[0][1 : len(inner[0])-1]); err != nil || len(inner) == 0 {
				return nil, errors.New("empty or bad function in parentheses")
			}
		}
		fn, err := parseFn(inner)
		if err != nil {
			return nil, err
		}
		return func(p *mini.Pattern) (*mini.Pattern, error) { return p.Every(n, fn) }, nil
	}
	return nil, fmt.Errorf("unknown function %q (fast slow rev every iter palindrome degradeBy rotL rotR ply)", name)
}
