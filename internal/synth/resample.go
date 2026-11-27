package synth

import "math"

// Band-limited sample-rate conversion for loading samples: a windowed-sinc
// (Kaiser) filter, so a 44.1 kHz or 96 kHz file plays at 48 kHz without the
// dulling and aliasing of interpolating between neighbours. It runs once per
// file at load time, never while audio is rendering.

const (
	sincZeros = 24  // zero crossings each side of the kernel's centre
	sincRes   = 512 // table points per zero crossing
	sincBeta  = 9.0 // Kaiser window shape: stopband ~-90 dB
	passband  = 0.95
)

// sincTable holds the windowed sinc for u = 0 .. sincZeros, sincRes per unit.
var sincTable = func() []float64 {
	t := make([]float64, sincZeros*sincRes+2)
	i0b := besselI0(sincBeta)
	for i := range t {
		u := float64(i) / sincRes
		if u >= sincZeros {
			continue
		}
		w := besselI0(sincBeta*math.Sqrt(1-(u/sincZeros)*(u/sincZeros))) / i0b
		s := 1.0
		if u > 0 {
			s = math.Sin(math.Pi*u) / (math.Pi * u)
		}
		t[i] = s * w
	}
	return t
}()

func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 64; k++ {
		term *= (x / 2) / float64(k)
		sum += term * term
		if term*term < sum*1e-17 {
			break
		}
	}
	return sum
}

func kernel(u float64) float64 {
	u = math.Abs(u) * sincRes
	i := int(u)
	if i >= sincZeros*sincRes {
		return 0
	}
	f := u - float64(i)
	return sincTable[i]*(1-f) + sincTable[i+1]*f
}

// resample converts x from rate in to rate out. Content above 95% of the
// lower rate's Nyquist frequency is removed first, so nothing folds back.
// Common ratios (44.1 to 48 kHz has 160 phases, halving has one) use a table
// of per-phase coefficients, so the work is plain multiply-adds.
func resample(x []float32, in, out int) []float32 {
	if in == out || len(x) == 0 {
		return append([]float32(nil), x...)
	}
	n := int(int64(len(x)) * int64(out) / int64(in))
	y := make([]float32, n)
	fc := passband * math.Min(1, float64(out)/float64(in)) // cutoff, in input Nyquists
	R := int(math.Ceil(float64(sincZeros) / fc))           // taps each side
	g := gcd(in, out)
	L, M := out/g, in/g // output j sits at input j*M/L
	if L > 4096 {       // an odd rate: work each coefficient out as needed
		step := float64(in) / float64(out)
		for j := range y {
			t := float64(j) * step
			base := int(t)
			var acc float64
			for k := max(base-R+1, 0); k <= min(base+R, len(x)-1); k++ {
				acc += float64(x[k]) * kernel(fc*(t-float64(k)))
			}
			y[j] = float32(acc * fc)
		}
		return y
	}
	coef := make([][]float64, L)
	for p := range coef {
		c := make([]float64, 2*R)
		frac := float64(p) / float64(L)
		for q := range c { // tap q reads x[base-R+1+q], at distance frac+R-1-q
			c[q] = fc * kernel(fc*(frac+float64(R-1-q)))
		}
		coef[p] = c
	}
	for j := range y {
		pos := int64(j) * int64(M)
		base, c := int(pos/int64(L)), coef[pos%int64(L)]
		lo := base - R + 1
		var acc float64
		if lo >= 0 && lo+2*R <= len(x) {
			for q, w := range c {
				acc += float64(x[lo+q]) * w
			}
		} else {
			for q, w := range c {
				if k := lo + q; k >= 0 && k < len(x) {
					acc += float64(x[k]) * w
				}
			}
		}
		y[j] = float32(acc)
	}
	return y
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Playback interpolation, for samples played at other than normal speed: a
// short windowed sinc (24 taps), read from a table with the value and the
// step to the next point side by side. It is far cleaner than a cubic for
// bright sounds (a tone at a quarter of the rate: ~94 dB clean, cubic 23 dB).
const (
	playZeros = 12
	playRes   = 256
	playBeta  = 8.0
)

var playTable = func() [][2]float32 {
	n := playZeros*playRes + 2
	v := make([]float64, n)
	i0b := besselI0(playBeta)
	for i := range v {
		u := float64(i) / playRes
		if u >= playZeros {
			continue
		}
		w := besselI0(playBeta*math.Sqrt(1-(u/playZeros)*(u/playZeros))) / i0b
		s := 1.0
		if u > 0 {
			s = math.Sin(math.Pi*u) / (math.Pi * u)
		}
		v[i] = s * w
	}
	t := make([][2]float32, n)
	for i := 0; i+1 < n; i++ {
		t[i] = [2]float32{float32(v[i]), float32(v[i+1] - v[i])}
	}
	return t
}()

// interpolate reads x between x[i] and x[i+1], f (0..1) of the way.
func interpolate(x []float32, i int, f float32) float32 {
	uf := f * playRes
	iu := int(uf)
	fr := uf - float32(iu)
	if iu >= playRes { // f rounded up to 1 in float32
		iu, fr = playRes-1, 1
	} else if iu < 0 {
		iu, fr = 0, 0
	}
	gr := 1 - fr
	var acc float32
	if i-playZeros+1 >= 0 && i+playZeros < len(x) {
		for m := 0; m < playZeros; m++ { // x[i-m], distance m+f
			e := playTable[m*playRes+iu]
			acc += x[i-m] * (e[0] + e[1]*fr)
		}
		for m := 1; m <= playZeros; m++ { // x[i+m], distance m-f
			e := playTable[m*playRes-iu-1]
			acc += x[i+m] * (e[0] + e[1]*gr)
		}
		return acc
	}
	for m := 0; m < playZeros; m++ { // near an end: outside is silence
		if j := i - m; j >= 0 && j < len(x) {
			e := playTable[m*playRes+iu]
			acc += x[j] * (e[0] + e[1]*fr)
		}
	}
	for m := 1; m <= playZeros; m++ {
		if j := i + m; j >= 0 && j < len(x) {
			e := playTable[m*playRes-iu-1]
			acc += x[j] * (e[0] + e[1]*gr)
		}
	}
	return acc
}
