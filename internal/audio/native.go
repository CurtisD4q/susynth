package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// The native backend talks to the sound system directly through oto: PulseAudio
// / PipeWire-pulse over its socket protocol (pure Go) or ALSA via dlopen, CoreAudio
// on macOS, WASAPI on Windows. No external player program is needed and no cgo.

const (
	nativeBuffer = 40 * time.Millisecond  // oto's device buffer
	nativeAhead  = 100 * time.Millisecond // how much we keep queued ahead of oto
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
			Format:       oto.FormatSignedInt16LE,
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
// the engine; the player reads from the queue and gets silence on underrun.
type nativeSink struct {
	player *oto.Player
	mu     sync.Mutex
	cond   *sync.Cond
	q      []byte
	closed bool
}

func openNative() (Sink, error) {
	c, err := nativeContext()
	if err != nil {
		return nil, err
	}
	s := &nativeSink{}
	s.cond = sync.NewCond(&s.mu)
	s.player = c.NewPlayer(s)
	s.player.Play()
	return s, nil
}

func (s *nativeSink) Name() string { return "native" }
func (s *nativeSink) Latency() time.Duration {
	return nativeBuffer + nativeAhead/2
}

// Read implements io.Reader for oto. It never blocks and always fills p.
func (s *nativeSink) Read(p []byte) (int, error) {
	s.mu.Lock()
	n := copy(p, s.q)
	s.q = s.q[n:]
	s.cond.Broadcast()
	s.mu.Unlock()
	clear(p[n:])
	return len(p), nil
}

func (s *nativeSink) Write(frames []int16) error {
	max := int(nativeAhead.Seconds() * 48000 * 4)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.q) > max && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return fmt.Errorf("audio sink closed")
	}
	for _, v := range frames {
		s.q = binary.LittleEndian.AppendUint16(s.q, uint16(v))
	}
	if err := s.player.Err(); err != nil {
		return err
	}
	return nil
}

func (s *nativeSink) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	return s.player.Close()
}
