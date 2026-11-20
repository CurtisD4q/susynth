package rubyvm

import "testing"

func TestBlockRangeKeepsBlankLinesInsideLoops(t *testing.T) {
	code := "" +
		"use_bpm 120\n" +
		"\n" +
		"live_loop :drums do\n" +
		"  sample :bd_haus\n" +
		"\n" +
		"  sleep 0.5\n" +
		"end\n" +
		"\n" +
		"live_loop :bass do\n" +
		"  play 60 if true\n" +
		"  sleep 1\n" +
		"end\n"
	// 1 use_bpm, 2 blank, 3-7 drums (blank line inside), 8 blank, 9-12 bass
	cases := []struct {
		cursor, first, last int
	}{
		{1, 1, 1},
		{2, 3, 7}, // blank line before the loop: the following statement
		{4, 3, 7}, // inside drums
		{5, 3, 7}, // the blank line inside drums
		{6, 3, 7},
		{7, 3, 7},
		{8, 9, 12}, // blank between loops: the next loop
		{10, 9, 12},
		{12, 9, 12},
	}
	for _, c := range cases {
		first, last, ok := BlockRange(code, c.cursor)
		if !ok || first != c.first || last != c.last {
			t.Errorf("cursor %d: got %d–%d ok=%v, want %d–%d", c.cursor, first, last, ok, c.first, c.last)
		}
	}
}

func TestBlockRangeStructureNotBlankLines(t *testing.T) {
	// No blank line between use_bpm and the loop: they are still two statements.
	code := "use_bpm 120\nlive_loop :a do\n  sleep 1\nend\n"
	if f, l, ok := BlockRange(code, 1); !ok || f != 1 || l != 1 {
		t.Errorf("use_bpm = %d–%d %v", f, l, ok)
	}
	if f, l, ok := BlockRange(code, 3); !ok || f != 2 || l != 4 {
		t.Errorf("loop = %d–%d %v", f, l, ok)
	}
}

func TestBlockRangeNestedAndModifier(t *testing.T) {
	code := "" +
		"live_loop :a do\n" +
		"  with_fx :reverb do\n" +
		"    play 60\n" +
		"\n" +
		"    sleep 1\n" +
		"  end\n" +
		"end\n" +
		"n = if one_in(2)\n" +
		"  60\n" +
		"else\n" +
		"  62\n" +
		"end\n" +
		"play n\n"
	if f, l, ok := BlockRange(code, 3); !ok || f != 1 || l != 7 {
		t.Errorf("nested cursor runs the whole live_loop, got %d–%d %v", f, l, ok)
	}
	if f, l, ok := BlockRange(code, 9); !ok || f != 8 || l != 12 {
		t.Errorf("assignment-if = %d–%d %v", f, l, ok)
	}
	if f, l, ok := BlockRange(code, 13); !ok || f != 13 || l != 13 {
		t.Errorf("play = %d–%d %v", f, l, ok)
	}
}

func TestBlockRangeWhileDoAndStrings(t *testing.T) {
	code := "while true do\n  sleep 1\nend\nplay \"end\"\nsleep 1\n"
	if f, l, ok := BlockRange(code, 2); !ok || f != 1 || l != 3 {
		t.Errorf("while = %d–%d %v", f, l, ok)
	}
	if f, l, ok := BlockRange(code, 4); !ok || f != 4 || l != 4 {
		t.Errorf("string = %d–%d %v", f, l, ok)
	}
	code = "live_loop :a do\n  play 60\n  sleep 1 while true\nend\n"
	if f, l, ok := BlockRange(code, 3); !ok || f != 1 || l != 4 {
		t.Errorf("modifier while swallowed the loop: %d–%d %v", f, l, ok)
	}
}

func TestBlockRangeMatchesCursorRun(t *testing.T) {
	// The line numbers TestBlockRunKeepsTopLevelTempo passes as a cursor.
	code := "use_bpm 120\n\nlive_loop :a do\n  play 60\n  sleep 1\nend\n"
	if f, l, ok := BlockRange(code, 4); !ok || f != 3 || l != 6 {
		t.Errorf("got %d–%d %v", f, l, ok)
	}
}

func TestLiveLoopNamesIgnoreComments(t *testing.T) {
	code := "live_loop :drums do\nend\n# live_loop :gone do\nputs \"live_loop :nope\"\nlive_loop(:bass) do\nend\n"
	got := LiveLoopNames(code)
	if !got["drums"] || !got["bass"] || got["gone"] {
		t.Errorf("names = %v", got)
	}
}
