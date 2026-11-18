// susynth is a terminal instrument for live-coding music.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/susynth/susynth/internal/audio"
	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/lang"
	"github.com/susynth/susynth/internal/midi"
	"github.com/susynth/susynth/internal/paths"
	"github.com/susynth/susynth/internal/synth"
	"github.com/susynth/susynth/internal/ui"
)

const usage = `susynth — live-code music in your terminal

usage:
  susynth [flags] [set.syn]        open the live editor
  susynth render [flags] set.syn   render a set to a WAV file, no sound device needed
  susynth check set.syn            parse a set and report errors
  susynth midi [flags] set.syn     export a set as a Standard MIDI File
  susynth samples                  list the sample banks found in the samples folder
  susynth devices                  list detected audio outputs and MIDI devices

flags (editor):
  -sink auto|native|pipewire|pulseaudio|alsa|none
  -theme default|large|contrast

flags (render, midi):
  -o FILE      output file (default: set name + .wav / .mid)
  -cycles N    cycles to render or export (default 8)
  -bpm N       override tempo
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

func parseFile(path string) (lang.Program, bool) {
	loadSamples()
	b, err := os.ReadFile(paths.Expand(path))
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return lang.Program{}, false
	}
	p := lang.Parse(string(b), 0)
	for _, e := range p.Errors {
		fmt.Fprintf(os.Stderr, "%s: %s\n", path, e)
	}
	return p, len(p.Errors) == 0
}

func check(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	p, ok := parseFile(args[0])
	if !ok {
		return 1
	}
	fmt.Printf("ok: %d track(s), %d command(s)\n", len(p.Tracks), len(p.Commands))
	return 0
}

func exportMIDI(args []string) int {
	fs := flag.NewFlagSet("midi", flag.ExitOnError)
	out := fs.String("o", "", "output .mid")
	cycles := fs.Int("cycles", 8, "cycles to export")
	bpm := fs.Float64("bpm", 0, "override tempo")
	fs.Parse(args)
	if fs.NArg() != 1 || *cycles < 1 || *cycles > 256 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	in := fs.Arg(0)
	p, ok := parseFile(in)
	if !ok {
		return 1
	}
	tempo := midi.BPMOf(p, 120)
	if *bpm > 0 {
		tempo = *bpm
	}
	notes, names, skipped := midi.FromProgram(p, *cycles)
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
	fmt.Printf("wrote %s (%d notes, %d cycles, %.0f bpm)", *out, len(notes), *cycles, tempo)
	if skipped > 0 {
		fmt.Printf(", %d sample hit(s) skipped", skipped)
	}
	fmt.Println()
	return 0
}

func render(args []string) int {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	out := fs.String("o", "", "output wav")
	cycles := fs.Float64("cycles", 8, "cycles to render")
	bpm := fs.Float64("bpm", 0, "override tempo")
	fs.Parse(args)
	if fs.NArg() != 1 || *cycles <= 0 || *cycles > 4096 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	in := fs.Arg(0)
	p, ok := parseFile(in)
	if !ok {
		return 1
	}
	if *out == "" {
		base := in[:len(in)-len(filepath.Ext(in))]
		*out = base + ".wav"
	}
	e := engine.New()
	e.Apply(p, true)
	if *bpm > 0 {
		e.SetBPM(*bpm)
	}
	w, err := engine.NewWavWriter(paths.Expand(*out), engine.SampleRate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	if err := e.RenderOffline(*cycles, w.Write); err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	if err := w.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "susynth:", err)
		return 1
	}
	fmt.Printf("wrote %s (%.1fs, %.0f bpm)\n", *out, float64(w.Frames())/engine.SampleRate, e.BPM())
	return 0
}
