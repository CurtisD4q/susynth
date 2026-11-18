package midi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"

	"github.com/susynth/susynth/internal/frac"
	"github.com/susynth/susynth/internal/lang"
)

// PPQ is ticks per quarter note; one cycle is four beats.
const PPQ = 480

// Note is one note in a MIDI file; times are in cycles.
type Note struct {
	Track, Channel, Note, Vel int
	Start, Dur                frac.F
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

func ticks(c frac.F) int64 { return c.Mul(frac.Int(4 * PPQ)).Add(frac.New(1, 2)).Floor() }

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
			name = "d" + itoa(id)
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

// FromProgram collects the notes of every track over the first `cycles` cycles.
// It returns the notes, the track names, and how many events had no MIDI
// meaning (user samples) and were skipped.
func FromProgram(p lang.Program, cycles int) (notes []Note, names map[int]string, skipped int) {
	names = map[int]string{}
	for _, t := range p.Tracks {
		names[t.ID] = "d" + itoa(t.ID) + " " + t.Source
		for _, ev := range t.Pattern.Query(frac.Int(0), frac.Int(int64(cycles))) {
			n, ch, ok := NoteFor(ev.Value, t.ID)
			if !ok {
				skipped++
				continue
			}
			notes = append(notes, Note{Track: t.ID, Channel: ch, Note: n, Vel: Velocity(t.Params.Gain),
				Start: ev.Start, Dur: ev.End.Sub(ev.Start)})
		}
	}
	return
}

// BPMOf returns the last "bpm" statement in the program, or def.
func BPMOf(p lang.Program, def float64) float64 {
	for _, c := range p.Commands {
		if c.Name == "bpm" {
			def = c.Value
		}
	}
	return def
}
