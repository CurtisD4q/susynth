package synth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Sonic Pi names mapped onto susynth's own voices. Everything is still
// synthesised here, so a Sonic Pi synth plays the closest susynth voice: the
// names work, the timbre is susynth's.

// SonicSynth is one Sonic Pi synth name: the tone voice it plays, or a drum
// for the sc808 drum synths.
type SonicSynth struct {
	Name, Wave, Drum, Desc string
}

// FilterDefaults are a synth's own cutoff (MIDI note, 0 = none), filter
// envelope floor (cutoff_min, 0 = no filter envelope) and resonance, as
// Sonic Pi gives them. Only :tb303 has a filter envelope in Sonic Pi.
type FilterDefaults struct{ Cutoff, CutoffMin, Res float64 }

var filterDefaults = map[string]FilterDefaults{
	"tb303": {Cutoff: 120, CutoffMin: 30, Res: 0.9},
}

// Filter returns the synth's filter defaults.
func (s SonicSynth) Filter() FilterDefaults { return filterDefaults[s.Name] }

// SonicSynths lists the Sonic Pi synth names susynth accepts.
var SonicSynths = []SonicSynth{
	{"beep", "sine", "", "pure sine (Sonic Pi's default)"},
	{"sine", "sine", "", "pure sine"},
	{"mod_sine", "sine", "", "sine"},
	{"mod_beep", "sine", "", "sine"},
	{"saw", "saw", "", "saw, gently low-passed"},
	{"mod_saw", "saw", "", "saw"},
	{"tb303", "saw", "", "acid bass: resonant filter sweep"},
	{"zawa", "saw", "", "saw"},
	{"growl", "saw", "", "saw"},
	{"winwood_lead", "saw", "", "saw lead"},
	{"bass_foundation", "saw", "", "saw bass"},
	{"bass_highend", "saw", "", "saw bass"},
	{"dsaw", "dsaw", "", "two saws a tenth of a semitone apart"},
	{"mod_dsaw", "dsaw", "", "two detuned saws"},
	{"supersaw", "supersaw", "", "five detuned saws"},
	{"prophet", "prophet", "", "pulse-width-modulated pulses, Prophet-style"},
	{"blade", "blade", "", "saw with vibrato through violin formants"},
	{"hoover", "supersaw", "", "detuned saws"},
	{"tech_saws", "supersaw", "", "detuned saws"},
	{"square", "square", "", "square, low-passed"},
	{"pulse", "square", "", "square"},
	{"subpulse", "square", "", "square"},
	{"dpulse", "square", "", "square"},
	{"mod_pulse", "square", "", "square"},
	{"chipbass", "square", "", "chiptune square bass"},
	{"chiplead", "square", "", "chiptune square lead"},
	{"tri", "tri", "", "triangle"},
	{"dtri", "tri", "", "triangle"},
	{"mod_tri", "tri", "", "triangle"},
	{"fm", "fm", "", "two-operator FM"},
	{"mod_fm", "fm", "", "FM"},
	{"pretty_bell", "bell", "", "sine-partial bell"},
	{"dull_bell", "dullbell", "", "dull sine-partial bell"},
	{"pluck", "pluck", "", "plucked string"},
	{"piano", "piano", "", "struck-string piano (a stand-in for Sonic Pi's)"},
	{"kalimba", "pluck", "", "plucked string"},
	{"hollow", "hollow", "", "breathy noise through narrow band-passes"},
	{"organ_tonewheel", "organ", "", "additive organ"},
	{"rodeo", "organ", "", "organ"},
	{"dark_ambience", "ambience", "", "dark ringing noise pad"},
	{"noise", "noise", "", "filtered noise"},
	{"pnoise", "noise", "", "noise"},
	{"bnoise", "noise", "", "noise"},
	{"gnoise", "noise", "", "noise"},
	{"cnoise", "noise", "", "noise"},
	{"chipnoise", "noise", "", "noise"},
	{"sc808_bassdrum", "", "bd", "808 kick"},
	{"sc808_snare", "", "sn", "808 snare"},
	{"sc808_clap", "", "cp", "808 clap"},
	{"sc808_closed_hihat", "", "hh", "808 closed hat"},
	{"sc808_open_hihat", "", "oh", "808 open hat"},
	{"sc808_rimshot", "", "rim", "808 rim"},
	{"sc808_tomlo", "", "tom", "808 tom"},
	{"sc808_tommid", "", "tom:4", "808 tom"},
	{"sc808_tomhi", "", "tom:8", "808 tom"},
	{"sc808_cowbell", "", "cb", "808 cowbell"},
	// Stand-ins for Sonic Pi synths susynth has no voice of its own for yet,
	// so code that uses them still plays.
	{"rhodey", "rhodey", "", "FM electric piano"},
	{"dark_sea_horn", "horn", "", "dark horn drone"},
	{"gabberkick", "gabber", "", "hard distorted kick"},
	{"sc808_claves", "", "clave", "808 claves"},
	{"sc808_congalo", "", "conga", "808 low conga"},
	{"sc808_congamid", "", "conga:4", "808 mid conga"},
	{"sc808_congahi", "", "conga:8", "808 high conga"},
	{"sc808_cymbal", "", "cym", "808 cymbal"},
	{"sc808_maracas", "", "maracas", "808 maracas"},
}

// SonicSample is a built-in Sonic Pi sample name and the drum that plays it.
type SonicSample struct{ Name, Drum string }

// SonicSamples lists the Sonic Pi sample names that have a built-in drum.
var SonicSamples = func() []SonicSample {
	var out []SonicSample
	add := func(drum string, names ...string) {
		for _, n := range names {
			out = append(out, SonicSample{n, drum})
		}
	}
	add("bd", "bd_haus", "bd_808", "bd_ada", "bd_pure", "bd_zum", "bd_gas", "bd_sone", "bd_zome", "bd_boom",
		"bd_klub", "bd_fat", "bd_tek", "bd_mehackit", "bd_chip", "bd_jazz", "bd_ada", "drum_heavy_kick",
		"drum_bass_hard", "drum_bass_soft")
	add("sn", "drum_snare_hard", "drum_snare_soft", "sn_dub", "sn_dolf", "sn_zome", "sn_generic",
		"elec_snare", "elec_hi_snare", "elec_mid_snare", "elec_lo_snare", "elec_filt_snare")
	add("cp", "perc_snap", "perc_snap2", "elec_hollow_kick")
	add("hh", "drum_cymbal_closed", "drum_cymbal_pedal", "hat_cab", "hat_raw", "hat_zap", "hat_snap",
		"hat_tap", "hat_bdu", "hat_gnu", "hat_zild", "hat_hier", "hat_yosh", "hat_psych", "hat_cats",
		"hat_star", "hat_gem", "hat_len", "hat_metal", "hat_sci", "hat_gump", "hat_mess", "hat_noiz")
	add("oh", "drum_cymbal_open", "drum_cymbal_hard", "drum_cymbal_soft", "drum_splash_hard", "drum_splash_soft")
	add("tom", "drum_tom_lo_hard", "drum_tom_lo_soft")
	add("tom:4", "drum_tom_mid_hard", "drum_tom_mid_soft")
	add("tom:8", "drum_tom_hi_hard", "drum_tom_hi_soft")
	add("cb", "drum_cowbell")
	add("rim", "elec_blip", "elec_blip2", "elec_tick", "elec_ping", "elec_pop", "elec_beep", "elec_plip",
		"elec_twip", "elec_triangle", "perc_bell")
	seen := map[string]bool{}
	uniq := out[:0]
	for _, s := range out {
		if !seen[s.Name] {
			seen[s.Name] = true
			uniq = append(uniq, s)
		}
	}
	return uniq
}()

var (
	synthByName  = map[string]SonicSynth{}
	sampleByName = map[string]string{}
)

func init() {
	for _, s := range SonicSynths {
		synthByName[s.Name] = s
	}
	for _, s := range SonicSamples {
		sampleByName[s.Name] = s.Drum
	}
}

// LookupSynth finds a Sonic Pi synth name.
func LookupSynth(name string) (SonicSynth, bool) {
	s, ok := synthByName[strings.TrimPrefix(name, ":")]
	return s, ok
}

// SynthNames lists every accepted synth name.
func SynthNames() []string {
	out := make([]string, len(SonicSynths))
	for i, s := range SonicSynths {
		out[i] = s.Name
	}
	return out
}

// SampleNames lists every sample name Ruby may use: the built-in Sonic Pi
// names and the user's banks.
func SampleNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range SonicSamples {
		out = append(out, s.Name)
		seen[s.Name] = true
	}
	for _, n := range BuiltinSamples() {
		if !seen[n] {
			out = append(out, n)
		}
	}
	for _, b := range Banks() {
		out = append(out, b.Name)
		for _, s := range b.Samples {
			base := strings.ToLower(strings.TrimSuffix(s.Name, filepath.Ext(s.Name)))
			if bankName.MatchString(base) {
				out = append(out, base)
			}
		}
	}
	sort.Strings(out)
	return out
}

var fileCache struct {
	sync.Mutex
	keys map[string]string
}

// ResolveSample turns what `sample` was given into a voice word: a user bank
// (with an index), a sample file anywhere in the samples folder by its name,
// a .wav path, or a built-in Sonic Pi drum.
func ResolveSample(name string, idx int) (string, error) {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".wav") || strings.HasSuffix(lower, ".flac") || strings.ContainsAny(name, `/\`) {
		return loadFile(name)
	}
	// a sample of the user's is read now, ahead of when it plays
	if sm := findSample(lower, idx); sm != nil {
		if err := sm.ensure(); err != nil {
			return "", fmt.Errorf("sample :%s: %w", name, err)
		}
		return fmt.Sprintf("%s:%d", lower, idx), nil
	}
	if key := fileInBank(lower); key != "" {
		if err := findSample(key, 0).ensure(); err != nil {
			return "", fmt.Errorf("sample :%s: %w", name, err)
		}
		return key, nil
	}
	if key, err := builtinKey(lower); key != "" || err != nil {
		return key, err
	}
	if d, ok := sampleByName[lower]; ok {
		return d, nil
	}
	return "", fmt.Errorf("sample :%s isn't available", name)
}

// fileInBank finds a sample file by its own name (kick for kick.wav) in any
// bank, registering it as a one-file bank.
func fileInBank(base string) string {
	reg.Lock()
	defer reg.Unlock()
	if b := reg.banks["@"+base]; b != nil {
		return "@" + base
	}
	for _, b := range reg.banks {
		if strings.HasPrefix(b.Name, "@") { // only the user's own banks
			continue
		}
		for _, s := range b.Samples {
			if strings.ToLower(strings.TrimSuffix(s.Name, filepath.Ext(s.Name))) == base {
				reg.banks["@"+base] = &Bank{Name: "@" + base, Samples: []*Sample{s}}
				return "@" + base
			}
		}
	}
	return ""
}

func loadFile(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(h, path[2:])
		}
	}
	fileCache.Lock()
	defer fileCache.Unlock()
	if fileCache.keys == nil {
		fileCache.keys = map[string]string{}
	}
	if k, ok := fileCache.keys[path]; ok {
		return k, nil
	}
	isFLAC := strings.HasSuffix(strings.ToLower(path), ".flac")
	if !strings.HasSuffix(strings.ToLower(path), ".wav") && !isFLAC {
		return "", errors.New("only .wav and .flac files can be loaded")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("sample %q: %w", path, err)
	}
	if info.Size() > maxSampleFile {
		return "", fmt.Errorf("sample %q is larger than %d MB", path, maxSampleFile>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var s *Sample
	if isFLAC {
		if s, err = sampleFromFLAC(b); err != nil {
			return "", fmt.Errorf("sample %q: %w", path, err)
		}
	} else {
		if s, err = ParseWAV(b); err != nil {
			return "", fmt.Errorf("sample %q: %w", path, err)
		}
		if s.prepared() > maxSampleTotal {
			return "", fmt.Errorf("sample %q is too long to load", path)
		}
		s.prepare()
	}
	s.Name = filepath.Base(path)
	key := fmt.Sprintf("@file%d", len(fileCache.keys)+1)
	reg.Lock()
	if reg.banks == nil {
		reg.banks = map[string]*Bank{}
	}
	reg.banks[key] = &Bank{Name: key, Samples: []*Sample{s}}
	reg.Unlock()
	fileCache.keys[path] = key
	return key, nil
}
