package engine

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/synth"
)

func rms(buf []int16, from, to int) float64 {
	var s float64
	for i := from * 2; i < to*2 && i < len(buf); i++ {
		s += float64(buf[i]) * float64(buf[i])
	}
	return math.Sqrt(s / float64((to-from)*2))
}

func hit(at int64, track, word string) Hit {
	return Hit{At: at, Track: track, Word: word, Params: synth.Params{Gain: 1}, Label: word[0]}
}

// onset finds the first frame louder than a small threshold.
func onset(buf []int16) int {
	for i := 0; i < len(buf)/2; i++ {
		if math.Abs(float64(buf[i*2])) > 200 {
			return i
		}
	}
	return -1
}

func TestHitsStartOnTheirSample(t *testing.T) {
	for _, at := range []int64{0, 1, 479, 480, 12345, 47999} {
		e := New()
		e.Play()
		e.Schedule(hit(at, "a", "bd"))
		var out []int16
		for i := 0; i < 110; i++ {
			out = append(out, e.Render(480)...)
		}
		if got := onset(out); got < int(at) || got > int(at)+3 {
			t.Errorf("hit at %d sounded at %d", at, got)
		}
	}
}

func TestLateHitsPlayAtOnce(t *testing.T) {
	e := New()
	e.Play()
	e.Render(4800)
	e.Schedule(hit(100, "a", "bd")) // already in the past
	if got := onset(e.Render(4800)); got < 0 || got > 3 {
		t.Errorf("late hit sounded at %d, want the start of the next block", got)
	}
}

func TestMuteSoloLevelByTrackNumber(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("drums", true)
	e.ThreadStarted("bass", true)
	v := e.Snapshot()
	if len(v.Tracks) != 2 || v.Tracks[0].Name != "drums" || v.Tracks[0].ID != 1 || v.Tracks[1].ID != 2 {
		t.Fatalf("tracks %+v", v.Tracks)
	}
	if !e.ToggleMute(1) {
		t.Fatal("mute 1")
	}
	e.Schedule(hit(100, "drums", "bd"))
	if r := rms(e.Render(9600), 0, 9600); r > 1 {
		t.Errorf("muted track audible: %.1f", r)
	}
	e.ToggleMute(1)
	e.SetSolo(2)
	e.Schedule(hit(e.samples+10, "drums", "bd"))
	if r := rms(e.Render(9600), 0, 9600); r > 1 {
		t.Errorf("non-solo track audible: %.1f", r)
	}
	e.SetSolo(0)
	e.Schedule(hit(e.samples+10, "drums", "bd"))
	if r := rms(e.Render(9600), 0, 9600); r < 100 {
		t.Errorf("track silent after unsolo: %.1f", r)
	}
	if l, ok := e.AdjustLevel(1, -0.3); !ok || math.Abs(l-0.7) > 1e-9 {
		t.Errorf("level %v %v", l, ok)
	}
	if e.ToggleMute(9) {
		t.Error("track 9 should not exist")
	}
}

func TestStepDisplayShowsTheCurrentBar(t *testing.T) {
	e := New() // 60 bpm: a bar is 4 s = 192000 frames, a cell 12000
	e.Play()
	e.ThreadStarted("drums", true)
	e.Schedule(hit(0, "drums", "bd"), hit(48000, "drums", "sn"), hit(96000, "drums", "bd"))
	for i := 0; i < 38; i++ { // still inside the first bar
		e.Render(4800)
	}
	tv := e.Snapshot().Tracks[0]
	for c, want := range map[int]byte{0: 'b', 4: 's', 8: 'b'} {
		if !tv.Steps[c].On || tv.Steps[c].Label != want {
			t.Errorf("cell %d = %+v, want %c", c, tv.Steps[c], want)
		}
	}
	if tv.Steps[2].On {
		t.Error("cell 2 should be empty")
	}
}

func TestEndedThreadsLeaveAndTheirQueuedNotesGo(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("riff", true)
	e.Schedule(hit(96000, "riff", "bd"))
	e.ThreadEnded("riff")
	var out []int16
	for i := 0; i < 30; i++ {
		out = append(out, e.Render(4800)...)
	}
	if r := rms(out, 0, len(out)/2); r > 1 {
		t.Errorf("a stopped loop's queued note played: %.1f", r)
	}
	for i := 0; i < 60; i++ {
		e.Render(4800)
	}
	if n := len(e.Snapshot().Tracks); n != 0 {
		t.Errorf("finished track still listed (%d)", n)
	}
}

func TestStopClearsEverything(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("x", true)
	e.Schedule(hit(100, "x", "bd"), hit(20000, "x", "bd"))
	e.Render(4800)
	e.Hush()
	out := e.Render(4800)
	out = append(out, e.Render(48000)...)
	if r := rms(out, 600, len(out)/2); r > 1 {
		t.Errorf("sound after hush: %.1f", r)
	}
	v := e.Snapshot()
	if v.Playing || !v.Hushed || len(v.Tracks) != 0 {
		t.Errorf("after hush: %+v", v)
	}
}

func TestBarCounterFollowsBPM(t *testing.T) {
	e := New()
	e.SetBPM(120) // a bar is 2 s
	e.Play()
	e.Render(96000 + 4800)
	if v := e.Snapshot(); v.Cycle != 1 || v.Beat != 1 {
		t.Errorf("at 2.1 s: bar %d beat %d", v.Cycle+1, v.Beat)
	}
	e.SetBPM(60) // slower: the bar number must not jump back
	if v := e.Snapshot(); v.Cycle != 1 {
		t.Errorf("tempo change moved the bar counter to %d", v.Cycle+1)
	}
}

func TestAuditionWorksWhileStopped(t *testing.T) {
	e := New()
	if err := e.Audition("bd", synth.Params{}, 0); err != nil {
		t.Fatal(err)
	}
	if r := rms(e.Render(4800), 0, 4800); r < 100 {
		t.Errorf("audition silent: %.1f", r)
	}
	if err := e.Audition("nope", synth.Params{}, 0); err == nil {
		t.Error("unknown sound should error")
	}
}

type recorder struct {
	mu  sync.Mutex
	evs []Event
}

func (r *recorder) Emit(e Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}
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
	e.SetBPM(120)
	rec := &recorder{}
	e.SetOutputs([]Output{rec})
	e.SetInternal(false)
	e.mu.Lock()
	e.wallBegin, e.samplesAtBegin = time.Now(), 0
	e.mu.Unlock()
	e.Play()
	for i := int64(0); i < 4; i++ {
		e.Schedule(hit(i*24000, "drums", "bd"))
	}
	for i := 0; i < 200; i++ { // one bar at 120 bpm
		e.Render(480)
	}
	if n := rec.kinds(KindNote); n != 4 {
		t.Errorf("want 4 notes, got %d", n)
	}
	if n := rec.kinds(KindClock); n != 96 {
		t.Errorf("want 96 clock ticks per bar, got %d", n)
	}
	if rec.kinds(KindStart) != 1 {
		t.Error("want exactly one start")
	}
	if r := rms(e.Render(4800), 0, 4800); r > 1 {
		t.Errorf("internal off but audible: %.1f", r)
	}
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
	wet.SetDelay(0.25, 0.6)
	h := hit(0, "a", "cp")
	dry.Schedule(h)
	h.Params.Delay, h.Params.Room = 0.9, 0.8
	wet.Schedule(h)
	dry.Play()
	wet.Play()
	var d, w []int16
	for i := 0; i < 150; i++ {
		d = append(d, dry.Render(480)...)
		w = append(w, wet.Render(480)...)
	}
	from, to := 21600, 67200
	if rd, rw := rms(d, from, to), rms(w, from, to); rd > 20 || rw < rd*5+50 {
		t.Errorf("effect tail missing: dry=%.1f wet=%.1f", rd, rw)
	}
}
