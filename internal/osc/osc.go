// Package osc sends susynth events to SuperDirt (or anything that speaks
// Tidal's /dirt/play) as timestamped OSC bundles over UDP.
package osc

import (
	"encoding/binary"
	"math"
	"net"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/synth"
)

// DefaultAddr is where SuperDirt listens.
const DefaultAddr = "127.0.0.1:57120"

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func str(b []byte, s string) []byte { return pad4(append(append(b, s...), 0)) }

// Message encodes one OSC message. Arguments may be string, int32 or float32.
func Message(addr string, args ...any) []byte {
	b := str(nil, addr)
	tags := ","
	var payload []byte
	for _, a := range args {
		switch v := a.(type) {
		case string:
			tags += "s"
			payload = str(payload, v)
		case int32:
			tags += "i"
			payload = binary.BigEndian.AppendUint32(payload, uint32(v))
		case float32:
			tags += "f"
			payload = binary.BigEndian.AppendUint32(payload, math.Float32bits(v))
		}
	}
	return append(str(b, tags), payload...)
}

// NTP returns the 64-bit OSC time tag for t.
func NTP(t time.Time) uint64 {
	const ntpEpoch = 2208988800
	sec := uint64(t.Unix()) + ntpEpoch
	frac := uint64(float64(t.Nanosecond()) / 1e9 * (1 << 32))
	return sec<<32 | frac
}

// Bundle wraps messages with a time tag.
func Bundle(at time.Time, msgs ...[]byte) []byte {
	b := str(nil, "#bundle")
	b = binary.BigEndian.AppendUint64(b, NTP(at))
	for _, m := range msgs {
		b = binary.BigEndian.AppendUint32(b, uint32(len(m)))
		b = append(b, m...)
	}
	return b
}

// superSynth maps susynth tone voices to SuperDirt synth names.
var superSynth = map[string]string{
	"sine": "superhammond", "tri": "superhammond", "organ": "superhammond",
	"saw": "supersaw", "supersaw": "supersaw", "square": "supersquare",
	"pluck": "superpiano", "fm": "superpwm", "noise": "supernoise",
	"dsaw": "supersaw", "prophet": "superpwm", "blade": "supersaw", "bell": "superfork",
	"dullbell": "superfork", "piano": "superpiano", "rhodey": "superfm", "hollow": "supernoise",
	"ambience": "supernoise", "gabber": "superkick",
}

// PlayMessage builds the /dirt/play message for a note event.
func PlayMessage(ev engine.Event, cps float64) []byte {
	word := ev.Word
	n := int32(0)
	if i := strings.IndexByte(word, ':'); i > 0 {
		for _, c := range word[i+1:] {
			if c >= '0' && c <= '9' {
				n = n*10 + int32(c-'0')
			}
		}
		word = word[:i]
	}
	args := []any{}
	add := func(k string, v any) { args = append(args, k, v) }
	if note, ok := synth.ParseNote(word); ok {
		wave := ev.Params.Wave
		if wave == "" {
			wave = "saw"
		}
		add("s", superSynth[wave])
		add("note", float32(note-60))
	} else {
		add("s", strings.ToLower(word))
		add("n", n)
	}
	add("cps", float32(cps))
	add("orbit", int32(0))
	add("gain", float32(ev.Params.Gain))
	add("pan", float32((ev.Params.Pan+1)/2))
	add("speed", float32(ev.Params.Speed))
	add("sustain", float32(ev.Dur.Seconds()))
	add("delta", float32(ev.Dur.Seconds()))
	if ev.Params.LPF > 0 {
		add("cutoff", float32(ev.Params.LPF))
	}
	if ev.Params.HPF > 0 {
		add("hcutoff", float32(ev.Params.HPF))
	}
	if ev.Params.Crush > 0 {
		add("crush", float32(ev.Params.Crush))
	}
	if ev.Params.Shape > 0 {
		add("shape", float32(ev.Params.Shape))
	}
	if ev.Params.Delay > 0 {
		add("delay", float32(ev.Params.Delay))
	}
	if ev.Params.Room > 0 {
		add("room", float32(ev.Params.Room))
	}
	return Message("/dirt/play", args...)
}

// Out is an engine.Output that sends note events to SuperDirt.
type Out struct {
	conn net.Conn
	in   chan engine.Event
	done chan struct{}
}

// Open dials addr ("host:port") over UDP.
func Open(addr string) (*Out, error) {
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	o := &Out{conn: conn, in: make(chan engine.Event, 2048), done: make(chan struct{})}
	go func() {
		defer close(o.done)
		for ev := range o.in {
			if ev.Kind != engine.KindNote {
				continue
			}
			cps := ev.BPM / 60 / engine.BeatsPerCycle
			o.conn.Write(Bundle(ev.At, PlayMessage(ev, cps)))
		}
	}()
	return o, nil
}

// Emit queues an event without blocking.
func (o *Out) Emit(ev engine.Event) {
	select {
	case o.in <- ev:
	default:
	}
}

// Close stops sending.
func (o *Out) Close() error {
	close(o.in)
	<-o.done
	return o.conn.Close()
}
