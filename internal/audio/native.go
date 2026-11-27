package audio

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"

	"github.com/susynth/susynth/internal/pcm"
)

// The native backend talks to the sound system directly through oto: PulseAudio
// / PipeWire-pulse over its socket protocol (pure Go) or ALSA via dlopen, CoreAudio
// on macOS, WASAPI on Windows. No external player program is needed and no cgo.

const (
	nativeBuffer = 40 * time.Millisecond  // oto's device buffer
	playerBuffer = 30 * time.Millisecond  // how much oto pulls from us at a time
	nativeAhead  = 100 * time.Millisecond // how much we keep queued ahead of oto
	frameBytes   = 8                      // stereo float32
	bytesPerSec  = 48000 * frameBytes
)

var (
	ctxOnce sync.Once
	ctx     *oto.Context
	ctxErr  error
)

// oto allows one context per process, so it is created once and shared.
func nativeContext() (*oto.Context, error) {
	ctxOnce.Do(func() {
		c, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   48000,
			ChannelCount: 2,
			Format:       oto.FormatFloat32LE, // oto converts for the device
			BufferSize:   nativeBuffer,
		})
		if err != nil {
			ctxErr = err
			return
		}
		select {
		case <-ready:
			ctx = c
		case <-time.After(3 * time.Second):
			ctxErr = fmt.Errorf("audio device did not become ready")
		}
	})
	return ctx, ctxErr
}

// nativeSink is a push-style Sink on top of oto's pull-style player: Write
// queues bytes and blocks while more than nativeAhead is queued, which paces
// the engine. Read hands oto only the audio that exists and waits briefly
// when there is none: padding short reads with silence would cut gaps into
// the music (oto asks for up to half a second per read by default).
type nativeSink struct {
	player *oto.Player
	mu     sync.Mutex
	cond   *sync.Cond
	q      []byte
	closed bool
	enc    *pcm.Encoder
}

func openNative() (Sink, error) {
	c, err := nativeContext()
	if err != nil {
		return nil, err
	}
	s := &nativeSink{enc: pcm.NewEncoder(pcm.Float32)}
	s.cond = sync.NewCond(&s.mu)
	s.player = c.NewPlayer(s)
	s.player.SetBufferSize(int(playerBuffer.Seconds()*bytesPerSec) / frameBytes * frameBytes)
	s.player.Play()
	return s, nil
}

func (s *nativeSink) Name() string { return "native" }
func (s *nativeSink) Latency() time.Duration {
	return nativeBuffer + playerBuffer + nativeAhead/2
}

// Read implements io.Reader for oto. It returns whatever audio is queued
// (whole frames only). With nothing queued it waits up to 50 ms for the
// engine, and only then returns a short stretch of silence so oto keeps going.
func (s *nativeSink) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.q) < frameBytes && !s.closed {
		deadline := time.Now().Add(50 * time.Millisecond)
		t := time.AfterFunc(50*time.Millisecond, func() {
			s.mu.Lock()
			s.cond.Broadcast()
			s.mu.Unlock()
		})
		for len(s.q) < frameBytes && !s.closed && time.Now().Before(deadline) {
			s.cond.Wait()
		}
		t.Stop()
	}
	if s.closed {
		return 0, io.EOF
	}
	if len(s.q) < frameBytes { // the engine has stalled: a little silence, not a long gap
		n := min(len(p), int(0.005*bytesPerSec)) / frameBytes * frameBytes
		clear(p[:n])
		return n, nil
	}
	n := min(len(p), len(s.q)) / frameBytes * frameBytes
	copy(p, s.q[:n])
	s.q = s.q[n:]
	s.cond.Broadcast()
	return n, nil
}

func (s *nativeSink) Write(frames []float32) error {
	max := int(nativeAhead.Seconds() * bytesPerSec)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.q) > max && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return fmt.Errorf("audio sink closed")
	}
	s.q = s.enc.Append(s.q, frames)
	s.cond.Broadcast()
	if s.player != nil {
		if err := s.player.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *nativeSink) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	// oto (v3.4 on) frees a player itself, so there is nothing to close.
	return nil
}
