package live

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/susynth/susynth/internal/rubyvm"
)

// played is what Ruby sent, as "time track note" lines, plus errors and logs.
type played struct {
	notes []string
	errs  []string
	logs  []string
}

func (p *played) add(ms []rubyvm.Msg) {
	for _, m := range ms {
		switch m.Kind() {
		case "ev":
			t, _ := strconv.ParseFloat(m.Arg(0), 64)
			n := m.Arg(4)
			for _, kv := range strings.Split(m.Arg(5), ",") {
				if v, ok := strings.CutPrefix(kv, "n="); ok {
					f, _ := strconv.ParseFloat(v, 64)
					n = strconv.Itoa(int(f))
				}
			}
			p.notes = append(p.notes, fmt.Sprintf("%.2f %s %s", t, m.Arg(1), n))
		case "err":
			p.errs = append(p.errs, m.Arg(2))
		case "log":
			p.logs = append(p.logs, m.Arg(0))
		}
	}
}

func (p *played) has(t *testing.T, want ...string) {
	t.Helper()
	got := strings.Join(p.notes, "\n")
	for _, w := range want {
		if !strings.Contains("\n"+got+"\n", "\n"+w+"\n") {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func (p *played) hasnt(t *testing.T, bad ...string) {
	t.Helper()
	got := strings.Join(p.notes, "\n")
	for _, b := range bad {
		if strings.Contains("\n"+got+"\n", "\n"+b+"\n") {
			t.Errorf("unexpected %q in:\n%s", b, got)
		}
	}
}

// play runs code at 0, advances to edit, swaps in code2 at edit and plays to end.
func play(t *testing.T, v *rubyvm.VM, code, code2 string, edit, end float64, mode string) *played {
	t.Helper()
	v.StopAll()
	p := &played{}
	ms, _ := v.RunMode(1, 0, 0, "swap", code)
	p.add(ms)
	for at := 0.1; at < edit+1e-9; at += 0.1 {
		ms, _ = v.Advance(at)
		p.add(ms)
	}
	ms, _ = v.RunMode(2, 1, edit, mode, code2)
	p.add(ms)
	for at := edit + 0.1; at < end+1e-9; at += 0.1 {
		ms, _ = v.Advance(at)
		p.add(ms)
	}
	return p
}

const eight = "live_loop :a do\n  play %s\n  sleep 4\n  %s\n  sleep 4\nend\n"

func song(a, b string) string { return fmt.Sprintf(eight, a, "play "+b) }

func TestSwapIsHeardInPlace(t *testing.T) {
	v := vm(t)
	// The change is asked for at 2 s, halfway through the loop's first half.
	// The new version picks up the same pass, so the second half is new.
	p := play(t, v, song("60", "62"), song("70", "72"), 2, 12, "swap")
	p.has(t, "0.00 a 60", "4.00 a 72", "8.00 a 70")
	p.hasnt(t, "4.00 a 62", "0.00 a 70", "2.00 a 70")
	if len(p.errs) > 0 {
		t.Fatal(p.errs)
	}
	// Sonic Pi's way: the old pass finishes first.
	p = play(t, v, song("60", "62"), song("70", "72"), 2, 12, "pass")
	p.has(t, "0.00 a 60", "4.00 a 62", "8.00 a 70")
}

func TestFailingChangeKeepsTheOldVersion(t *testing.T) {
	v := vm(t)
	// Fails at once, while it is still catching up: the old one never stops.
	p := play(t, v, song("60", "62"), "live_loop :a do\n  plya 70\n  sleep 4\n  play 72\n  sleep 4\nend\n", 2, 12, "swap")
	p.has(t, "0.00 a 60", "4.00 a 62", "8.00 a 60")
	if len(p.errs) != 1 || !strings.Contains(strings.Join(p.logs, "\n"), "kept the old version") {
		t.Fatalf("errs %v logs %v", p.errs, p.logs)
	}
	// Fails after it took over: the old code comes back in time, from the
	// moment of the error.
	p = play(t, v, song("60", "62"), fmt.Sprintf(eight, "70", "plya 72"), 2, 12, "swap")
	p.has(t, "0.00 a 60", "4.00 a 62", "8.00 a 60")
	if len(p.errs) != 1 || !strings.Contains(strings.Join(p.logs, "\n"), "went back to the version that worked") {
		t.Fatalf("errs %v logs %v", p.errs, p.logs)
	}
	// The same in Sonic Pi's way: the new code fails on its first pass.
	p = play(t, v, song("60", "62"), fmt.Sprintf(eight, "70", "plya 72"), 2, 20, "pass")
	p.has(t, "0.00 a 60", "4.00 a 62", "8.00 a 70", "12.00 a 62", "16.00 a 60")
}

func TestSwapKeepsTicksAndSyncs(t *testing.T) {
	v := vm(t)
	tick := "live_loop :a do\n  play (ring %s).tick\n  sleep 1\nend\n"
	p := play(t, v, fmt.Sprintf(tick, "60, 62, 64, 65"), fmt.Sprintf(tick, "70, 72, 74, 75"), 2.5, 5, "swap")
	// pass 3 (at 3 s) is the fourth tick of either ring
	p.has(t, "0.00 a 60", "1.00 a 62", "2.00 a 64", "3.00 a 75", "4.00 a 70")
	p.hasnt(t, "3.00 a 65", "2.00 a 74")

	// A loop that waits for another still lands on its cue.
	drums := "live_loop :drums do\n  sample :bd_haus\n  sleep 2\nend\n"
	bass := "live_loop :bass do\n  sync :drums\n  play %s\n  sleep 1\n  play %s\n  sleep 0.5\nend\n"
	p = play(t, v, drums+fmt.Sprintf(bass, "40", "41"), "\n\n\n\n"+fmt.Sprintf(bass, "50", "51"), 3, 7, "swap")
	p.has(t, "2.00 bass 40", "3.00 bass 51", "4.00 bass 50", "5.00 bass 51", "6.00 bass 50")
	p.hasnt(t, "3.00 bass 41", "4.00 bass 40")
}

func TestQuickChangesInARow(t *testing.T) {
	v := vm(t)
	v.StopAll()
	p := &played{}
	ms, _ := v.RunMode(1, 0, 0, "swap", song("60", "62"))
	p.add(ms)
	ms, _ = v.Advance(1)
	p.add(ms)
	for i, n := range []string{"70", "80", "90"} {
		ms, _ = v.RunMode(2+i, 1, 2, "swap", song(n, n))
		p.add(ms)
	}
	ms, _ = v.Advance(12)
	p.add(ms)
	p.has(t, "0.00 a 60", "4.00 a 90", "8.00 a 90")
	p.hasnt(t, "4.00 a 62", "4.00 a 70", "4.00 a 80")
	if len(p.notes) != 3 {
		t.Fatalf("%v", p.notes)
	}
}

func TestDecksKeepTheirLoopsApart(t *testing.T) {
	v := vm(t)
	v.StopAll()
	p := &played{}
	setA := "live_loop :drums do\n  sample :bd_haus\n  sleep 1\nend\nlive_loop :bass do\n  sync :drums\n  play 40\n  sleep 0.5\nend\n"
	setB := "live_loop :drums do\n  sample :drum_snare_hard\n  sleep 1.5\nend\n"
	ms, _ := v.RunMode(1, 0, 0, "swap", setA)
	p.add(ms)
	ms, _ = v.Advance(2.1)
	p.add(ms)
	ms, _ = v.NewDeck()
	var ren []string
	for _, m := range ms {
		if m.Kind() == "ren" {
			ren = append(ren, m.Arg(0)+">"+m.Arg(1))
		}
	}
	if strings.Join(ren, " ") != "drums>old/drums bass>old/bass" && strings.Join(ren, " ") != "bass>old/bass drums>old/drums" {
		t.Fatalf("renames %v", ren)
	}
	ms, _ = v.RunMode(2, 0, 4, "swap", setB)
	p.add(ms)
	ms, _ = v.Advance(7.1)
	p.add(ms)
	// the old bass still follows the old drums, not the new ones
	p.has(t, "3.00 old/drums bd_haus", "3.00 old/bass 40", "4.00 drums drum_snare_hard", "5.00 old/bass 40", "5.50 drums drum_snare_hard")
	p.hasnt(t, "5.50 old/bass 40", "4.00 bass 40")
	ms, _ = v.StopOld()
	p.notes = nil
	p.add(ms)
	ms, _ = v.Advance(10)
	p.add(ms)
	for _, n := range p.notes {
		if strings.Contains(n, "old/") {
			t.Fatalf("the old set still plays: %v", p.notes)
		}
	}
	if len(p.notes) == 0 {
		t.Fatal("the new set stopped too")
	}
}
