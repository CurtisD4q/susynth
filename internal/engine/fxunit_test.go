package engine

import (
	"math"
	"reflect"
	"testing"

	"github.com/susynth/susynth/internal/synth"
)

func rmsOf(x []float32) float64 {
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(len(x)+1))
}

func renderSecs(e *Engine, secs float64) []float32 {
	var out []float32
	for i := 0; i < int(secs*SampleRate/480); i++ {
		out = append(out, e.Render(480)...)
	}
	return out
}

func unitScene(opts map[string]float64, closeAt float64) *Engine {
	e := New()
	e.Play()
	e.Schedule(
		Hit{At: 0, Track: "t", Unit: &UnitOp{Op: "open", ID: 7, Name: "reverb", Opts: opts, BPM: 60}},
		Hit{At: 0, Track: "t", Word: "a4", Gate: SampleRate / 5, Bus: 7,
			Params: synth.Params{Wave: "sine", Gain: 1, Release: 0.05}},
		Hit{At: int64(closeAt * SampleRate), Track: "t", Unit: &UnitOp{Op: "close", ID: 7}},
	)
	return e
}

// A sound inside with_fx :reverb rings on after the block, and the unit is
// freed once it has waited out its kill delay.
func TestUnitTailOutlivesBlockThenIsFreed(t *testing.T) {
	e := unitScene(map[string]float64{"mix": 1, "room": 0.2}, 0.3)
	out := renderSecs(e, 1)
	if len(e.units) != 1 {
		t.Fatalf("unit freed too early: %d", len(e.units))
	}
	l, _ := splitLR(out)
	if tail := rmsOf(l[int(0.5*SampleRate):int(0.8*SampleRate)]); tail < 1e-4 {
		t.Fatalf("no tail after the block: %g", tail)
	}
	renderSecs(e, 5)
	if len(e.units) != 0 {
		t.Fatalf("unit not freed after its kill delay (room 0.2 -> 3 s): %d left", len(e.units))
	}
}

func splitLR(x []float32) (l, r []float32) {
	for i := 0; i+1 < len(x); i += 2 {
		l, r = append(l, x[i]), append(r, x[i+1])
	}
	return
}

// mix 0 passes the sound through; the dry sound is the same as with no unit.
func TestUnitMixZeroIsDry(t *testing.T) {
	wet := renderSecs(unitScene(map[string]float64{"mix": 0}, 0.3), 0.3)
	e := New()
	e.Play()
	e.Schedule(Hit{At: 0, Track: "t", Word: "a4", Gate: SampleRate / 5,
		Params: synth.Params{Wave: "sine", Gain: 1, Release: 0.05}})
	dry := renderSecs(e, 0.3)
	var worst float64
	for i := range dry {
		worst = math.Max(worst, math.Abs(float64(dry[i]-wet[i])))
	}
	if worst > 1e-5 {
		t.Fatalf("mix 0 differs from dry by %g", worst)
	}
}

// Control changes a running unit; kill frees it and the sound goes direct.
func TestUnitControlAndKill(t *testing.T) {
	e := unitScene(map[string]float64{"mix": 0}, 5)
	e.Schedule(Hit{At: int64(0.3 * SampleRate), Track: "t", Unit: &UnitOp{Op: "ctl", ID: 7, Opts: map[string]float64{"amp": 0}}})
	out := renderSecs(e, 0.6)
	l, _ := splitLR(out)
	if rmsOf(l[int(0.4*SampleRate):int(0.6*SampleRate)]) > 1e-6 {
		t.Fatalf("amp 0 left sound")
	}
	e.Schedule(Hit{At: e.samples + 10, Track: "t", Unit: &UnitOp{Op: "kill", ID: 7}})
	renderSecs(e, 0.1)
	if len(e.units) != 0 {
		t.Fatalf("kill left %d units", len(e.units))
	}
}

// A unit inside another feeds it: the inner effect's output goes through the outer.
func TestUnitsNest(t *testing.T) {
	e := New()
	e.Play()
	e.Schedule(
		Hit{At: 0, Track: "t", Unit: &UnitOp{Op: "open", ID: 1, Name: "echo", Opts: map[string]float64{"mix": 1, "amp": 0}, BPM: 60}},
		Hit{At: 0, Track: "t", Unit: &UnitOp{Op: "open", ID: 2, Parent: 1, Name: "echo", Opts: map[string]float64{"mix": 1}, BPM: 60}},
		Hit{At: 0, Track: "t", Word: "a4", Gate: SampleRate / 5, Bus: 2,
			Params: synth.Params{Wave: "sine", Gain: 1, Release: 0.05}},
	)
	// the outer unit has amp 0, so nothing from inside it reaches the mix
	l, _ := splitLR(renderSecs(e, 1))
	if rmsOf(l) > 1e-6 {
		t.Fatalf("sound escaped the outer unit: %g", rmsOf(l))
	}
}

// The unit names the engine runs are the names Ruby is told about.
func TestFXUnitNamesAgree(t *testing.T) {
	if got, want := FXUnitNames(), synth.FXUnits(); !reflect.DeepEqual(got, want) {
		t.Fatalf("engine units %v, catalogue %v", got, want)
	}
}
