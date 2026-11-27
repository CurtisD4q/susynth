package synth

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

func levels(e Envelope) []float64 {
	var out []float64
	for !e.Done() {
		out = append(out, float64(e.Next()))
	}
	return out
}

func TestEnvelopeHitsEveryLevelOnTime(t *testing.T) {
	sr := 1000.0 // 1 sample per ms keeps the arithmetic plain
	e := NewEnvelope(sr, EnvTimes{Attack: 0.1, Decay: 0.2, Sustain: 0.3, Release: 0.4,
		AttackLevel: 1, DecayLevel: 0.7, SustainLevel: 0.5})
	x := levels(e)
	if len(x) != 1000 || e.Length() != 1000 {
		t.Fatalf("length %d samples, want 1000", len(x))
	}
	for _, c := range []struct {
		at   int
		want float64
	}{{0, 0}, {50, 0.5}, {100, 1}, {300, 0.7}, {450, 0.6}, {600, 0.5}} {
		if math.Abs(x[c.at]-c.want) > 0.02 {
			t.Errorf("level at %d ms = %.3f, want %.2f", c.at, x[c.at], c.want)
		}
	}
	if x[999] > 0.01 {
		t.Errorf("last sample %.4f, want ~0", x[999])
	}
}

// Release and decay curve like natural fades: most of the drop comes early.
func TestEnvelopeCurvesItsFades(t *testing.T) {
	x := levels(NewEnvelope(1000, EnvTimes{Release: 1}))
	r := x[len(x)-1000:]
	if mid := r[500]; mid > 0.2 || mid < 0.05 {
		t.Fatalf("halfway through the release the level is %.3f, want a curved fade (~0.12)", mid)
	}
	for i := 1; i < len(r); i++ {
		if r[i] > r[i-1] {
			t.Fatalf("release rises at %d", i)
		}
	}
}

// Sonic Pi's rules: decay_level defaults to sustain_level; levels default to 1.
func TestEnvelopeDefaults(t *testing.T) {
	x := levels(NewEnvelope(1000, EnvTimes{Decay: 0.1, Sustain: 0.1, Release: 0.1, SustainLevel: 0.3}))
	if math.Abs(x[150]-0.3) > 0.01 || math.Abs(x[3]-1) > 0.01 { // the 3 ms minimum attack ends at sample 3
		t.Fatalf("attack peak %.3f (want 1), sustain %.3f (want 0.3)", x[3], x[150])
	}
	x = levels(NewEnvelope(1000, EnvTimes{Sustain: 0.1, Release: 0.1}))
	if math.Abs(x[50]-1) > 1e-9 {
		t.Fatalf("default sustain level %.3f, want 1", x[50])
	}
}

// A note neither clicks in nor out, and ends in exact silence.
func TestToneStartsAndEndsCleanly(t *testing.T) {
	v, _ := New("a4", 48000, 0, 4800, Params{Gain: 1, Wave: "saw", Note: 69.3, Release: 0.05}, 1)
	var x []float64
	for !v.Done() {
		l, _ := v.Next()
		x = append(x, float64(l))
	}
	body := x[2400:4800]
	steady := audiotest.LargestStep(body)
	if s := audiotest.LargestStep(x[:200]); s > steady*1.2 {
		t.Errorf("attack steps %.3f vs %.3f in the body: it clicks", s, steady)
	}
	if s := audiotest.LargestStep(x[len(x)-500:]); s > steady {
		t.Errorf("release end steps %.3f vs %.3f: it clicks", s, steady)
	}
	if x[len(x)-1] != 0 || math.Abs(x[len(x)-2]) > 1e-3 {
		t.Errorf("note ends at %.5f, %.5f", x[len(x)-2], x[len(x)-1])
	}
	if want := 4800 + 2400; len(x) < want || len(x) > want+5 {
		t.Errorf("note lasted %d samples, want %d (gate + release)", len(x), want)
	}
}

// tb303's filter envelope: bright at the start of the note, dark at the end.
func TestFilterEnvelopeSweeps(t *testing.T) {
	p := Params{Gain: 1, Wave: "saw", Note: 40, LPF: 8372, Res: 0.9, CutoffMin: 46, Release: 1,
		CutoffEnv: EnvTimes{Release: 1}}
	v, _ := New("e2", 48000, 0, 0, p, 1)
	var x []float64
	for !v.Done() {
		l, _ := v.Next()
		x = append(x, float64(l))
	}
	early, late := x[1000:9192], x[24000:32192]
	bright := func(s []float64) float64 {
		p := audiotest.Spectrum(s)
		var hi, all float64
		for k, v := range p {
			all += v
			if float64(k)*48000/float64(len(p)*2) > 1000 {
				hi += v
			}
		}
		return hi / all
	}
	b0, b1 := bright(early), bright(late)
	t.Logf("energy above 1 kHz: %.1f%% early, %.2f%% late", b0*100, b1*100)
	// a saw at 82 Hz has ~5% of its energy above 1 kHz, more with resonance
	if b0 < 0.05 || b1 > b0/20 {
		t.Fatalf("the filter did not sweep down: %.3f -> %.4f", b0, b1)
	}
}
