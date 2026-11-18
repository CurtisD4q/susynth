package midi

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/synth"
)

func TestNoteFor(t *testing.T) {
	cases := []struct {
		w     string
		tr    int
		n, ch int
		ok    bool
	}{
		{"bd", 1, 36, 9, true}, {"sn:2", 2, 38, 9, true}, {"c4", 1, 60, 0, true},
		{"c4", 3, 60, 2, true}, {"a3", 10, 57, 15, true}, {"mysample", 1, 0, 0, false},
	}
	for _, c := range cases {
		n, ch, ok := NoteFor(c.w, c.tr)
		if n != c.n || ch != c.ch || ok != c.ok {
			t.Errorf("%s track %d: got %d,%d,%v", c.w, c.tr, n, ch, ok)
		}
	}
}

// parse a Standard MIDI File just enough to check it is well-formed.
func TestSMFIsWellFormed(t *testing.T) {
	// two beats at 90 bpm: drums on one loop, a melody on another, a user sample skipped
	beat := int64(engine.SampleRate * 60 / 90)
	var hits []engine.Hit
	for i := int64(0); i < 4; i++ {
		w := "bd"
		if i%2 == 1 {
			w = "sn"
		}
		hits = append(hits, engine.Hit{At: i * beat, Track: "drums", Word: w, Params: synth.Params{Gain: 1}})
	}
	for i, n := range []string{"60", "64", "67", "60", "64", "67"} {
		hits = append(hits, engine.Hit{At: int64(i) * beat / 2, Track: "melody", Word: n, Gate: 1000,
			Params: synth.Params{Gain: 0.5, Release: 0.2}})
	}
	hits = append(hits, engine.Hit{At: 0, Track: "melody", Word: "@file1"})
	notes, names, skipped := FromHits(hits, 90)
	if len(notes) != 10 || skipped != 1 || names[1] != "drums" || names[2] != "melody" {
		t.Fatalf("notes=%d skipped=%d names=%v", len(notes), skipped, names)
	}
	if notes[1].Start != 1 {
		t.Errorf("second drum hit should be on beat 1, got %v", notes[1].Start)
	}
	var buf bytes.Buffer
	if err := WriteSMF(&buf, 90, names, notes); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if string(b[:4]) != "MThd" || binary.BigEndian.Uint16(b[8:]) != 1 || binary.BigEndian.Uint16(b[10:]) != 3 || binary.BigEndian.Uint16(b[12:]) != PPQ {
		t.Fatalf("bad header % x", b[:14])
	}
	// walk the chunks and count note-ons; 90 bpm = 666666 us per beat
	pos, tracks, ons := 14, 0, 0
	for pos < len(b) {
		if string(b[pos:pos+4]) != "MTrk" {
			t.Fatalf("expected MTrk at %d", pos)
		}
		n := int(binary.BigEndian.Uint32(b[pos+4:]))
		body := b[pos+8 : pos+8+n]
		if body[n-3] != 0xFF || body[n-2] != 0x2F || body[n-1] != 0 {
			t.Fatal("track does not end with end-of-track")
		}
		for i := 0; i+2 < len(body); i++ {
			if body[i]&0xF0 == 0x90 && body[i+2] != 0 && body[i+1] < 128 && body[i+2] <= 127 && (body[i+1] == 36 || body[i+1] == 38 || body[i+1] == 60 || body[i+1] == 64 || body[i+1] == 67) {
				ons++
			}
		}
		if tracks == 0 && !bytes.Contains(body, []byte{0xFF, 0x51, 3, 0x0A, 0x2C, 0x2B}) {
			t.Errorf("tempo for 90 bpm (0x0A2C2B) missing: % x", body)
		}
		tracks++
		pos += 8 + n
	}
	if tracks != 3 || ons != 10 {
		t.Fatalf("tracks=%d note-ons=%d", tracks, ons)
	}
}

func TestVLQ(t *testing.T) {
	for v, want := range map[uint32][]byte{0: {0}, 127: {0x7f}, 128: {0x81, 0}, 16384: {0x81, 0x80, 0}} {
		if got := vlq(v); !bytes.Equal(got, want) {
			t.Errorf("vlq(%d)=% x want % x", v, got, want)
		}
	}
}

func TestLiveOutSendsNotesAndClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "port")
	os.WriteFile(path, nil, 0o644)
	o, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	o.Emit(engine.Event{Kind: engine.KindStart, At: now})
	o.Emit(engine.Event{Kind: engine.KindClock, At: now.Add(5 * time.Millisecond)})
	o.Emit(engine.Event{Kind: engine.KindNote, At: now.Add(10 * time.Millisecond), Track: 1, Word: "c4", Dur: 30 * time.Millisecond, Params: synth.Params{Gain: 1}})
	o.Emit(engine.Event{Kind: engine.KindNote, At: now.Add(12 * time.Millisecond), Track: 1, Word: "bd", Dur: 30 * time.Millisecond, Params: synth.Params{Gain: 1}})
	time.Sleep(150 * time.Millisecond)
	o.Close()
	got, _ := os.ReadFile(path)
	want := []byte{0xFA, 0xF8, 0x90, 60, 100, 0x99, 36, 100}
	if !bytes.HasPrefix(got, want) {
		t.Fatalf("got % x, want prefix % x", got, want)
	}
	if !bytes.Contains(got, []byte{0x80, 60, 0}) || !bytes.Contains(got, []byte{0x89, 36, 0}) {
		t.Errorf("missing note-offs: % x", got)
	}
	if !bytes.HasSuffix(got, []byte{0xBF, 123, 0}) {
		t.Errorf("should end with all-notes-off on close: % x", got[len(got)-6:])
	}
}
