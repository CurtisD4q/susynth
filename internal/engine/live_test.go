package engine

import (
	"math"
	"testing"

	"github.com/susynth/susynth/internal/synth"
)

func render(e *Engine, seconds float64) []float32 {
	var out []float32
	for i := 0; i < int(seconds*100); i++ {
		out = append(out, e.Render(SampleRate/100)...)
	}
	return out
}

// brightness is the size of the sample-to-sample change: high notes and
// buzz make it large, a low-passed sound small.
func brightness(buf []float32) float64 {
	var s float64
	for i := 2; i < len(buf); i += 2 {
		d := i16(buf[i]) - i16(buf[i-2])
		s += d * d
	}
	return math.Sqrt(s / float64(len(buf)/2))
}

func TestNextGrid(t *testing.T) {
	e := New()
	e.SetBPM(120)
	e.Play()
	for _, c := range []struct{ step, from, want float64 }{
		{1, 0.6, 1.0}, {4, 0.6, 2.0}, {1, 1.0, 1.0}, {1, 0, 0}, {4, 2.01, 4.0},
	} {
		if got := e.NextGrid(c.step, c.from); math.Abs(got-c.want) > 1e-4 {
			t.Errorf("NextGrid(%v, %v) = %v, want %v", c.step, c.from, got, c.want)
		}
	}
}

func TestTrackFilterSweepsNotesAlreadyPlaying(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("a", true)
	e.Schedule(Hit{At: 0, Track: "a", Word: "60", Gate: 3 * SampleRate,
		Params: synth.Params{Wave: "saw", Note: 60, Gain: 1, Release: 0.1}})
	open := brightness(render(e, 0.3)[SampleRate/5:])
	hz, ok := e.AdjustFilter(1, -FilterSteps)
	if !ok || math.Abs(hz-90) > 1 {
		t.Fatalf("closed filter %v %v", hz, ok)
	}
	render(e, 0.4) // the glide
	closed := brightness(render(e, 0.3))
	if closed > open/8 {
		t.Errorf("filter barely changed the note: open %.0f closed %.0f", open, closed)
	}
	if v := e.Snapshot().Tracks; len(v) != 1 || math.Abs(v[0].LPF-90) > 1 {
		t.Errorf("view %+v", v)
	}
	if hz, _ := e.AdjustFilter(1, FilterSteps); hz != 0 {
		t.Fatalf("opened to %v", hz)
	}
	render(e, 0.4)
	if again := brightness(render(e, 0.3)); again < open*0.7 {
		t.Errorf("filter did not open again: %.0f vs %.0f", again, open)
	}
}

func TestOldSetFadesAndNewTracksStartMuted(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("drums", true)
	for i := int64(0); i < 20; i++ {
		e.Schedule(hit(i*SampleRate/4, "drums", "bd"))
	}
	e.RenameTrack("drums", "old/drums")
	e.MuteNewTracks(true)
	e.ThreadStarted("drums", true)
	e.MuteNewTracks(false)
	v := e.Snapshot().Tracks
	if len(v) != 2 || !v[0].Old || v[0].Name != "old/drums" || v[0].Mute || v[1].Old || !v[1].Mute {
		t.Fatalf("tracks %+v", v)
	}
	if loud := rms(render(e, 1), 0, SampleRate); loud < 200 {
		t.Fatalf("old set silent before the fade: %.0f", loud)
	}
	e.FadeOld(1)
	if e.FadeDone() {
		t.Fatal("done at once")
	}
	out := render(e, 2)
	if rms(out, SampleRate+SampleRate/10, 2*SampleRate) > 5 {
		t.Errorf("old set still sounds after the fade: %.0f", rms(out, SampleRate+SampleRate/10, 2*SampleRate))
	}
	if !e.FadeDone() || e.FadeDone() {
		t.Error("FadeDone should report once")
	}
}

func TestLitLinesAreWhatIsSoundingNow(t *testing.T) {
	e := New()
	e.Play()
	e.ThreadStarted("a", true)
	e.ThreadStarted("old/b", true)
	h := hit(SampleRate/20, "a", "bd")
	h.Line, h.Run = 5, 2
	o := hit(SampleRate/20, "old/b", "bd")
	o.Line, o.Run = 9, 1
	e.Schedule(h, o)
	if l := e.LitLines(0.2); len(l) != 0 {
		t.Fatalf("lit before it played: %+v", l)
	}
	render(e, 0.1)
	l := e.LitLines(0.2)
	if len(l) != 1 || l[0].Line != 5 || l[0].Run != 2 {
		t.Fatalf("lit %+v", l)
	}
	render(e, 0.5)
	if l := e.LitLines(0.2); len(l) != 0 {
		t.Fatalf("still lit: %+v", l)
	}
}
