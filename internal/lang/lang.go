// Package lang parses susynth source: one statement per line.
//
//	d1 "bd*2 [sn cp]" # gain 0.8 # pan -0.3
//	d2 $ every 4 (fast 2) $ rev $ s "c3 e3 g3" # voice pluck # lpf 800 # room 0.3
//	bpm 128 | hush | mute 1 | unmute 1 | solo 2 | unsolo | level 1 0.7 | stop 3
//	delaytime 0.75 | delayfb 0.5 | roomsize 0.7
package lang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/susynth/susynth/internal/mini"
	"github.com/susynth/susynth/internal/synth"
)

// TrackDef is a parsed "dN ..." line.
type TrackDef struct {
	ID      int
	Source  string // the mini-notation text
	Pattern *mini.Pattern
	Params  synth.Params
	Line    int // absolute line index in the editor buffer
}

// Command is a non-pattern statement.
type Command struct {
	Name  string // bpm, hush, mute, unmute, solo, unsolo, level, stop
	Track int
	Value float64
	Line  int
}

// Error is a problem on one line.
type Error struct {
	Line int // absolute line index
	Col  int // 0-based column, -1 if unknown
	Msg  string
}

func (e Error) String() string {
	if e.Col >= 0 {
		return fmt.Sprintf("line %d, col %d: %s", e.Line+1, e.Col+1, e.Msg)
	}
	return fmt.Sprintf("line %d: %s", e.Line+1, e.Msg)
}

// Program is the result of parsing a block.
type Program struct {
	Tracks   []TrackDef
	Commands []Command
	Errors   []Error
}

// Parse parses src; lineOffset is added to line numbers so errors point at
// the right editor row.
func Parse(src string, lineOffset int) Program {
	var prog Program
	for i, raw := range strings.Split(src, "\n") {
		line := lineOffset + i
		text := strings.TrimRight(raw, " \t\r")
		trim := strings.TrimSpace(text)
		if trim == "" || strings.HasPrefix(trim, "--") || strings.HasPrefix(trim, "//") {
			continue
		}
		indent := len(text) - len(strings.TrimLeft(text, " \t"))
		if err := parseLine(&prog, trim, indent, line); err != nil {
			prog.Errors = append(prog.Errors, *err)
		}
	}
	return prog
}

func fail(line, col int, f string, a ...any) *Error {
	return &Error{Line: line, Col: col, Msg: fmt.Sprintf(f, a...)}
}

func parseLine(prog *Program, s string, indent, line int) *Error {
	fields := strings.Fields(s)
	head := strings.ToLower(fields[0])

	if len(head) >= 2 && head[0] == 'd' && isDigits(head[1:]) {
		return parseTrack(prog, s, head, indent, line)
	}
	num := func(i int, what string) (float64, *Error) {
		if len(fields) <= i {
			return 0, fail(line, -1, "%s needs a %s", head, what)
		}
		f, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return 0, fail(line, -1, "%q is not a number", fields[i])
		}
		return f, nil
	}
	trackArg := func() (int, *Error) {
		f, e := num(1, "track number")
		if e != nil {
			return 0, e
		}
		return int(f), nil
	}
	switch head {
	case "hush", "unsolo":
		prog.Commands = append(prog.Commands, Command{Name: head, Line: line})
	case "bpm", "setbpm":
		v, e := num(1, "tempo")
		if e != nil {
			return e
		}
		if v < 20 || v > 400 {
			return fail(line, -1, "bpm must be between 20 and 400")
		}
		prog.Commands = append(prog.Commands, Command{Name: "bpm", Value: v, Line: line})
	case "mute", "unmute", "solo", "stop":
		t, e := trackArg()
		if e != nil {
			return e
		}
		prog.Commands = append(prog.Commands, Command{Name: head, Track: t, Line: line})
	case "delaytime", "delayfb", "roomsize":
		v, e := num(1, "value")
		if e != nil {
			return e
		}
		lim := map[string][2]float64{"delaytime": {0.0625, 8}, "delayfb": {0, 0.95}, "roomsize": {0, 1}}[head]
		if v < lim[0] || v > lim[1] {
			return fail(line, -1, "%s must be between %g and %g", head, lim[0], lim[1])
		}
		prog.Commands = append(prog.Commands, Command{Name: head, Value: v, Line: line})
	case "level":
		t, e := trackArg()
		if e != nil {
			return e
		}
		v, e := num(2, "level 0..1.5")
		if e != nil {
			return e
		}
		if v < 0 || v > 1.5 {
			return fail(line, -1, "level must be between 0 and 1.5")
		}
		prog.Commands = append(prog.Commands, Command{Name: "level", Track: t, Value: v, Line: line})
	default:
		return fail(line, indent, "unknown statement %q (tracks look like d1 \"bd sn\")", fields[0])
	}
	return nil
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func parseTrack(prog *Program, s, head string, indent, line int) *Error {
	id, _ := strconv.Atoi(head[1:])
	if id < 1 || id > 16 {
		return fail(line, indent, "track number must be 1..16")
	}
	q1 := strings.IndexByte(s, '"')
	if q1 < 0 {
		return fail(line, indent+len(head), "expected a quoted pattern, e.g. %s \"bd sn\"", head)
	}
	// Between dN and the quote: optional "$", s/n, and a chain of functions.
	var chain [][]string
	for _, seg := range strings.Split(s[len(head):q1], "$") {
		toks, terr := tokenize(seg)
		if terr != nil {
			return fail(line, indent+len(head), "%v", terr)
		}
		if n := len(toks); n > 0 {
			switch strings.ToLower(toks[n-1]) {
			case "s", "sound", "n", "note":
				toks = toks[:n-1]
			}
		}
		if len(toks) > 0 {
			chain = append(chain, toks)
		}
	}
	q2 := strings.IndexByte(s[q1+1:], '"')
	if q2 < 0 {
		return fail(line, indent+q1, "missing closing quote")
	}
	q2 += q1 + 1
	src := s[q1+1 : q2]
	pat, err := mini.Parse(src, synth.Known)
	if err != nil {
		col := indent + q1 + 1
		if pe, ok := err.(*mini.ParseError); ok {
			return fail(line, col+pe.Pos, "%s", pe.Msg)
		}
		return fail(line, col, "%v", err)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		fn, ferr := parseFn(chain[i])
		if ferr == nil {
			pat, ferr = fn(pat)
		}
		if ferr != nil {
			return fail(line, indent+len(head), "%v", ferr)
		}
	}
	p := synth.Params{Gain: 1, Speed: 1}
	rest := strings.TrimSpace(s[q2+1:])
	if rest != "" {
		if !strings.HasPrefix(rest, "#") {
			return fail(line, indent+q2+1, "expected '# key value' after the pattern")
		}
		for _, mod := range strings.Split(rest, "#")[1:] {
			f := strings.Fields(mod)
			if len(f) != 2 {
				return fail(line, -1, "modifier %q needs a name and a value", strings.TrimSpace(mod))
			}
			key := strings.ToLower(f[0])
			if key == "voice" || key == "wave" {
				ok := false
				for _, w := range synth.Waves {
					ok = ok || w == f[1]
				}
				if !ok {
					return fail(line, -1, "unknown voice %q (sine tri saw square pluck)", f[1])
				}
				p.Wave = f[1]
				continue
			}
			v, perr := strconv.ParseFloat(f[1], 64)
			if perr != nil {
				return fail(line, -1, "%q is not a number", f[1])
			}
			switch key {
			case "gain":
				if v < 0 || v > 2 {
					return fail(line, -1, "gain must be between 0 and 2")
				}
				p.Gain = v
			case "pan":
				if v < -1 || v > 1 {
					return fail(line, -1, "pan must be between -1 and 1")
				}
				p.Pan = v
			case "speed":
				if v <= 0 || v > 8 {
					return fail(line, -1, "speed must be between 0 and 8")
				}
				p.Speed = v
			case "lpf", "cutoff", "hpf", "hcutoff":
				if v < 20 || v > 20000 {
					return fail(line, -1, "%s must be between 20 and 20000 Hz", key)
				}
				if key == "lpf" || key == "cutoff" {
					p.LPF = v
				} else {
					p.HPF = v
				}
			case "crush":
				if v < 1 || v > 16 {
					return fail(line, -1, "crush is a bit depth, 1 to 16")
				}
				p.Crush = v
			case "shape", "delay", "room":
				if v < 0 || v > 1 {
					return fail(line, -1, "%s must be between 0 and 1", key)
				}
				switch key {
				case "shape":
					p.Shape = v
				case "delay":
					p.Delay = v
				default:
					p.Room = v
				}
			case "release", "rel":
				if v < 0.005 || v > 8 {
					return fail(line, -1, "release must be between 0.005 and 8 seconds")
				}
				p.Release = v
			default:
				return fail(line, -1, "unknown modifier %q (gain pan speed voice lpf hpf crush shape delay room release)", key)
			}
		}
	}
	prog.Tracks = append(prog.Tracks, TrackDef{ID: id, Source: src, Pattern: pat, Params: p, Line: line})
	return nil
}
