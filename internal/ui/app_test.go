package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

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
	// 8 and 9 are not pages. 1–7 switch pages and are covered separately.
	a.handleKey(key{kind: kRune, r: '9'})
	for _, tr := range a.eng.Snapshot().Tracks {
		if tr.Mute {
			t.Fatalf("digit after Esc muted track %d", tr.ID)
		}
	}
	if a.ed.Text() != before {
		t.Fatalf("the digit was typed into the buffer: %q", a.ed.Text())
	}
	if a.page != pagePlay {
		t.Fatalf("digit 9 changed page to %d", a.page)
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

func TestPageKeysSkipTheEditor(t *testing.T) {
	a := testApp()
	before := a.ed.Text()

	// A bare digit, letter or Tab is code, never a page change.
	a.handleKey(key{kind: kRune, r: '4'})
	a.handleKey(key{kind: kRune, r: 'f'})
	a.handleKey(key{kind: kRune, r: 'b'})
	a.handleKey(key{kind: kTab})
	if a.page != pagePlay {
		t.Fatalf("typing changed page to %d", a.page)
	}
	if a.ed.Text() == before {
		t.Fatal("typed keys did not reach the editor")
	}
	got := a.ed.Text()
	if !strings.HasPrefix(got, "4fb  ") {
		t.Fatalf("editor received %q", got)
	}

	// Esc then a tab number jumps there and does not insert the digit.
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: '4'})
	if a.page != pageSounds {
		t.Fatalf("Esc 4 -> page %d, want Sounds", a.page)
	}
	if a.ed.Text() != got {
		t.Fatalf("Esc 4 inserted into the editor: %q", a.ed.Text())
	}

	// Alt+digit is the same jump, in one chord.
	a.handleKey(key{kind: kRune, r: '5', alt: true})
	if a.page != pageSettings {
		t.Fatalf("Alt+5 -> page %d, want Settings", a.page)
	}

	// Esc f / Esc b cycle from the page Esc was pressed on, wrapping.
	a.handleKey(key{kind: kEsc})
	if a.page != pagePlay {
		t.Fatal("Esc did not return to Play")
	}
	a.handleKey(key{kind: kRune, r: 'f'})
	if a.page != pageHelp {
		t.Fatalf("Esc f from Settings -> page %d, want Help", a.page)
	}
	a.handleKey(key{kind: kRune, r: 'b', alt: true})
	if a.page != pageSettings {
		t.Fatalf("Alt+b -> page %d, want Settings", a.page)
	}
	a.page = pagePlay
	a.handleKey(key{kind: kRune, r: 'b', alt: true})
	if a.page != pagePerform {
		t.Fatalf("Alt+b from Play -> page %d, want Perform", a.page)
	}
	a.handleKey(key{kind: kRune, r: 'f', alt: true})
	if a.page != pagePlay {
		t.Fatalf("Alt+f from Perform -> page %d, want Play", a.page)
	}

	// Older cycle keys and F-keys still move, and never insert.
	text := a.ed.Text()
	a.handleKey(key{kind: kRune, r: ']', alt: true})
	if a.page != pageSets {
		t.Fatalf("Alt+] -> page %d, want Sets", a.page)
	}
	a.handleKey(key{kind: kRune, r: '[', alt: true})
	if a.page != pagePlay {
		t.Fatalf("Alt+[ -> page %d, want Play", a.page)
	}
	a.handleKey(key{kind: kFn, r: 7})
	if a.page != pagePerform {
		t.Fatalf("F7 -> page %d, want Perform", a.page)
	}
	a.handleKey(key{kind: kFn, r: 8})
	if a.page != pagePerform {
		t.Fatalf("F8 changed page to %d", a.page)
	}
	if a.ed.Text() != text {
		t.Fatal("a page key changed the editor")
	}
}

func TestPageKeysLeaveTransportAndTrackShortcuts(t *testing.T) {
	a := testApp()
	a.errs = []codeErr{{Line: 1, Text: "nope"}}
	a.handleKey(key{kind: kCtrl, r: 'l'})
	if a.page != pagePlay || a.errs != nil {
		t.Fatalf("page %d errs %v", a.page, a.errs)
	}

	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: 'l'})
	if a.page != pagePlay || a.side != sideLog {
		t.Fatalf("Esc l page %d side %d", a.page, a.side)
	}
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: 'k'})
	if a.awaiting != "stop" || a.page != pagePlay {
		t.Fatalf("awaiting %q page %d", a.awaiting, a.page)
	}
	// The digit completes "stop", it does not open Sounds.
	a.handleKey(key{kind: kRune, r: '4'})
	if a.page != pagePlay || strings.Contains(a.ed.Text(), "4") {
		t.Fatalf("page %d text %q", a.page, a.ed.Text())
	}
	// Esc while a number is pending cancels that command. It does not arm
	// the next key, so solo starts with a fresh Esc.
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: 's'})
	if a.awaiting != "solo" || a.page != pagePlay {
		t.Fatalf("awaiting %q page %d", a.awaiting, a.page)
	}

	a = testApp()
	a.handleKey(key{kind: kEsc})
	a.handleKey(key{kind: kRune, r: 'u'})
	a.handleKey(key{kind: kRune, r: '1'})
	muted := false
	for _, tr := range a.eng.Snapshot().Tracks {
		if tr.ID == 1 && tr.Mute {
			muted = true
		}
	}
	if !muted || a.page != pagePlay {
		t.Fatal("Esc u 1 did not mute, or it changed page")
	}
	a.handleKey(key{kind: kCtrl, r: 'q'})
	if !a.quit || a.page != pagePlay {
		t.Fatalf("quit %v page %d", a.quit, a.page)
	}

	// A prompt owns the keyboard; a page chord must not switch away under it.
	a = testApp()
	a.startPrompt("save", "save set as: ", "kit")
	a.handleKey(key{kind: kRune, r: '4', alt: true})
	if a.prompt == nil || a.page != pagePlay || string(a.prompt.buf) != "kit" {
		t.Fatalf("prompt %v page %d", a.prompt, a.page)
	}
}

func TestTabsShowEscNumbers(t *testing.T) {
	a := testApp()
	wide, _, _ := a.draw(110, 32)
	title := plain(wide.rows()[1])
	screen := plain(strings.Join(wide.rows(), "\n"))
	if strings.Contains(screen, "F6") {
		t.Fatalf("the screen advertises a function key:\n%s", screen)
	}
	// No hint line under the code: each page's keys are on the Help page.
	if strings.Contains(screen, "Shift+arrows select") {
		t.Fatalf("the hint line is still drawn:\n%s", screen)
	}
	for _, want := range []string{"Esc+ [1 Play]", "2 Sets", "3 Recordings", "4 Sounds", "5 Settings", "6 Help", "7 Perform"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", title, want)
		}
	}
	if strings.Contains(title, "F1") || strings.Contains(title, "F4") {
		t.Fatalf("title still advertises function keys: %q", title)
	}

	a.gotoPage(pageSounds)
	title = plain(a.drawRow(110, 1))
	if !strings.Contains(title, "[4 Sounds]") || !strings.Contains(title, "Esc+") {
		t.Fatalf("sounds title %q", title)
	}

	narrow := plain(a.drawRow(80, 1))
	if !strings.Contains(narrow, "Esc 4 Sounds") || !strings.Contains(narrow, "f next") {
		t.Fatalf("narrow title %q", narrow)
	}
	if strings.Contains(narrow, "F4") || strings.Contains(narrow, "2 Sets") {
		t.Fatalf("narrow title %q", narrow)
	}

	a.gotoPage(pageHelp)
	help := plain(strings.Join(a.drawRows(110), "\n"))
	for _, want := range []string{"Esc 1 … Esc 7", "Esc f  /  Esc b"} {
		if !strings.Contains(help, want) {
			t.Errorf("help page missing %q", want)
		}
	}
	if strings.Contains(help, "F1 … F7") {
		t.Fatal("help still leads with function keys")
	}
	var all strings.Builder
	for _, l := range helpLines {
		all.WriteString(l.key)
		all.WriteByte('\n')
		all.WriteString(l.text)
		all.WriteByte('\n')
	}
	body := all.String()
	for _, want := range []string{"Esc 4 Sounds", "Esc 5 Settings", "Esc 1 … Esc 7", "Esc f  /  Esc b",
		"a add to the set list", "Enter show the path", "i insert the example", "[ ] reorder", "←→ change"} {
		if !strings.Contains(body, want) {
			t.Errorf("help text missing %q", want)
		}
	}
	if strings.Contains(body, "F1 … F7") || strings.Contains(body, "F4") || strings.Contains(body, "F5") || strings.Contains(body, "F2") {
		t.Fatalf("help text still documents function keys:\n%s", body)
	}
}

func (a *App) drawRow(w, y int) string {
	f, _, _ := a.draw(w, 32)
	return f.rows()[y]
}

func (a *App) drawRows(w int) []string {
	f, _, _ := a.draw(w, 32)
	return f.rows()
}

func TestRunBlockIsHighlighted(t *testing.T) {
	a := testApp()
	a.ed.row = 3 // blank line inside :bass
	got := frameText(a)
	if strings.Count(got, "┊") < 3 {
		t.Fatal("the live_loop ^E would run is not highlighted")
	}
}
