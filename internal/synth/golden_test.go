package synth

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// voiceHash renders a voice for half a second and hashes its exact output.
func voiceHash(t *testing.T, word string, p Params) uint64 {
	t.Helper()
	v, err := New(word, 48000, 7, 9000, p, 42)
	if err != nil {
		t.Fatal(err)
	}
	h := fnv.New64a()
	var b [8]byte
	for i := 0; i < 24000; i++ {
		l, r := v.Next()
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(l))
		binary.LittleEndian.PutUint32(b[4:], math.Float32bits(r))
		h.Write(b[:])
	}
	return h.Sum64()
}

// monoWAV writes a 48 kHz mono 16-bit test sample: a decaying 330 Hz tone
// with a little noise, like a real one-shot.
func monoWAV(t *testing.T, dir, name string) {
	t.Helper()
	const n = 12000
	b := make([]byte, 44+n*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 48000)
	binary.LittleEndian.PutUint32(b[28:], 96000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], n*2)
	rng := uint32(1)
	for i := 0; i < n; i++ {
		rng = rng*1664525 + 1013904223
		x := math.Sin(2*math.Pi*330*float64(i)/48000)*math.Exp(-float64(i)/4000) + (float64(rng>>16)/65536-0.5)*0.05
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(int16(x*20000)))
	}
	if err := os.WriteFile(filepath.Join(dir, name+".wav"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Voices that play the same sound as before must give bit-identical output:
// this pins drums, every tone wave and a mono 48 kHz sample at normal speed,
// each with the per-voice effects, so sample-playback changes cannot leak.
func TestVoicesUnchanged(t *testing.T) {
	dir := t.TempDir()
	monoWAV(t, dir, "goldmono")
	if _, _, err := LoadSamples(dir); err != nil {
		t.Fatal(err)
	}
	fx := Params{Gain: 0.8, Pan: -0.3, LPF: 3000, HPF: 120, Crush: 10, Shape: 0.2}
	cases := []struct {
		name, word string
		p          Params
	}{
		{"bd", "bd", Params{Gain: 1}},
		{"sn fx", "sn:2", fx},
		{"hh", "hh", Params{Gain: 1, Pan: 0.5}},
		{"cp", "cp", Params{Gain: 1}},
		{"sample", "goldmono", Params{Gain: 1}},
		{"sample fx", "goldmono", fx},
		{"sine", "a4", Params{Gain: 1, Wave: "sine"}},
		{"tri", "c5", Params{Gain: 1, Wave: "tri", Release: 0.2}},
		{"square", "e3", Params{Gain: 1, Wave: "square"}},
		{"fm", "g4", Params{Gain: 1, Wave: "fm"}},
		{"supersaw", "c4", Params{Gain: 1, Wave: "supersaw", Attack: 0.02}},
		{"organ", "d4", Params{Gain: 1, Wave: "organ"}},
		{"pluck", "a3", Params{Gain: 1, Wave: "pluck"}},
		{"noise", "c6", Params{Gain: 1, Wave: "noise"}},
	}
	for _, c := range cases {
		got := voiceHash(t, c.word, c.p)
		want, ok := goldenVoices[c.name]
		if !ok {
			t.Logf("%q: %#x,", c.name, got)
			continue
		}
		if got != want {
			t.Errorf("%s: output changed (hash %#x, was %#x)", c.name, got, want)
		}
	}
}

// goldenVoices were recorded from the voices before samples went stereo and
// band-limited. The saw is left out: it changed on purpose (PolyBLEP).
var goldenVoices = map[string]uint64{
	"bd":        0x3adcf34407fd1475,
	"sn fx":     0xde6829a68ba71765,
	"hh":        0x9005c3893b3299e6,
	"cp":        0xcc5880ffb8d3bded,
	"sample":    0x9b79e53fac8ca8f9,
	"sample fx": 0xb63913bb668dc23c,
	"sine":      0x6fdec24dd84d81d,
	"tri":       0x6120a0999ba02605,
	"square":    0x7044142963582381,
	"fm":        0x66104a5dcf373af1,
	"supersaw":  0xf3aea813c415a761,
	"organ":     0xcdeab9f74053647d,
	"pluck":     0xa3f7cf10833d4ea5,
	"noise":     0xcc420fa9fcf873dd,
}
