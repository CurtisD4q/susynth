# susynth

A terminal instrument for live-coding music in **Sonic Pi's language**. You write
`live_loop`s in an editor pane, press `^E`, and hear the change on the loop's next
pass. One standalone Go binary: no cgo, no Ruby to install, no external player programs.

```ruby
use_bpm 120

live_loop :drums do
  sample :bd_haus
  sleep 0.5
  sample :drum_snare_hard, amp: 0.8
  sleep 0.5
end

live_loop :bass do
  sync :drums
  use_synth :tb303
  play (ring :e2, :e2, :g2, :a2).tick, release: 0.2, cutoff: rrand(70, 110)
  sleep 0.25
end
```

## How it works

```
  terminal UI (Go)  ──code──▶  live (Go middleman)  ──"run" / "advance to t"──▶  Ruby sandbox
        ▲                         │        ▲                                      (CRuby 3.4 in
        │ tracks, errors, puts    │        └──── timed notes, errors, puts ─────  WebAssembly)
        │                         ▼
        └──────────────── audio engine (Go) ──▶ sound card / WAV / MIDI / OSC
```

* **Real Ruby.** Your code runs in CRuby 3.4, compiled to WebAssembly and run by
  [wazero](https://github.com/tetratelabs/wazero), a pure-Go runtime. Everything Ruby can
  do works: methods, `define`, blocks, `each`/`map`, hashes, string interpolation, classes.
* **Sandboxed.** The Ruby side has no network and no file system. It can only make sound.
* **Sonic Pi's runtime, rewritten.** `internal/rubyvm/runtime.rb` implements Sonic Pi's
  API in Ruby: `live_loop`, `in_thread`, `sync`/`cue`, `play`, `sample`, `sleep`, `use_bpm`,
  `use_synth`, `with_fx`, synth and sample defaults, `ring`/`tick`/`look`, `chord`, `scale`,
  `spread`, `knit`, `line`, `range`, `rrand`/`choose`/`dice` (deterministic, as in Sonic Pi),
  `define`, `at`, `density`, `set`/`get`, `puts`. Each loop is a Ruby Fiber with its own
  virtual time; `sleep` hands control back to a scheduler that always runs the thread that is
  furthest behind, so timing is exact and never depends on the real clock.
* **The middleman** (`internal/live`) keeps Ruby's virtual time about 250 ms ahead of the
  audio, turns the notes Ruby plays into sample-accurate engine hits, and brings errors and
  `puts` back to the screen.
* **The audio engine** (`internal/engine`) starts every note on its exact sample and mixes,
  records, and sends MIDI/OSC as before.

Sonic Pi's synth, sample and effect **names** work, but the sounds are susynth's own
synthesised voices (no SuperCollider, no sample files): `:prophet` plays a supersaw,
`:bd_haus` plays the built-in kick, and so on. F4 Sounds lists every name. Effects map onto
susynth's filters, bit crusher, distortion, echo and reverb; effects with no equivalent are
skipped with a note. Put your own `.wav` files in the samples folder to use real samples
(`sample :mybank, 2`, `sample :kick`, or `sample "~/path/to/file.wav"`).

## Quick start

```
make build                            # CGO_ENABLED=0 go build -o susynth ./cmd/susynth
./susynth                             # open the live editor
./susynth examples/first.rb
./susynth render -seconds 30 set.rb   # render a WAV, no sound device needed
./susynth midi -seconds 30 set.rb     # export a Standard MIDI File
./susynth check set.rb                # syntax check, then run 8 s silently to catch errors
./susynth samples                     # list loaded sample banks
./susynth devices                     # audio outputs and MIDI devices
```

**First launch:** Ruby is compiled to machine code the first time (about 10–15 s, cached in
`~/.cache/susynth/ruby`, roughly 85 MB). The editor opens at once and Ruby warms up in the
background; code you run before it is ready plays as soon as it is. Later launches start Ruby
in about a second.

Audio is built in (PulseAudio/PipeWire, ALSA, CoreAudio, WASAPI via oto), with a fallback to
`pw-cat`/`paplay`/`aplay`. With nothing, it runs silent and `^R` still records a WAV.

## Keys

| key | action |
|---|---|
| `^E` | run the block under the cursor (top-level statements up to a blank line) |
| `Esc Enter` | run the whole buffer (Sonic Pi's Run) |
| `^G` | stop every loop and thread (Sonic Pi's Stop) |
| `^P` | stop, or run the whole buffer again |
| `^R` | record a WAV · `^S` / `^O` save / load a set (`.rb`) |
| `Esc 1-9` · `Esc s n` | mute / solo a track (numbers in the Tracks pane) |
| `Esc -` / `Esc =` | level of the live_loop the cursor is in |
| `Esc x` | export MIDI (runs silently in a second sandbox; the music keeps playing) |
| `F1`–`F7` | Play, Sets, Recordings, Sounds, Settings, Help, Perform |

As in Sonic Pi: re-running a `live_loop` swaps its body in on its next pass; a loop you
delete from the text keeps playing until `^G`; an error inside a loop stops that loop only.
A loop that never sleeps would freeze Ruby, so susynth restarts the sandbox after 2 seconds
and stops everything.

## Build

Needs Go 1.25+. Dependencies are vendored, so it builds offline.

```
make dist     # linux amd64/arm64, macOS amd64/arm64, windows amd64 -> dist/
make test     # vet + tests (the race detector needs CGO_ENABLED=1)
```

Binaries are about 15 MB, most of which is the compressed Ruby (`internal/rubyvm/ruby.wasm.gz`,
provenance and licences in `internal/rubyvm/LICENSES`).

## Libraries

* [wazero](https://github.com/tetratelabs/wazero) v1.11 (Apache-2.0): runs the Ruby sandbox.
* [ruby.wasm](https://github.com/ruby/ruby.wasm) 2.7.1 build of CRuby 3.4.1 (Ruby/BSD; MIT).
* [oto](https://github.com/ebitengine/oto) v3.5.1 for audio output, with purego, jfreymuth/pulse
  and x/sys.

The sandbox blocked the Go module proxy and checksum database, so modules were fetched from
GitHub with checksum verification off; run `go mod verify` on a networked machine.

## Verification status

Verified in a sandbox: unit tests (with `-race`), Sonic Pi features through real Ruby (loops,
sync, ring/tick, define, hashes, interpolation, chords, spread, effects, error line numbers,
recovery from a loop that never sleeps), Ruby → engine → audio rendered offline and checked
for beats, live redefinition in real time, the editor through a pty including a first launch
with an empty cache, rendered levels (no clipping), MIDI export, cross-compiles for five targets.

**Not verified:** a real sound card, macOS, Windows, a real MIDI device or SuperDirt, and how
close each Sonic Pi synth sounds to the original (it won't sound the same).

## Known gaps

* No `control` (changing a note while it plays), no `sample` `start:`/`finish:`/`beat_stretch:`,
  and only the effects listed on F4. MIDI/OSC calls in code are skipped; use F5 Settings.
* Ruby's own `Thread` isn't available; Sonic Pi's `in_thread` and `live_loop` are.
* No undo in the editor.
