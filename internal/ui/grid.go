package ui

import (
	"fmt"
	"strings"

	"github.com/susynth/susynth/internal/frac"
	"github.com/susynth/susynth/internal/mini"
	"github.com/susynth/susynth/internal/synth"
)

const gridCells = 16

// gridState is the step-grid editor for one track line.
type gridState struct {
	line, id     int
	prefix, tail string // text of the line around the quoted pattern
	cells        [gridCells]string
	cur          int
	word         int // index into words
	words        []string
	warn         string
}

var defaultGridWords = []string{"bd", "sn", "cp", "hh", "oh", "rim", "tom", "cb"}

// startGrid opens the grid for the track on the cursor line.
func (a *App) startGrid() {
	row := a.ed.row
	line := a.ed.Line(row)
	id, ok := a.trackOnCursor()
	q1 := strings.IndexByte(line, '"')
	q2 := -1
	if q1 >= 0 {
		q2 = strings.IndexByte(line[q1+1:], '"')
	}
	if !ok || q1 < 0 || q2 < 0 {
		a.say("put the cursor on a track line like d1 \"bd sn\" to edit it as a grid", true)
		return
	}
	q2 += q1 + 1
	src := line[q1+1 : q2]
	pat, err := mini.Parse(src, synth.Known)
	if err != nil {
		a.say("fix the pattern before editing it as a grid: "+err.Error(), true)
		return
	}
	g := &gridState{line: row, id: id, prefix: line[:q1+1], tail: line[q2:]}
	seen := map[string]bool{}
	add := func(w string) {
		if !seen[w] {
			seen[w] = true
			g.words = append(g.words, w)
		}
	}
	var first []mini.Event
	for c := int64(0); c < 4; c++ {
		evs := pat.Query(frac.Int(c), frac.Int(c+1))
		if c == 0 {
			first = evs
		} else if !sameEvents(first, evs, c) {
			g.warn = "pattern changes between cycles — the grid keeps cycle 1 only"
		}
	}
	for _, e := range first {
		add(e.Value)
		pos := e.Start.Mul(frac.Int(gridCells))
		if pos.D != 1 && g.warn == "" {
			g.warn = "some steps are finer than 16ths — they snap to the grid"
		}
		if i := int(pos.Floor()); i >= 0 && i < gridCells && g.cells[i] == "" {
			g.cells[i] = e.Value
		}
	}
	for _, w := range defaultGridWords {
		add(w)
	}
	a.grid = g
	a.say("grid: ←→ move · Space toggle · ↑↓ sound · Enter apply · Esc cancel", false)
}

func sameEvents(a, b []mini.Event, c int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Value != b[i].Value || !a[i].Start.Eq(b[i].Start.Sub(frac.Int(c))) {
			return false
		}
	}
	return true
}

func (g *gridState) pattern() string {
	toks := make([]string, gridCells)
	for i, c := range g.cells {
		if c == "" {
			toks[i] = "~"
		} else {
			toks[i] = c
		}
	}
	return strings.Join(toks, " ")
}

func (a *App) gridKey(k key) {
	g := a.grid
	isRune := func(r rune) bool { return k.kind == kRune && !k.alt && k.r == r }
	switch {
	case k.kind == kEsc:
		a.grid = nil
		a.say("grid cancelled — nothing changed", false)
	case k.kind == kLeft:
		g.cur = (g.cur + gridCells - 1) % gridCells
	case k.kind == kRight:
		g.cur = (g.cur + 1) % gridCells
	case k.kind == kUp || k.kind == kDown:
		d := 1
		if k.kind == kUp {
			d = -1
		}
		g.word = (g.word + d + len(g.words)) % len(g.words)
		if g.cells[g.cur] != "" {
			g.cells[g.cur] = g.words[g.word]
		}
	case isRune(' '):
		if g.cells[g.cur] == "" {
			g.cells[g.cur] = g.words[g.word]
		} else {
			g.cells[g.cur] = ""
		}
	case isRune('c'):
		g.cells = [gridCells]string{}
	case k.kind == kEnter:
		a.ed.SetLine(g.line, g.prefix[:len(g.prefix)-1]+"\""+g.pattern()+g.tail)
		a.ed.row = g.line
		a.grid = nil
		a.edited()
		a.eval(false)
	}
}

func (a *App) drawGrid(f *frame, x, y, w, h int) {
	th, g := a.th, a.grid
	f.text(x, y, fmt.Sprintf("GRID d%d", g.id), th.Pane, 0)
	cw := w / 8
	if cw > 6 {
		cw = 6
	}
	if cw < 3 {
		cw = 3
	}
	for i := 0; i < gridCells; i++ {
		row, col := i/8, i%8
		cx, cy := x+col*cw, y+2+row*3
		f.text(cx, cy, fmt.Sprint(i/4+1)+"."+fmt.Sprint(i%4+1), th.Muted, cw-1)
		label, s := "·", th.StepOff
		if g.cells[i] != "" {
			label, s = g.cells[i], th.StepOn
		}
		if i == g.cur {
			s = th.Playhead
		}
		f.fill(cx, cy+1, cw-1, 1, s)
		f.text(cx, cy+1, label, s, cw-1)
	}
	f.text(x, y+9, "sound  ‹ "+g.words[g.word]+" ›", style{fg: th.Keyword.fg, bg: th.Base.bg, bold: true}, w)
	f.text(x, y+11, "Space toggle · ←→ move · ↑↓ sound", th.Muted, w)
	f.text(x, y+12, "c clear · Enter apply · Esc cancel", th.Muted, w)
	if g.warn != "" {
		f.text(x, y+14, "! "+g.warn, th.ToastErr, w)
	}
}
