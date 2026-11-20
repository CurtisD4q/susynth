package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
)

func testApp() *App {
	a := &App{
		eng:   engine.New(),
		ed:    newEditor("use_bpm 120\nlive_loop :bass do\n  play 60\n\n  sleep 1\nend\n"),
		th:    themeByName("default"),
		marks: map[int]bool{},
		page:  pagePlay,
	}
	a.lineSnap = a.ed.Lines()
	a.eng.Play()
	a.eng.ThreadStarted("drums", true)
	a.eng.ThreadStarted("bass", true)
	return a
}

func frameText(a *App) string {
	f, _, _ := a.draw(110, 32)
	return strings.Join(f.rows(), "\n")
}

func TestEscDoesNotClearOrMute(t *testing.T) {
	a := testApp()
	a.say("line 3: nope", true)
	before := a.ed.Text()
	a.handleKey(key{kind: kEsc})
	if a.status != "line 3: nope" || !a.statusErr {
		t.Fatalf("Esc cleared the error: %q", a.status)
	}
	if !strings.Contains(frameText(a), "Esc…") {
		t.Fatal("transport has no pending-Esc indicator")
	}
	a.handleKey(key{kind: kRune, r: '1'})
	for _, tr := range a.eng.Snapshot().Tracks {
		if tr.Mute {
			t.Fatalf("digit after Esc muted track %d", tr.ID)
		}
	}
	if a.ed.Text() != before {
		t.Fatalf("the digit was typed into the buffer: %q", a.ed.Text())
	}
	if !strings.Contains(a.status, "Esc u") {
		t.Fatalf("status %q", a.status)
	}
	// The log kept the original error.
	found := false
	for _, e := range a.log {
		if e.Kind == "err" && strings.Contains(e.Text, "nope") {
			found = true
		}
	}
	if !found {
		t.Fatal("error left the log")
	}
}

func TestMuteAndStopNeedAShownPrefix(t *testing.T) {
	a := testApp()
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: 'u'})
	if a.awaiting != "mute" || a.pendingLabel() != "mute…" {
		t.Fatalf("awaiting %q label %q", a.awaiting, a.pendingLabel())
	}
	a.handleKey(key{kind: kRune, r: '1'})
	muted := false
	for _, tr := range a.eng.Snapshot().Tracks {
		if tr.ID == 1 && tr.Mute && tr.Name == "drums" {
			muted = true
		}
	}
	if !muted {
		t.Fatal("Esc u then 1 did not mute drums")
	}
}

func TestDeletedLoopIsMarkedNotInCode(t *testing.T) {
	a := testApp()
	got := frameText(a)
	if !strings.Contains(got, "not in code") {
		t.Fatal("drums was removed from the text but is not marked")
	}
	// :bass is still in the buffer, so only one track is marked.
	if strings.Count(got, "not in code") != 1 {
		t.Fatalf("marked %d tracks", strings.Count(got, "not in code"))
	}
}

func TestLogPaneKeepsErrorsPutsAndCues(t *testing.T) {
	a := testApp()
	at := time.Date(2026, 1, 2, 15, 4, 5, 0, time.Local)
	a.log = []logLine{
		{At: at, Kind: "err", Text: "line 4: did not sleep"},
		{At: at.Add(time.Second), Kind: "puts", Text: "hello"},
		{At: at.Add(2 * time.Second), Kind: "cue", Text: "cue :drop"},
	}
	a.side = sideLog
	got := frameText(a)
	for _, want := range []string{"15:04:05", "did not sleep", "hello", "cue :drop"} {
		if !strings.Contains(got, want) {
			t.Errorf("log pane missing %q", want)
		}
	}
	a.scrollLog(key{kind: kUp}, 8)
	if a.logOff != 1 {
		t.Fatalf("scroll off %d", a.logOff)
	}
}

func TestAltArrowsScrollOnlyTheLog(t *testing.T) {
	a := testApp()
	a.ed.row = 2
	a.handleKey(key{kind: kUp, alt: true})
	if a.ed.row != 1 {
		t.Fatalf("Alt+Up should move the cursor when the log is closed, row %d", a.ed.row)
	}
	a.log = []logLine{{At: time.Now(), Kind: "puts", Text: "one"}, {At: time.Now(), Kind: "puts", Text: "two"}}
	a.side = sideLog
	a.ed.row = 2
	a.handleKey(key{kind: kUp, alt: true})
	if a.logOff != 1 || a.ed.row != 2 {
		t.Fatalf("log off %d row %d", a.logOff, a.ed.row)
	}
}

func TestRunBlockIsHighlighted(t *testing.T) {
	a := testApp()
	a.ed.row = 3 // blank line inside :bass
	got := frameText(a)
	if strings.Count(got, "┊") < 3 {
		t.Fatal("the live_loop ^E would run is not highlighted")
	}
}
