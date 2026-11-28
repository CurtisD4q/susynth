package synth

import (
	"math"
	"testing"
)

func TestFXUnitsAreFiniteAndQuietOnSilence(t *testing.T) {
	for _, name := range FXUnits() {
		f := NewFX(name, 48000, nil)
		in := make([]float32, 2*512)
		out := make([]float32, len(in))
		for blk := 0; blk < 40; blk++ {
			for i := range in {
				in[i] = float32(0.5 * math.Sin(float64(blk*len(in)+i)*0.05))
			}
			f.Process(out, in)
			for _, v := range out {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("%s: %v", name, v)
				}
			}
		}
		clear(in)
		for blk := 0; blk < 800; blk++ {
			f.Process(out, in)
		}
		for _, v := range out {
			if math.Abs(float64(v)) > 0.05 {
				t.Errorf("%s still sounds %.4f after %d s of silence", name, v, 800*512/48000)
				break
			}
		}
		if f.KillDelay() < 0 {
			t.Errorf("%s: kill delay %v", name, f.KillDelay())
		}
	}
}

func TestEchoIsACombFilter(t *testing.T) {
	f := NewFX("echo", 48000, map[string]float64{"phase": 0.25, "decay": 2})
	in := make([]float32, 2*48000)
	in[0], in[1] = 1, 1
	out := make([]float32, len(in))
	f.Process(out, in)
	// the dry click, then a copy each phase, each (10^-3 per decay) times the one before
	fb := math.Pow(0.001, 0.25/2)
	for k := 1; k <= 3; k++ {
		got := float64(out[2*k*12000])
		want := math.Pow(fb, float64(k-1))
		if math.Abs(got-want) > 1e-4 {
			t.Errorf("echo %d: %.4f, want %.4f", k, got, want)
		}
	}
}

func TestPingPongAlternatesSides(t *testing.T) {
	f := NewFX("ping_pong", 48000, map[string]float64{"phase": 0.25, "feedback": 0.6})
	in := make([]float32, 2*48000*2)
	in[0] = 1 // a click on the left
	out := make([]float32, len(in))
	f.Process(out, in)
	at := func(k, ch int) float64 { return math.Abs(float64(out[2*k*12000+ch])) }
	if at(1, 1) < 0.99 || at(1, 0) > 0.01 || at(2, 0) < 0.59 || at(2, 1) > 0.01 || at(3, 1) < 0.35 {
		t.Errorf("echoes: %v %v / %v %v / %v %v", at(1, 0), at(1, 1), at(2, 0), at(2, 1), at(3, 0), at(3, 1))
	}
}

func TestRecordFillsABufferThatSamplePlays(t *testing.T) {
	f := NewFX("record", 48000, map[string]float64{"buffer": 77, "seconds": 1})
	in := make([]float32, 2*100)
	for i := range in {
		in[i] = float32(i%7) / 10
	}
	out := make([]float32, len(in))
	f.Process(out, in)
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("record changed the sound at %d", i)
		}
	}
	s := lookupSample("@rec77", 0)
	if s == nil || len(s.Data) != 48000 {
		t.Fatalf("no buffer: %+v", s)
	}
	if s.Data[3] != in[6] || s.Right[3] != in[7] {
		t.Errorf("recorded %v %v, want %v %v", s.Data[3], s.Right[3], in[6], in[7])
	}
}
