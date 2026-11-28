package synth

import (
	"strings"
	"testing"
)

func TestCatalogue(t *testing.T) {
	if !IsPorted("tb303") || !IsPorted("sc808_snare") || IsPorted("prophet") || IsPorted("nonsense") {
		t.Fatal("IsPorted is wrong for tb303, sc808_snare, prophet or an unknown name")
	}
	cat := Catalogue()
	ents, rest, ok := strings.Cut(cat, "~")
	beats, _, _ := strings.Cut(rest, "~")
	if !ok || !strings.Contains(beats, "release") {
		t.Fatalf("no time options in %q", cat)
	}
	for _, want := range []string{"sc808_bassdrum|34|", "tb303||attack=0.01", "piano||"} {
		if !strings.Contains(ents, want) {
			t.Errorf("catalogue lacks %q", want)
		}
	}
	// every ported synth has exactly one entry
	seen := map[string]bool{}
	for _, e := range strings.Split(ents, ",") {
		n, _, _ := strings.Cut(e, "|")
		if seen[n] {
			t.Errorf("%s twice", n)
		}
		seen[n] = true
	}
	for _, sy := range SonicSynths {
		if IsNative(sy.Wave) != seen[sy.Name] {
			t.Errorf("%s: ported %v, in catalogue %v", sy.Name, IsNative(sy.Wave), seen[sy.Name])
		}
	}
}
