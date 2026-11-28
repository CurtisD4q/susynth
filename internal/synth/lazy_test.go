package synth

import (
	"os"
	"path/filepath"
	"testing"
)

// The samples folder is listed from file headers and each file is read the
// first time it plays; when too much is loaded the oldest is let go.
func TestSamplesLoadLazily(t *testing.T) {
	dir := t.TempDir()
	kit := filepath.Join(dir, "kit")
	os.Mkdir(kit, 0o755)
	for _, n := range []string{"a", "b", "c"} {
		monoWAV(t, kit, n)
	}
	flacData, err := builtinFS.ReadFile("sonicpi_samples/bd_haus.flac")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(kit, "d.flac"), flacData, 0o644)
	banks, files, err := LoadSamples(dir)
	if err != nil || banks != 1 || files != 4 {
		t.Fatalf("%d banks %d files %v", banks, files, err)
	}
	b := Banks()[0]
	for _, s := range b.Samples {
		if s.Data != nil {
			t.Errorf("%s was read before it played", s.Name)
		}
	}
	if sec := b.Samples[0].Seconds(); sec != 0.25 { // 12000 frames at 48 kHz
		t.Errorf("length from the header: %v", sec)
	}
	if w, err := ResolveSample("kit", 3); err != nil || w != "kit:3" || b.Samples[3].Data == nil {
		t.Fatalf("flac in a bank: %q %v", w, err)
	}

	old := maxLoaded
	defer func() { maxLoaded = old }()
	maxLoaded = b.Samples[0].prepared() + 10 // room for one WAV
	for i := 0; i < 3; i++ {
		if _, err := ResolveSample("kit", i); err != nil {
			t.Fatal(err)
		}
	}
	held := 0
	for _, s := range b.Samples[:3] {
		if s.Data != nil {
			held++
		}
	}
	if held != 1 || b.Samples[2].Data == nil {
		t.Errorf("%d samples held; the newest should be the one kept", held)
	}
	if v, err := New("kit:0", 48000, 0, 0, Params{Gain: 1, Speed: 1}, 1); err != nil || v.k != kSample {
		t.Errorf("a let-go sample didn't come back: %v", err)
	}
}
