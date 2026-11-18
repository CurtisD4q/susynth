package synth

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func energy(t *testing.T, word string, p Params, n int) float64 {
	t.Helper()
	v, err := New(word, 48000, 0, 24000, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	var e float64
	for i := 0; i < n; i++ {
		l, r := v.Next()
		e += float64(l*l + r*r)
	}
	return math.Sqrt(e / float64(n))
}

func TestAllVoicesSound(t *testing.T) {
	for _, w := range Waves {
		if e := energy(t, "c3", Params{Wave: w, Gain: 1}, 9600); e < 0.01 {
			t.Errorf("voice %s is silent (%.4f)", w, e)
		}
	}
	for _, d := range Drums {
		if e := energy(t, d, Params{Gain: 1}, 4800); e < 0.01 {
			t.Errorf("drum %s is silent (%.4f)", d, e)
		}
	}
}

func TestFiltersChangeTheSound(t *testing.T) {
	open := energy(t, "hh", Params{Gain: 1}, 4000)
	lp := energy(t, "hh", Params{Gain: 1, LPF: 300}, 4000)
	if lp > open*0.5 {
		t.Errorf("lpf 300 should dull a hat: open=%.3f lp=%.3f", open, lp)
	}
	hp := energy(t, "bd", Params{Gain: 1, HPF: 4000}, 4000)
	if hp > energy(t, "bd", Params{Gain: 1}, 4000)*0.4 {
		t.Errorf("hpf 4000 should thin a kick, got %.3f", hp)
	}
}

func wavBytes(rate, ch, bits int, frames int, fill func(i int) int32) []byte {
	bps := bits / 8
	data := make([]byte, frames*ch*bps)
	for i := 0; i < frames; i++ {
		for c := 0; c < ch; c++ {
			x := fill(i)
			o := (i*ch + c) * bps
			switch bits {
			case 16:
				binary.LittleEndian.PutUint16(data[o:], uint16(int16(x)))
			case 24:
				data[o], data[o+1], data[o+2] = byte(x), byte(x>>8), byte(x>>16)
			}
		}
	}
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(data)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(ch))
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*ch*bps))
	binary.LittleEndian.PutUint16(h[32:], uint16(ch*bps))
	binary.LittleEndian.PutUint16(h[34:], uint16(bits))
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(data)))
	return append(h, data...)
}

func TestParseWAV(t *testing.T) {
	b := wavBytes(22050, 2, 16, 100, func(i int) int32 { return 16384 })
	s, err := ParseWAV(b)
	if err != nil || s.Rate != 22050 || len(s.Data) != 100 || math.Abs(float64(s.Data[0])-0.5) > 0.001 {
		t.Fatalf("16-bit stereo: %v %+v", err, s)
	}
	s, err = ParseWAV(wavBytes(48000, 1, 24, 10, func(i int) int32 { return -4194304 }))
	if err != nil || math.Abs(float64(s.Data[0])+0.5) > 0.001 {
		t.Fatalf("24-bit: %v", err)
	}
	// hostile inputs must fail cleanly, never panic
	for _, bad := range [][]byte{nil, []byte("RIFF"), b[:30], append([]byte("RIFF\x00\x00\x00\x00WAVEfmt \xff\xff\xff\xff"), make([]byte, 8)...)} {
		if _, err := ParseWAV(bad); err == nil {
			t.Errorf("expected an error for %d-byte junk", len(bad))
		}
	}
	trunc := append([]byte{}, b...)
	binary.LittleEndian.PutUint32(trunc[40:], 0xffffff00) // lying data length
	if _, err := ParseWAV(trunc); err != nil {
		t.Errorf("a lying data length should be clamped, got %v", err)
	}
}

func TestSamplesFolder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "kick"), 0o755)
	for i, n := range []string{"a.wav", "b.wav"} {
		amp := int32(8000 * (i + 1))
		os.WriteFile(filepath.Join(dir, "kick", n), wavBytes(48000, 1, 16, 4800, func(int) int32 { return amp }), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "clap.wav"), wavBytes(44100, 1, 16, 2000, func(int) int32 { return 1000 }), 0o644)
	os.WriteFile(filepath.Join(dir, "junk.wav"), []byte("nope"), 0o644)
	os.MkdirAll(filepath.Join(dir, "Bad Name"), 0o755)

	banks, files, err := LoadSamples(dir)
	if banks != 2 || files != 3 || err == nil {
		t.Fatalf("banks=%d files=%d err=%v", banks, files, err)
	}
	defer LoadSamples(t.TempDir())
	if Known("kick") != nil || Known("kick:1") != nil || Known("clap") != nil {
		t.Fatal("loaded samples should be known sounds")
	}
	if Known("nothing") == nil {
		t.Fatal("unknown word accepted")
	}
	a, b := energy(t, "kick:0", Params{Gain: 1}, 2000), energy(t, "kick:1", Params{Gain: 1}, 2000)
	if !(b > a*1.5) {
		t.Errorf("variant 1 should be louder than 0: %.3f vs %.3f", a, b)
	}
	// samples override built-in drums of the same name
	os.MkdirAll(filepath.Join(dir, "bd"), 0o755)
	os.WriteFile(filepath.Join(dir, "bd", "x.wav"), wavBytes(48000, 1, 16, 100, func(int) int32 { return 20000 }), 0o644)
	LoadSamples(dir)
	if v, _ := New("bd", 48000, 0, 0, Params{}, 1); v.k != kSample {
		t.Error("user bd sample should override the synth kick")
	}
}
