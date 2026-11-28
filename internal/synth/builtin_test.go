package synth

import (
	"math"
	"testing"
)

func TestBuiltinSamples(t *testing.T) {
	names := BuiltinSamples()
	if len(names) != 206 {
		t.Errorf("%d built-in samples, want Sonic Pi's 206", len(names))
	}
	sec, ok := SampleSeconds("loop_amen")
	if !ok || math.Abs(sec-1.7533) > 0.001 {
		t.Errorf("loop_amen is %v s (%v), Sonic Pi says 1.7533", sec, ok)
	}
	w, err := ResolveSample("loop_amen", 0)
	if err != nil || w != "@sp.loop_amen" {
		t.Fatalf("%q %v", w, err)
	}
	v, err := New(w, 48000, 0, 0, Params{Gain: 1, Speed: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for !v.Done() && n < 48000*3 {
		v.Next()
		n++
	}
	if got := float64(n) / 48000; math.Abs(got-1.7533) > 0.01 {
		t.Errorf("played for %.3f s", got)
	}
}

// playLen plays a sample voice to its end and returns its length in frames
// and its first non-silent frame.
func playLen(t *testing.T, p Params) (int, float32) {
	t.Helper()
	v, err := New("@sp.loop_amen", 48000, 0, 0, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var first float32
	for !v.Done() && n < 48000*5 {
		l, _ := v.Next()
		if first == 0 && l != 0 {
			first = l
		}
		n++
	}
	return n, first
}

func TestSampleOptions(t *testing.T) {
	if _, err := ResolveSample("loop_amen", 0); err != nil {
		t.Fatal(err)
	}
	full, fwd := playLen(t, Params{Gain: 1, Speed: 1})
	half, _ := playLen(t, Params{Gain: 1, Speed: 1, Start: 0.5})
	if d := math.Abs(float64(half) - float64(full)/2); d > 50 {
		t.Errorf("start: 0.5 plays %d of %d frames", half, full)
	}
	slice, _ := playLen(t, Params{Gain: 1, Speed: 1, Start: 0.25, Finish: 0.5})
	if d := math.Abs(float64(slice) - float64(full)/4); d > 50 {
		t.Errorf("a quarter plays %d of %d frames", slice, full)
	}
	rev, back := playLen(t, Params{Gain: 1, Speed: 1, Reverse: true})
	if d := math.Abs(float64(rev - full)); d > 50 || back == fwd {
		t.Errorf("reverse: %d frames (forward %d), first value %v vs %v", rev, full, back, fwd)
	}
	held, _ := playLen(t, Params{Gain: 1, Speed: 1, Held: 0.5, Release: 0.25})
	if d := math.Abs(float64(held) - 0.75*48000); d > 50 {
		t.Errorf("sustain 0.5 + release 0.25 plays %d frames", held)
	}
}
