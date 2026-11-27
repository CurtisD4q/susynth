package ui

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/susynth/susynth/internal/audio"
	"github.com/susynth/susynth/internal/midi"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/synth"
)

const (
	setTheme = iota
	setAudio
	setTempo
	setStartBar
	setInternal
	setOSC
	setOSCAddr
	setMidiDev
	setMidiClock
	setSamples
	setAutoLoad
	setRefresh
	numSettings
)

type settingRow struct{ label, hint string }

var settingRows = [numSettings]settingRow{
	setTheme:     {"Theme", "default, large (projector) or contrast (washed-out projectors)"},
	setAudio:     {"Audio output", "auto = built-in (native), then PipeWire/PulseAudio/ALSA players; none = silent (recording still works)"},
	setTempo:     {"Tempo (BPM)", "bar counter and MIDI clock until your code calls use_bpm (Sonic Pi starts at 60)"},
	setStartBar:  {"Start on next bar", "while music is playing, ^E and Run wait for the next bar"},
	setInternal:  {"Internal synth", "off = only OSC/MIDI devices make sound (events still flow)"},
	setOSC:       {"OSC → SuperDirt", "send every note to SuperDirt as /dirt/play bundles over UDP"},
	setOSCAddr:   {"OSC address", "Enter to edit host:port (SuperDirt default 127.0.0.1:57120)"},
	setMidiDev:   {"MIDI output", "←→ cycle detected devices · Enter to type a device path, e.g. /dev/snd/midiC1D0"},
	setMidiClock: {"MIDI clock", "send clock, start and stop so external gear follows susynth's tempo"},
	setSamples:   {"Samples", "Enter reloads the samples folder (one sub-folder per bank of .wav files)"},
	setAutoLoad:  {"Reopen last set", "load the set you were editing when susynth starts"},
	setRefresh:   {"Screen refresh", "playhead redraw rate; 15 Hz is lighter on slow terminals"},
}

func (a *App) samplesDir() string {
	if a.cfg.SamplesDir != "" {
		return paths.Expand(a.cfg.SamplesDir)
	}
	return paths.SamplesDir()
}

func (a *App) settingValue(i int) string {
	switch i {
	case setTheme:
		return a.th.Name
	case setAudio:
		return a.cfg.Sink
	case setTempo:
		return fmt.Sprintf("%.0f", a.eng.BPM())
	case setStartBar:
		return onOff(a.cfg.StartOnBar)
	case setInternal:
		return onOff(a.cfg.Internal)
	case setOSC:
		return onOff(a.cfg.OSC)
	case setOSCAddr:
		return a.cfg.OSCAddr
	case setMidiDev:
		if a.cfg.MidiDev == "" {
			return "off"
		}
		return a.cfg.MidiDev
	case setMidiClock:
		return onOff(a.cfg.MidiClock)
	case setSamples:
		n := 0
		for _, b := range synth.Banks() {
			n += len(b.Samples)
		}
		return fmt.Sprintf("%d banks, %d files", len(synth.Banks()), n)
	case setAutoLoad:
		return onOff(a.cfg.AutoLoad)
	case setRefresh:
		return fmt.Sprintf("%d Hz", a.cfg.FrameHz)
	}
	return ""
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (a *App) changeSetting(i, d int, enter bool) {
	switch i {
	case setTheme:
		n := len(themes)
		for k, t := range themes {
			if t == a.th {
				a.th = themes[((k+d)%n+n)%n]
				a.usePalette()
				break
			}
		}
		a.cfg.Theme = a.th.Name
	case setAudio:
		a.stepAudio(d)
	case setTempo:
		a.eng.SetBPM(a.eng.BPM() + float64(d))
		a.cfg.BPM = a.eng.BPM()
	case setStartBar:
		a.cfg.StartOnBar = !a.cfg.StartOnBar
		if a.seq != nil {
			a.seq.SetQuantise(a.cfg.StartOnBar)
		}
	case setInternal:
		a.cfg.Internal = !a.cfg.Internal
		a.applyOutputs()
	case setOSC:
		a.cfg.OSC = !a.cfg.OSC
		a.applyOutputs()
	case setOSCAddr:
		if enter {
			a.startPrompt("oscaddr", "OSC host:port: ", a.cfg.OSCAddr)
		}
		return
	case setMidiDev:
		if enter {
			a.startPrompt("mididev", "MIDI device path (empty = off): ", a.cfg.MidiDev)
			return
		}
		opts := append([]string{""}, midi.Devices()...)
		idx := 0
		for k, o := range opts {
			if o == a.cfg.MidiDev {
				idx = k
			}
		}
		a.cfg.MidiDev = opts[((idx+d)%len(opts)+len(opts))%len(opts)]
		if len(opts) == 1 {
			a.say("no MIDI devices found — press Enter to type a device path", true)
		}
		a.applyOutputs()
	case setMidiClock:
		a.cfg.MidiClock = !a.cfg.MidiClock
		a.applyOutputs()
	case setSamples:
		if enter {
			a.reloadSamples(true)
		}
		return
	case setAutoLoad:
		a.cfg.AutoLoad = !a.cfg.AutoLoad
	case setRefresh:
		opts := []int{15, 30, 60}
		for k, o := range opts {
			if o == a.cfg.FrameHz {
				a.cfg.FrameHz = opts[((k+d)%3+3)%3]
				break
			}
		}
	}
	a.saveConfig()
}

func (a *App) saveConfig() {
	if err := a.cfg.Save(); err != nil {
		a.say("could not save settings: "+err.Error(), true)
	}
}

func (a *App) stepAudio(d int) {
	opts := append([]string{"auto"}, audio.Available()...)
	opts = append(opts, "none")
	idx := 0
	for i, o := range opts {
		if o == a.cfg.Sink {
			idx = i
		}
	}
	a.cfg.Sink = opts[((idx+d)%len(opts)+len(opts))%len(opts)]
	a.startAudio(a.cfg.Sink)
	if !a.statusErr {
		a.say("audio: "+a.sink.Name()+" (setting: "+a.cfg.Sink+")", false)
	}
}

// ---- outputs, samples ----------------------------------------------------------------

// applyOutputs (re)creates the OSC and MIDI outputs from the config.
func (a *App) applyOutputs() {
	var next []engineOutput
	var problems []string
	if a.cfg.OSC {
		if o, err := openOSC(a.cfg.OSCAddr); err != nil {
			problems = append(problems, "OSC: "+err.Error())
			a.cfg.OSC = false
		} else {
			next = append(next, o)
		}
	}
	if a.cfg.MidiDev != "" {
		if o, err := midi.Open(paths.Expand(a.cfg.MidiDev), a.cfg.MidiClock); err != nil {
			problems = append(problems, "MIDI: "+err.Error())
			a.cfg.MidiDev = ""
		} else {
			next = append(next, o)
		}
	}
	old := a.outs
	a.outs = next
	a.eng.SetOutputs(next)
	a.eng.SetInternal(a.cfg.Internal)
	var delay time.Duration
	if a.cfg.Internal && a.sink != nil {
		delay = a.sink.Latency() // line external gear up with the delayed audio
	}
	a.eng.SetOutputDelay(delay)
	for _, o := range old {
		o.Close()
	}
	if len(problems) > 0 {
		a.say(strings.Join(problems, " · ")+" — switched off", true)
	}
}

func validAddr(s string) error {
	h, p, err := net.SplitHostPort(s)
	if err != nil || h == "" || p == "" {
		return fmt.Errorf("use host:port, e.g. 127.0.0.1:57120")
	}
	return nil
}

func (a *App) reloadSamples(report bool) {
	banks, files, err := synth.LoadSamples(a.samplesDir())
	if a.seq != nil {
		a.seq.RefreshTables()
	}
	switch {
	case err != nil:
		a.say(fmt.Sprintf("samples: %d banks, %d files loaded — %v", banks, files, err), true)
	case a.statusErr && !report:
		// keep an earlier error (e.g. no audio player) on screen
	case report || banks > 0:
		if banks == 0 {
			a.say("no samples found in "+a.samplesDir()+" (make a sub-folder per bank with .wav files)", false)
		} else {
			a.say(fmt.Sprintf("samples: %d banks, %d files from %s", banks, files, a.samplesDir()), false)
		}
	}
}
