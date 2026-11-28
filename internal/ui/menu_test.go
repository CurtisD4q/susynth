package ui

import (
	"testing"

	mint "github.com/CurtisD4q/pmin-kit"
)

// Off the editor m is left to Mint, which opens the pages pop-up, as in any
// other Mint program. In the editor it is a letter.
func TestMOpensThePagesExceptWhereItIsTyped(t *testing.T) {
	press := func() *mint.Key { return &mint.Key{Name: "m"} }
	for p := page(0); p < numPages; p++ {
		if p == pagePlay {
			continue
		}
		a := testApp()
		a.gotoPage(p)
		if a.grab(nil, press()) {
			t.Errorf("%s: susynth took m, so the pages do not open", pageNames[p])
		}
	}

	a := testApp()
	before := a.ed.Text()
	if !a.grab(nil, press()) {
		t.Fatal("in the editor m was left to Mint")
	}
	if a.ed.Text() == before {
		t.Fatal("m was not typed")
	}

	// A question being typed keeps its letters.
	a = testApp()
	a.gotoPage(pageSettings)
	a.startPrompt("save", "save set as: ", "")
	if !a.grab(nil, press()) {
		t.Fatal("m in a question was left to Mint")
	}
	if got := string(a.prompt.buf); got != "m" {
		t.Fatalf("the question reads %q", got)
	}
}

// Quitting with code that is not saved asks first (Mint's CanLeave); moving
// between pages does not, as the code is kept.
func TestQuitAsksWhenUnsaved(t *testing.T) {
	a := testApp()
	if !a.canLeave(nil) {
		t.Error("nothing to lose, yet leaving was refused")
	}
	a.dirty = true
	if !a.canLeave(nil) {
		t.Error("changing page asks, but the editor keeps its code")
	}
	a.quit = true
	a.dirty = false
	if !a.canLeave(nil) {
		t.Error("saved code, yet quitting was refused")
	}
}
