package engine

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

// impulse returns the reverb's response to a click in both channels, with
// the settings applied at once (no glide).
func impulse(size, damp float64, seconds float64) (l, r []float64) {
	rv := newReverb()
	rv.set(size, damp)
	for i := range rv.lines {
		rv.lines[i].g, rv.lines[i].a = rv.lines[i].gT, rv.lines[i].aT
	}
	n := int(seconds * SampleRate)
	in, out := make([]float32, n*2), make([]float32, n*2)
	in[0], in[1] = 1, 1
	rv.process(out, in)
	return audiotest.Split(out)
}

func sumSquares(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v * v
	}
	return s
}

// The tail decays in the time the room size asks for.
func TestReverbDecaysAsSet(t *testing.T) {
	for _, size := range []float64{0.2, 0.6, 1} {
		l, _ := impulse(size, 0, 10)
		got, want := audiotest.DecayTime(l, SampleRate), DecayTime(size)
		t.Logf("size %.1f: RT60 %.2f s, want %.2f", size, got, want)
		if math.Abs(got-want)/want > 0.15 {
			t.Errorf("size %.1f: RT60 %.2f s, want %.2f ±15%%", size, got, want)
		}
	}
}

// The tail is dense and smooth: the replaced reverb measured echo density
// 0.35 at 100 ms and 0.56 at 300 ms, and 7-13 dB of spectral roughness
// (ringing).
func TestReverbTailIsDenseSmoothAndWide(t *testing.T) {
	for _, damp := range []float64{0, 0.5} {
		l, r := impulse(0.6, damp, 4)
		early, late := audiotest.EchoDensity(l, SampleRate, 0.1, 0.02), audiotest.EchoDensity(l, SampleRate, 0.3, 0.05)
		rough := audiotest.Roughness(l[4800:4800+65536], SampleRate, 200, 8000, 100)
		corr := audiotest.Correlation(l[2400:], r[2400:])
		t.Logf("damp %.1f: density %.2f at 100 ms, %.2f at 300 ms; roughness %.1f dB; L/R correlation %.2f", damp, early, late, rough, corr)
		if early < 0.65 || late < 0.85 {
			t.Errorf("damp %.1f: tail too sparse (%.2f, %.2f)", damp, early, late)
		}
		if rough > 3 {
			t.Errorf("damp %.1f: tail rings, roughness %.1f dB", damp, rough)
		}
		if math.Abs(corr) > 0.2 {
			t.Errorf("damp %.1f: tail too narrow, correlation %.2f", damp, corr)
		}
	}
}

// Damping makes treble die before bass.
func TestReverbDampingDarkensTheTail(t *testing.T) {
	ratio := func(damp float64) float64 {
		l, _ := impulse(0.6, damp, 2)
		seg := func(at float64) []float64 { a := int(at * SampleRate); return l[a : a+8192] }
		band := func(p []float64, f0, f1 float64) float64 {
			var s float64
			for k := int(f0 * 16384 / SampleRate); k < int(f1*16384/SampleRate); k++ {
				s += p[k]
			}
			return s
		}
		e, late := audiotest.Spectrum(seg(0.05)), audiotest.Spectrum(seg(0.6))
		return (band(late, 5000, 9000) / band(e, 5000, 9000)) / (band(late, 200, 600) / band(e, 200, 600))
	}
	open, damped := ratio(0), ratio(1)
	t.Logf("treble left at 0.6 s relative to bass: damp 0 %.2f dB, damp 1 %.2f dB", audiotest.DB(open), audiotest.DB(damped))
	if audiotest.DB(open) < -3 || audiotest.DB(damped) > audiotest.DB(open)-15 {
		t.Fatalf("damping does not darken the tail: %.1f dB vs %.1f dB", audiotest.DB(open), audiotest.DB(damped))
	}
}

// Existing sets keep their balance: the default room is about as loud as
// the reverb it replaced (impulse energy 1.52).
func TestReverbKeepsItsLevel(t *testing.T) {
	l, r := impulse(0.6, 0.5, 8)
	got := audiotest.DB((sumSquares(l) + sumSquares(r)) / 1.5233)
	t.Logf("default room level vs the old reverb: %+.2f dB", got)
	if math.Abs(got) > 1 {
		t.Fatalf("default room is %+.1f dB from the old reverb", got)
	}
}

// Even the biggest, least damped room dies away, and changing the room
// while it rings glides instead of clicking.
func TestReverbIsStableAndChangesSmoothly(t *testing.T) {
	rv := newReverb()
	rv.set(1, 0)
	block := func(drive bool) []float64 {
		in, out := make([]float32, 960), make([]float32, 960)
		if drive {
			for i := range in {
				in[i] = float32(math.Sin(float64(i/2) * 0.031))
			}
		}
		rv.process(out, in)
		l, _ := audiotest.Split(out)
		return l
	}
	var pre []float64
	for i := 0; i < 200; i++ {
		pre = append(pre, block(true)...)
	}
	rv.set(0.1, 1)
	var post []float64
	for i := 0; i < 5; i++ {
		post = append(post, block(true)...)
	}
	if r := audiotest.ClickRatio(pre, post); r > 1.5 {
		t.Errorf("changing the room clicks: step ratio %.1f", r)
	}
	rv.set(1, 0)
	var e []float64
	for s := 0; s < 12; s++ {
		var sec []float64
		for i := 0; i < 100; i++ {
			sec = append(sec, block(false)...)
		}
		e = append(e, audiotest.RMS(sec))
	}
	for i := 1; i < len(e); i++ {
		if math.IsNaN(e[i]) || e[i] > e[i-1]*1.01 {
			t.Fatalf("tail grows or breaks at %d s: %v -> %v", i, e[i-1], e[i])
		}
	}
	if e[len(e)-1] > 1e-6 {
		t.Fatalf("tail still at %.2g after 12 s", e[len(e)-1])
	}
}

func BenchmarkReverb(b *testing.B) {
	rv := newReverb()
	in, out := make([]float32, 960), make([]float32, 960)
	for i := range in {
		in[i] = float32(math.Sin(float64(i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rv.process(out, in)
	}
}
