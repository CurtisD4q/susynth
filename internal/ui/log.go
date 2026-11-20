package ui

import "time"

// sidePane is what the right-hand pane shows on the Play page.
type sidePane int

const (
	sideTracks sidePane = iota
	sideRoll
	sideLog
)

// logLine is one session entry. It stays until susynth quits.
type logLine struct {
	At   time.Time
	Kind string // "err", "puts", "cue", "info"
	Text string
	Line int // 1-based source line, 0 if none
}

func isLogScroll(k key) bool {
	switch k.kind {
	case kUp, kDown, kPgUp, kPgDn, kHome, kEnd:
		return true
	}
	return false
}

// scrollLog moves the log pane. logOff is how many entries above the newest
// the bottom row shows, so 0 keeps following new lines.
func (a *App) scrollLog(k key, page int) {
	if len(a.log) == 0 {
		return
	}
	if page < 1 {
		page = 1
	}
	step := 1
	if k.kind == kPgUp || k.kind == kPgDn {
		step = page
	}
	switch k.kind {
	case kUp, kPgUp:
		a.logOff += step
	case kDown, kPgDn:
		a.logOff -= step
	case kHome:
		a.logOff = len(a.log) - 1
	case kEnd:
		a.logOff = 0
	}
	if a.logOff < 0 {
		a.logOff = 0
	}
	if a.logOff > len(a.log)-1 {
		a.logOff = len(a.log) - 1
	}
}

// pendingLabel is the transport hint while a prefix is waiting for its key.
func (a *App) pendingLabel() string {
	switch a.awaiting {
	case "mute":
		if a.awaitBuf != "" {
			return "mute " + a.awaitBuf + "…"
		}
		return "mute…"
	case "solo":
		if a.awaitBuf != "" {
			return "solo " + a.awaitBuf + "…"
		}
		return "solo…"
	case "stop":
		if a.awaitBuf != "" {
			return "stop " + a.awaitBuf + "…"
		}
		return "stop…"
	}
	if !a.escAt.IsZero() && time.Since(a.escAt) < escWindow {
		return "Esc…"
	}
	return ""
}
