package rubyvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var shared *VM

func vm(t *testing.T) *VM {
	t.Helper()
	if shared == nil {
		dir := os.Getenv("SUSYNTH_TEST_CACHE")
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "susynth-test-wasm")
		}
		shared = Start(dir)
		shared.SetTables([]string{"beep", "saw", "prophet"}, []string{"bd_haus", "drum_snare_hard"})
	}
	select {
	case <-shared.Ready():
	case <-time.After(5 * time.Minute):
		t.Fatal("Ruby did not start")
	}
	if err := shared.Err(); err != nil {
		t.Fatal(err)
	}
	return shared
}

func run(t *testing.T, v *VM, code string, upto float64) (evs []Msg, other []Msg) {
	t.Helper()
	ms, err := v.StopAll()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := v.Run(1, 0, 0, code)
	if err != nil {
		t.Fatal(err)
	}
	m3, err := v.Advance(upto)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range append(append(ms, m2...), m3...) {
		if m.Kind() == "ev" {
			evs = append(evs, m)
		} else {
			other = append(other, m)
		}
	}
	return
}

func summary(evs []Msg) string {
	var b []string
	for _, e := range evs {
		b = append(b, e.Arg(0)[:5]+" "+e.Arg(1)+" "+e.Arg(4))
	}
	return strings.Join(b, " | ")
}

func TestLiveLoopsAndSync(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
use_bpm 120
live_loop :drums do
  sample :bd_haus
  sleep 1
end
live_loop :bass do
  sync :drums
  use_synth :saw
  play (ring :e2, :g2).tick, release: 0.2
  sleep 1
end
`, 2.0)
	got := summary(evs)
	t.Log(got)
	for _, want := range []string{"0.000 drums bd_haus", "0.500 drums bd_haus", "0.000 bass saw", "0.500 bass saw", "1.000 bass saw"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, m := range other {
		if m.Kind() == "err" {
			t.Errorf("unexpected error %v", m)
		}
	}
	// notes from the ring: e2 = 40, g2 = 43
	var notes []string
	for _, e := range evs {
		if e.Arg(1) == "bass" {
			for _, kv := range strings.Split(e.Arg(5), ",") {
				if strings.HasPrefix(kv, "n=") {
					notes = append(notes, kv)
				}
			}
		}
	}
	if strings.Join(notes, " ") != "n=40.0 n=43.0 n=40.0 n=43.0" {
		t.Errorf("ring/tick notes = %v", notes)
	}
}

func TestErrorsHaveLines(t *testing.T) {
	v := vm(t)
	_, other := run(t, v, "live_loop :x do\n  play 60\n  plya 62\n  sleep 1\nend\n", 1)
	found := false
	for _, m := range other {
		if m.Kind() == "err" {
			t.Log(m)
			found = m.Arg(0) == "3"
		}
	}
	if !found {
		t.Errorf("no error on line 3: %v", other)
	}
	_, other = run(t, v, "live_loop :y do\n  play 60\nend\n", 1)
	if !strings.Contains(strings.Join(flat(other), " "), "did not sleep") {
		t.Errorf("no did-not-sleep error: %v", other)
	}
	ms, _ := v.Check("live_loop :z do\n  play 60\n  sleep 1\n")
	t.Log(ms)
	if len(ms) == 0 || ms[0].Kind() != "err" || ms[0].Arg(0) != "3" {
		t.Errorf("syntax error not reported: %v", ms)
	}
	_, other = run(t, v, "sample :loop_nope\n", 1)
	if !strings.Contains(strings.Join(flat(other), " "), "isn't available") {
		t.Errorf("unknown sample not reported: %v", other)
	}
}

func flat(ms []Msg) []string {
	var out []string
	for _, m := range ms {
		out = append(out, strings.Join(m, " "))
	}
	return out
}

func TestRubyFeatures(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
define :bassline do |n|
  n.times { |i| play scale(:c3, :minor_pentatonic)[i], amp: 0.5; sleep 0.25 }
end
notes = {kick: 36, snare: 38}
puts "hi #{notes[:kick]} #{chord(:c4, :m7).to_a.inspect} #{spread(3, 8).to_a.map { |b| b ? 'x' : '.' }.join}"
with_fx :reverb, mix: 0.5 do
  with_fx :echo, phase: 0.5 do
    bassline 3
  end
end
[1, 2].each_with_index.map { |a, i| a * i }.sum.then { |x| puts x }
`, 10)
	logs := strings.Join(flat(other), " | ")
	t.Log(logs)
	t.Log(summary(evs))
	if !strings.Contains(logs, "hi 36 [60, 63, 67, 70] x..x..x.") {
		t.Errorf("Ruby features: %s", logs)
	}
	if len(evs) != 3 || !strings.Contains(evs[0].Arg(5), "room=0.5") || !strings.Contains(evs[0].Arg(5), "dtime=0.5") {
		t.Errorf("fx events: %v", evs)
	}
}

func TestStuckLoopStopsOnlyItself(t *testing.T) {
	v := vm(t)
	cases := []struct {
		name string
		bad  string
	}{
		{"tight loop", "loop do\n    1 + 1\n  end\n  sleep 1\n"},
		{"empty loop", "loop do\n  end\n  sleep 1\n"},
		{"while true", "while true\n  end\n  sleep 1\n"},
		{"sleep 0", "play 64\n  sleep 0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := "live_loop :ok do\n  play 60\n  sleep 1\nend\nlive_loop :bad do\n  " + tc.bad + "end\n"
			if _, err := v.StopAll(); err != nil {
				t.Fatal(err)
			}
			if _, err := v.Run(1, 0, 0, code); err != nil {
				t.Fatal(err)
			}
			ms, err := v.Advance(2)
			if err != nil {
				t.Fatalf("stuck loop took the VM down: %v", err)
			}
			var okN int
			saw := false
			for _, m := range ms {
				if m.Kind() == "ev" && m.Arg(1) == "ok" {
					okN++
				}
				if m.Kind() == "ev" && m.Arg(1) == "bad" && tc.name != "sleep 0" {
					t.Errorf("bad loop still sounded: %v", m)
				}
				if m.Kind() == "err" && strings.Contains(strings.Join(m, " "), "did not sleep") && strings.Contains(strings.Join(m, " "), "bad") {
					saw = true
				}
			}
			if !saw {
				t.Fatalf("no did-not-sleep for :bad: %v", flat(ms))
			}
			if okN < 2 {
				t.Fatalf(":ok stopped too (%d events): %v", okN, flat(ms))
			}
			ms, err = v.Advance(3.5)
			if err != nil {
				t.Fatal(err)
			}
			okN = 0
			for _, m := range ms {
				if m.Kind() == "ev" && m.Arg(1) == "ok" {
					okN++
				}
				if m.Kind() == "ev" && m.Arg(1) == "bad" {
					t.Errorf("bad loop resumed: %v", m)
				}
			}
			if okN < 1 {
				t.Fatalf(":ok silent after the stuck loop was stopped: %v", flat(ms))
			}
		})
	}
}

func TestKillOneLoop(t *testing.T) {
	v := vm(t)
	if _, err := v.StopAll(); err != nil {
		t.Fatal(err)
	}
	code := "live_loop :keep do\n  play 60\n  sleep 1\nend\nlive_loop :drop do\n  play 64\n  sleep 1\nend\n"
	if _, err := v.Run(1, 0, 0, code); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Advance(0.5); err != nil {
		t.Fatal(err)
	}
	ms, err := v.Kill("drop")
	if err != nil {
		t.Fatal(err)
	}
	ended := false
	for _, m := range ms {
		if m.Kind() == "thr-" && m.Arg(0) == "drop" {
			ended = true
		}
	}
	if !ended {
		t.Fatalf("kill did not end :drop: %v", ms)
	}
	ms, err = v.Advance(2)
	if err != nil {
		t.Fatal(err)
	}
	var keep, drop int
	for _, m := range ms {
		if m.Kind() != "ev" {
			continue
		}
		if m.Arg(1) == "keep" {
			keep++
		}
		if m.Arg(1) == "drop" {
			drop++
		}
	}
	if keep < 1 || drop != 0 {
		t.Fatalf("after kill keep=%d drop=%d (%v)", keep, drop, flat(ms))
	}
}

func TestBlockRunKeepsTopLevelTempo(t *testing.T) {
	v := vm(t)
	code := "use_bpm 120\n\nlive_loop :a do\n  play 60\n  sleep 1\nend\n"
	v.StopAll()
	v.Run(1, 0, 0, code)
	v.Advance(1.0)
	// now change only the loop (cursor on line 4), as ^E would
	code2 := "use_bpm 120\n\nlive_loop :a do\n  play 72\n  sleep 1\nend\n"
	ms, _ := v.Run(2, 4, 1.0, code2)
	m2, _ := v.Advance(3.0)
	var times []string
	for _, m := range append(ms, m2...) {
		if m.Kind() == "ev" && strings.Contains(m.Arg(5), "n=72") {
			times = append(times, m.Arg(0)[:5])
		}
	}
	// at 120 bpm sleep 1 is 0.5 s. A run at 1.0 goes before the pass that
	// starts at 1.0, so that pass is already the new body.
	if strings.Join(times, " ") != "1.000 1.500 2.000 2.500" {
		t.Errorf("redefined loop times %v (tempo lost?)", times)
	}
}
