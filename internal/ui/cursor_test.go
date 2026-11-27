package ui

import (
	"slices"
	"strings"
	"testing"

	mint "github.com/CurtisD4q/pmin-kit"
)

// Sounds, Settings and Perform are screens with one pane. These draw them
// as Mint does and check that what susynth does follows the cursor.

// shownApp is page p drawn once, with the cursor down rows below the top.
func shownApp(t *testing.T, p page, setlist ...string) (*App, func() string) {
	t.Helper()
	a := layoutApp(t, "default", pagePlay)
	a.setlist, a.live = setlist, -1
	a.gotoPage(p)
	s := a.screen(p)
	draw := func() string { return strings.Join(gridText(s.FrameText(32, 110), 32, 110), "\n") }
	draw()
	return a, draw
}

func down(a *App, draw func() string, n int) {
	a.scr.MoveCursor(n)
	draw()
}

func typed(a *App, draw func() string, r string) {
	a.grab(nil, &mint.Key{Name: r})
	draw()
}

func TestSoundsDescriptionFollowsTheCursor(t *testing.T) {
	a, draw := shownApp(t, pageSounds)
	down(a, draw, 2)
	it, ok := a.selectedSound(a.scr)
	if !ok || it.name != "sine" {
		t.Fatalf("selected %q, %v", it.name, ok)
	}
	if got := draw(); !strings.Contains(got, "try it: use_synth :sine") {
		t.Fatalf("the description is not about sine:\n%s", got)
	}
}

func TestSoundsGroupHeadingIsDescribed(t *testing.T) {
	a, draw := shownApp(t, pageSounds)
	synths := 0
	for _, it := range a.sounds() {
		if it.group == "Synths" {
			synths++
		}
	}
	down(a, draw, synths+1) // past its heading and its sounds
	if got := draw(); !strings.Contains(got, blurbOf("Drum synths")) {
		t.Fatalf("the Drum synths heading is not described:\n%s", got)
	}
}

func TestSoundsInsertUsesTheRowUnderTheCursor(t *testing.T) {
	a, draw := shownApp(t, pageSounds)
	down(a, draw, 2)
	a.grab(nil, &mint.Key{Name: "i"})
	if !strings.Contains(a.ed.Text(), "use_synth :sine\n") {
		t.Fatalf("i did not insert the sine example:\n%s", a.ed.Text())
	}
}

func TestSettingsHintFollowsTheCursor(t *testing.T) {
	a, draw := shownApp(t, pageSettings)
	for i := range settingRows {
		got := draw()
		if want := strings.Fields(settingRows[i].hint)[0:3]; !strings.Contains(got, strings.Join(want, " ")) {
			t.Fatalf("row %d (%s) shows another hint:\n%s", i, settingRows[i].label, got)
		}
		down(a, draw, 1)
	}
}

func TestPerformKeysActOnTheSetUnderTheCursor(t *testing.T) {
	// Now and Next come first, then the three sets.
	a, draw := shownApp(t, pagePerform, "one", "two", "three")
	for _, key := range []string{"d", "[", "]"} {
		typed(a, draw, key)
		if !slices.Equal(a.setlist, []string{"one", "two", "three"}) {
			t.Fatalf("%s on Now changed the set list: %v", key, a.setlist)
		}
	}
	down(a, draw, 4)
	typed(a, draw, "d")
	if !slices.Equal(a.setlist, []string{"one", "two"}) {
		t.Fatalf("d on the third set left %v", a.setlist)
	}

	// A moved set keeps the cursor, so the key can be pressed again.
	a, draw = shownApp(t, pagePerform, "one", "two", "three")
	down(a, draw, 4)
	typed(a, draw, "[")
	typed(a, draw, "[")
	if !slices.Equal(a.setlist, []string{"three", "one", "two"}) {
		t.Fatalf("[ [ on the third set left %v", a.setlist)
	}
	typed(a, draw, "[") // already at the top
	typed(a, draw, "]")
	if !slices.Equal(a.setlist, []string{"one", "three", "two"}) {
		t.Fatalf("[ ] at the top left %v", a.setlist)
	}
}
