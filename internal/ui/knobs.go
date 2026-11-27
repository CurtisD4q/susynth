package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/susynth/susynth/internal/engine"
)

// Live controls: nudging a number in the code, a filter on the loop under
// the cursor, mixing a set in beside the one playing, and the code lighting
// up as it plays.

// numberAt finds the number literal at col in line: under the cursor or just
// before it. A digit inside a name (:e3, drum_tom2) is not a number.
func numberAt(line []rune, col int) (start, end int, ok bool) {
	isNum := func(i int) bool { return i >= 0 && i < len(line) && (unicode.IsDigit(line[i]) || line[i] == '.') }
	i := col
	if !isNum(i) {
		i = col - 1
	}
	if !isNum(i) {
		return 0, 0, false
	}
	start, end = i, i+1
	for isNum(start - 1) {
		start--
	}
	for isNum(end) {
		end++
	}
	if start > 0 && (isIdent(line[start-1]) || line[start-1] == ':') {
		return 0, 0, false
	}
	if strings.Count(string(line[start:end]), ".") > 1 || !unicode.IsDigit(line[start]) && (start+1 >= end || !unicode.IsDigit(line[start+1])) {
		return 0, 0, false
	}
	if line[end-1] == '.' {
		end-- // 3.times: the dot is a method call
	}
	// a minus sign belongs to the number when it is not a subtraction
	if start > 0 && line[start-1] == '-' {
		j := start - 2
		for j >= 0 && line[j] == ' ' {
			j--
		}
		if j < 0 || !(isIdent(line[j]) || line[j] == ')' || line[j] == ']') {
			start--
		}
	}
	return start, end, true
}

// nudged is text (a number) moved by dir steps of its last decimal place, or
// ten of them when big.
func nudged(text string, dir int, big bool) (string, bool) {
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return "", false
	}
	dec := 0
	if i := strings.IndexByte(text, '.'); i >= 0 {
		dec = len(text) - i - 1
	}
	step := math.Pow(10, -float64(dec))
	if big {
		step *= 10
	}
	v += float64(dir) * step
	out := strconv.FormatFloat(v, 'f', dec, 64)
	if strings.Trim(out, "-0.") == "" {
		out = strings.TrimPrefix(out, "-")
	}
	return out, true
}

// numberUnderCursor reports whether the editor's cursor is on a number.
func (a *App) numberUnderCursor() bool {
	_, _, ok := numberAt(a.ed.lines[a.ed.row], a.ed.col)
	return ok
}

// nudge changes the number under the cursor and, while music plays, runs the
// block it is in again, so the change is heard on the next beat: a knob made
// of code.
func (a *App) nudge(dir int, big bool) {
	ed := a.ed
	line := ed.lines[ed.row]
	s, e, ok := numberAt(line, ed.col)
	if !ok {
		a.say("put the cursor on a number to nudge it (Ctrl+↑↓, Shift for 10×)", false)
		return
	}
	old := string(line[s:e])
	next, ok := nudged(old, dir, big)
	if !ok {
		return
	}
	atEnd := ed.col >= e
	ed.Unmark()
	ed.Checkpoint("nudge")
	ed.SetLine(ed.row, string(line[:s])+next+string(line[e:]))
	if atEnd {
		ed.col = s + len([]rune(next))
	} else {
		ed.col = min(ed.col, s+len([]rune(next))-1)
	}
	ed.want = ed.col
	a.edited()
	msg := old + " → " + next
	if a.seq != nil && a.seq.Running() {
		a.eval(false)
		msg += " · " + a.heardWhen()
	}
	a.say(msg, false)
}

// heardWhen says when a change will be heard.
func (a *App) heardWhen() string {
	switch a.seq.ChangeOn() {
	case "bar":
		return "on the next bar"
	case "pass":
		return "on the loop's next pass"
	}
	return "on the next beat"
}

// filterKey sweeps the low-pass filter of the loop under the cursor: d < 0
// closes it (darker), d > 0 opens it.
func (a *App) filterKey(d int) {
	id, name, ok := a.trackOnCursor()
	if name == "" {
		a.say("put the cursor inside a live_loop to filter it", true)
		return
	}
	if !ok {
		a.say(":"+name+" is not playing — ^E runs it", true)
		return
	}
	hz, ok := a.eng.AdjustFilter(id, d)
	if !ok {
		return
	}
	a.say(fmt.Sprintf(":%s filter %s", name, hzLabel(hz)), false)
}

func hzLabel(hz float64) string {
	switch {
	case hz <= 0:
		return "open"
	case hz >= 1000:
		return fmt.Sprintf("%.1f kHz", hz/1000)
	}
	return fmt.Sprintf("%.0f Hz", hz)
}

// mixIn plays set list entry i beside what is playing. Its loops start on the
// next bar with every part muted; the loops that were playing become the old
// set. With nothing playing it is the same as going live.
func (a *App) mixIn(i int) {
	if i < 0 || i >= len(a.setlist) {
		return
	}
	if !a.seq.Running() {
		a.goLive(i)
		return
	}
	if !a.mayReplaceEditor() {
		return
	}
	name := a.setlist[i]
	a.loadSet(name)
	if a.statusErr {
		return
	}
	a.live = i
	a.errs = nil
	id, err := a.seq.MixIn(a.ed.Text())
	if err != nil {
		a.say(err.Error(), true)
		return
	}
	a.runStarted(id)
	a.say(fmt.Sprintf("MIXING IN %d/%d: %s, muted — Esc u brings parts in, Esc o fades the old set",
		i+1, len(a.setlist), name), false)
}

// mixStep mixes in the next (+1) or previous (-1) set in the list.
func (a *App) mixStep(d int) {
	if len(a.setlist) == 0 {
		a.say("the set list is empty — add sets on the Sets page with 'a'", true)
		return
	}
	i := a.live + d
	if a.live < 0 {
		i = 0
	}
	if i < 0 || i >= len(a.setlist) {
		a.say("that is the end of the set list", true)
		return
	}
	a.mixIn(i)
}

// fadeOld fades the old set out over two bars; it stops when the fade ends.
func (a *App) fadeOld() {
	if !a.eng.HasOld() {
		a.say("no old set is playing (Esc i mixes in the next set)", false)
		return
	}
	bpm := a.eng.BPM()
	if bpm <= 0 {
		bpm = 120
	}
	a.eng.FadeOld(2 * engine.BeatsPerCycle * 60 / bpm)
	a.say("fading out the old set over 2 bars", false)
}

// lineEdit is one edit that moved lines, kept so lines from an older run
// still point at the right code.
type lineEdit struct{ prefix, suffix, oldN, newN int }

// runStarted remembers the buffer as it was for run id.
func (a *App) runStarted(id int) {
	if a.runEpoch == nil {
		a.runEpoch = map[int]int{}
	}
	a.runEpoch[id] = len(a.lineEdits)
}

// litRows maps editor rows to how long ago a sound from them was heard, for
// the sounds of the last fraction of a second.
func (a *App) litRows() map[int]float64 {
	if a.eng == nil || len(a.runEpoch) == 0 {
		return nil
	}
	out := map[int]float64{}
	for _, l := range a.eng.LitLines(litWindow) {
		ep, ok := a.runEpoch[l.Run]
		if !ok || l.Line <= 0 {
			continue
		}
		row := l.Line - 1
		for _, e := range a.lineEdits[ep:] {
			if row, ok = remapLine(row, e.oldN, e.newN, e.prefix, e.suffix); !ok {
				break
			}
		}
		if !ok || row < 0 || row >= len(a.ed.lines) {
			continue
		}
		if age, seen := out[row]; !seen || l.Age < age {
			out[row] = l.Age
		}
	}
	return out
}

// litWindow is how long a line stays lit after its sound starts.
const litWindow = 0.16

// litSpan is the part of a lit line to colour: the sound call (play, sample…)
// and its first argument, or the whole line if it has none.
func litSpan(line []rune) (int, int) {
	s := 0
	for s < len(line) && line[s] == ' ' {
		s++
	}
	for _, w := range []string{"play", "sample", "synth", "play_chord", "play_pattern", "play_pattern_timed"} {
		i := strings.Index(string(line), w+" ")
		if i < 0 {
			continue
		}
		start := len([]rune(string(line)[:i]))
		end := start + len(w)
		for end < len(line) && line[end] == ' ' {
			end++
		}
		if end < len(line) && (line[end] == '(' || line[end] == '[') {
			// a bracketed argument: (ring :e2, :g2).tick, up to its close
			depth := 0
			for ; end < len(line); end++ {
				switch line[end] {
				case '(', '[':
					depth++
				case ')', ']':
					depth--
				}
				if depth == 0 {
					end++
					break
				}
			}
		}
		for end < len(line) && line[end] != ',' && line[end] != ' ' && line[end] != '#' && line[end] != ')' {
			end++
		}
		return start, end
	}
	e := len(line)
	for e > s && line[e-1] == ' ' {
		e--
	}
	return s, e
}
