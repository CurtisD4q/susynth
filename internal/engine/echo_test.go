package engine

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

// runEcho feeds the stereo send in through e in 10 ms blocks.
func runEcho(e *echo, in []float32) []float32 {
	out := make([]float32, len(in))
	for a := 0; a < len(in); a += 960 {
		b := min(a+960, len(in))
		e.process(out[a:b], in[a:b])
	}
	return out
}

func clickOf(n int) []float32 { x := make([]float32, n*2); x[0], x[1] = 1, 1; return x }

func TestEchoRepeatsOnTimeAndTheFirstIsTheSound(t *testing.T) {
	e := newEcho()
	e.set(0.25, 0.5, false)
	e.d, e.fb = e.dT, e.fbT
	l, _ := audiotest.Split(runEcho(e, clickOf(SampleRate)))
	d := int(0.25 * SampleRate)
	if l[d] != 1 {
		t.Fatalf("first repeat %.4f at %d samples, want exactly the click", l[d], d)
	}
	// the second repeat is the first through the feedback filter: spread a
	// sample or two, but on time
	at, best := 0, 0.0
	for i := 2*d - 20; i < 2*d+40; i++ {
		if math.Abs(l[i]) > best {
			at, best = i, math.Abs(l[i])
		}
	}
	if at < 2*d || at > 2*d+3 {
		t.Fatalf("second repeat peaks at %d, want %d", at, 2*d)
	}
}

// burst is a 50 ms Hann-shaped tone at hz.
func burst(hz float64, n int) []float32 {
	x := make([]float32, n*2)
	m := int(0.05 * SampleRate)
	for i := 0; i < m; i++ {
		v := float32(math.Sin(2*math.Pi*hz*float64(i)/SampleRate) * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(m))))
		x[i*2], x[i*2+1] = v, v
	}
	return x
}

func repeatLevels(e *echo, in []float32, n int) []float64 {
	l, _ := audiotest.Split(runEcho(e, in))
	d := int(e.dT)
	var out []float64
	for k := 1; k <= n; k++ {
		out = append(out, audiotest.RMS(l[k*d:k*d+int(0.06*SampleRate)]))
	}
	return out
}

func TestEchoFeedbackSetsTheRepeatLevel(t *testing.T) {
	e := newEcho()
	e.set(0.2, 0.6, false)
	e.d, e.fb = e.dT, e.fbT
	lv := repeatLevels(e, burst(1000, SampleRate*2), 4)
	for k := 1; k < len(lv); k++ {
		got := 20 * math.Log10(lv[k]/lv[k-1])
		want := 20 * math.Log10(0.6)
		if math.Abs(got-want) > 0.6 {
			t.Errorf("repeat %d is %.2f dB below the last, want %.2f", k+1, got, want)
		}
	}
}

func TestEchoRepeatsGetDarker(t *testing.T) {
	ratio := func(hz float64) float64 {
		e := newEcho()
		e.set(0.2, 0.8, false)
		e.d, e.fb = e.dT, e.fbT
		lv := repeatLevels(e, burst(hz, SampleRate*2), 4)
		return 20 * math.Log10(lv[3]/lv[0])
	}
	lo, hi := ratio(500), ratio(12000)
	t.Logf("fourth repeat vs first: 500 Hz %.1f dB, 12 kHz %.1f dB", lo, hi)
	if hi > lo-6 {
		t.Fatalf("treble should fade faster than the mids: %.1f vs %.1f dB", hi, lo)
	}
}

func TestPingPongAlternates(t *testing.T) {
	e := newEcho()
	e.set(0.1, 0.7, true)
	e.d, e.fb = e.dT, e.fbT
	l, r := audiotest.Split(runEcho(e, clickOf(SampleRate)))
	d := int(0.1 * SampleRate)
	win := func(x []float64, k int) float64 { return audiotest.Peak(x[k*d-10 : k*d+40]) }
	if !(win(l, 1) > 0.4 && win(r, 1) == 0 && win(r, 2) > 0.2 && win(l, 2) < 1e-6 && win(l, 3) > 0.1) {
		t.Fatalf("ping-pong: L %.3f %.3f %.3f, R %.3f %.3f", win(l, 1), win(l, 2), win(l, 3), win(r, 1), win(r, 2))
	}
}

// Changing the echo time while it sounds glides instead of jumping (the old
// echo made a step 7x the signal's own).
func TestEchoTimeChangeDoesNotClick(t *testing.T) {
	for _, to := range []float64{0.2503, 0.4, 0.05} {
		e := newEcho()
		e.set(0.25, 0.6, false)
		e.d, e.fb = e.dT, e.fbT
		tone := make([]float32, SampleRate*2*2)
		for i := 0; i < len(tone)/2; i++ {
			v := float32(0.5 * math.Sin(2*math.Pi*440.7*float64(i)/SampleRate))
			tone[i*2], tone[i*2+1] = v, v
		}
		pre, _ := audiotest.Split(runEcho(e, tone[:SampleRate*2]))
		e.set(to, 0.6, false)
		post, _ := audiotest.Split(runEcho(e, tone[SampleRate*2:SampleRate*2+9600]))
		if r := audiotest.ClickRatio(pre, post); r > 1.5 {
			t.Errorf("0.25 s -> %.4f s: step ratio %.1f, it clicks or sweeps", to, r)
		}
		// and it settles on the new time
		runEcho(e, tone[SampleRate*2+9600:])
		if math.Abs(e.d-to*SampleRate) > 1 {
			t.Errorf("echo time %.1f samples, want %.1f", e.d, to*SampleRate)
		}
	}
}

func TestEchoIsStableAndSleeps(t *testing.T) {
	e := newEcho()
	e.set(0.25, 0.95, false)
	e.d, e.fb = e.dT, e.fbT
	in := burst(300, SampleRate)
	var last float64 = math.Inf(1)
	for s := 0; s < 20; s++ {
		l, _ := audiotest.Split(runEcho(e, in))
		in = make([]float32, SampleRate*2)
		lv := audiotest.RMS(l)
		if s > 1 && (math.IsNaN(lv) || lv > last*1.05) {
			t.Fatalf("echo grows at %d s: %.3g -> %.3g", s, last, lv)
		}
		last = lv
	}
	e.set(0.25, 0.3, false)
	for s := 0; s < 10 && !e.idle; s++ {
		runEcho(e, make([]float32, SampleRate*2))
	}
	if !e.idle {
		t.Fatal("the echo never went idle after dying away")
	}
}
