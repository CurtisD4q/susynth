// Package engine is the audio core. The sample counter is the master clock:
// patterns are queried per audio block, evals are applied exactly on a cycle
// boundary, and everything else (UI, recording) follows from it.
package engine

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/susynth/susynth/internal/frac"
	"github.com/susynth/susynth/internal/lang"
	"github.com/susynth/susynth/internal/synth"
)

const (
	// SampleRate is fixed so recordings and offline renders are reproducible.
	SampleRate = 48000
	// BeatsPerCycle: one cycle is one 4/4 bar.
	BeatsPerCycle = 4
	// StepCells is the width of the step display.
	StepCells = 16

	tickEvery = 10 * time.Millisecond
	lookAhead = 100 * time.Millisecond
	maxVoices = 96
	posDenom  = 1 << 20 // keeps cross-multiplied fractions inside int64 for days of playing
)

type track struct {
	def   lang.TrackDef
	mute  bool
	solo  bool
	level float64
}

type change struct {
	prog       lang.Program
	replaceAll bool
}

// Engine renders audio. All methods are safe for concurrent use.
type Engine struct {
	mu sync.Mutex

	bpm     float64
	playing bool
	tracks  map[int]*track
	pending []change
	voices  []*Voice
	seedCtr uint32

	samples   int64  // frames rendered so far
	anchorS   int64  // sample at which anchorPos was set
	anchorPos frac.F // cycle position at anchorS
	lead      int64  // frames rendered but not yet audible (for the playhead)
	hushed    bool
	underruns int

	rec *WavWriter

	// effects
	fx                  *fxBus
	delayBeats, delayFB float64
	roomSize            float64
	dsend, rsend        []float32

	// outputs
	outputs        []Output
	internal       bool
	wallBegin      time.Time
	samplesAtBegin int64
	startPending   bool
	outputDelay    time.Duration
}

// Voice aliases synth.Voice so engine callers need one import.
type Voice = synth.Voice

// New returns a stopped engine at 120 BPM.
func New() *Engine {
	return &Engine{bpm: 120, tracks: map[int]*track{}, anchorPos: frac.Int(0), internal: true,
		fx: newFXBus(), delayBeats: 0.75, delayFB: 0.45, roomSize: 0.6}
}

func (e *Engine) cps() float64 { return e.bpm / 60 / BeatsPerCycle }

func (e *Engine) posAt(s int64) frac.F {
	d := float64(s-e.anchorS) / SampleRate * e.cps()
	return e.anchorPos.Add(frac.New(int64(math.Round(d*posDenom)), posDenom))
}

// reanchor keeps the cycle position continuous across a tempo change.
func (e *Engine) reanchor() {
	p := e.posAt(e.samples)
	e.anchorS, e.anchorPos = e.samples, p
}

// auditionTrack is the pseudo-track for one-shot previews from the Sounds page.
const auditionTrack = -1

// Audition plays one sound immediately, even while stopped. wave picks the
// tone voice for notes ("" = default).
func (e *Engine) Audition(word, wave string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seedCtr++
	v, err := synth.New(word, SampleRate, 0, SampleRate/2, synth.Params{Gain: 1, Speed: 1, Wave: wave}, e.seedCtr)
	if err != nil {
		return err
	}
	v.Track = auditionTrack
	e.voices = append(e.voices, v)
	return nil
}

// Play starts the transport from cycle 0.
func (e *Engine) Play() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.playing {
		return
	}
	e.playing = true
	e.startPending = true
	e.anchorS, e.anchorPos = e.samples, frac.Int(0)
}

// Stop halts the transport and fades out sounding voices.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.playing = false
	for _, v := range e.voices {
		v.Kill()
	}
	if len(e.outputs) > 0 {
		e.emit(Event{Kind: KindStop, At: time.Now()})
		e.emit(Event{Kind: KindPanic, At: time.Now()})
	}
}

// TogglePlay flips the transport and reports the new state.
func (e *Engine) TogglePlay() bool {
	e.mu.Lock()
	p := e.playing
	e.mu.Unlock()
	if p {
		e.Stop()
	} else {
		e.Play()
	}
	return !p
}

// Hush silences everything immediately: clears tracks, pending evals, voices.
func (e *Engine) Hush() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tracks = map[int]*track{}
	e.pending = nil
	for _, v := range e.voices {
		v.Kill()
	}
	e.hushed = true
	if len(e.outputs) > 0 {
		e.emit(Event{Kind: KindPanic, At: time.Now()})
	}
}

// SetBPM changes tempo immediately, keeping the playhead continuous.
func (e *Engine) SetBPM(b float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setBPM(b)
}

func (e *Engine) setBPM(b float64) {
	b = math.Max(20, math.Min(400, b))
	e.reanchor()
	e.bpm = b
}

// BPM returns the current tempo.
func (e *Engine) BPM() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bpm
}

// Apply queues a parsed program for the next cycle boundary (immediately if
// stopped). With replaceAll, tracks missing from the program are stopped.
func (e *Engine) Apply(p lang.Program, replaceAll bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pending = append(e.pending, change{p, replaceAll})
	if !e.playing {
		e.flushPending()
	}
}

// PendingEvals is the number of evals waiting for the next cycle.
func (e *Engine) PendingEvals() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

func (e *Engine) flushPending() {
	for _, c := range e.pending {
		e.applyChange(c)
	}
	e.pending = nil
}

func (e *Engine) applyChange(c change) {
	e.hushed = false
	if c.replaceAll {
		keep := map[int]bool{}
		for _, d := range c.prog.Tracks {
			keep[d.ID] = true
		}
		for id := range e.tracks {
			if !keep[id] {
				delete(e.tracks, id)
			}
		}
	}
	for _, d := range c.prog.Tracks {
		if t, ok := e.tracks[d.ID]; ok {
			t.def = d
		} else {
			e.tracks[d.ID] = &track{def: d, level: 1}
		}
	}
	for _, cmd := range c.prog.Commands {
		switch cmd.Name {
		case "hush":
			e.tracks = map[int]*track{}
			for _, v := range e.voices {
				v.Kill()
			}
			e.hushed = true
		case "bpm":
			e.setBPM(cmd.Value)
		case "delaytime":
			e.delayBeats = cmd.Value
		case "delayfb":
			e.delayFB = cmd.Value
		case "roomsize":
			e.roomSize = cmd.Value
		case "unsolo":
			for _, t := range e.tracks {
				t.solo = false
			}
		case "stop":
			delete(e.tracks, cmd.Track)
		default:
			if t, ok := e.tracks[cmd.Track]; ok {
				switch cmd.Name {
				case "mute":
					t.mute = true
				case "unmute":
					t.mute = false
				case "solo":
					t.solo = true
				case "level":
					t.level = cmd.Value
				}
			}
		}
	}
}

// ToggleMute flips mute on a track; reports whether the track exists.
func (e *Engine) ToggleMute(id int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tracks[id]
	if ok {
		t.mute = !t.mute
	}
	return ok
}

// SetSolo solos one track (id 0 clears all solos). Reports success.
func (e *Engine) SetSolo(id int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == 0 {
		for _, t := range e.tracks {
			t.solo = false
		}
		return true
	}
	t, ok := e.tracks[id]
	if ok {
		t.solo = !t.solo
	}
	return ok
}

// AdjustLevel nudges a track's level by delta (clamped 0..1.5).
func (e *Engine) AdjustLevel(id int, delta float64) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tracks[id]
	if !ok {
		return 0, false
	}
	t.level = math.Max(0, math.Min(1.5, math.Round((t.level+delta)*100)/100))
	return t.level, true
}

// StartRecording begins streaming the master output to path.
func (e *Engine) StartRecording(path string) error {
	w, err := NewWavWriter(path, SampleRate)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rec != nil {
		e.rec.Close()
	}
	e.rec = w
	return nil
}

// StopRecording finalises the WAV and returns the recorded duration.
func (e *Engine) StopRecording() (time.Duration, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rec == nil {
		return 0, nil
	}
	d := time.Duration(e.rec.Frames()) * time.Second / SampleRate
	err := e.rec.Close()
	e.rec = nil
	return d, err
}

// boundarySample returns the first sample >= s that starts a new cycle.
func (e *Engine) boundarySample(s int64) int64 {
	p := e.posAt(s)
	target := p.Ceil()
	if frac.Int(target).Eq(p) {
		return s
	}
	exact := float64(e.anchorS) + (float64(target)-e.anchorPos.Float())/e.cps()*SampleRate
	bs := int64(math.Ceil(exact))
	if bs < s {
		bs = s
	}
	for e.posAt(bs).Floor() < target {
		bs++
	}
	for bs > s && e.posAt(bs-1).Floor() >= target {
		bs--
	}
	return bs
}

// Render produces n stereo frames (interleaved int16).
func (e *Engine) Render(n int) []int16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	mix := make([]float32, n*2)
	e.dsend, e.rsend = make([]float32, n*2), make([]float32, n*2)
	start := 0
	for start < n {
		end := n
		if len(e.pending) > 0 {
			if !e.playing {
				e.flushPending()
			} else {
				cur := e.samples + int64(start)
				bs := e.boundarySample(cur)
				if bs == cur {
					e.flushPending()
				} else if int(bs-e.samples) < end {
					end = int(bs - e.samples)
				}
			}
		}
		e.renderSpan(mix, start, end)
		start = end
	}
	ds := int(e.delayBeats * 60 / e.bpm * SampleRate)
	e.fx.process(mix, e.dsend, e.rsend, ds, float32(e.delayFB), float32(e.roomSize))
	out := make([]int16, n*2)
	for i, v := range mix {
		out[i] = int16(math.Tanh(float64(v)*0.9) * 32000)
	}
	e.samples += int64(n)
	if e.rec != nil {
		if err := e.rec.Write(out); err != nil {
			e.rec.Close()
			e.rec = nil
		}
	}
	return out
}

func (e *Engine) audible(t *track, anySolo bool) bool {
	return !t.mute && (!anySolo || t.solo)
}

func (e *Engine) renderSpan(mix []float32, start, end int) {
	if end <= start {
		return
	}
	anySolo := false
	for _, t := range e.tracks {
		anySolo = anySolo || t.solo
	}
	if e.playing {
		cur := e.samples + int64(start)
		a, b := e.posAt(cur), e.posAt(e.samples+int64(end))
		ids := make([]int, 0, len(e.tracks))
		for id := range e.tracks {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		cps := e.cps()
		for _, id := range ids {
			t := e.tracks[id]
			if !e.audible(t, anySolo) {
				continue
			}
			for _, ev := range t.def.Pattern.Query(a, b) {
				exact := float64(e.anchorS) + (ev.Start.Float()-e.anchorPos.Float())/cps*SampleRate
				off := int64(math.Round(exact)) - cur
				if off < 0 {
					off = 0
				}
				if off >= int64(end-start) {
					off = int64(end-start) - 1
				}
				gate := int(math.Round(ev.End.Sub(ev.Start).Float() / cps * SampleRate))
				if len(e.outputs) > 0 {
					if at := e.wallAt(cur + off); !at.IsZero() {
						e.emit(Event{Kind: KindNote, At: at, Track: id, Word: ev.Value, Params: t.def.Params,
							Dur: time.Duration(gate) * time.Second / SampleRate, BPM: e.bpm})
					}
				}
				if !e.internal {
					continue
				}
				e.seedCtr++
				v, err := synth.New(ev.Value, SampleRate, int(off), gate, t.def.Params, e.seedCtr)
				if err != nil {
					continue
				}
				v.Track = id
				e.voices = append(e.voices, v)
			}
		}
		if len(e.outputs) > 0 && !e.wallBegin.IsZero() {
			for k := a.Mul(frac.Int(96)).Ceil(); frac.New(k, 96).Lt(b); k++ {
				abs := int64(math.Round(float64(e.anchorS) + (float64(k)/96-e.anchorPos.Float())/cps*SampleRate))
				if e.startPending && k%96 == 0 { // MIDI start goes out on a bar line
					e.startPending = false
					e.emit(Event{Kind: KindStart, At: e.wallAt(abs)})
				}
				e.emit(Event{Kind: KindClock, At: e.wallAt(abs), BPM: e.bpm})
			}
		}
		if over := len(e.voices) - maxVoices; over > 0 {
			for _, v := range e.voices[:over] {
				v.Kill()
			}
		}
	}
	gains := map[int]float32{auditionTrack: 1}
	for id, t := range e.tracks {
		if e.audible(t, anySolo) {
			gains[id] = float32(t.level)
		}
	}
	live := e.voices[:0]
	for _, v := range e.voices {
		g := gains[v.Track]
		for i := start; i < end && !v.Done(); i++ {
			l, r := v.Next()
			l, r = l*g*0.8, r*g*0.8
			mix[i*2] += l
			mix[i*2+1] += r
			if v.SendDelay > 0 {
				e.dsend[i*2] += l * v.SendDelay
				e.dsend[i*2+1] += r * v.SendDelay
			}
			if v.SendRoom > 0 {
				e.rsend[i*2] += l * v.SendRoom
				e.rsend[i*2+1] += r * v.SendRoom
			}
		}
		if !v.Done() {
			live = append(live, v)
		}
	}
	e.voices = live
}

// StepCell is one cell of the step display.
type StepCell struct {
	On    bool
	Label byte
}

// TrackView is what the UI shows for one track.
type TrackView struct {
	ID    int
	Src   string
	Mute  bool
	Solo  bool
	Level float64
	Steps [StepCells]StepCell
}

// View is a snapshot of the engine for the UI.
type View struct {
	Playing   bool
	BPM       float64
	Cycle     int64
	Beat      int     // 1..4
	Phase     float64 // 0..1 within the cycle
	Tracks    []TrackView
	Recording bool
	RecTime   time.Duration
	Hushed    bool
	Pending   int
	Voices    int
	Underruns int
}

// Snapshot captures the display state, offset by the output latency so the
// playhead matches what is audible, not what has merely been rendered.
func (e *Engine) Snapshot() View {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := View{Playing: e.playing, BPM: e.bpm, Hushed: e.hushed, Pending: len(e.pending),
		Voices: len(e.voices), Underruns: e.underruns}
	if e.rec != nil {
		v.Recording = true
		v.RecTime = time.Duration(e.rec.Frames()) * time.Second / SampleRate
	}
	pos := e.posAt(e.samples - e.lead)
	if !e.playing || pos.Lt(frac.Int(0)) {
		pos = frac.Int(0)
	}
	v.Cycle = pos.Floor()
	v.Phase = pos.Sub(frac.Int(v.Cycle)).Float()
	v.Beat = int(v.Phase*BeatsPerCycle) + 1
	ids := make([]int, 0, len(e.tracks))
	for id := range e.tracks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		t := e.tracks[id]
		tv := TrackView{ID: id, Src: t.def.Source, Mute: t.mute, Solo: t.solo, Level: t.level}
		c := frac.Int(v.Cycle)
		for _, ev := range t.def.Pattern.Query(c, c.Add(frac.Int(1))) {
			cell := int(ev.Start.Sub(c).Float() * StepCells)
			if cell >= 0 && cell < StepCells {
				tv.Steps[cell] = StepCell{On: true, Label: label(ev.Value)}
			}
		}
		v.Tracks = append(v.Tracks, tv)
	}
	return v
}

func label(w string) byte {
	if w == "" {
		return 'x'
	}
	return w[0]
}

// Run renders audio into write until ctx is cancelled. The scheduler ticks
// every 10 ms and keeps ~100 ms rendered ahead of the wall clock.
func (e *Engine) Run(ctx context.Context, write func([]int16) error, sinkLatency time.Duration) error {
	block := SampleRate / 100
	begin := time.Now()
	e.mu.Lock()
	e.wallBegin, e.samplesAtBegin = begin, e.samples
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.wallBegin = time.Time{}
		e.mu.Unlock()
	}()
	var written int64
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		elapsed := time.Since(begin)
		heard := int64(elapsed.Seconds() * SampleRate)
		want := heard + int64(lookAhead.Seconds()*SampleRate)
		if written > 0 && written < heard {
			e.mu.Lock()
			e.underruns++
			e.mu.Unlock()
		}
		for written < want {
			buf := e.Render(block)
			if err := write(buf); err != nil {
				return err
			}
			written += int64(block)
		}
		e.mu.Lock()
		e.lead = written - heard + int64(sinkLatency.Seconds()*SampleRate)
		e.mu.Unlock()
	}
}

// RenderOffline renders cycles of music without a device, for tests, previews
// and the `render` command. The engine must have its program applied first.
func (e *Engine) RenderOffline(cycles float64, write func([]int16) error) error {
	e.Play()
	total := int64(cycles / e.cpsLocked() * SampleRate)
	for done := int64(0); done < total; {
		n := int64(SampleRate / 100)
		if total-done < n {
			n = total - done
		}
		if err := write(e.Render(int(n))); err != nil {
			return err
		}
		done += n
	}
	return nil
}

func (e *Engine) cpsLocked() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cps()
}
