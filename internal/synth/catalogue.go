package synth

import (
	"fmt"
	"sort"
	"strings"
)

// The catalogue is the one list of what each Sonic Pi synth is here: whether
// it is a port of Sonic Pi's own design, the note it plays when the code names
// none, and the option defaults its design has. Ruby learns it from here when
// it starts, so the language side keeps no lists of its own.

// nativeDefaults are options a ported synth's design gives when the code does
// not (times are beats).
var nativeDefaults = map[string]map[string]float64{
	"tb303":           {"attack": 0.01},
	"hoover":          {"attack": 0.05},
	"growl":           {"attack": 0.1},
	"kalimba":         {"sustain": 4},
	"gabberkick":      {"amp": 0.5, "attack": 0.001, "decay": 0.01, "sustain": 0.3, "release": 0.02, "decay_level": 0.7, "sustain_level": 0.7},
	"organ_tonewheel": {"attack": 0.01, "sustain": 1, "release": 0.01},
	"rhodey":          {"attack": 0.001, "decay": 1, "sustain": 0, "release": 1, "sustain_level": 0},
}

// timeOptions are the options that are lengths of time in beats, which the
// tempo scales to seconds.
var timeOptions = []string{"attack", "decay", "sustain", "release", "cutoff_attack", "cutoff_decay",
	"cutoff_sustain", "cutoff_release", "phase"}

// IsPorted reports whether the Sonic Pi synth called name plays a port of its
// own design.
func IsPorted(name string) bool {
	sy, ok := LookupSynth(name)
	return ok && IsNative(sy.Wave)
}

// Catalogue is the text Ruby is given: entries of name|note|k=v;k=v for each
// ported synth, then the time options.
func Catalogue() string {
	var ents []string
	for _, sy := range SonicSynths {
		if !IsNative(sy.Wave) {
			continue
		}
		note := ""
		if n, ok := DefaultNote(sy.Wave); ok {
			note = fmt.Sprint(n)
		}
		var kv []string
		for k, v := range nativeDefaults[sy.Name] {
			kv = append(kv, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(kv)
		ents = append(ents, sy.Name+"|"+note+"|"+strings.Join(kv, ";"))
	}
	return strings.Join(ents, ",") + "\t" + strings.Join(timeOptions, ",")
}
