package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	a := testApp()
	a.th = themeByName(theme)
	if p != pagePlay {
		a.gotoPage(p)
	}
	return a
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
