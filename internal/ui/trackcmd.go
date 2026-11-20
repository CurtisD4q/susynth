package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// trackCommand finishes a mute, solo or stop that is waiting for a number.
// The prefix (Esc u, Esc s, Esc k) is already on the message row and the
// transport, so a stray digit cannot change a track by itself.
func (a *App) trackCommand(k key) {
	mode := a.awaiting
	switch {
	case k.kind == kEsc:
		a.awaiting, a.awaitBuf = "", ""
	case k.kind == kRune && k.r >= '0' && k.r <= '9':
		if mode == "solo" && a.awaitBuf == "" && k.r == '0' {
			a.awaiting, a.awaitBuf = "", ""
			a.eng.SetSolo(0)
			a.say("solo cleared", false)
			return
		}
		a.awaitBuf += string(k.r)
		if id, ok := a.resolveTrack(a.awaitBuf); ok {
			a.finishTrack(mode, id)
		}
	case k.kind == kEnter && mode == "stop" && a.awaitBuf == "":
		a.awaiting = ""
		a.stopCursor()
	case k.kind == kEnter && a.awaitBuf != "":
		id, _ := strconv.Atoi(a.awaitBuf)
		a.awaiting, a.awaitBuf = "", ""
		a.finishTrack(mode, id)
	default:
		a.awaiting, a.awaitBuf = "", ""
	}
}

// resolveTrack reports a track number when prefix matches exactly one track
// and is not the start of another (so "1" waits if track 10 is also playing).
func (a *App) resolveTrack(prefix string) (int, bool) {
	if prefix == "" {
		return 0, false
	}
	var exact, longer, id int
	for _, t := range a.eng.Snapshot().Tracks {
		s := strconv.Itoa(t.ID)
		switch {
		case s == prefix:
			exact++
			id = t.ID
		case strings.HasPrefix(s, prefix):
			longer++
		}
	}
	if exact == 1 && longer == 0 {
		return id, true
	}
	return 0, false
}

func (a *App) finishTrack(mode string, id int) {
	a.awaiting, a.awaitBuf = "", ""
	switch mode {
	case "mute":
		if !a.eng.ToggleMute(id) {
			a.say(fmt.Sprintf("no track %d playing", id), true)
			return
		}
		name, _ := a.eng.TrackName(id)
		state := "on"
		for _, t := range a.eng.Snapshot().Tracks {
			if t.ID == id && t.Mute {
				state = "muted"
			}
		}
		if name != "" {
			a.say(fmt.Sprintf(":%s %s", name, state), false)
		}
	case "solo":
		if !a.eng.SetSolo(id) {
			a.say(fmt.Sprintf("no track %d to solo", id), true)
		}
	case "stop":
		a.stopID(id)
	}
}

func (a *App) stopCursor() {
	id, name, ok := a.trackOnCursor()
	if name == "" {
		a.say("type a track number, or put the cursor in a live_loop", true)
		return
	}
	if !ok {
		a.say(":"+name+" is not playing", true)
		return
	}
	a.stopID(id)
}

func (a *App) stopID(id int) {
	name, ok := a.eng.TrackName(id)
	if !ok || a.seq == nil {
		a.say(fmt.Sprintf("no track %d playing", id), true)
		return
	}
	if err := a.seq.StopOne(name); err != nil {
		return
	}
	a.say("stopped :"+name, false)
}
