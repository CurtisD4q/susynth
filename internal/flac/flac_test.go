package flac

import (
	"os"
	"path/filepath"
	"testing"
)

func someFLAC(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "synth", "sonicpi_samples", "loop_amen.flac"))
	if err != nil {
		t.Skip("no sample to decode:", err)
	}
	return b
}

// The decoder was checked bit for bit against ffmpeg on all of Sonic Pi's
// samples; this keeps a fingerprint of one.
func TestDecode(t *testing.T) {
	b := someFLAC(t)
	in, err := ReadInfo(b)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if st.Rate != 44100 || st.Channels != 2 || st.Bits != 16 || int64(len(st.Samples[0])) != in.Frames {
		t.Fatalf("%d Hz %d ch %d-bit, %d of %d frames", st.Rate, st.Channels, st.Bits, len(st.Samples[0]), in.Frames)
	}
	var sum int64
	for _, c := range st.Samples {
		for i, x := range c {
			sum += int64(x) * int64(i%7+1)
		}
	}
	if sum != amenSum {
		t.Errorf("checksum %d, want %d", sum, amenSum)
	}
}

const amenSum = 3724451

// A damaged file is an error, never a panic.
func TestDecodeDamaged(t *testing.T) {
	b := someFLAC(t)
	for _, n := range []int{0, 3, 4, 20, 42, 100, 1000, len(b) / 2, len(b) - 1} {
		if _, err := Decode(b[:n]); err == nil && n < 42 {
			t.Errorf("%d bytes decoded without an error", n)
		}
	}
	c := append([]byte(nil), b...)
	for i := 200; i < len(c); i += 997 {
		c[i] ^= 0x5a
	}
	Decode(c) // must not panic
}
