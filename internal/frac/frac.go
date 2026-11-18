// Package frac provides exact rational arithmetic for pattern time.
package frac

import "math"

// F is a rational number N/D with D > 0, always in lowest terms.
type F struct{ N, D int64 }

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// New returns n/d normalised. d must be non-zero.
func New(n, d int64) F {
	if d < 0 {
		n, d = -n, -d
	}
	g := gcd(n, d)
	return F{n / g, d / g}
}

// Int returns n/1.
func Int(n int64) F { return F{n, 1} }

func (a F) Add(b F) F { return New(a.N*b.D+b.N*a.D, a.D*b.D) }
func (a F) Sub(b F) F { return New(a.N*b.D-b.N*a.D, a.D*b.D) }
func (a F) Mul(b F) F { return New(a.N*b.N, a.D*b.D) }
func (a F) Div(b F) F { return New(a.N*b.D, a.D*b.N) }

// Cmp returns -1, 0 or 1.
func (a F) Cmp(b F) int {
	l, r := a.N*b.D, b.N*a.D
	switch {
	case l < r:
		return -1
	case l > r:
		return 1
	}
	return 0
}

func (a F) Lt(b F) bool { return a.Cmp(b) < 0 }
func (a F) Le(b F) bool { return a.Cmp(b) <= 0 }
func (a F) Gt(b F) bool { return a.Cmp(b) > 0 }
func (a F) Ge(b F) bool { return a.Cmp(b) >= 0 }
func (a F) Eq(b F) bool { return a.Cmp(b) == 0 }

// Floor returns the largest integer <= a.
func (a F) Floor() int64 {
	q := a.N / a.D
	if a.N%a.D != 0 && a.N < 0 {
		q--
	}
	return q
}

// Ceil returns the smallest integer >= a.
func (a F) Ceil() int64 {
	q := a.N / a.D
	if a.N%a.D != 0 && a.N > 0 {
		q++
	}
	return q
}

func (a F) Float() float64 { return float64(a.N) / float64(a.D) }

// FromFloat approximates f with denominator 1<<20 (used for user numbers).
func FromFloat(f float64) F {
	const d = 1 << 20
	return New(int64(math.Round(f*d)), d)
}
