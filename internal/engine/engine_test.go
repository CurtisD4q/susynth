package engine

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/frac"
	"github.com/susynth/susynth/internal/lang"
)

func prog(t *testing.T, src string) lang.Program {
	t.Helper()
	p := lang.Parse(src, 0)
	if len(p.Errors) > 0 {
		t.Fatalf("parse: %v", p.Errors)
	}
	return p
}

func rms(buf []int16, from, to int) float64 {
	var s float64
	for i := from * 2; i < to*2 && i < len(buf); i++ {
		s += float64(buf[i]) * float64(buf[i])
	}
	return math.Sqrt(s / float64((to-from)*2))
}

// At 120 BPM one cycle = 2 s = 96000 frames. "bd*4" fires every 24000 frames.
func TestOnsetTiming(t *testing.T) {
	e := New()
	e.Apply(prog(t, `d1 "bd*4"`), false)
	e.Play()
	var all []int16
	for i := 0; i < 4*100; i++ { // 2 cycles (4 s at 120 BPM) in 10ms blocks
		all = append(all, e.Render(480)...)
	}
	for k := 0; k < 8; k++ {
		at := k * 24000
		before := 0.0
		if k > 0 {
			before = rms(all, at-200, at-1)
		}
		after := rms(all, at, at+400)
		if after < 500 || after < before*3 {
			t.Errorf("hit %d at %d: before=%.0f after=%.0f", k, at, before, after)
		}
	}
}

func TestEvalQuantisedToNextCycle(t *testing.T) {
	e := New()
	e.Apply(prog(t, `d1 "~"`), false)
	e.Play()
	var all []int16
	// run 0.3 cycles, then eval a loud pattern; it must start at frame 96000, not before.
	all = append(all, e.Render(28800)...)
	e.Apply(prog(t, `d2 "bd*4"`), false)
	if e.PendingEvals() != 1 {
		t.Fatal("eval should be pending")
	}
	all = append(all, e.Render(96000+10000-28800)...)
	if r := rms(all, 28800, 96000-1); r > 1 {
		t.Fatalf("sound before cycle boundary: %.1f", r)
	}
	if r := rms(all, 96000, 96400); r < 500 {
		t.Fatalf("no downbeat at the boundary: %.1f", r)
	}
	if e.PendingEvals() != 0 {
		t.Fatal("eval not applied")
	}
}

func TestMuteSoloHush(t *testing.T) {
	e := New()
	e.Apply(prog(t, "d1 \"bd*4\"\nd2 \"hh*8\""), false)
	e.Play()
	e.Render(480)
	e.ToggleMute(1)
	e.SetSolo(2)
	v := e.Snapshot()
	if !v.Tracks[0].Mute || !v.Tracks[1].Solo {
		t.Fatalf("bad view %+v", v.Tracks)
	}
	e.Hush()
	var all []int16
	for i := 0; i < 50; i++ {
		all = append(all, e.Render(480)...)
	}
	if r := rms(all, 480, len(all)/2); r > 1 {
		t.Fatalf("hush left sound: %.1f", r)
	}
	if len(e.Snapshot().Tracks) != 0 || !e.Snapshot().Hushed {
		t.Fatal("hush state wrong")
	}
}

func TestBPMKeepsPlayheadContinuous(t *testing.T) {
	e := New()
	e.Play()
	e.Render(48000) // 0.5 cycle at 120
	before := e.Snapshot().Phase
	e.SetBPM(240)
	after := e.Snapshot().Phase
	if math.Abs(before-after) > 0.001 {
		t.Fatalf("playhead jumped %v -> %v", before, after)
	}
}

// Long sets must not overflow the fraction arithmetic.
func TestLongRunStaysInTime(t *testing.T) {
	e := New()
	e.Apply(prog(t, `d1 "bd*4 [sn cp]/3" # gain 0.9`), false)
	e.Play()
	e.mu.Lock()
	e.samples = 96000 * 5000 // pretend we have been playing for 5000 cycles
	e.anchorS = e.samples
	e.anchorPos = frac.Int(5000)
	e.mu.Unlock()
	var all []int16
	for i := 0; i < 200; i++ {
		all = append(all, e.Render(480)...)
	}
	if r := rms(all, 0, 400); r < 500 {
		t.Fatalf("silent downbeat after long run: %.1f", r)
	}
	if r := rms(all, 24000, 24400); r < 500 {
		t.Fatalf("missing beat 2: %.1f", r)
	}
}

func TestReplaceAllStopsMissingTracks(t *testing.T) {
	e := New()
	e.Apply(prog(t, "d1 \"bd\"\nd2 \"hh\""), false)
	e.Apply(prog(t, `d1 "bd sn"`), true)
	v := e.Snapshot()
	if len(v.Tracks) != 1 || v.Tracks[0].ID != 1 || v.Tracks[0].Src != "bd sn" {
		t.Fatalf("unexpected tracks %+v", v.Tracks)
	}
	// a normal block leaves other tracks alone
	e.Apply(prog(t, `d3 "cp"`), false)
	if len(e.Snapshot().Tracks) != 2 {
		t.Fatal("block eval should not remove other tracks")
	}
}

func TestAuditionWorksWhileStopped(t *testing.T) {
	e := New()
	if err := e.Audition("bd", ""); err != nil {
		t.Fatal(err)
	}
	if r := rms(e.Render(2400), 0, 2400); r < 500 {
		t.Fatalf("audition silent: %.1f", r)
	}
	if err := e.Audition("nope", ""); err == nil {
		t.Fatal("expected error for unknown sound")
	}
	e.Hush()
	e.Render(480)
	if r := rms(e.Render(4800), 0, 4800); r > 1 {
		t.Fatalf("hush should silence auditions: %.1f", r)
	}
}

type recorder struct {
	mu  sync.Mutex
	evs []Event
}

func (r *recorder) Emit(e Event) { r.mu.Lock(); r.evs = append(r.evs, e); r.mu.Unlock() }
func (r *recorder) Close() error { return nil }
func (r *recorder) kinds(k Kind) (n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.evs {
		if e.Kind == k {
			n++
		}
	}
	return
}

func TestOutputsGetNotesClockAndStart(t *testing.T) {
	e := New()
	rec := &recorder{}
	e.SetOutputs([]Output{rec})
	e.SetInternal(false)
	e.mu.Lock()
	e.wallBegin, e.samplesAtBegin = time.Now(), 0
	e.mu.Unlock()
	e.Apply(prog(t, `d1 "bd*4"`), false)
	e.Play()
	for i := 0; i < 200; i++ { // exactly one cycle at 120 bpm
		e.Render(480)
	}
	if n := rec.kinds(KindNote); n != 4 {
		t.Errorf("want 4 notes in one cycle, got %d", n)
	}
	if n := rec.kinds(KindClock); n != 96 { // 24 ppqn * 4 beats
		t.Errorf("want 96 clock ticks per cycle, got %d", n)
	}
	if rec.kinds(KindStart) != 1 {
		t.Error("want exactly one start")
	}
	// internal synth is off: the audio must be silent while events still flow
	if r := rms(e.Render(4800), 0, 4800); r > 1 {
		t.Errorf("internal off but audible: %.1f", r)
	}
	// event times are spaced a quarter cycle (0.5 s) apart
	var notes []Event
	for _, ev := range rec.evs {
		if ev.Kind == KindNote {
			notes = append(notes, ev)
		}
	}
	if d := notes[1].At.Sub(notes[0].At); d < 495*time.Millisecond || d > 505*time.Millisecond {
		t.Errorf("note spacing %v, want 500ms", d)
	}
	e.Hush()
	if rec.kinds(KindPanic) != 1 {
		t.Error("hush should emit a panic event")
	}
}

func TestEffectsAddTailsAndSends(t *testing.T) {
	dry, wet := New(), New()
	dry.Apply(prog(t, `d1 "cp ~ ~ ~"`), false)
	wet.Apply(prog(t, "delaytime 0.5\ndelayfb 0.6\nd1 \"cp ~ ~ ~\" # delay 0.9 # room 0.8"), false)
	dry.Play()
	wet.Play()
	var d, w []int16
	for i := 0; i < 150; i++ {
		d = append(d, dry.Render(480)...)
		w = append(w, wet.Render(480)...)
	}
	// 0.45-1.4 s: the dry clap has long decayed, the wet one is still echoing
	from, to := 21600, 67200
	if rd, rw := rms(d, from, to), rms(w, from, to); rd > 20 || rw < rd*5+50 {
		t.Errorf("effect tail missing: dry=%.1f wet=%.1f", rd, rw)
	}
}

func TestMidiStartOnNextBarWhenAttachedMidPlay(t *testing.T) {
	e := New()
	e.SetInternal(false)
	e.mu.Lock()
	e.wallBegin, e.samplesAtBegin = time.Now(), 0
	e.mu.Unlock()
	e.Apply(prog(t, `d1 "bd"`), false)
	e.Play()
	e.Render(48000) // half a cycle in, nobody listening
	rec := &recorder{}
	e.SetOutputs([]Output{rec})
	e.Render(40000) // still before the bar line (96000)
	if rec.kinds(KindStart) != 0 {
		t.Fatal("start must wait for the bar line")
	}
	e.Render(20000) // crosses 96000
	if rec.kinds(KindStart) != 1 {
		t.Fatalf("want one start after the bar line, got %d", rec.kinds(KindStart))
	}
}
