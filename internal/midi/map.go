// Package midi maps susynth sounds to MIDI notes, writes Standard MIDI Files
// and drives a live MIDI output device with clock.
package midi

import (
	"strings"

	"github.com/susynth/susynth/internal/synth"
)

// DrumChannel is MIDI channel 10 (zero-based 9), General MIDI percussion.
const DrumChannel = 9

var gmDrums = map[string]int{
	"bd": 36, "kick": 36, "sn": 38, "snare": 38, "cp": 39, "clap": 39,
	"hh": 42, "hat": 42, "ch": 42, "oh": 46, "openhat": 46,
	"rim": 37, "tom": 45, "cb": 56, "cowbell": 56,
}

// NoteFor converts a sound word to (note, channel). Drums go to channel 10 as
// General MIDI percussion; pitched words use the track's own channel.
// ok is false for sounds with no MIDI meaning (e.g. user samples).
func NoteFor(word string, track int) (note, channel int, ok bool) {
	name := word
	if i := strings.IndexByte(name, ':'); i > 0 {
		name = name[:i]
	}
	if n, found := gmDrums[strings.ToLower(name)]; found {
		return n, DrumChannel, true
	}
	if n, found := synth.ParseNote(name); found {
		ch := (track - 1) % 16
		if ch < 0 {
			ch = 0
		}
		if ch == DrumChannel {
			ch = 15 // keep melodic tracks off the drum channel
		}
		return n, ch, true
	}
	return 0, 0, false
}

// Velocity turns a track gain (0..2) into MIDI velocity 1..127.
func Velocity(gain float64) int {
	v := int(gain*100 + 0.5)
	if v < 1 {
		v = 1
	}
	if v > 127 {
		v = 127
	}
	return v
}
