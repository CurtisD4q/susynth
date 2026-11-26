package ui

import (
	"fmt"
	"math"
	"sort"

	"github.com/susynth/susynth/internal/engine"
)

// The piano roll: a live, read-only view of what every loop plays. Time runs
// left to right with "now" near the right edge; pitched notes sit on a
// semitone grid that fits itself to the notes on screen, and drums and
// samples get lanes of their own underneath. Each loop has its own colour.

var rollColors = []int16{114, 110, 214, 176, 116, 222, 203, 147}

func rollColor(track int) int16 {
	if track <= 0 {
		return 250
	}
	return rollColors[(track-1)%len(rollColors)]
}

var drumOrder = map[string]int{"bd": 0, "sn": 1, "cp": 2, "rim": 3, "hh": 4, "oh": 5, "tom": 6, "tom:4": 7, "tom:8": 8, "cb": 9}

var pitchNames = [12]string{"c", "cs", "d", "eb", "e", "f", "fs", "g", "gs", "a", "bb", "b"}

func pitchName(n int) string { return fmt.Sprintf("%s%d", pitchNames[((n%12)+12)%12], n/12-1) }

func drumLabel(d string) string {
	switch {
	case d == "tom:4":
		return "tom2"
	case d == "tom:8":
		return "tom3"
	case len(d) > 0 && d[0] == '@':
		d = d[1:]
	}
	if len(d) > 4 {
		d = d[:4]
	}
	return d
}

func (a *App) drawRoll(f *frame, x, y, w, h int, v engine.View) {
	th := a.th
	const labelW = 5
	cols, rows := w-labelW, h-2
	if cols < 8 || rows < 3 {
		f.text(x, y+1, "too small", th.Muted, w)
		return
	}
	cpb := 4 // columns per beat
	if cols < 28 {
		cpb = 2
	}
	future := 1.0
	nowCol := cols - int(future)*cpb - 1
	past := float64(nowCol) / float64(cpb)
	rv := a.eng.Roll(past+1, future+1)

	if len(rv.Notes) == 0 {
		f.text(x, y+2, "nothing playing", th.Muted, w)
		f.text(x, y+3, "notes appear here as your", th.Muted, w)
		f.text(x, y+4, "loops play them", th.Muted, w)
		a.drawRollLegend(f, x, y+h-1, w, v)
		return
	}

	// which drums, and which pitch range, are on screen
	drums := map[string]bool{}
	lo, hi := math.MaxInt, math.MinInt
	for _, n := range rv.Notes {
		if n.Drum != "" {
			drums[n.Drum] = true
			continue
		}
		p := int(math.Round(n.Note))
		lo, hi = min(lo, p), max(hi, p)
	}
	var lanes []string
	for d := range drums {
		lanes = append(lanes, d)
	}
	sort.Slice(lanes, func(i, j int) bool {
		oi, iok := drumOrder[lanes[i]]
		oj, jok := drumOrder[lanes[j]]
		if iok != jok {
			return iok
		}
		if iok {
			return oi < oj
		}
		return lanes[i] < lanes[j]
	})
	pitched := lo <= hi
	maxLanes := rows
	if pitched {
		maxLanes = max(1, rows/3)
	}
	if len(lanes) > maxLanes {
		lanes = lanes[:maxLanes]
	}
	pitchRows := rows
	if len(lanes) > 0 {
		pitchRows = rows - len(lanes)
		if pitched {
			pitchRows-- // a rule between notes and drums
		}
	}
	step := 1
	if pitched {
		if hi-lo < 11 { // show at least an octave, centred on the notes
			pad := (11 - (hi - lo)) / 2
			lo, hi = lo-pad, lo-pad+11
		}
		for (hi-lo)/step+1 > pitchRows {
			step++
		}
		hi = lo + ((hi-lo)/step+1)*step - 1                     // whole buckets
		if extra := pitchRows - ((hi-lo)/step + 1); extra > 0 { // spread spare rows above and below
			hi += (extra / 2) * step
			lo -= (extra - extra/2) * step
		}
	}

	gx := x + labelW
	colOf := func(s int64) int {
		return nowCol + int(math.Floor(float64(s-rv.Now)/rv.SamplesPerBeat*float64(cpb)))
	}
	beatAtCol := func(c int) float64 { return rv.Beat + float64(c-nowCol)/float64(cpb) }

	// background: beat and bar lines, and the "now" line
	grid := func(yy int) {
		for c := 0; c < cols; c++ {
			b := beatAtCol(c)
			prev := beatAtCol(c - 1)
			switch {
			case c == nowCol:
				f.put(gx+c, yy, '│', th.Frame)
			case rv.Playing && math.Floor(b/4) != math.Floor(prev/4):
				f.put(gx+c, yy, '┆', st(240, -1))
			case rv.Playing && math.Floor(b) != math.Floor(prev):
				f.put(gx+c, yy, '·', st(238, -1))
			}
		}
	}

	// pitched rows
	rowOf := func(p int) int { return (hi - p) / step }
	if pitched {
		for r := 0; r < pitchRows; r++ {
			yy := y + 1 + r
			top := hi - r*step
			label := ""
			for p := top; p > top-step; p-- {
				if ((p%12)+12)%12 == 0 {
					label = pitchName(p)
				}
			}
			if r == 0 || r == pitchRows-1 {
				label = pitchName(top - step + 1)
				if r == 0 {
					label = pitchName(top)
				}
			}
			f.text(x, yy, label, th.Muted, labelW-1)
			grid(yy)
		}
	}
	laneTop := y + 1 + pitchRows
	if pitched && len(lanes) > 0 {
		f.hline(x, laneTop, w, '─', th.Rule)
		laneTop++
	}
	laneRow := map[string]int{}
	for i, d := range lanes {
		laneRow[d] = laneTop + i
		f.text(x, laneTop+i, drumLabel(d), th.Muted, labelW-1)
		grid(laneTop + i)
	}

	// notes: solid once heard, shaded while still to come
	for _, n := range rv.Notes {
		c0, c1 := colOf(n.At), colOf(n.End)
		if c1 <= c0 {
			c1 = c0 + 1
		}
		var yy int
		if n.Drum != "" {
			r, ok := laneRow[n.Drum]
			if !ok {
				continue
			}
			yy, c1 = r, c0+1
		} else {
			yy = y + 1 + rowOf(int(math.Round(n.Note)))
		}
		col := rollColor(n.Track)
		if n.Muted {
			col = 240
		}
		for c := max(c0, 0); c < min(c1, cols); c++ {
			ch := '█'
			switch {
			case n.Drum != "":
				ch = '■'
			case n.At > rv.Now:
				ch = '▒'
			case c == c0:
				ch = '▐'
			}
			f.put(gx+c, yy, ch, st(col, -1))
		}
	}
	a.drawRollLegend(f, x, y+h-1, w, v)
}

// drawRollLegend shows each loop in its colour, so the roll can be read.
func (a *App) drawRollLegend(f *frame, x, y, w int, v engine.View) {
	cx := x
	for _, t := range v.Tracks {
		col := rollColor(t.ID)
		if t.Mute {
			col = 240
		}
		label := ":" + t.Name
		if cx+len(label)+2 > x+w {
			break
		}
		f.put(cx, y, '■', st(col, -1))
		cx = f.text(cx+2, y, label, st(col, -1), 0) + 2
	}
}
