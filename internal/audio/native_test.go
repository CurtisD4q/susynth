package audio

import (
	"math"
	"os"
	"testing"
	"time"
)

// Plays a 2 s tone through the real sound device; listen for a smooth tone.
// (Gap-freedom is checked without a device in TestNativeSinkNeverInsertsGaps.)
func TestNativeTone(t *testing.T) {
	if os.Getenv("SUSYNTH_AUDIO_TEST") == "" {
		t.Skip("set SUSYNTH_AUDIO_TEST=1 to play a 2 s tone")
	}
	s, err := Open("native")
	if err != nil {
		t.Skip("no device:", err)
	}
	buf := make([]int16, 960)
	ph := 0.0
	start := time.Now()
	for i := 0; i < 200; i++ { // 2 seconds, written in real-time-sized blocks
		for j := 0; j < 480; j++ {
			v := int16(8000 * math.Sin(ph))
			ph += 2 * math.Pi * 440 / 48000
			buf[2*j], buf[2*j+1] = v, v
		}
		if err := s.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	s.Close()
	t.Log("2 s written in", time.Since(start))
}
