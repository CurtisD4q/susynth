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
	if !strings.Contains(strings.Join(flat(other), " "), "isn't in susynth yet; it is skipped") {
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

// Code written for Sonic Pi keeps playing: names susynth hasn't got fall
// back with a warning, and the rest of Sonic Pi's language runs.
func TestSonicPiCompat(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
use_synth :rhodey_nope
play release: 0.1
sample :loop_nope_two
s = play 60, release: 2
control s, note: 65
puts "ring #{"x-x".ring.tick} #{(:c4 + 12)} #{(chord :c4, :major).to_a.inspect} #{(chord(:c4, :major) + 12).to_a.inspect}"
puts "warp #{rand(2..2)} #{ramp(1, 2)[5]} #{octs(60, 2).to_a.inspect} #{doubles(1, 3).to_a.inspect}"
time_warp 0.5 do
  play 70
end
sleep 1
sleep -0.5
play 72
density 2 do |i|
  puts "d#{i}"
  sleep 1
end
uncomment do
  sample "bd_", 0
end
comment do
  play 99
end
use_random_source :pink
midi_all_notes_off
with_bpm_mul 2 do
  sleep 1
end
`, 10)
	logs := strings.Join(flat(other), " | ")
	t.Log(logs)
	t.Log(summary(evs))
	if strings.Contains(logs, "err") {
		t.Errorf("errors: %s", logs)
	}
	for _, want := range []string{"ring x", "72", "[60, 64, 67]", "[72, 76, 79]", "warp 2", " 2 ", "[60, 72]",
		"[1, 2, 4]", "d0", "d1", ":rhodey_nope isn't in susynth", ":loop_nope_two isn't in susynth yet"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %q in %s", want, logs)
		}
	}
	got := summary(evs)
	for _, want := range []string{"0.000 main beep", "0.500 main beep", "0.500 main beep", "1.500 main bd_haus"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(strings.Join(flat(evs), " "), "n=99") {
		t.Error("comment block played")
	}
}

func TestControlEvents(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
s = play 60, release: 4, note_slide: 1
sleep 1
control s, note: 65, amp: 0.5
s.control cutoff: 80, cutoff_slide: 2
kill s
`, 4)
	t.Log(flat(other))
	if len(evs) != 4 {
		t.Fatalf("events: %v", flat(evs))
	}
	if evs[0].Arg(3) != "p" || !strings.Contains(evs[0].Arg(5), "id=") || !strings.Contains(evs[0].Arg(5), "nsl=1") {
		t.Errorf("play: %v", evs[0])
	}
	id := strings.Split(strings.Split(evs[0].Arg(5), "id=")[1], ",")[0]
	for i, want := range []string{"n=65.0", "cusl=2", "kill=1"} {
		e := evs[i+1]
		if e.Arg(3) != "c" || !strings.Contains(e.Arg(5), want) || !strings.Contains(e.Arg(5), "id="+id) || e.Arg(0)[:5] != "1.000" {
			t.Errorf("control %d: %v", i, e)
		}
	}
}

func TestSampleOptionsEvents(t *testing.T) {
	v := vm(t)
	v.SetTables([]string{"beep", "saw", "prophet"}, []string{"bd_haus", "drum_snare_hard", "loop_amen=1.75"})
	defer v.SetTables([]string{"beep", "saw", "prophet"}, []string{"bd_haus", "drum_snare_hard"})
	evs, other := run(t, v, `
use_bpm 120
sample :loop_amen, beat_stretch: 4
sample :loop_amen, slice: 2, num_slices: 4, rate: -1
sample :loop_amen, start: 0.1, finish: 0.3, sustain: 0.5, release: 0.25, lpf: 80
puts "dur #{sample_duration(:loop_amen).round(3)} #{sample_duration(:loop_amen, beat_stretch: 4).round(3)}"
use_sample_bpm :loop_amen
puts "bpm #{current_bpm.round(3)}"
`, 2)
	logs := strings.Join(flat(other), " | ")
	t.Log(logs)
	var kv []string
	for _, e := range evs {
		kv = append(kv, e.Arg(5))
	}
	t.Log(kv)
	if len(kv) != 3 || !strings.Contains(kv[0], "rate=0.875") || !strings.Contains(kv[1], "st=0.5,fi=0.75") ||
		!strings.Contains(kv[1], "rate=-1.0") || !strings.Contains(kv[2], "st=0.1,fi=0.3") ||
		!strings.Contains(kv[2], "cutoff=80.0") || !strings.Contains(kv[2], "s=0.25") || !strings.Contains(kv[2], "r=0.125") {
		t.Errorf("events: %v", kv)
	}
	if !strings.Contains(logs, "dur 3.5 4.0") || !strings.Contains(logs, "bpm 34.286") {
		t.Errorf("durations: %s", logs)
	}
}

func TestModEffects(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
use_bpm 120
with_fx :slicer, phase: 0.5 do |s|
  with_fx :wobble do
    sleep 1
    play 60
  end
  control s, phase: 1
  play 62
end
with_fx :pitch_shift, pitch: 12 do
  play 60
end
with_fx :record do
  play 64
end
`, 4)
	t.Log(flat(other))
	var kv []string
	for _, e := range evs {
		kv = append(kv, e.Arg(5))
	}
	t.Log(kv)
	if len(kv) != 4 {
		t.Fatalf("events %v", kv)
	}
	// innermost first: wobble (4) then slicer (1); the slicer had run 1 beat
	if !strings.Contains(kv[0], "x0k=4") || !strings.Contains(kv[0], "x1k=1") || !strings.Contains(kv[0], "x1ph=0.25") ||
		!strings.Contains(kv[0], "x1org=0.5") {
		t.Errorf("nested effects: %s", kv[0])
	}
	if !strings.Contains(kv[1], "x0ph=0.5") {
		t.Errorf("control on the slicer: %s", kv[1])
	}
	if !strings.Contains(kv[2], "n=72.0") || strings.Contains(kv[3], "x0k") {
		t.Errorf("pitch_shift / record: %s %s", kv[2], kv[3])
	}
	if !strings.Contains(strings.Join(flat(other), " "), "with_fx :record does nothing in susynth") {
		t.Error("no note about :record")
	}
}

// Every chord and scale name Sonic Pi has works, with Sonic Pi's notes
// (checked in full against chord.rb and scale.rb when the tables were made).
func TestSonicPiChordsAndScales(t *testing.T) {
	v := vm(t)
	_, other := run(t, v, `
puts chord(:e3, :minor9).to_a.inspect
puts chord(:c4, :'9+5').to_a.inspect
puts chord(:c4, :maj7).to_a.inspect
puts "#{chord_names.size} #{scale_names.size}"
puts scale(:c4, :messiaen1).to_a.inspect
puts scale(:c4, :rast).to_a.map { |x| x.round(3) }.inspect
`, 1)
	logs := strings.Join(flat(other), " | ")
	for _, want := range []string{"[52, 55, 59, 62, 66]", "[60, 64, 68, 70, 74]", "[60, 64, 67, 71]", "88 160",
		"[60, 62, 64, 66, 68, 70, 72]", "[60, 62.038, 63.849, 64.981, 67.019, 69.057, 70.868, 72.0]"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %s in %s", want, logs)
		}
	}
}

// Synth options reach the engine, mono_player plays a sample, fx pre_amp and
// amp scale the notes inside them, and on runs only when it is true.
func TestSynthOptionsAndPlayers(t *testing.T) {
	v := vm(t)
	v.SetTables([]string{"beep", "dsaw", "mod_saw", "fm", "mono_player"}, []string{"bd_haus"})
	defer v.SetTables([]string{"beep", "saw", "prophet"}, []string{"bd_haus", "drum_snare_hard"})
	evs, other := run(t, v, `
use_bpm 120
play 60, synth: :dsaw, detune: 0.3
play 60, synth: :fm, divisor: 3, depth: 2
play 60, synth: :mod_saw, mod_range: 7, mod_wave: 3
synth :mono_player, path: :bd_haus, amp: 0.5
with_fx :level, pre_amp: 0.5, amp: 0.5 do
  play 60
end
on true do puts "on-yes" end
on false do puts "on-no" end
`, 2)
	logs := strings.Join(flat(other), " | ")
	var kv []string
	for _, e := range evs {
		kv = append(kv, e.Arg(4)+" "+e.Arg(5))
	}
	t.Log(kv, logs)
	if len(kv) != 5 {
		t.Fatalf("events: %v", kv)
	}
	for i, want := range []string{"det=0.3", "div=3.0,dep=2.0", "mr=7.0,mp=0.125,mw=3", "bd_haus", "amp=0.25"} {
		if !strings.Contains(kv[i], want) {
			t.Errorf("event %d %q: want %q", i, kv[i], want)
		}
	}
	if !strings.Contains(logs, "on-yes") || strings.Contains(logs, "on-no") {
		t.Errorf("on: %s", logs)
	}
}

// midi_* calls become raw MIDI events at the right time, with no warning.
func TestMIDICalls(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
use_bpm 120
midi_note_on :c4, 100, channel: 2
midi :e4, sustain: 2
midi_cc 7, 90
midi_pitch_bend 0
midi_pc 5
`, 2)
	var got []string
	for _, e := range evs {
		got = append(got, e.Arg(0)[:5]+" "+e.Arg(4)+" "+e.Arg(5))
	}
	want := []string{"0.000 midi st=145,d1=60,d2=100", "0.000 midi st=144,d1=64,d2=127", "1.000 midi st=128,d1=64,d2=64",
		"0.000 midi st=176,d1=7,d2=90", "0.000 midi st=224,d1=0,d2=64", "0.000 midi st=192,d1=5"}
	if len(got) != len(want) {
		t.Fatalf("events: %v other: %v", got, flat(other))
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if l := strings.Join(flat(other), " "); strings.Contains(l, "isn't available") || strings.Contains(l, "does nothing") {
		t.Errorf("warned: %s", l)
	}
}

// Ruby's Set (reached by autoload), json, csv, stringio and a few more work
// without files on disk; a gem that is not there is still a LoadError.
func TestStdlibStandIns(t *testing.T) {
	v := vm(t)
	_, other := run(t, v, `
s = Set.new([1, 2, 3]) << 9
puts "set #{(s & Set[2, 9, 10]).to_a} #{[3, 3, 4].to_set.size}"
require 'json'
puts "json #{JSON.parse('{"a":[1,2.5,null],"b":"x"}', symbolize_names: true)} #{JSON.generate({a: [1, nil, "q"]})}"
require 'stringio'
io = StringIO.new; io.puts "hi"; io << "x"
puts "io #{io.string.inspect}"
require 'csv'
puts "csv #{CSV.parse("a,b\n1,\"x,y\"\n", headers: true).map(&:to_h)} #{CSV.generate_line([1, "a,b"]).inspect}"
require 'securerandom'
require 'ostruct'
puts "misc #{SecureRandom.hex(4).size} #{OpenStruct.new(a: 1).a}"
begin
  require 'socket'
rescue LoadError
  puts "loaderror"
end
`, 1)
	logs := strings.Join(flat(other), " | ")
	for _, want := range []string{"set [2, 9] 2", `json {a: [1, 2.5, nil], b: "x"} {"a":[1,null,"q"]}`, `io "hi\nx"`,
		`csv [{"a" => "1", "b" => "x,y"}] "1,\"a,b\"\n"`, "misc 8 1", "loaderror"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %q in %s", want, logs)
		}
	}
}

// A sample's pitch: and with_fx :pitch_shift on a sample become the voice's
// pitch shifter (kind 12) with no warning; a note is simply transposed.
func TestPitchShiftEvents(t *testing.T) {
	v := vm(t)
	evs, other := run(t, v, `
sample :bd_haus, pitch: 3
with_fx :pitch_shift, pitch: -12 do
  sample :bd_haus
  play 60
end
`, 1)
	var kv []string
	for _, e := range evs {
		kv = append(kv, e.Arg(5))
	}
	if len(kv) != 3 || !strings.Contains(kv[0], "x0k=12") || !strings.Contains(kv[0], "x0a=3.0") ||
		!strings.Contains(kv[1], "x0k=12") || !strings.Contains(kv[1], "x0a=-12.0") ||
		strings.Contains(kv[2], "x0k") || !strings.Contains(kv[2], "n=48.0") {
		t.Errorf("events: %v", kv)
	}
	if l := strings.Join(flat(other), " "); strings.Contains(l, "speed") || strings.Contains(l, "isn't in susynth") {
		t.Errorf("warned: %s", l)
	}
}

// invert_chord and the small settings functions exist.
func TestInvertChordAndSettings(t *testing.T) {
	v := vm(t)
	_, other := run(t, v, `
puts invert_chord(chord(:c4, :major), 1).to_a.inspect
puts invert_chord(chord(:c4, :major), -1).to_a.inspect
use_timing_warnings false
with_timing_warnings(false) { puts "block" }
`, 1)
	logs := strings.Join(flat(other), " | ")
	for _, want := range []string{"[64, 67, 72]", "[55, 60, 64]", "block"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %s in %s", want, logs)
		}
	}
}
