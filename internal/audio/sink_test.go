package audio

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/pcm"
)

// A simulated sound card pulls 30 ms at a time at real speed, while an
// engine-like writer keeps ~100 ms ahead of the wall clock in 10 ms blocks.
// Every sample written must come out once, in order, with no silence
// inserted: the bug this guards against padded short reads with zeros,
// which chopped live playback into short pieces.
func TestNativeSinkNeverInsertsGaps(t *testing.T) {
	s := &nativeSink{enc: pcm.NewEncoder(pcm.Float32)}
	s.cond = sync.NewCond(&s.mu)

	const secs = 2
	total := 48000 * secs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { // the engine
		begin := time.Now()
		written := 0
		buf := make([]float32, 960)
		for written < total {
			heard := int(time.Since(begin).Seconds() * 48000)
			for written < heard+4800 && written < total {
				for j := 0; j < 480; j++ {
					v := float32((written+j)%30000+1) / 32768 // never zero: easy to spot gaps
					buf[2*j], buf[2*j+1] = v, v
				}
				s.Write(buf)
				written += 480
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	var got []float32
	chunk := make([]byte, 1440*frameBytes) // 30 ms
	begin := time.Now()
	for len(got) < total && ctx.Err() == nil {
		n, err := s.Read(chunk)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+frameBytes-1 < n; i += frameBytes {
			got = append(got, math.Float32frombits(binary.LittleEndian.Uint32(chunk[i:])))
		}
		// the card plays what it got before asking again
		time.Sleep(time.Duration(n/frameBytes) * time.Second / 48000)
		if time.Since(begin) > 10*time.Second {
			t.Fatal("too slow")
		}
	}
	s.Close()
	silent, wrong := 0, 0
	for i, v := range got[:total] {
		if v == 0 {
			silent++
		} else if v != float32(i%30000+1)/32768 {
			wrong++
		}
	}
	if silent > 0 || wrong > 0 {
		t.Errorf("%d silent and %d out-of-order frames in %d", silent, wrong, total)
	}
	t.Logf("%d frames, played in %v", total, time.Since(begin).Round(time.Millisecond))
}
