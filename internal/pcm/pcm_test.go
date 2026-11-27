package pcm

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/susynth/susynth/internal/audiotest"
)

func decode16(b []byte) []float64 {
	x := make([]float64, len(b)/2)
	for i := range x {
		x[i] = float64(int16(binary.LittleEndian.Uint16(b[i*2:]))) / 32767
	}
	return x
}

func quietSine() []float32 {
	// -80 dBFS: only a few LSBs at 16 bits, where plain rounding distorts
	x := audiotest.Sine(48000, 1000.3, 65536)
	out := make([]float32, len(x))
	for i, v := range x {
		out[i] = float32(v * 1e-4)
	}
	return out
}

// harmonicPower sums the spectrum around harmonics 2..10 of hz.
func harmonicPower(x []float64, hz float64) float64 {
	p := audiotest.Spectrum(x)
	var s float64
	for k := 2.0; k <= 10; k++ {
		c := int(math.Round(k * hz * float64(len(p)*2) / 48000))
		for d := -12; d <= 12; d++ {
			s += p[c+d]
		}
	}
	return s
}

// Dither turns quantisation distortion into plain noise: the harmonic bins
// hold no more than the noise dithering silence leaves there.
func TestDitheredQuietToneHasNoHarmonics(t *testing.T) {
	x := quietSine()
	d := decode16(NewEncoder(Int16).Append(nil, x))
	silence := decode16(NewEncoder(Int16).Append(nil, make([]float32, len(x))))
	var round []float64
	for _, v := range x {
		round = append(round, math.Round(float64(v)*32767)/32767)
	}
	floor := harmonicPower(silence, 1000.3)
	dith, plain := harmonicPower(d, 1000.3)/floor, harmonicPower(round, 1000.3)/floor
	t.Logf("-80 dBFS tone at 16 bits, harmonic bins over the dither noise: rounded %.1f dB, dithered %.1f dB",
		audiotest.DB(plain), audiotest.DB(dith))
	if audiotest.DB(plain) < 5 || audiotest.DB(dith) > 1.5 {
		t.Fatalf("dither should leave only noise in the harmonic bins: rounded %+.1f dB, dithered %+.1f dB",
			audiotest.DB(plain), audiotest.DB(dith))
	}
}

func TestFormatsRoundTrip(t *testing.T) {
	in := []float32{0, 0.5, -0.5, 0.999, -1, 2, -2}
	f := NewEncoder(Float32).Append(nil, in)
	for i, v := range in {
		if math.Float32frombits(binary.LittleEndian.Uint32(f[i*4:])) != v {
			t.Fatal("float32 must pass through untouched")
		}
	}
	b := NewEncoder(Int24).Append(nil, in)
	for i, v := range in {
		q := int32(uint32(b[i*3]) | uint32(b[i*3+1])<<8 | uint32(b[i*3+2])<<16)
		if q&0x800000 != 0 {
			q |= ^0xFFFFFF
		}
		want := math.Max(-1, math.Min(1, float64(v)))
		if got := float64(q) / 8388607; math.Abs(got-want) > 3.0/8388607 {
			t.Errorf("24-bit %v came back %v", v, got)
		}
	}
	s := decode16(NewEncoder(Int16).Append(nil, []float32{2, -2}))
	if s[0] < 0.999 || s[1] > -0.999 {
		t.Fatalf("16-bit must clamp, got %v", s)
	}
}
