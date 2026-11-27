package synth

import "testing"

// crossings counts upward zero crossings over n frames: about the pitch in
// Hz when n is one second.
func crossings(v *Voice, n int) int {
	c := 0
	var prev float32
	for i := 0; i < n; i++ {
		l, _ := v.Next()
		if prev < 0 && l >= 0 {
			c++
		}
		prev = l
	}
	return c
}

func TestControlNoteGlides(t *testing.T) {
	sr := 48000
	v, err := New("60", sr, 0, sr*4, Params{Gain: 1, Speed: 1, Wave: "sine", Note: 60, Release: 0.1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f := crossings(v, sr/4); f < 63 || f > 67 { // 261.6 Hz for a quarter second
		t.Fatalf("start pitch %d", f*4)
	}
	c := NoControl()
	c.Note, c.NoteSlide = 72, 0.5
	v.Control(c, 0)
	crossings(v, sr/2) // the glide
	if f := crossings(v, sr/4); f < 129 || f > 133 {
		t.Errorf("after the glide %d Hz, want about 523", f*4)
	}
}

func TestControlAmpAndKill(t *testing.T) {
	sr := 48000
	v, _ := New("60", sr, 0, sr*4, Params{Gain: 1, Speed: 1, Wave: "sine", Note: 60}, 1)
	c := NoControl()
	c.Amp = 0
	v.Control(c, 100)
	var peak float32
	for i := 0; i < 2000; i++ {
		l, _ := v.Next()
		if i > 200 && (l > peak || -l > peak) {
			peak = max(l, -l)
		}
	}
	if peak > 1e-6 {
		t.Errorf("amp 0 still sounds: %v", peak)
	}
	k := NoControl()
	k.Kill = true
	v.Control(k, 0)
	for i := 0; i < sr/10; i++ {
		v.Next()
	}
	if !v.Done() {
		t.Error("kill did not end the voice")
	}
}
