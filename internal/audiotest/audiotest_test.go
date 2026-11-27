package audiotest

import (
	"math"
	"testing"
)

const sr = 48000

func TestPureSineMeasuresClean(t *testing.T) {
	x := Sine(sr, 1234.5, 65536)
	if p := Purity(x, sr, 1234.5); p < 95 {
		t.Fatalf("pure sine purity %.1f dB", p)
	}
	if d := THD(x, sr, 1234.5); d > 1e-4 {
		t.Fatalf("pure sine THD %.6f", d)
	}
}

func TestClippedSineShowsDistortion(t *testing.T) {
	x := Sine(sr, 1000, 65536)
	for i := range x {
		x[i] = math.Max(-0.7, math.Min(0.7, x[i]))
	}
	if d := THD(x, sr, 1000); d < 0.05 {
		t.Fatalf("clipped sine THD only %.4f", d)
	}
}

func TestInharmonicSeesAliasing(t *testing.T) {
	// a naive saw at 3 kHz aliases badly; a sum of its true harmonics does not
	f := 3001.7
	naive, clean := make([]float64, 65536), make([]float64, 65536)
	for i := range naive {
		ph := math.Mod(f*float64(i)/sr, 1)
		naive[i] = 2*ph - 1
		for k := 1.0; k*f < sr/2; k++ {
			clean[i] -= 2 / math.Pi * math.Sin(2*math.Pi*k*f*float64(i)/sr) / k
		}
	}
	a, b := Inharmonic(naive, sr, f, 20000), Inharmonic(clean, sr, f, 20000)
	if a > 30 || b < 80 {
		t.Fatalf("naive saw %.1f dB, band-limited %.1f dB", a, b)
	}
}

func TestClickRatio(t *testing.T) {
	x := Sine(sr, 440.7, 4800)
	cut := make([]float64, 480)
	if r := ClickRatio(x, cut); r < 5 {
		t.Fatalf("a hard cut should click, ratio %.1f", r)
	}
	if r := ClickRatio(x[:2400], x[2400:]); r > 1.05 {
		t.Fatalf("a continuous signal should not click, ratio %.2f", r)
	}
}

func noise(n int, seed uint32) []float64 {
	x := make([]float64, n)
	for i := range x {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		x[i] = float64(seed)/math.MaxUint32*2 - 1
	}
	return x
}

// gauss is approximately Gaussian noise (a sum of 12 uniform values).
func gauss(n int, seed uint32) []float64 {
	u := noise(n*12, seed)
	x := make([]float64, n)
	for i := range x {
		for _, v := range u[i*12 : i*12+12] {
			x[i] += v / 2
		}
	}
	return x
}

func TestDecayTimeAndDensity(t *testing.T) {
	x := gauss(sr*3, 7)
	for i := range x { // 60 dB down in 1.2 s
		x[i] *= math.Pow(10, -3*float64(i)/sr/1.2)
	}
	if d := DecayTime(x, sr); math.Abs(d-1.2) > 0.05 {
		t.Fatalf("decay %.3f s, want 1.2", d)
	}
	if e := EchoDensity(x, sr, 0.2, 0.05); e < 0.85 || e > 1.15 {
		t.Fatalf("noise echo density %.2f, want ~1", e)
	}
	sparse := make([]float64, sr)
	for i := 0; i < len(sparse); i += 1500 {
		sparse[i] = 1
	}
	if e := EchoDensity(sparse, sr, 0.2, 0.05); e > 0.2 {
		t.Fatalf("sparse clicks echo density %.2f", e)
	}
}

func TestRoughnessAndCorrelation(t *testing.T) {
	x := noise(65536, 3)
	if r := Roughness(x, sr, 200, 8000, 100); r > 1.5 {
		t.Fatalf("white noise roughness %.2f dB", r)
	}
	ring := make([]float64, len(x))
	for i := range ring { // noise plus a strong resonance
		ring[i] = x[i] + 20*math.Sin(2*math.Pi*1500*float64(i)/sr)
	}
	if r := Roughness(ring, sr, 200, 8000, 100); r < 3 {
		t.Fatalf("resonant roughness %.2f dB", r)
	}
	if c := Correlation(x, noise(65536, 9)); math.Abs(c) > 0.05 {
		t.Fatalf("unrelated noise correlates %.2f", c)
	}
	if g := GainDB(x, scale(x, 0.5)); math.Abs(g+6.02) > 0.01 {
		t.Fatalf("half level reads %.2f dB", g)
	}
}

func scale(x []float64, g float64) []float64 {
	y := make([]float64, len(x))
	for i := range x {
		y[i] = x[i] * g
	}
	return y
}
