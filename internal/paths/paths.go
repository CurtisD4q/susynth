// Package paths finds susynth's config and data folders and expands
// "~" and "$VAR" in user-supplied paths.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const app = "susynth"

// Expand replaces a leading ~ and any $VAR / ${VAR} in p.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return os.ExpandEnv(p)
}

// ConfigDir is where config.json lives (created on demand).
func ConfigDir() string {
	if d := os.Getenv("SUSYNTH_CONFIG"); d != "" {
		return Expand(d)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = Expand("~/.config")
	}
	return filepath.Join(base, app)
}

// DataDir is where sets and recordings live.
func DataDir() string {
	if d := os.Getenv("SUSYNTH_DATA"); d != "" {
		return Expand(d)
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, app)
	}
	return Expand("~/.local/share/" + app)
}

// SetsDir holds saved .syn files.
func SetsDir() string { return filepath.Join(DataDir(), "sets") }

// RecordingsDir holds recorded WAV files.
func RecordingsDir() string { return filepath.Join(DataDir(), "recordings") }

// Ensure creates dir (and parents) and returns it.
func Ensure(dir string) (string, error) {
	return dir, os.MkdirAll(dir, 0o755)
}

// SamplesDir holds user WAV samples (one sub-folder per bank).
func SamplesDir() string { return filepath.Join(DataDir(), "samples") }

// SetlistFile is the performance set list, one set name per line.
func SetlistFile() string { return filepath.Join(ConfigDir(), "setlist.txt") }
