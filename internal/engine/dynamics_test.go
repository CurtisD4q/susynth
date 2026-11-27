package engine

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
	"github.com/susynth/susynth/internal/synth"
)

func stereoSine(hz, amp float64, n int) []float32 {
	x := make([]float32, n*2)
	for i := 0; i < n; i++ {
		v := float32(amp * math.Sin(2*math.Pi*hz*float64(i)/SampleRate))
		x[i*2], x[i*2+1] = v, v
	}
	return x
}

func dbOf(x []float32) float64 {
	l, _ := audiotest.Split(x)
	return 20 * math.Log10(audiotest.Peak(l))
}

// The compressor's static curve: unity below the threshold, the set slope
// above it (past the soft knee).
func TestCompressorCurve(t *testing.T) {
	for _, c := range []struct{ inDB, wantDB float64 }{
		{-30, -30},          // far below threshold 0.2 (-14 dB)
		{-14 + 12, -14 + 6}, // 12 dB over at 2:1 comes out 6 dB over
		{-14 + 18, -14 + 9},
	} {
		cp := compressor{set: DefaultComp}
		x := stereoSine(997, math.Pow(10, c.inDB/20), SampleRate)
		cp.process(x)
		got := dbOf(x[SampleRate:]) // the second half, settled
		if math.Abs(got-c.wantDB) > 0.4 {
			t.Errorf("in %.0f dB: out %.2f dB, want %.0f", c.inDB, got, c.wantDB)
		}
		l, _ := audiotest.Split(x[SampleRate:])
		if d := audiotest.THD(l[:16384], SampleRate, 997); d > 0.005 {
			t.Errorf("in %.0f dB: %.2f%% distortion from the gain moving", c.inDB, d*100)
		}
	}
}

func TestCompressorAttackAndStereoLink(t *testing.T) {
	cp := compressor{set: CompSettings{Threshold: 0.1, SlopeAbove: 0.25, Clamp: 0.01, Relax: 0.2}}
	n := SampleRate / 2
	x := make([]float32, n*2)
	for i := 0; i < n; i++ { // loud left, quiet right
		v := math.Sin(2 * math.Pi * 500 * float64(i) / SampleRate)
		x[i*2], x[i*2+1] = float32(0.8*v), float32(0.2*v)
	}
	cp.process(x)
	l, r := audiotest.Split(x)
	if ratio := audiotest.RMS(r[n/2:]) / audiotest.RMS(l[n/2:]); math.Abs(ratio-0.25) > 0.002 {
		t.Errorf("stereo image moved: right/left %.4f, want 0.25", ratio)
	}
	// the reduction is mostly in place within a few attack times
	early, late := audiotest.Peak(l[int(0.04*SampleRate):int(0.05*SampleRate)]), audiotest.Peak(l[n/2:])
	if 20*math.Log10(early/late) > 1 {
		t.Errorf("40 ms in, still %.1f dB above the settled level", 20*math.Log10(early/late))
	}
}

func eqGain(set EQSettings, hz float64) float64 {
	q := eq{set: set}
	x := stereoSine(hz, 0.5, SampleRate/2)
	in := append([]float32(nil), x...)
	for a := 0; a < len(x); a += 960 {
		q.process(x[a:min(a+960, len(x))])
	}
	l, _ := audiotest.Split(x)
	li, _ := audiotest.Split(in)
	return audiotest.GainDB(li[SampleRate/4:], l[SampleRate/4:])
}

func TestEQBands(t *testing.T) {
	flat := DefaultEQ
	for _, hz := range []float64{40, 1000, 12000} {
		if g := eqGain(flat, hz); math.Abs(g) > 1e-4 {
			t.Errorf("flat EQ changes %.0f Hz by %.5f dB", hz, g)
		}
	}
	set := DefaultEQ
	set.LowDB, set.MidDB, set.HighDB = 6, -6, 4
	for _, c := range []struct{ hz, want, tol float64 }{
		{30, 6, 0.5}, {1000, -6, 0.3}, {16000, 4, 0.5},
	} {
		if g := eqGain(set, c.hz); math.Abs(g-c.want) > c.tol {
			t.Errorf("%.0f Hz: %+.2f dB, want %+.0f", c.hz, g, c.want)
		}
	}
	// the bell (Q 0.6, about two octaves wide) dips less either side of it,
	// and the same both sides
	lo, hi := eqGain(set, 300), eqGain(set, 3333)
	if lo > -0.3 || lo < -3 || math.Abs(lo-hi) > 0.6 {
		t.Errorf("bell shoulders %+.2f dB (300 Hz) and %+.2f dB (3.3 kHz)", lo, hi)
	}
}

func TestChorusIsWideAndSmooth(t *testing.T) {
	c := &chorus{set: DefaultChorus, idle: true}
	in := stereoSine(440, 0.5, SampleRate*2)
	out := make([]float32, len(in))
	for a := 0; a < len(in); a += 960 {
		c.process(out[a:a+960], in[a:a+960])
	}
	l, r := audiotest.Split(out)
	l, r = l[SampleRate/2:], r[SampleRate/2:]
	corr := audiotest.Correlation(l, r)
	lvl := audiotest.GainDB(audiotest.Sine(SampleRate, 440, len(l)), l) - 20*math.Log10(0.5)
	t.Logf("chorus: L/R correlation %.2f, level %+.1f dB", corr, lvl)
	if corr > 0.6 {
		t.Errorf("chorus too narrow: correlation %.2f", corr)
	}
	if lvl < -8 || lvl > 1 {
		t.Errorf("chorus level %+.1f dB", lvl)
	}
	if s := audiotest.LargestStep(l); s > 0.1 {
		t.Errorf("chorus output jumps by %.3f", s)
	}
}

// A track inside with_fx :compressor is compressed; one outside is not.
func TestOnlyRoutedTracksAreCompressed(t *testing.T) {
	level := func(comp bool) float64 {
		e := New()
		e.SetCompressor(CompSettings{Threshold: 0.05, SlopeAbove: 0.2, Clamp: 0.005, Relax: 0.1})
		e.Play()
		e.Schedule(Hit{At: 0, Track: "a", Word: "60", Gate: SampleRate,
			Params: synth.Params{Wave: "sine", Note: 69, Gain: 1, Comp: comp}})
		var out []float32
		for i := 0; i < 50; i++ {
			out = append(out, e.Render(480)...)
		}
		return dbOf(out[len(out)/2:])
	}
	dry, comp := level(false), level(true)
	t.Logf("sine at gain 1: %.1f dBFS dry, %.1f dBFS through the compressor", dry, comp)
	if dry-comp < 6 {
		t.Fatalf("compressor took only %.1f dB off a routed track", dry-comp)
	}
}
