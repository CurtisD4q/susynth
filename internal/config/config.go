// Package config persists the few settings susynth keeps between runs.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/susynth/susynth/internal/paths"
)

// Config is saved as config.json in the config dir.
type Config struct {
	Theme string  `json:"theme"` // default | large | contrast
	Sink  string  `json:"sink"`  // auto | pipewire | pulseaudio | alsa | none
	BPM   float64 `json:"bpm"`
	Set   string  `json:"last_set"`

	AutoLoad   bool `json:"auto_load_last_set"` // reopen the last set on start
	Hints      bool `json:"show_key_hints"`     // key hint row under the editor
	StartOnBar bool `json:"start_on_bar"`       // ^E and Run wait for the next bar
	FrameHz    int  `json:"frame_hz"`           // screen redraw rate: 15, 30 or 60

	Internal   bool   `json:"internal_synth"` // play through the built-in synth
	OSC        bool   `json:"osc_enabled"`    // send notes to SuperDirt
	OSCAddr    string `json:"osc_addr"`       // host:port
	MidiDev    string `json:"midi_device"`    // raw MIDI device path, "" = off
	MidiClock  bool   `json:"midi_clock"`     // send clock/start/stop to the MIDI device
	SamplesDir string `json:"samples_dir"`    // "" = <data dir>/samples
}

// Default returns first-run settings.
func Default() Config {
	return Config{Theme: "default", Sink: "auto", BPM: 120, AutoLoad: true, Hints: true, FrameHz: 30,
		Internal: true, OSCAddr: "127.0.0.1:57120", MidiClock: true}
}

func file() string { return filepath.Join(paths.ConfigDir(), "config.json") }

// Load returns the saved config and whether this is the first launch.
func Load() (Config, bool) {
	c := Default()
	b, err := os.ReadFile(file())
	if err != nil {
		return c, true
	}
	if json.Unmarshal(b, &c) != nil {
		return Default(), true
	}
	if c.BPM < 20 || c.BPM > 400 {
		c.BPM = 120
	}
	if c.FrameHz != 15 && c.FrameHz != 30 && c.FrameHz != 60 {
		c.FrameHz = 30
	}
	return c, false
}

// Save writes the config.
func (c Config) Save() error {
	if _, err := paths.Ensure(paths.ConfigDir()); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(file(), b, 0o644)
}
