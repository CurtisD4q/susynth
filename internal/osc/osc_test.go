package osc

import (
	"bytes"
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/synth"
)

func TestMessageEncoding(t *testing.T) {
	got := Message("/a", "hi", int32(7), float32(1.5))
	want := []byte("/a\x00\x00,sif\x00\x00\x00\x00hi\x00\x00\x00\x00\x00\x07\x3f\xc0\x00\x00")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if len(got)%4 != 0 {
		t.Fatal("OSC messages must be 4-byte aligned")
	}
}

func TestNTP(t *testing.T) {
	ts := NTP(time.Unix(0, 0).Add(500 * time.Millisecond))
	if ts>>32 != 2208988800 || math.Abs(float64(ts&0xffffffff)/(1<<32)-0.5) > 1e-6 {
		t.Fatalf("bad time tag %x", ts)
	}
}

func TestSendsDirtPlayBundle(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	defer pc.Close()
	o, err := Open(pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(50 * time.Millisecond)
	o.Emit(engine.Event{Kind: engine.KindClock, At: at}) // ignored
	o.Emit(engine.Event{Kind: engine.KindNote, At: at, Word: "bd:3", BPM: 120, Dur: 250 * time.Millisecond,
		Params: synth.Params{Gain: 0.8, Pan: 1, Speed: 1, LPF: 900}})
	buf := make([]byte, 2048)
	pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	b := buf[:n]
	if !bytes.HasPrefix(b, []byte("#bundle\x00")) {
		t.Fatalf("not a bundle: %q", b[:8])
	}
	if binary.BigEndian.Uint64(b[8:]) != NTP(at) {
		t.Error("time tag mismatch")
	}
	size := int(binary.BigEndian.Uint32(b[16:]))
	msg := b[20 : 20+size]
	if !bytes.HasPrefix(msg, []byte("/dirt/play\x00")) {
		t.Fatalf("wrong address %q", msg[:12])
	}
	for _, want := range []string{"cps", "orbit", "gain", "pan", "cutoff", "bd"} {
		if !bytes.Contains(msg, []byte(want)) {
			t.Errorf("message lacks %q", want)
		}
	}
	o.Close()
}
