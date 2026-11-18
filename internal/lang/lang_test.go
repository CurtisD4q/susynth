package lang

import (
	"strings"
	"testing"

	"github.com/susynth/susynth/internal/frac"
)

func TestParseOK(t *testing.T) {
	src := "-- comment\nd1 \"bd*2 [sn cp]\" # gain 0.8 # pan -0.5\nd2 $ s \"c3 e3\" # voice pluck\nbpm 128\nmute 1\nlevel 2 0.5\nhush\n"
	p := Parse(src, 10)
	if len(p.Errors) != 0 {
		t.Fatalf("errors: %v", p.Errors)
	}
	if len(p.Tracks) != 2 || len(p.Commands) != 4 {
		t.Fatalf("tracks=%d cmds=%d", len(p.Tracks), len(p.Commands))
	}
	if p.Tracks[0].Params.Gain != 0.8 || p.Tracks[0].Params.Pan != -0.5 || p.Tracks[0].Line != 11 {
		t.Fatalf("bad params %+v", p.Tracks[0])
	}
	if p.Tracks[1].Params.Wave != "pluck" {
		t.Fatal("voice not parsed")
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]int{
		`d1 "bd xx"`:          7, // unknown sound at col of xx
		`d1 "bd [sn"`:         7, // unclosed [
		`d1 "bd`:              3, // missing quote
		`d99 "bd"`:            0,
		`wibble`:              0,
		`bpm 9000`:            -1,
		`d1 "bd" # gain loud`: -1,
		`d1 "bd" # colour 1`:  -1,
	}
	for src, col := range cases {
		p := Parse(src, 0)
		if len(p.Errors) != 1 {
			t.Errorf("%q: want 1 error, got %v", src, p.Errors)
			continue
		}
		if p.Errors[0].Col != col {
			t.Errorf("%q: col %d want %d (%s)", src, p.Errors[0].Col, col, p.Errors[0].Msg)
		}
	}
}

func TestFunctionChainsAndEffects(t *testing.T) {
	src := `d1 $ every 4 (fast 2) $ rev $ s "a b c d" # lpf 800 # hpf 100 # crush 8 # shape 0.3 # delay 0.4 # room 0.2 # rel 0.5 # voice fm` + "\n" +
		`d2 $ s "bd sn"` + "\ndelaytime 0.5\ndelayfb 0.6\nroomsize 0.8\n"
	src = strings.ReplaceAll(src, `"a b c d"`, `"c3 d3 e3 f3"`)
	p := Parse(src, 0)
	if len(p.Errors) != 0 {
		t.Fatalf("errors: %v", p.Errors)
	}
	pr := p.Tracks[0].Params
	if pr.LPF != 800 || pr.HPF != 100 || pr.Crush != 8 || pr.Shape != 0.3 || pr.Delay != 0.4 || pr.Room != 0.2 || pr.Release != 0.5 || pr.Wave != "fm" {
		t.Fatalf("params %+v", pr)
	}
	if len(p.Commands) != 3 {
		t.Fatalf("commands %v", p.Commands)
	}
	// every 4 (fast 2) of rev: cycle 0 is doubled-speed, cycle 1 is a plain reverse
	c0 := p.Tracks[0].Pattern.Query(frac.Int(0), frac.Int(1))
	c1 := p.Tracks[0].Pattern.Query(frac.Int(1), frac.Int(2))
	if len(c0) != 8 || len(c1) != 4 || c1[0].Value != "f3" {
		t.Fatalf("chain wrong: c0=%d c1=%v", len(c0), c1)
	}
}

func TestFunctionErrors(t *testing.T) {
	for _, src := range []string{
		`d1 $ wobble $ s "bd"`, `d1 $ fast $ s "bd"`, `d1 $ fast x $ s "bd"`, `d1 $ every 2 $ s "bd"`,
		`d1 $ every 2 (fast 2 $ s "bd"`, `d1 $ rev 2 $ s "bd"`, `d1 $ fast 0 $ s "bd"`,
		`d1 "bd" # lpf 5`, `d1 "bd" # crush 40`, `delayfb 2`, `roomsize -1`,
	} {
		if p := Parse(src, 0); len(p.Errors) != 1 {
			t.Errorf("%q: want 1 error, got %v", src, p.Errors)
		}
	}
}
