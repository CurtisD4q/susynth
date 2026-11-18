// susynth is a terminal instrument for live-coding music.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/susynth/susynth/internal/audio"
	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/live"
	"github.com/susynth/susynth/internal/midi"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/rubyvm"
	"github.com/susynth/susynth/internal/synth"
	"github.com/susynth/susynth/internal/ui"
)

const usage = `susynth — live-code music in your terminal, in Sonic Pi's language

usage:
  susynth [flags] [set.rb]         open the live editor
  susynth render [flags] set.rb    render a set to a WAV file, no sound device needed
  susynth check set.rb             report syntax and runtime errors
  susynth midi [flags] set.rb      export a set as a Standard MIDI File
  susynth samples                  list the sample banks found in the samples folder
  susynth devices                  list detected audio outputs and MIDI devices

flags (editor):
  -sink auto|native|pipewire|pulseaudio|alsa|none
  -theme default|large|contrast

flags (render, midi):
  -o FILE      output file (default: set name + .wav / .mid)
  -seconds N   how long to render or export (default 30)

Code runs in real Ruby (3.4) inside a sandbox. The first run compiles Ruby,
which takes a while; later runs start in about a second.
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "render":
			os.Exit(render(args[1:]))
		case "check":
			os.Exit(check(args[1:]))
		case "midi":
			os.Exit(exportMIDI(args[1:]))
		case "samples":
			loadSamples()
			banks := synth.Banks()
			fmt.Printf("samples folder: %s\n", paths.SamplesDir())
			if len(banks) == 0 {
				fmt.Println("no banks found (make a sub-folder per bank and put .wav files in it)")
			}
			for _, b := range banks {
				fmt.Printf("  %-16s %d file(s), %.1fs\n", b.Name, len(b.Samples), b.Seconds())
			}
			return
		case "devices":
			fmt.Println("audio outputs:", audio.Available())
			fmt.Println("midi devices: ", midi.Devices())
			return
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		}
	}
	fs := flag.NewFlagSet("susynth", flag.ExitOnError)
	sink := fs.String("sink", "", "audio output")
	theme := fs.String("theme", "", "colour theme")
	fs.Usage = func() { fmt.Print(usage) }
	fs.Parse(args)
	if err := ui.Run(ui.Options{File: fs.Arg(0), Sink: *sink, Theme: *theme}); err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		os.Exit(1)
	}
}

// loadSamples makes user sample names valid before any set is parsed.
func loadSamples() {
	if _, _, err := synth.LoadSamples(paths.SamplesDir()); err != nil {
		fmt.Fprintln(os.Stderr, "samples:", err)
	}
}

// startRuby boots the sandbox and waits for it, telling the person the
// first start takes a while.
func startRuby() (*rubyvm.VM, bool) {
	vm := rubyvm.Start(paths.CacheDir())
	vm.SetTables(synth.SynthNames(), synth.SampleNames())
	select {
	case <-vm.Ready():
	case <-time.After(2 * time.Second):
		fmt.Fprintln(os.Stderr, "starting Ruby (the first run compiles it, which takes a while)…")
		<-vm.Ready()
	}
	if err := vm.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return nil, false
	}
	return vm, true
}

func readSet(path string) (string, bool) {
	loadSamples()
	b, err := os.ReadFile(paths.Expand(path))
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return "", false
	}
	return string(b), true
}

func printNotes(path string, ns []live.Note) (errs int) {
	for _, n := range ns {
		switch n.Kind {
		case "err":
			errs++
			if n.Line > 0 {
				fmt.Fprintf(os.Stderr, "%s:%d: %s\n", path, n.Line, n.Text)
			} else {
				fmt.Fprintf(os.Stderr, "%s: %s\n", path, n.Text)
			}
		case "log":
			fmt.Fprintln(os.Stderr, n.Text)
		}
	}
	return
}

func check(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	code, ok := readSet(args[0])
	if !ok {
		return 1
	}
	vm, ok := startRuby()
	if !ok {
		return 1
	}
	if printNotes(args[0], live.New(vm, engine.New()).Check(code)) > 0 {
		return 1
	}
	// run it silently for a few bars to catch mistakes that only show up running
	hits, _, notes, err := live.Collect(vm, code, 8)
	if printNotes(args[0], notes) > 0 || err != nil {
		if err != nil {
			fmt.Fprintln(os.Stderr, "susynth:", err)
		}
		return 1
	}
	fmt.Printf("ok: %d note(s) in the first 8 seconds\n", len(hits))
	return 0
}

func exportMIDI(args []string) int {
	fs := flag.NewFlagSet("midi", flag.ExitOnError)
	out := fs.String("o", "", "output .mid")
	secs := fs.Float64("seconds", 30, "seconds to export")
	fs.Parse(args)
	if fs.NArg() != 1 || *secs <= 0 || *secs > 3600 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	in := fs.Arg(0)
	code, ok := readSet(in)
	if !ok {
		return 1
	}
	vm, ok := startRuby()
	if !ok {
		return 1
	}
	hits, tempo, ns, err := live.Collect(vm, code, *secs)
	if printNotes(in, ns) > 0 || err != nil {
		if err != nil {
			fmt.Fprintln(os.Stderr, "susynth:", err)
		}
		return 1
	}
	notes, names, skipped := midi.FromHits(hits, tempo)
	if len(notes) == 0 {
		fmt.Fprintln(os.Stderr, "susynth: nothing to export (no drum or note sounds)")
		return 1
	}
	if *out == "" {
		*out = in[:len(in)-len(filepath.Ext(in))] + ".mid"
	}
	f, err := os.Create(paths.Expand(*out))
	if err == nil {
		err = midi.WriteSMF(f, tempo, names, notes)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	fmt.Printf("wrote %s (%d notes, %.0fs, %.0f bpm)", *out, len(notes), *secs, tempo)
	if skipped > 0 {
		fmt.Printf(", %d sample hit(s) skipped", skipped)
	}
	fmt.Println()
	return 0
}

func render(args []string) int {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	out := fs.String("o", "", "output wav")
	secs := fs.Float64("seconds", 30, "seconds to render")
	fs.Parse(args)
	if fs.NArg() != 1 || *secs <= 0 || *secs > 3600 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	in := fs.Arg(0)
	code, ok := readSet(in)
	if !ok {
		return 1
	}
	vm, ok := startRuby()
	if !ok {
		return 1
	}
	if *out == "" {
		*out = in[:len(in)-len(filepath.Ext(in))] + ".wav"
	}
	e := engine.New()
	w, err := engine.NewWavWriter(paths.Expand(*out), engine.SampleRate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	ns, err := live.Render(vm, e, code, *secs, w.Write)
	errs := printNotes(in, ns)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	fmt.Printf("wrote %s (%.1fs, %.0f bpm)\n", *out, float64(w.Frames())/engine.SampleRate, e.BPM())
	if errs > 0 {
		return 1
	}
	return 0
}
