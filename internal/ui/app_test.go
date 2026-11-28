package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mint "github.com/CurtisD4q/pmin-kit"
	"github.com/susynth/susynth/internal/engine"
)

func testApp() *App {
	a := &App{
		eng:   engine.New(),
		ed:    newEditor("use_bpm 120\nlive_loop :bass do\n  play 60\n\n  sleep 1\nend\n"),
		th:    themeByName("default"),
		marks: map[int]bool{},
		page:  pagePlay,
		ps:    pageState{closed: map[string]bool{}},
	}
	a.lineSnap = a.ed.Lines()
	a.eng.Play()
	a.eng.ThreadStarted("drums", true)
	a.eng.ThreadStarted("bass", true)
	return a
}

func frameText(a *App) string {
	return strings.Join(a.drawRows(110), "\n")
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
	// A digit after Esc changes nothing; it says how the track keys work.
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
	got := strings.Join(a.drawText(140, 32), "\n")
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

	// Esc or Alt with a digit is no page shortcut, and inserts nothing.
	for r := '0'; r <= '9'; r++ {
		a.handleKey(key{kind: kEsc})
		a.handleKey(key{kind: kRune, r: r})
		if a.page != pagePlay {
			t.Fatalf("Esc %c changed page to %d", r, a.page)
		}
		a.handleKey(key{kind: kRune, r: r, alt: true})
		if a.page != pagePlay {
			t.Fatalf("Alt+%c changed page to %d", r, a.page)
		}
	}
	if a.ed.Text() != got {
		t.Fatalf("Esc or Alt with a digit typed into the editor: %q", a.ed.Text())
	}
	a.gotoPage(pageSettings)

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
	if a.page != pageMixer {
		t.Fatalf("Alt+b from Play -> page %d, want Mixer", a.page)
	}
	a.handleKey(key{kind: kRune, r: 'f', alt: true})
	if a.page != pagePlay {
		t.Fatalf("Alt+f from Perform -> page %d, want Play", a.page)
	}

	// Older cycle keys still move, and never insert.
	text := a.ed.Text()
	a.handleKey(key{kind: kRune, r: ']', alt: true})
	if a.page != pageSets {
		t.Fatalf("Alt+] -> page %d, want Sets", a.page)
	}
	a.handleKey(key{kind: kRune, r: '[', alt: true})
	if a.page != pagePlay {
		t.Fatalf("Alt+[ -> page %d, want Play", a.page)
	}
	// Function keys do nothing: they are not read at all.
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("f%d", i)
		if k, ok := keyFrom(&mint.Key{Name: name, Special: true}); ok {
			t.Fatalf("%s was read as %+v", name, k)
		}
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

func TestTheTrailAndThePageIcons(t *testing.T) {
	a := testApp()
	rows := a.drawRows(110)
	screen := strings.Join(rows, "\n")
	if strings.Contains(screen, "F6") {
		t.Fatalf("the screen advertises a function key:\n%s", screen)
	}
	// No hint line under the code: each page's keys are on the Help page.
	if strings.Contains(screen, "Shift+arrows select") {
		t.Fatalf("the hint line is still drawn:\n%s", screen)
	}
	// The top line is the trail; the pages are icons down the left, and
	// Esc m opens them as Mint's pop-up from the editor.
	if !strings.Contains(rows[1], "susynth › ▶ Play") {
		t.Errorf("trail %q", rows[1])
	}
	for _, icon := range pageIcons {
		if !strings.Contains(screen, icon) {
			t.Errorf("no %s icon:\n%s", icon, screen)
		}
	}
	if strings.Contains(screen, "Recordings") {
		t.Errorf("the shut pages show their names:\n%s", screen)
	}
	if !strings.Contains(screen, "Esc m pages") {
		t.Errorf("the footer does not say how to open the pages:\n%s", screen)
	}

	a.gotoPage(pageSounds)
	if footer := strings.Join(a.drawRows(110), "\n"); !strings.Contains(footer, " m pages") || strings.Contains(footer, "Esc m pages") {
		t.Errorf("off the editor the footer does not say m opens the pages:\n%s", footer)
	}
	if title := a.drawRows(110)[1]; !strings.Contains(title, "susynth › ♪ Sounds") {
		t.Fatalf("sounds trail %q", title)
	}

	a.gotoPage(pageHelp)
	help := strings.Join(a.drawRows(110), "\n")
	for _, want := range []string{"the pages pop-up", "Esc f  /  Esc b"} {
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
	for _, want := range []string{"press Esc m there", "the Sounds page", "the Settings page", "Esc f  /  Esc b",
		"a add to the set list", "Enter show the path", "i insert the example", "[ ] reorder", "←→ change"} {
		if !strings.Contains(body, want) {
			t.Errorf("help text missing %q", want)
		}
	}
	if regexp.MustCompile(`(Esc|Alt\+) ?[0-9]`).MatchString(body) {
		t.Fatalf("help text still documents Esc and a number:\n%s", body)
	}
	if regexp.MustCompile(`\bF[0-9]`).MatchString(body) {
		t.Fatalf("help text still documents function keys:\n%s", body)
	}
}

// drawRows is the page as Mint shows it, w columns by 32 rows, as text.
func (a *App) drawRows(w int) []string { return a.drawText(w, 32) }

func (a *App) drawText(w, h int) []string {
	return gridText(a.app().FrameText(int(a.page)+1, h, w), h, w)
}

// gridText lays a frame out on an h × w grid of plain text.
func gridText(frame string, h, w int) []string {
	grid := make([][]rune, h)
	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", w))
	}
	row, col := 0, 0
	for i := 0; i < len(frame); {
		if frame[i] == 0x1b && i+1 < len(frame) && frame[i+1] == '[' {
			j := i + 2
			for j < len(frame) && (frame[j] < 0x40 || frame[j] > 0x7e) {
				j++
			}
			if j < len(frame) && frame[j] == 'H' {
				r, c, _ := strings.Cut(frame[i+2:j], ";")
				row, col = atoi(r)-1, atoi(c)-1
			}
			i = j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(frame[i:])
		i += n
		if r < ' ' {
			continue
		}
		if row >= 0 && row < h && col >= 0 && col < w {
			grid[row][col] = r
		}
		col++
	}
	out := make([]string, h)
	for i, g := range grid {
		out[i] = string(g)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 1
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 1
	}
	return n
}

func TestRunBlockIsHighlighted(t *testing.T) {
	a := testApp()
	a.ed.row = 3 // blank line inside :bass
	got := frameText(a)
	if strings.Count(got, "┊") < 3 {
		t.Fatal("the live_loop ^E would run is not highlighted")
	}
}

// TestMixerPage: the Mixer page shows the master mixer and changes it.
func TestMixerPage(t *testing.T) {
	a := testApp()
	a.changeMixer(mixVolume, -1, false)
	if m := a.eng.Mixer(); m.Amp < 0.94 || m.Amp > 0.96 {
		t.Fatalf("volume after one step down: %v", m.Amp)
	}
	if v := a.mixerValue(mixVolume); v != "95%" {
		t.Fatalf("volume shows %q", v)
	}
	a.changeMixer(mixMono, 1, false)
	a.changeMixer(mixLimiter, 1, false)
	m := a.eng.Mixer()
	if !m.ForceMono || !m.LimiterBypass {
		t.Fatalf("toggles: %+v", m)
	}
	a.changeMixer(mixHPF, 1, false)
	if v := a.mixerValue(mixHPF); v != "note 30" {
		t.Fatalf("high-pass %q", v)
	}
	a.changeMixer(mixHPF, -1, false)
	if v := a.mixerValue(mixHPF); v != "off" {
		t.Fatalf("high-pass back %q", v)
	}
	a.changeMixer(mixReset, 1, true)
	if a.eng.Mixer() != engine.DefaultMixer {
		t.Fatalf("reset: %+v", a.eng.Mixer())
	}
}
