# susynth

A terminal instrument for live-coding music (Tidal/Strudel style). One standalone Go binary:
no cgo, no C compiler, no external player programs. You write patterns in a text editor pane,
press `^E`, and hear them on the next cycle.

## Quick start

```
make build               # CGO_ENABLED=0 go build -o susynth ./cmd/susynth
./susynth                # open the live editor
./susynth examples/first.syn
./susynth render -cycles 8 set.syn    # render a WAV, no sound device needed
./susynth midi -cycles 8 set.syn      # export a Standard MIDI File
./susynth check set.syn               # parse and report errors
./susynth samples                     # list loaded sample banks
./susynth devices                     # audio outputs and MIDI devices
```

Audio is built in: susynth opens the sound device itself (PulseAudio/PipeWire over their socket,
or ALSA; CoreAudio on macOS; WASAPI on Windows). If that fails it falls back to piping into
`pw-cat`/`paplay`/`aplay` when one is installed. With nothing it runs silent, and `^R` still
records a WAV. Force a backend with `-sink native|pipewire|pulseaudio|alsa|none`.

## Build

Needs Go 1.25+. Dependencies are vendored, so it builds offline.

```
make dist     # linux amd64/arm64, macOS amd64/arm64, windows amd64 -> dist/
make test     # vet + tests (the race detector needs CGO_ENABLED=1)
```

## Pages (F1-F7)

Play (editor + tracks), Sets, Recordings, Sounds (drums, tones, sample banks), Settings,
Help (keys and language reference), Perform (set list). A grid editor and themes
(`default`, `large`, `contrast`) are available; see Help inside the app for current keys.

## Language

```
bpm 124
d1 "bd*2 [~ bd] bd ~"
d2 "~ sn ~ [sn cp]" # gain 0.9
d4 "c2 ~ [c2 g2] ~" # voice saw # lpf 800
d5 every 4 (fast 2) $ s "bd sn" # room 0.3
hush
```

* Mini-notation: sequences, `[groups]`, `*` repeat, `~` rest, `<alternation>`, euclid, notes.
* Functions: fast slow rev palindrome rot iter every degradeBy ply.
* Modifiers: gain pan speed voice lpf hpf crush shape delay room release.
* Commands: bpm mute unmute solo unsolo level stop hush delaytime delayfb roomsize.
* Sounds: bd sn cp hh oh rim tom cb; sine tri saw square pluck fm supersaw organ noise;
  plus any WAV bank found in the samples folder (one sub-folder per bank).

## Outputs

* WAV recording and offline render; Standard MIDI File export (format 1, 480 PPQ).
* OSC to SuperDirt (`/dirt/play` bundles over UDP, NTP time tags).
* Live MIDI by writing to a device node such as `/dev/snd/midiC1D0` (no cgo needed), with
  optional 24-ppqn clock.

## Design notes

* Sample-counter master clock (48 kHz, 10 ms blocks, 100 ms lookahead) with exact rational
  time; evaluation is quantised to cycle boundaries.
* Libraries: [oto](https://github.com/ebitengine/oto) v3.5.1 for audio output (pulls in purego,
  jfreymuth/pulse and x/sys), all vendored. OSC, MIDI, terminal handling and the UI are
  hand-written on the standard library.
* The raw terminal uses ioctls directly on Linux, macOS and FreeBSD (no `stty`).
  Windows builds compile but the live editor is not supported there; `render`, `check`
  and `midi` work.

## Verification status

Verified in a sandbox: unit tests (including `-race`), MIDI output parsed with mido, OSC over a
UDP listener, MIDI device writes captured from a file, the editor driven through a pty (also with
an empty `PATH`), and native audio played into an ALSA file device (440 Hz at the right level).
Cross-compiles for all five targets.

**Not verified:** a real sound card, the PulseAudio/PipeWire socket path, macOS, Windows, a real
MIDI device or SuperDirt. The sandbox blocked the Go module proxy and checksum database, so
modules were fetched from GitHub with checksum verification off; run `go mod verify` on a
networked machine.

## Known gaps

No undo in the editor; ASCII/BMP text only; error markers are cleared when lines are inserted
or removed.
