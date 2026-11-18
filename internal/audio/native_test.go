package audio

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestNativeTone(t *testing.T) {
	if os.Getenv("SUSYNTH_AUDIO_TEST") == "" {
		t.Skip("set SUSYNTH_AUDIO_TEST=1 to play a 1s tone")
	}
	s, err := Open("native")
	if err != nil {
		t.Skip("no device:", err)
	}
	defer s.Close()
	t.Log("opened", s.Name())
	start := time.Now()
	buf := make([]int16, 960)
	ph := 0.0
	for i := 0; i < 100; i++ { // 1 second
		for j := 0; j < 480; j++ {
			v := int16(8000 * math.Sin(ph))
			ph += 2 * math.Pi * 440 / 48000
			buf[2*j], buf[2*j+1] = v, v
		}
		if err := s.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("1s of audio written in", time.Since(start))
}
