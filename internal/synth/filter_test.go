package synth

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

const fsr = 48000.0

// response is the filter's gain in dB at hz: a settled sine in, compared out.
func response(f Filter, hz float64) float64 {
	in := audiotest.Sine(fsr, hz, 24000)
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(f.Process(float32(v)))
	}
	return audiotest.GainDB(in[12000:], out[12000:])
}

func TestLowPassIs24dBPerOctave(t *testing.T) {
	fc := 1000.0
	f := NewLowPass(fc, 0, fsr)
	pass, at, oct2, oct3 := response(f, fc/8), response(f, fc), response(f, fc*2), response(f, fc*4)
	t.Logf("low-pass 1 kHz: %.2f dB at 125 Hz, %.2f at 1 kHz, %.1f at 2 kHz, %.1f at 4 kHz", pass, at, oct2, oct3)
	if math.Abs(pass) > 0.1 || math.Abs(at+3) > 0.3 {
		t.Errorf("passband %.2f dB, at cutoff %.2f dB (want 0, -3)", pass, at)
	}
	if slope := oct2 - oct3; math.Abs(slope-24) > 1.5 {
		t.Errorf("slope %.1f dB/octave, want 24", slope)
	}
}

func TestHighPassIs12dBPerOctave(t *testing.T) {
	f := NewHighPass(1000, 0, fsr)
	pass, slope := response(f, 8000), response(f, 250)-response(f, 125)
	if math.Abs(pass) > 0.2 || math.Abs(slope-12) > 1 {
		t.Errorf("high-pass: passband %.2f dB, slope %.1f dB/octave (want 0, 12)", pass, slope)
	}
}

// res: lifts the response at the cutoff, as on Sonic Pi's resonant synths.
func TestResonancePeaksAtTheCutoff(t *testing.T) {
	var last float64 = -100
	for _, res := range []float64{0, 0.3, 0.6, 0.9} {
		at := response(NewLowPass(800, res, fsr), 800)
		t.Logf("res %.1f: %.1f dB at the cutoff", res, at)
		if at <= last {
			t.Errorf("res %.1f: peak %.1f dB is not above res below it (%.1f)", res, at, last)
		}
		last = at
	}
	if last < 12 {
		t.Errorf("res 0.9 peak only %.1f dB", last)
	}
}

// Fast sweeps at full resonance stay bounded, and a cutoff jump glides.
func TestFilterIsStableAndGlides(t *testing.T) {
	f := NewLowPass(200, 1, fsr)
	var rng uint32 = 1
	peak := 0.0
	for i := 0; i < fsr*4; i++ {
		if i%64 == 0 { // jump between 30 Hz and 20 kHz every 64 samples
			hz := 30 * math.Pow(666, float64((i/64)%7)/6)
			f.Set(hz, 1, fsr)
		}
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		y := float64(f.Process(float32(rng)/math.MaxUint32*2 - 1))
		if math.IsNaN(y) {
			t.Fatal("NaN")
		}
		peak = math.Max(peak, math.Abs(y))
	}
	if peak > 60 {
		t.Fatalf("output reached %.1f on full-scale noise", peak)
	}
	g := NewLowPass(500, 0.5, fsr)
	tone := audiotest.Sine(fsr, 220.7, 9600)
	var pre, post []float64
	for i, v := range tone {
		if i == 4800 {
			g.Set(4000, 0.5, fsr)
		}
		y := float64(g.Process(float32(v)))
		if i < 4800 {
			pre = append(pre, y)
		} else {
			post = append(post, y)
		}
	}
	if r := audiotest.ClickRatio(pre, post); r > 1.5 {
		t.Fatalf("cutoff jump clicks: step ratio %.1f", r)
	}
}

func BenchmarkLowPass(b *testing.B) {
	f := NewLowPass(1000, 0.5, fsr)
	x := float32(0.1)
	for i := 0; i < b.N; i++ {
		x = f.Process(x) + 0.1
	}
}
