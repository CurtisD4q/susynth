package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
	"github.com/susynth/susynth/internal/synth"
)

// quietTone renders a held sine about 80 dB below full scale.
func quietTone(e *Engine) []float64 {
	e.Play()
	e.Schedule(Hit{At: 0, Track: "s", Word: "a4", Gate: SampleRate * 3,
		Params: synth.Params{Wave: "sine", Note: 81.3, Gain: 0.0003, Attack: 0.01}})
	var out []float32
	for i := 0; i < 200; i++ {
		out = append(out, e.Render(480)...)
	}
	l, _ := audiotest.Split(out)
	return l[8192 : 8192+65536]
}

// The engine's output is float: a very quiet tone keeps its shape instead of
// being chopped into a few 16-bit steps (as a reverb tail or fade-out is).
func TestQuietSoundsStayClean(t *testing.T) {
	x := quietTone(New())
	hz := 440 * math.Pow(2, (81.3-69)/12)
	level := 20 * math.Log10(audiotest.Peak(x))
	got := audiotest.Purity(x, SampleRate, hz)
	var steps []float64
	for _, v := range x {
		steps = append(steps, math.Round(v*32767)/32767)
	}
	old := audiotest.Purity(steps, SampleRate, hz)
	t.Logf("%.0f dBFS tone: %.1f dB clean in float, %.1f dB if rounded to 16 bits", level, got, old)
	if got < 90 || old > 60 {
		t.Fatalf("quiet tone: float %.1f dB clean, 16-bit %.1f dB", got, old)
	}
}

// Recordings are 24-bit and match the engine's output to 24-bit precision.
func TestRecordingsAre24Bit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.wav")
	e := New()
	if err := e.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	x := quietTone(e)
	if _, err := e.StopRecording(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bits := int(b[34]) | int(b[35])<<8; bits != 24 {
		t.Fatalf("recording is %d-bit", bits)
	}
	s, err := synth.ParseWAV(b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Rate != SampleRate || len(s.Data) != 200*480 {
		t.Fatalf("rate %d, %d frames", s.Rate, len(s.Data))
	}
	var worst float64
	for i, v := range x {
		worst = math.Max(worst, math.Abs(float64(s.Data[8192+i])-v))
	}
	if worst > 2.0/8388608 {
		t.Fatalf("recording differs from the output by %.2g (more than 2 steps of 24 bits)", worst)
	}
	diff := make([]float64, len(x))
	for i, v := range x {
		diff[i] = float64(s.Data[8192+i]) - v
	}
	if floor := audiotest.NoiseFloor(diff); floor > -138 {
		t.Fatalf("recording error floor %.1f dBFS, want the 24-bit floor (~-144)", floor)
	} else {
		t.Logf("recording error floor %.1f dBFS", floor)
	}
}
