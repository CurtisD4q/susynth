// Package pcm turns the engine's float samples into the bytes a device or
// file wants. Float output passes through untouched; integer output is
// dithered (TPDF), so quiet sounds and reverb tails fade into a smooth,
// constant hiss instead of the gritty distortion of plain rounding.
package pcm

import (
	"encoding/binary"
	"math"
)

// Format is a sample encoding.
type Format int

const (
	Float32 Format = iota // 32-bit float, little-endian
	Int16                 // 16-bit signed, little-endian, dithered
	Int24                 // 24-bit signed, little-endian, dithered
)

// Bytes is the size of one sample.
func (f Format) Bytes() int {
	switch f {
	case Int16:
		return 2
	case Int24:
		return 3
	}
	return 4
}

// Encoder writes samples in one format. It keeps the dither's random state,
// so use one per stream.
type Encoder struct {
	Format Format
	rng    uint32
}

// NewEncoder returns an encoder for f.
func NewEncoder(f Format) *Encoder { return &Encoder{Format: f, rng: 0x9E3779B9} }

func (e *Encoder) rand() float32 {
	e.rng ^= e.rng << 13
	e.rng ^= e.rng >> 17
	e.rng ^= e.rng << 5
	return float32(e.rng) / float32(math.MaxUint32)
}

// tpdf is triangular dither of ±1 LSB: the difference of two uniform values.
func (e *Encoder) tpdf() float32 { return e.rand() - e.rand() }

// quantise scales v (full scale ±1) to an integer of the given peak value,
// dithered and clamped.
func (e *Encoder) quantise(v float32, peak float32) int32 {
	x := float64(v*peak + e.tpdf())
	x = math.Round(x)
	return int32(math.Max(-float64(peak)-1, math.Min(float64(peak), x)))
}

// Append encodes samples onto dst and returns it.
func (e *Encoder) Append(dst []byte, samples []float32) []byte {
	switch e.Format {
	case Int16:
		for _, v := range samples {
			dst = binary.LittleEndian.AppendUint16(dst, uint16(int16(e.quantise(v, 32767))))
		}
	case Int24:
		for _, v := range samples {
			q := uint32(e.quantise(v, 8388607))
			dst = append(dst, byte(q), byte(q>>8), byte(q>>16))
		}
	default:
		for _, v := range samples {
			dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(v))
		}
	}
	return dst
}
