package synth

import (
	"encoding/binary"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeWAV writes a 16-bit WAV with ch channels; f gives channel c of frame i.
func writeWAV(t *testing.T, path string, rate, ch, frames int, f func(i, c int) float64) {
	t.Helper()
	n := frames * ch
	b := make([]byte, 44+n*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(ch))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*ch*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(ch*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n*2))
	for i := 0; i < frames; i++ {
		for c := 0; c < ch; c++ {
			binary.LittleEndian.PutUint16(b[44+(i*ch+c)*2:], uint16(int16(math.Round(f(i, c)*30000))))
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func playSample(t *testing.T, word string, p Params, frames int) (l, r []float64) {
	t.Helper()
	if p.Gain == 0 {
		p.Gain = 1
	}
	v, err := New(word, 48000, 0, 0, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < frames && !v.Done(); i++ {
		a, b := v.Next()
		l, r = append(l, float64(a)), append(r, float64(b))
	}
	return l, r
}

func powerSpectrum(x []float64) []float64 {
	n := 1
	for n*2 <= len(x) {
		n *= 2
	}
	a := make([]complex128, n)
	for i := range a {
		// 4-term Blackman-Harris: sidelobes 92 dB down, so leakage does not
		// pass for distortion
		p := 2 * math.Pi * float64(i) / float64(n)
		w := 0.35875 - 0.48829*math.Cos(p) + 0.14128*math.Cos(2*p) - 0.01168*math.Cos(3*p)
		a[i] = complex(x[i]*w, 0)
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wn := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := a[i+k], a[i+k+l/2]*wn
				a[i+k], a[i+k+l/2] = u+v, u-v
				wn *= w
			}
		}
	}
	p := make([]float64, n/2)
	for i := range p {
		p[i] = real(a[i])*real(a[i]) + imag(a[i])*imag(a[i])
	}
	return p
}

// purity is how far everything except the wanted tone sits below it, in dB.
func purity(x []float64, hz float64) float64 {
	p := powerSpectrum(x)
	c := int(math.Round(hz * float64(len(p)*2) / 48000))
	var sig, rest float64
	for k := 1; k < len(p); k++ {
		if k >= c-12 && k <= c+12 {
			sig += p[k]
		} else {
			rest += p[k]
		}
	}
	return 10 * math.Log10(sig/rest)
}

func rmsOf(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

func loadTones(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, hz := range []int{1000, 8000, 15000} {
		writeWAV(t, filepath.Join(dir, "t"+itoa(hz)+".wav"), 44100, 1, 44100, func(i, _ int) float64 {
			return math.Sin(2 * math.Pi * float64(hz) * float64(i) / 44100)
		})
	}
	if _, _, err := LoadSamples(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { LoadSamples(t.TempDir()) })
}

func itoa(n int) string {
	b := []byte{}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// A 44.1 kHz file plays at 48 kHz at the right pitch and level, and clean:
// linear interpolation left a 15 kHz tone only 11 dB above its own junk.
func TestResampledSamplesAreClean(t *testing.T) {
	loadTones(t)
	for _, c := range []struct {
		word string
		hz   float64
		want float64
	}{{"t1000", 1000, 70}, {"t8000", 8000, 70}, {"t15000", 15000, 70}} {
		l, _ := playSample(t, c.word, Params{}, 48000)
		if len(l) < 47900 || len(l) > 48000 {
			t.Errorf("%s: one second at 44.1 kHz played for %d frames at 48 kHz", c.word, len(l))
		}
		mid := l[2000:44000]
		got := purity(mid, c.hz)
		// equal-power centre pan: 0.9 * 30000/32768 * cos(pi/4) peak, rms / sqrt 2
		level := rmsOf(mid) / (0.9 * 30000 / 32768 * math.Cos(math.Pi/4) / math.Sqrt2)
		t.Logf("%s: everything else %.1f dB down, level %.3f", c.word, got, level)
		if got < c.want {
			t.Errorf("%s: unwanted energy only %.1f dB down, want %.0f", c.word, got, c.want)
		}
		if level < 0.98 || level > 1.02 {
			t.Errorf("%s: level %.3f of the source, want 1", c.word, level)
		}
	}
}

// Playing fast keeps tones that stay under Nyquist clean, and removes ones
// that would go over it instead of folding them back as false notes.
func TestFastPlaybackDoesNotAlias(t *testing.T) {
	loadTones(t)
	for _, c := range []struct {
		word  string
		hz    float64
		speed float64
	}{{"t8000", 8000, 1.1}, {"t8000", 8000, 1.7}, {"t15000", 15000, 0.9}, {"t1000", 1000, 2.5}, {"t1000", 1000, 7}, {"t8000", 8000, 0.7}} {
		l, _ := playSample(t, c.word, Params{Speed: c.speed}, 48000)
		mid := l[len(l)/8 : len(l)*7/8]
		got := purity(mid, c.hz*c.speed)
		t.Logf("%s at speed %.1f: everything else %.1f dB down", c.word, c.speed, got)
		if got < 60 {
			t.Errorf("%s at speed %.1f: only %.1f dB clean, want 60", c.word, c.speed, got)
		}
	}
	// 15 kHz at speed 2 would be 30 kHz: it must be filtered, not folded to 18 kHz
	l, _ := playSample(t, "t15000", Params{Speed: 2}, 24000)
	ref, _ := playSample(t, "t15000", Params{}, 24000)
	drop := 20 * math.Log10(rmsOf(ref[2000:20000])/rmsOf(l[1000:10000]))
	t.Logf("15 kHz at speed 2 (above Nyquist): %.1f dB quieter than at speed 1", drop)
	if drop < 50 {
		t.Errorf("15 kHz at speed 2 only %.1f dB down: it aliases", drop)
	}
}

func TestStereoSamplesStayStereo(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, filepath.Join(dir, "left.wav"), 48000, 2, 4800, func(i, c int) float64 {
		if c == 1 {
			return 0
		}
		return 0.5 * math.Sin(float64(i)*0.1)
	})
	writeWAV(t, filepath.Join(dir, "dual.wav"), 44100, 2, 4410, func(i, c int) float64 { return 0.5 * math.Sin(float64(i)*0.1) })
	if _, _, err := LoadSamples(dir); err != nil {
		t.Fatal(err)
	}
	defer LoadSamples(t.TempDir())
	l, r := playSample(t, "left", Params{}, 4000)
	if rmsOf(l) < 0.1 || rmsOf(r) != 0 {
		t.Fatalf("left-only sample: left rms %.3f, right rms %.3f", rmsOf(l), rmsOf(r))
	}
	// pan is a balance on stereo samples
	l, r = playSample(t, "left", Params{Pan: 1}, 4000)
	if rmsOf(l) > 1e-6 || rmsOf(r) != 0 {
		t.Fatalf("hard right pan on a left-only sample should be silent: rms %.4f, %.4f", rmsOf(l), rmsOf(r))
	}
	if s := lookupSample("dual", 0); s.Stereo() {
		t.Fatal("a stereo file with two identical channels should load as mono")
	}
}

func TestLoadingIsBoundedAndQuick(t *testing.T) {
	dir := t.TempDir()
	const secs = 10
	writeWAV(t, filepath.Join(dir, "long.wav"), 44100, 2, 44100*secs, func(i, c int) float64 {
		return 0.3 * math.Sin(float64(i)*0.01*float64(c+1))
	})
	t0 := time.Now()
	if _, _, err := LoadSamples(dir); err != nil {
		t.Fatal(err)
	}
	defer LoadSamples(t.TempDir())
	took := time.Since(t0)
	s := lookupSample("long", 0)
	t.Logf("10 s stereo 44.1 kHz file: loaded in %v, %d floats (estimate %d)", took, s.floats(), s.prepared())
	if s.floats() > s.prepared() {
		t.Errorf("memory estimate %d is below the real %d", s.prepared(), s.floats())
	}
	if s.Rate != PlayRate || len(s.oct) != octaves {
		t.Errorf("rate %d, %d octave copies", s.Rate, len(s.oct))
	}
}

func BenchmarkFastSample(b *testing.B) {
	dir := b.TempDir()
	tb := &testing.T{}
	writeWAV(tb, filepath.Join(dir, "bench.wav"), 48000, 2, 48000*4, func(i, c int) float64 { return 0.3 * math.Sin(float64(i)*0.05*float64(c+1)) })
	LoadSamples(dir)
	defer LoadSamples(b.TempDir())
	v, _ := New("bench", 48000, 0, 0, Params{Gain: 1, Speed: 1.37}, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if v.Done() {
			v, _ = New("bench", 48000, 0, 0, Params{Gain: 1, Speed: 1.37}, 1)
		}
		v.Next()
	}
}

// Odd rates take the slow path, and high-rate files lose only what 48 kHz
// cannot hold.
func TestOtherRates(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, filepath.Join(dir, "odd.wav"), 44099, 1, 44099, func(i, _ int) float64 { return math.Sin(2 * math.Pi * 5000 * float64(i) / 44099) })
	writeWAV(t, filepath.Join(dir, "hi.wav"), 96000, 1, 96000, func(i, _ int) float64 {
		return 0.5*math.Sin(2*math.Pi*3000*float64(i)/96000) + 0.4*math.Sin(2*math.Pi*30000*float64(i)/96000)
	})
	if _, _, err := LoadSamples(dir); err != nil {
		t.Fatal(err)
	}
	defer LoadSamples(t.TempDir())
	l, _ := playSample(t, "odd", Params{}, 48000)
	if got := purity(l[2000:44000], 5000); got < 70 {
		t.Errorf("44099 Hz file: only %.1f dB clean", got)
	}
	l, _ = playSample(t, "hi", Params{}, 48000)
	if got := purity(l[2000:44000], 3000); got < 70 {
		t.Errorf("96 kHz file: the 30 kHz part folded back, only %.1f dB clean", got)
	}
}
