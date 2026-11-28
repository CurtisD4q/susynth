package engine

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"

	"github.com/susynth/susynth/internal/synth"
)

// optInScene is a busy set that uses no compressor, EQ or chorus: drums,
// a resonant bass, a pad through reverb and echo, a filtered track.
func optInScene() uint64 {
	e := New()
	e.Play()
	var hs []Hit
	for b := 0; b < 8; b++ {
		at := int64(b) * SampleRate / 2
		hs = append(hs,
			Hit{At: at, Track: "kick", Word: "bd", Params: synth.Params{Gain: 1}},
			Hit{At: at + SampleRate/4, Track: "hat", Word: "hh", Params: synth.Params{Gain: 0.6, Room: 0.3}},
			Hit{At: at, Track: "bass", Word: "40", Gate: SampleRate / 8, Params: synth.Params{Wave: "saw", Note: 40, Gain: 1,
				LPF: 3000, Res: 0.7, CutoffMin: 60, Release: 0.3, CutoffEnv: synth.EnvTimes{Release: 0.3}}},
			Hit{At: at, Track: "pad", Word: "60", Gate: SampleRate, Params: synth.Params{Wave: "organ", Note: 60 + float64(b%3), Gain: 0.5,
				Room: 0.6, Delay: 0.3, Attack: 0.2, Release: 1}})
	}
	e.Schedule(hs...)
	e.ThreadStarted("pad", true)
	var out []float32
	for i := 0; i < 500; i++ {
		if i == 100 {
			e.AdjustFilter(e.tracks["pad"].id, -6)
		}
		out = append(out, e.Render(480)...)
	}
	h := fnv.New64a()
	var b [4]byte
	for _, v := range out {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		h.Write(b[:])
	}
	return h.Sum64()
}

// The compressor, EQ and chorus are opt-in: a set that does not use them
// sounds bit-for-bit as it did before they existed.
func TestDynamicsAreOptIn(t *testing.T) {
	const before = 0x2e59615a3e8b9e39 // recorded before the supersaw became a native voice; the older voices are unchanged bit for bit
	if got := optInScene(); got != before {
		t.Fatalf("a set without compressor/EQ/chorus changed: hash %#x, was %#x", got, before)
	}
}
