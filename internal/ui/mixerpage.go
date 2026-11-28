package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	mint "github.com/CurtisD4q/pmin-kit"
	"github.com/susynth/susynth/internal/engine"
)

// The Mixer page is Sonic Pi's master mixer: drive into the limiter, the
// volume fader after it, high- and low-pass filters, mono and channel swap.
// The same controls are set_drive!, set_volume! and set_mixer_control! in
// code; whichever changed last wins.

const (
	mixVolume = iota
	mixDrive
	mixHPF
	mixLPF
	mixMono
	mixSwap
	mixLimiter
	mixReset
	numMixer
)

var mixerRows = [numMixer]settingRow{
	mixVolume:  {"Volume", "the fader after the limiter, 0–100%: changes how loud, not how it sounds (set_volume!)"},
	mixDrive:   {"Drive", "pushes the mix into the limiter: 0 is a quarter, 50% is unity, 100% is four times (set_drive!)"},
	mixHPF:     {"High-pass", "cuts the bass of the whole mix below this MIDI note; off = no filter (hpf:)"},
	mixLPF:     {"Low-pass", "cuts the treble of the whole mix above this MIDI note; off = no filter (lpf:)"},
	mixMono:    {"Mono", "mix left and right into the same signal (force_mono:)"},
	mixSwap:    {"Swap channels", "left becomes right and right becomes left (invert_stereo:)"},
	mixLimiter: {"Limiter", "on keeps the mix from clipping; off hard-clips (limiter_bypass:)"},
	mixReset:   {"Reset", "Enter returns every control to Sonic Pi's start-up setting (reset_mixer!)"},
}

func (a *App) mixerValue(i int) string {
	m := a.eng.Mixer()
	switch i {
	case mixVolume:
		return fmt.Sprintf("%.0f%%", m.Amp*100)
	case mixDrive:
		return fmt.Sprintf("%.0f%%", engine.PreAmpToDrive(m.PreAmp)*100)
	case mixHPF:
		if m.HPF <= 0 {
			return "off"
		}
		return fmt.Sprintf("note %.0f", m.HPF)
	case mixLPF:
		if m.LPF >= 135 {
			return "off"
		}
		return fmt.Sprintf("note %.0f", m.LPF)
	case mixMono:
		return onOff(m.ForceMono)
	case mixSwap:
		return onOff(m.InvertStereo)
	case mixLimiter:
		return onOff(!m.LimiterBypass)
	case mixReset:
		return "Enter"
	}
	return ""
}

// changeMixer steps control i by d (±1); enter is Enter.
func (a *App) changeMixer(i, d int, enter bool) {
	m := a.eng.Mixer()
	set := engine.MixerSet{}
	switch i {
	case mixVolume:
		set["amp"] = math.Round((m.Amp+0.05*float64(d))*20) / 20
	case mixDrive:
		set["drive"] = math.Round((engine.PreAmpToDrive(m.PreAmp)+0.05*float64(d))*20) / 20
	case mixHPF:
		switch {
		case m.HPF <= 0 && d > 0:
			set["hpf"] = 30
		case m.HPF <= 30 && d < 0:
			set["hpf_bypass"] = 1
		default:
			set["hpf"] = m.HPF + 5*float64(d)
		}
	case mixLPF:
		switch {
		case m.LPF >= 135 && d < 0:
			set["lpf"] = 120
		case m.LPF >= 120 && d > 0:
			set["lpf_bypass"] = 1
		default:
			set["lpf"] = m.LPF + 5*float64(d)
		}
	case mixMono:
		set["force_mono"] = b2f(!m.ForceMono)
	case mixSwap:
		set["invert_stereo"] = b2f(!m.InvertStereo)
	case mixLimiter:
		set["limiter_bypass"] = b2f(!m.LimiterBypass)
	case mixReset:
		if !enter {
			return
		}
		set["reset"] = 1
		a.say("mixer reset", false)
	}
	a.eng.SetMixer(set)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// mixerMeter is a level bar for the output peak, with a mark when the limiter works.
func (a *App) mixerMeter() string {
	peak, limiting := a.eng.MixerLevel()
	db := -60.0
	if peak > 0 {
		db = math.Max(-60, 20*math.Log10(float64(peak)))
	}
	const w = 40
	n := int((db + 60) / 60 * w)
	bar := strings.Repeat("█", n) + strings.Repeat("·", w-n)
	lim := ""
	if limiting {
		lim = "  limiting"
	}
	return "level " + bar + " " + strconv.FormatFloat(db, 'f', 0, 64) + " dB" + lim
}

func (a *App) mixerScreen(s *mint.Screen) {
	s.TopTitle = pageNames[pageMixer]
	s.TopInfo = func() string { return "Sonic Pi's master mixer · set_volume! set_drive! set_mixer_control!" }
	var rows []mint.Row
	for i := range mixerRows {
		i := i
		rows = append(rows, mint.Picker(mint.PickerOpts{
			ID:    "mix:" + strconv.Itoa(i),
			Label: mixerRows[i].label,
			Get:   func() string { return a.mixerValue(i) },
			Step: func(d int) {
				a.ps.sel = i
				a.changeMixer(i, d, d == 0)
			},
		}))
	}
	s.Sections = []mint.Section{{Rows: rows}}
	s.Refresh = func(s *mint.Screen) {
		if sel, ok := s.Selected(); ok {
			if n, ok := strings.CutPrefix(sel.ID, "mix:"); ok {
				if i, err := strconv.Atoi(n); err == nil {
					a.ps.sel = i
				}
			}
		}
		s.Dock = []mint.Section{{Rows: []mint.Row{mint.Note(a.mixerMeter()), mint.Note(mixerRows[min(a.ps.sel, int(numMixer)-1)].hint)}}}
	}
}
