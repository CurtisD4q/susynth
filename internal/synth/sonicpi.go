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

// SonicSynths lists the Sonic Pi synth names susynth accepts.
var SonicSynths = []SonicSynth{
	{"beep", "sine", "", "pure sine (Sonic Pi's default)"},
	{"sine", "sine", "", "pure sine"},
	{"mod_sine", "sine", "", "sine"},
	{"mod_beep", "sine", "", "sine"},
	{"saw", "saw", "", "saw, gently low-passed"},
	{"mod_saw", "saw", "", "saw"},
	{"tb303", "saw", "", "acid bass: saw, use cutoff:"},
	{"zawa", "saw", "", "saw"},
	{"growl", "saw", "", "saw"},
	{"winwood_lead", "saw", "", "saw lead"},
	{"bass_foundation", "saw", "", "saw bass"},
	{"bass_highend", "saw", "", "saw bass"},
	{"dsaw", "supersaw", "", "detuned saws"},
	{"mod_dsaw", "supersaw", "", "detuned saws"},
	{"supersaw", "supersaw", "", "five detuned saws"},
	{"prophet", "supersaw", "", "wide analogue-style pad"},
	{"blade", "supersaw", "", "detuned saws"},
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
	{"pretty_bell", "fm", "", "FM bell"},
	{"dull_bell", "fm", "", "FM bell"},
	{"pluck", "pluck", "", "plucked string"},
	{"piano", "pluck", "", "plucked string (no piano here)"},
	{"kalimba", "pluck", "", "plucked string"},
	{"hollow", "organ", "", "organ-like"},
	{"organ_tonewheel", "organ", "", "additive organ"},
	{"rodeo", "organ", "", "organ"},
	{"dark_ambience", "organ", "", "organ drone"},
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
	for _, s := range SonicSamples {
		out = append(out, s.Name)
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
	if strings.HasSuffix(lower, ".wav") || strings.ContainsAny(name, `/\`) {
		return loadFile(name)
	}
	if lookupSample(lower, 0) != nil {
		return fmt.Sprintf("%s:%d", lower, idx), nil
	}
	if key := fileInBank(lower); key != "" {
		return key, nil
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
	if !strings.HasSuffix(strings.ToLower(path), ".wav") {
		return "", errors.New("only .wav files can be loaded")
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
	s, err := ParseWAV(b)
	if err != nil {
		return "", fmt.Errorf("sample %q: %w", path, err)
	}
	if s.prepared() > maxSampleTotal {
		return "", fmt.Errorf("sample %q is too long to load", path)
	}
	s.prepare()
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
