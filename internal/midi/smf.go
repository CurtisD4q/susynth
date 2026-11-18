package midi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"

	"github.com/susynth/susynth/internal/engine"
)

// PPQ is ticks per quarter note; one cycle is four beats.
const PPQ = 480

// Note is one note in a MIDI file; times are in beats.
type Note struct {
	Track, Channel, Note, Vel int
	Start, Dur                float64
}

func vlq(v uint32) []byte {
	b := []byte{byte(v & 0x7f)}
	for v >>= 7; v > 0; v >>= 7 {
		b = append([]byte{byte(v&0x7f) | 0x80}, b...)
	}
	return b
}

type msg struct {
	tick  int64
	order int // note-offs sort before note-ons at the same tick
	data  []byte
}

func ticks(beats float64) int64 { return int64(beats*PPQ + 0.5) }

// WriteSMF writes a format-1 file: a tempo track plus one track per susynth
// track (names come from names, keyed by track number).
func WriteSMF(w io.Writer, bpm float64, names map[int]string, notes []Note) error {
	if bpm < 20 || bpm > 400 {
		return errors.New("bpm out of range")
	}
	byTrack := map[int][]Note{}
	for _, n := range notes {
		byTrack[n.Track] = append(byTrack[n.Track], n)
	}
	ids := make([]int, 0, len(byTrack))
	for id := range byTrack {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	var out bytes.Buffer
	out.WriteString("MThd")
	binary.Write(&out, binary.BigEndian, uint32(6))
	binary.Write(&out, binary.BigEndian, uint16(1))
	binary.Write(&out, binary.BigEndian, uint16(1+len(ids)))
	binary.Write(&out, binary.BigEndian, uint16(PPQ))

	chunk := func(body []byte) {
		out.WriteString("MTrk")
		binary.Write(&out, binary.BigEndian, uint32(len(body)))
		out.Write(body)
	}
	meta := func(typ byte, data []byte) []byte {
		return append(append([]byte{0xFF, typ}, vlq(uint32(len(data)))...), data...)
	}

	// tempo track
	var t0 bytes.Buffer
	us := uint32(60e6/bpm + 0.5)
	t0.Write([]byte{0})
	t0.Write(meta(0x51, []byte{byte(us >> 16), byte(us >> 8), byte(us)}))
	t0.Write([]byte{0})
	t0.Write(meta(0x58, []byte{4, 2, 24, 8})) // 4/4
	t0.Write([]byte{0})
	t0.Write(meta(0x2F, nil))
	chunk(t0.Bytes())

	for _, id := range ids {
		var msgs []msg
		for _, n := range byTrack[id] {
			s := ticks(n.Start)
			e := s + ticks(n.Dur)
			if e <= s {
				e = s + 1
			}
			ch := byte(n.Channel & 15)
			msgs = append(msgs,
				msg{s, 1, []byte{0x90 | ch, byte(n.Note), byte(n.Vel)}},
				msg{e, 0, []byte{0x80 | ch, byte(n.Note), 0}})
		}
		sort.SliceStable(msgs, func(i, j int) bool {
			if msgs[i].tick != msgs[j].tick {
				return msgs[i].tick < msgs[j].tick
			}
			return msgs[i].order < msgs[j].order
		})
		var tb bytes.Buffer
		name := names[id]
		if name == "" {
			name = "track " + itoa(id)
		}
		tb.Write([]byte{0})
		tb.Write(meta(0x03, []byte(name)))
		var last int64
		for _, m := range msgs {
			tb.Write(vlq(uint32(m.tick - last)))
			tb.Write(m.data)
			last = m.tick
		}
		tb.Write([]byte{0})
		tb.Write(meta(0x2F, nil))
		chunk(tb.Bytes())
	}
	_, err := w.Write(out.Bytes())
	return err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// FromHits turns played hits into MIDI notes: one MIDI track per live_loop
// or thread, in the order they first played. Hits with no MIDI meaning (user
// samples) are counted in skipped.
func FromHits(hits []engine.Hit, bpm float64) (notes []Note, names map[int]string, skipped int) {
	names = map[int]string{}
	ids := map[string]int{}
	for _, h := range hits {
		id, ok := ids[h.Track]
		if !ok {
			id = len(ids) + 1
			ids[h.Track] = id
			names[id] = h.Track
		}
		n, ch, ok := NoteFor(h.Word, id)
		if !ok {
			skipped++
			continue
		}
		start := float64(h.At) / engine.SampleRate * bpm / 60
		dur := float64(h.Gate)/engine.SampleRate*bpm/60 + h.Params.Release*bpm/60
		if dur <= 0 {
			dur = 0.25
		}
		notes = append(notes, Note{Track: id, Channel: ch, Note: n, Vel: Velocity(h.Params.Gain), Start: start, Dur: dur})
	}
	return
}
