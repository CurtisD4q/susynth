// Package audio sends rendered PCM to the system. The default "native" backend
// uses oto (no external programs, no cgo). If it cannot open a device, the
// engine falls back to piping raw PCM into pw-cat, paplay or aplay.
package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// Sink consumes interleaved stereo 48 kHz int16 frames.
type Sink interface {
	Name() string
	Write([]int16) error
	Close() error
	// Latency is the player's own buffering, used to line up the playhead.
	Latency() time.Duration
}

type backend struct {
	name    string
	bin     string
	args    []string
	latency time.Duration
}

var backends = []backend{
	{"pipewire", "pw-cat", []string{"-p", "--rate", "48000", "--channels", "2", "--format", "s16", "--latency", "60ms", "-"}, 90 * time.Millisecond},
	{"pulseaudio", "paplay", []string{"--raw", "--format=s16le", "--rate=48000", "--channels=2", "--latency-msec=60"}, 90 * time.Millisecond},
	{"alsa", "aplay", []string{"-q", "-t", "raw", "-f", "S16_LE", "-r", "48000", "-c", "2", "-B", "60000"}, 90 * time.Millisecond},
}

// Available lists the backend names whose player binary is installed.
func Available() []string {
	out := []string{"native"}
	for _, b := range backends {
		if _, err := exec.LookPath(b.bin); err == nil {
			out = append(out, b.name)
		}
	}
	return out
}

// Open starts a sink. name is "auto" (first available), a backend name, or
// "none". "none" returns a silent sink that still paces the engine.
func Open(name string) (Sink, error) {
	if name == "none" {
		return &nullSink{}, nil
	}
	var nativeErr error
	if name == "auto" || name == "native" {
		s, err := openNative()
		if err == nil {
			return s, nil
		}
		nativeErr = err
		if name == "native" {
			return nil, fmt.Errorf("native audio: %w", err)
		}
	}
	for _, b := range backends {
		if name != "auto" && name != b.name {
			continue
		}
		path, err := exec.LookPath(b.bin)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, b.args...)
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("%s: %w", b.bin, err)
		}
		return &pipeSink{name: b.name, cmd: cmd, in: in, latency: b.latency}, nil
	}
	if name == "auto" {
		return nil, fmt.Errorf("no audio output: native: %v; no pw-cat/paplay/aplay found either", nativeErr)
	}
	return nil, fmt.Errorf("audio backend %q not available", name)
}

type pipeSink struct {
	name    string
	cmd     *exec.Cmd
	in      io.WriteCloser
	latency time.Duration
	buf     []byte
}

func (s *pipeSink) Name() string           { return s.name }
func (s *pipeSink) Latency() time.Duration { return s.latency }

func (s *pipeSink) Write(frames []int16) error {
	if cap(s.buf) < len(frames)*2 {
		s.buf = make([]byte, len(frames)*2)
	}
	b := s.buf[:len(frames)*2]
	for i, v := range frames {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	_, err := s.in.Write(b)
	return err
}

func (s *pipeSink) Close() error {
	s.in.Close()
	done := make(chan struct{})
	go func() { s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		s.cmd.Process.Kill()
		<-done
	}
	return nil
}

type nullSink struct{}

func (*nullSink) Name() string           { return "silent" }
func (*nullSink) Write([]int16) error    { return nil }
func (*nullSink) Close() error           { return nil }
func (*nullSink) Latency() time.Duration { return 0 }
