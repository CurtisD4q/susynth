package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/susynth/susynth/internal/engine"
)

// Layout snapshots: every page drawn as plain text at three sizes, kept in
// testdata/layout. A change to the layout shows as a diff of these files.
// After a change that is meant, rewrite them with:
//
//	go test ./internal/ui -run TestLayout -update
var update = flag.Bool("update", false, "rewrite the layout snapshots")

var layoutSizes = [][2]int{{80, 24}, {110, 32}, {160, 45}}

func layoutApp(t *testing.T, theme string, p page) *App {
	t.Helper()
	// Empty folders, so the snapshots do not depend on this machine. The
	// folder's name is shown on some pages, so it is fixed rather than a
	// random temporary one.
	dir := "/tmp/susynth-layout"
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("SUSYNTH_DATA", filepath.Join(dir, "data"))
	t.Setenv("SUSYNTH_CONFIG", filepath.Join(dir, "config"))
	t.Setenv("SUSYNTH_CACHE", filepath.Join(dir, "cache"))
	// Times are shown in local time: draw them in UTC so the snapshots
	// are the same in every time zone.
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	seedLayoutData(t, filepath.Join(dir, "data"))
	a := testApp()
	a.th = themeByName(theme)
	a.setName, a.dirty = "night-drive", true
	a.setlist, a.live = []string{"night-drive", "opener", "gone-missing"}, 0
	if p != pagePlay {
		a.gotoPage(p)
	}
	return a
}

// seedLayoutData writes three sets and two recordings, with fixed times.
func seedLayoutData(t *testing.T, data string) {
	t.Helper()
	at := time.Date(2026, 11, 20, 21, 30, 0, 0, time.UTC)
	write := func(path string, body []byte, size int64, age time.Duration) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if err := os.Truncate(path, size); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(path, at.Add(-age), at.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	loop := "live_loop :drums do\n  sample :bd_haus\n  sleep 1\nend\n"
	write(filepath.Join(data, "sets", "night-drive.syn"), []byte(loop+loop+loop), 0, 0)
	write(filepath.Join(data, "sets", "opener.syn"), []byte(loop), 0, time.Hour)
	write(filepath.Join(data, "sets", "a-very-long-set-name-that-will-not-fit-the-column.syn"), []byte(loop+loop), 0, 48*time.Hour)
	// 44-byte header, then 4 bytes a frame: 3 minutes 25 seconds.
	write(filepath.Join(data, "recordings", "set-20261120-2100.wav"), nil, 44+205*engine.SampleRate*4, 30*time.Minute)
	write(filepath.Join(data, "recordings", "set-20261119-2200.mid"), []byte("MThd"), 0, 24*time.Hour)
}

func checkLayout(t *testing.T, name string, a *App, w, h int) {
	t.Helper()
	f, _, _ := a.draw(w, h)
	got := plain(strings.Join(f.rows(), "\n"))
	lines := strings.Split(got, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	got = strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "layout", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no snapshot %s: run go test ./internal/ui -run TestLayout -update", path)
	}
	if string(want) != got {
		t.Errorf("%s changed. If that is meant, run go test ./internal/ui -run TestLayout -update\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestLayout(t *testing.T) {
	for p := page(0); p < numPages; p++ {
		for _, sz := range layoutSizes {
			name := strings.ToLower(pageNames[p]) + "-" + itoa(sz[0]) + "x" + itoa(sz[1])
			t.Run(name, func(t *testing.T) {
				checkLayout(t, name, layoutApp(t, "default", p), sz[0], sz[1])
			})
		}
	}
	// The side pane's other views, and an error under the code.
	t.Run("play-log-110x32", func(t *testing.T) {
		a := layoutApp(t, "default", pagePlay)
		a.side = sideLog
		a.log = append(a.log, logLine{Kind: "puts", Text: "hello from the drums loop"}, logLine{Kind: "err", Text: "undefined method 'sampel' for main", Line: 3})
		checkLayout(t, "play-log-110x32", a, 110, 32)
	})
	t.Run("play-roll-110x32", func(t *testing.T) {
		a := layoutApp(t, "default", pagePlay)
		a.side = sideRoll
		checkLayout(t, "play-roll-110x32", a, 110, 32)
	})
	// Questions: pop-ups off the Play page, the line under the code on it.
	t.Run("popup-settings-osc-80x24", func(t *testing.T) {
		a := layoutApp(t, "default", pageSettings)
		a.startPrompt("oscaddr", "OSC host:port: ", "127.0.0.1:4560")
		checkLayout(t, "popup-settings-osc-80x24", a, 80, 24)
	})
	t.Run("popup-sets-delete-110x32", func(t *testing.T) {
		a := layoutApp(t, "default", pageSets)
		a.ps.armed = "opener"
		checkLayout(t, "popup-sets-delete-110x32", a, 110, 32)
	})
	t.Run("popup-perform-discard-110x32", func(t *testing.T) {
		a := layoutApp(t, "default", pagePerform)
		a.discardUntil = time.Now().Add(time.Hour)
		checkLayout(t, "popup-perform-discard-110x32", a, 110, 32)
	})
	t.Run("prompt-play-save-110x32", func(t *testing.T) {
		a := layoutApp(t, "default", pagePlay)
		a.startPrompt("save", "save set as: ", "night-drive")
		checkLayout(t, "prompt-play-save-110x32", a, 110, 32)
	})
	// The projector themes widen the step display, so the Play page moves.
	for _, theme := range []string{"large", "contrast"} {
		name := "play-" + theme + "-110x32"
		t.Run(name, func(t *testing.T) {
			checkLayout(t, name, layoutApp(t, theme, pagePlay), 110, 32)
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
