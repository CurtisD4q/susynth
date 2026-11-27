// Package live is the middleman between the Ruby sandbox and the audio
// engine. It keeps Ruby's virtual time a little ahead of the audio clock,
// turns the notes Ruby plays into engine hits, and collects errors and
// output for the screen.
package live

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/susynth/susynth/internal/engine"
	"github.com/susynth/susynth/internal/rubyvm"
	"github.com/susynth/susynth/internal/synth"
)

// ahead is how far Ruby runs ahead of the rendered audio. It is larger than
// the engine's own buffer so notes are always scheduled before they are due.
const ahead = 0.25

// Note is something for the screen: an error, a puts line, or "ran".
type Note struct {
	Kind   string // "err", "log", "ran", "info", "cue"
	Line   int
	Thread string
	Text   string
	First  int // ran: first and last line evaluated
	Last   int
	RunID  int
	When   string // ran: "beat" or "bar" when the change waits for one
}

// Changes say when a run reaches music that is already playing.
const (
	OnBeat = "beat" // a changed loop is heard from the next beat, in place
	OnBar  = "bar"  // the same, from the next bar
	OnPass = "pass" // Sonic Pi's way: a changed loop starts on its next pass
)

// Seq runs code in Ruby and feeds the engine.
type Seq struct {
	VM  *rubyvm.VM
	Eng *engine.Engine

	// vm keeps a run and a step of the clock apart, so a change can be timed
	// after everything Ruby has already played.
	vm sync.Mutex

	mu       sync.Mutex
	notes    []Note
	running  bool
	horizon  float64
	runID    int
	pending  *pendingRun
	warned   map[string]bool
	changeOn string
}

type pendingRun struct {
	id     int
	code   string
	cursor int
}

// New ties a VM to an engine. Call Start to begin feeding in real time.
func New(vm *rubyvm.VM, eng *engine.Engine) *Seq {
	s := &Seq{VM: vm, Eng: eng, warned: map[string]bool{}, changeOn: OnBeat}
	s.RefreshTables()
	return s
}

// RefreshTables sends the synth and sample names to Ruby (after the samples
// folder is reloaded, for example).
func (s *Seq) RefreshTables() {
	s.VM.SetTables(synth.SynthNames(), synth.SampleNames())
}

// Start keeps Ruby ahead of the audio until ctx ends.
func (s *Seq) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.flushPending()
			s.tick()
		}
	}()
}

func (s *Seq) note(n Note) {
	s.mu.Lock()
	s.notes = append(s.notes, n)
	if len(s.notes) > 200 {
		s.notes = s.notes[len(s.notes)-200:]
	}
	s.mu.Unlock()
}

// Notes returns and clears what happened since the last call.
func (s *Seq) Notes() []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notes
	s.notes = nil
	return n
}

// Running reports whether code is running.
func (s *Seq) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Waiting reports whether a run is queued until Ruby is ready.
func (s *Seq) Waiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending != nil
}

func (s *Seq) tick() {
	s.vm.Lock()
	defer s.vm.Unlock()
	s.mu.Lock()
	running, h := s.running, s.horizon
	s.mu.Unlock()
	if !running || !s.VM.IsReady() {
		return
	}
	target := s.Eng.Now() + ahead
	if target <= h {
		return
	}
	ms, err := s.VM.Advance(target)
	s.mu.Lock()
	s.horizon = target
	s.mu.Unlock()
	s.dispatch(ms, "")
	if err != nil {
		s.crashed(err)
	}
}

// SetChangeOn says when a run reaches music that is already playing: OnBeat,
// OnBar or OnPass. The first run always starts at once.
func (s *Seq) SetChangeOn(when string) {
	if when != OnBar && when != OnPass {
		when = OnBeat
	}
	s.mu.Lock()
	s.changeOn = when
	s.mu.Unlock()
}

// ChangeOn reports when changes are heard.
func (s *Seq) ChangeOn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changeOn
}

func (s *Seq) crashed(err error) {
	s.mu.Lock()
	s.running = false
	s.horizon = 0
	s.mu.Unlock()
	s.Eng.Hush()
	s.note(Note{Kind: "err", Text: err.Error()})
}

// Run evaluates code: the block around cursor (a 1-based line), or all of it
// when cursor is 0. If Ruby is still starting, the run waits for it. The id
// it returns comes back on the "ran" note and on every note the code plays.
func (s *Seq) Run(code string, cursor int) int {
	s.mu.Lock()
	s.runID++
	id := s.runID
	s.mu.Unlock()
	if !s.VM.IsReady() {
		s.mu.Lock()
		s.pending = &pendingRun{id, code, cursor}
		s.mu.Unlock()
		if err := s.VM.Err(); err != nil {
			s.note(Note{Kind: "err", Text: err.Error()})
		}
		return id
	}
	s.run(id, code, cursor, false)
	return id
}

func (s *Seq) flushPending() {
	s.mu.Lock()
	p := s.pending
	if p == nil || !s.VM.IsReady() {
		s.mu.Unlock()
		return
	}
	s.pending = nil
	s.mu.Unlock()
	s.run(p.id, p.code, p.cursor, false)
}

// MixIn plays code beside what is playing: the loops that are playing become
// the old set (old/drums and so on) and the new code starts on the next bar.
// An old set left from an earlier mix stops. With nothing playing it is an
// ordinary run.
func (s *Seq) MixIn(code string) (int, error) {
	if !s.VM.IsReady() {
		return 0, errors.New("Ruby is still starting")
	}
	s.mu.Lock()
	s.runID++
	id := s.runID
	s.mu.Unlock()
	s.run(id, code, 0, true)
	return id, nil
}

// StopOld stops the old set left by MixIn.
func (s *Seq) StopOld() {
	if !s.VM.IsReady() {
		return
	}
	s.vm.Lock()
	defer s.vm.Unlock()
	ms, err := s.VM.StopOld()
	s.dispatch(ms, "")
	if err != nil {
		s.crashed(err)
	}
}

// when returns the time a run starts and how it treats loops already playing.
// While music plays, a change is timed from the next beat or bar after
// everything Ruby has already sent, so no note is played twice.
func (s *Seq) when(mix bool) (at float64, mode, waits string) {
	at = s.Eng.Now() + 0.03
	mode = "swap"
	if !s.running {
		return at, mode, ""
	}
	on := s.changeOn
	if mix {
		on = OnBar
	}
	if on == OnPass {
		return at, "pass", ""
	}
	step := 1.0
	if on == OnBar {
		step = engine.BeatsPerCycle
	}
	// at least 50 ms ahead, and after everything Ruby has already sent
	return s.Eng.NextGrid(step, math.Max(s.horizon, s.Eng.Now()+0.05)), mode, on
}

func (s *Seq) run(id int, code string, cursor int, mix bool) {
	s.vm.Lock()
	defer s.vm.Unlock()
	if !s.Eng.Playing() {
		s.Eng.Play()
	}
	if mix && s.Running() {
		ms, err := s.VM.NewDeck()
		s.dispatch(ms, "")
		if err != nil {
			s.crashed(err)
			return
		}
		s.Eng.MuteNewTracks(true)
		defer s.Eng.MuteNewTracks(false)
	}
	s.mu.Lock()
	at, mode, waits := s.when(mix)
	if !s.running {
		s.horizon = at
		s.Eng.SetDownbeat(at) // the first run starts the bar
	}
	s.mu.Unlock()
	ms, err := s.VM.RunMode(id, cursor, at, mode, code)
	s.dispatch(ms, waits)
	if err != nil {
		s.crashed(err)
		return
	}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	if mix {
		// the new set's loops start when Ruby reaches at; step there now, so
		// they are made (and muted) before anyone can hear them
		s.mu.Lock()
		h := s.horizon
		s.mu.Unlock()
		if at+0.001 > h {
			ms, err = s.VM.Advance(at + 0.001)
			s.mu.Lock()
			s.horizon = at + 0.001
			s.mu.Unlock()
			s.dispatch(ms, "")
			if err != nil {
				s.crashed(err)
			}
		}
	}
}

// Stop ends every loop and thread and silences the engine.
func (s *Seq) Stop(hush bool) {
	s.mu.Lock()
	s.running = false
	s.pending = nil
	s.horizon = 0
	s.mu.Unlock()
	if s.VM.IsReady() {
		s.vm.Lock()
		ms, _ := s.VM.StopAll()
		s.dispatch(ms, "")
		s.vm.Unlock()
	}
	if hush {
		s.Eng.Hush()
	} else {
		s.Eng.Stop()
	}
}

// StopOne stops a single live_loop or thread. Everything else keeps playing.
func (s *Seq) StopOne(name string) error {
	if !s.VM.IsReady() {
		return errors.New("Ruby is not running")
	}
	s.vm.Lock()
	ms, err := s.VM.Kill(name)
	s.dispatch(ms, "")
	s.vm.Unlock()
	s.Eng.SilenceTrack(name)
	if err != nil {
		s.crashed(err)
	}
	return err
}

// Check reports syntax errors without running anything.
func (s *Seq) Check(code string) []Note {
	ms, err := s.VM.Check(code)
	var out []Note
	for _, m := range ms {
		if m.Kind() == "err" {
			out = append(out, errNote(m))
		}
	}
	if err != nil {
		out = append(out, Note{Kind: "err", Text: err.Error()})
	}
	return out
}

func errNote(m rubyvm.Msg) Note {
	line, _ := strconv.Atoi(m.Arg(0))
	return Note{Kind: "err", Line: line, Thread: m.Arg(1), Text: m.Arg(2)}
}

func (s *Seq) dispatch(ms []rubyvm.Msg, waits string) {
	var hits []engine.Hit
	for _, m := range ms {
		switch m.Kind() {
		case "ev":
			h, g, err := Translate(m)
			if err != nil {
				key := err.Error()
				s.mu.Lock()
				seen := s.warned[key]
				s.warned[key] = true
				s.mu.Unlock()
				if !seen {
					line, _ := strconv.Atoi(m.Arg(2))
					s.note(Note{Kind: "err", Line: line, Thread: m.Arg(1), Text: key})
				}
				continue
			}
			if g.DelaySet {
				s.Eng.SetDelay(g.DelayTime, g.DelayFB, g.PingPong)
			}
			if g.RoomSet {
				s.Eng.SetReverb(g.RoomSize, g.RoomDamp)
			}
			hits = append(hits, h)
		case "bpm":
			if b, err := strconv.ParseFloat(m.Arg(0), 64); err == nil {
				if at, err := strconv.ParseFloat(m.Arg(1), 64); err == nil {
					s.Eng.SetBPMAt(b, at)
				} else {
					s.Eng.SetBPM(b)
				}
			}
		case "thr+":
			s.Eng.ThreadStarted(m.Arg(0), m.Arg(1) == "loop")
		case "thr-":
			s.Eng.ThreadEnded(m.Arg(0))
		case "ren":
			s.Eng.RenameTrack(m.Arg(0), m.Arg(1))
		case "err":
			s.note(errNote(m))
		case "log":
			s.note(Note{Kind: "log", Text: m.Arg(0)})
		case "cue":
			s.note(Note{Kind: "cue", Text: "cue :" + m.Arg(0)})
		case "ran":
			id, _ := strconv.Atoi(m.Arg(0))
			a, _ := strconv.Atoi(m.Arg(1))
			b, _ := strconv.Atoi(m.Arg(2))
			n := Note{Kind: "ran", RunID: id, First: a, Last: b}
			if a > 0 {
				n.When = waits
			}
			s.note(n)
		}
	}
	s.Eng.Schedule(hits...)
}

// Globals are bus settings carried by a note (echo time, reverb size).
type Globals struct {
	DelaySet           bool
	DelayTime, DelayFB float64
	PingPong           bool
	RoomSet            bool
	RoomSize           float64
	RoomDamp           float64
}

func midiHz(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

var noteLetters = "ccddeffggaab"

// Translate turns one "ev" line from Ruby into an engine hit.
func Translate(m rubyvm.Msg) (engine.Hit, Globals, error) {
	var h engine.Hit
	var g Globals
	t, err := strconv.ParseFloat(m.Arg(0), 64)
	if err != nil {
		return h, g, errors.New("bad event time")
	}
	h.At = int64(math.Round(t * engine.SampleRate))
	h.Track = m.Arg(1)
	h.Line, _ = strconv.Atoi(m.Arg(2))
	h.Run, _ = strconv.Atoi(m.Arg(6))
	kind, name := m.Arg(3), m.Arg(4)
	kv := map[string]float64{}
	for _, f := range strings.Split(m.Arg(5), ",") {
		if k, v, ok := strings.Cut(f, "="); ok {
			if x, err := strconv.ParseFloat(v, 64); err == nil {
				kv[k] = x
			}
		}
	}
	get := func(k string, d float64) float64 {
		if v, ok := kv[k]; ok {
			return v
		}
		return d
	}
	p := synth.Params{Gain: get("amp", 1), Pan: get("pan", 0), Speed: 1}
	switch kind {
	case "p":
		sy, ok := synth.LookupSynth(name)
		if !ok {
			return h, g, fmt.Errorf("synth :%s isn't available", name)
		}
		if sy.Drum != "" {
			h.Word = sy.Drum
			h.Label = sy.Drum[0]
			break
		}
		n := get("n", 60)
		if n < 0 || n > 135 {
			return h, g, fmt.Errorf("note %.1f is out of range", n)
		}
		p.Wave, p.Note = sy.Wave, n
		h.Word = strconv.Itoa(int(math.Round(math.Min(127, n))))
		h.Label = noteLetters[int(math.Round(n))%12]
		a, d, sus, r := get("a", 0), get("d", 0), get("s", 0), get("r", 1)
		p.Attack = a
		p.Release = math.Max(r, 0.005)
		h.Gate = int((a + d + sus) * engine.SampleRate)
		if c, ok := kv["cutoff"]; ok {
			p.LPF = midiHz(c)
		}
	case "s":
		idx := int(get("i", 0))
		w, err := synth.ResolveSample(name, idx)
		if err != nil {
			return h, g, err
		}
		h.Word = w
		h.Label = w[0]
		if strings.HasPrefix(w, "@") || strings.Contains(w, ":") && !strings.HasPrefix(w, "tom") {
			base := strings.ToLower(filepath.Base(name))
			if base == "" {
				base = "x"
			}
			h.Label = base[0]
		}
		p.Speed = math.Abs(get("rate", 1))
		if p.Speed == 0 {
			p.Speed = 1
		}
		if c, ok := kv["cutoff"]; ok {
			p.LPF = midiHz(c)
		}
	default:
		return h, g, fmt.Errorf("unknown event kind %q", kind)
	}
	if c, ok := kv["flpf"]; ok {
		hz := midiHz(c)
		if p.LPF == 0 || hz < p.LPF {
			p.LPF = hz
		}
	}
	if c, ok := kv["fhpf"]; ok {
		p.HPF = midiHz(c)
	}
	p.Room = get("room", 0)
	p.Delay = get("dly", 0)
	p.Crush = get("crush", 0)
	p.Shape = get("shape", 0)
	if v, ok := kv["dtime"]; ok {
		g.DelaySet, g.DelayTime, g.DelayFB = true, v, get("dfb", 0.45)
		g.PingPong = get("dpp", 0) != 0
	}
	if v, ok := kv["rsize"]; ok {
		g.RoomSet, g.RoomSize, g.RoomDamp = true, v, get("rdamp", 0.5)
	}
	h.Params = p
	return h, g, nil
}

// Collect runs code from time 0 for seconds without audio and returns every
// hit, for MIDI export. Ruby must be ready.
func Collect(vm *rubyvm.VM, code string, seconds float64) ([]engine.Hit, float64, []Note, error) {
	var notes []Note
	if _, err := vm.StopAll(); err != nil {
		return nil, 0, nil, err
	}
	ms, err := vm.Run(1, 0, 0, code)
	if err != nil {
		return nil, 0, nil, err
	}
	m2, err := vm.Advance(seconds)
	ms = append(ms, m2...)
	bpm := 60.0
	var hits []engine.Hit
	for _, m := range ms {
		switch m.Kind() {
		case "ev":
			if h, _, e := Translate(m); e == nil {
				hits = append(hits, h)
			} else {
				notes = append(notes, Note{Kind: "err", Text: e.Error()})
			}
		case "bpm":
			if b, e := strconv.ParseFloat(m.Arg(0), 64); e == nil {
				bpm = b
			}
		case "err":
			notes = append(notes, errNote(m))
		}
	}
	vm.StopAll()
	return hits, bpm, notes, err
}

// Render plays code through a fresh engine for seconds and writes the audio,
// in lockstep with Ruby. Ruby must be ready.
func Render(vm *rubyvm.VM, eng *engine.Engine, code string, seconds float64, write func([]float32) error) ([]Note, error) {
	s := New(vm, eng)
	if _, err := vm.StopAll(); err != nil {
		return nil, err
	}
	ms, err := vm.Run(1, 0, 0, code)
	s.dispatch(ms, "")
	if err != nil {
		return s.Notes(), err
	}
	err = eng.RenderOffline(seconds, func(until float64) error {
		ms, err := vm.Advance(until)
		s.dispatch(ms, "")
		return err
	}, write)
	vm.StopAll()
	return s.Notes(), err
}
