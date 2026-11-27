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
		a.quitAsk = true
		if a.grab(nil, press()) {
			t.Errorf("%s: susynth took m, so the pages do not open", pageNames[p])
		}
		if a.quitAsk {
			t.Errorf("%s: opening the pages left the quit question armed", pageNames[p])
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
