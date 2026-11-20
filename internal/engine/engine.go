// Package engine is the audio core. The sample counter is the master clock.
// Code running in the Ruby sandbox decides what plays and when; it sends
// timed hits ahead of time, and the engine starts each one on the exact
// sample it is due. Everything else (UI, recording, MIDI clock) follows from
// the same counter.
package engine

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/susynth/susynth/internal/synth"
)

const (
	// SampleRate is fixed so recordings and offline renders are reproducible.
	SampleRate = 48000
	// BeatsPerCycle is the bar length used by the display and MIDI clock.
	BeatsPerCycle = 4
	// StepCells is the width of the step display (one bar).
	StepCells = 16

	tickEvery = 10 * time.Millisecond
	lookAhead = 100 * time.Millisecond
	maxVoices = 128
)

// Hit is one note or sample to start at an exact sample.
type Hit struct {
	At     int64 // absolute sample
	Track  string
	Word   string
	Params synth.Params
	Gate   int // frames a tone is held before its release
	Label  byte
	Line   int // source line, for the editor
}

type stamp struct {
	at    int64
	label byte
}

type track struct {
	id         int
	name       string
	loop       bool
	refs       int
	mute, solo bool
	level      float64
	hist       []stamp
	lastHit    int64
	ended      int64
	src        string
}

// Engine renders audio. All methods are safe for concurrent use.
type Engine struct {
	mu sync.Mutex

	bpm        float64
	playing    bool
	anchorS    int64   // sample where anchorBeat was measured
	anchorBeat float64 // beats since play at anchorS
	queue      []Hit
	tracks     map[string]*track
	roll       []RollNote // recent notes for the piano roll
	voices     []*Voice
	seedCtr    uint32

	samples   int64 // frames rendered so far
	lead      int64 // frames rendered but not yet audible (for the playhead)
	hushed    bool
	underruns int

	rec *WavWriter

	// effects
	fx           *fxBus
	delaySeconds float64
	delayFB      float64
	roomSize     float64
	dsend, rsend []float32

	// outputs
	outputs        []Output
	internal       bool
	wallBegin      time.Time
	samplesAtBegin int64
	startPending   bool
	outputDelay    time.Duration
	lastClock      int64
}

// Voice aliases synth.Voice so engine callers need one import.
type Voice = synth.Voice

// New returns a stopped engine at 60 BPM, Sonic Pi's default.
func New() *Engine {
	return &Engine{bpm: 60, tracks: map[string]*track{}, internal: true,
		fx: newFXBus(), delaySeconds: 0.25, delayFB: 0.45, roomSize: 0.6, lastClock: -1}
}

// beatAt is the number of beats since play started at sample s.
func (e *Engine) beatAt(s int64) float64 {
	return e.anchorBeat + float64(s-e.anchorS)/SampleRate*e.bpm/60
}

// sampleAtBeat is the inverse of beatAt.
func (e *Engine) sampleAtBeat(b float64) int64 {
	return e.anchorS + int64(math.Round((b-e.anchorBeat)*60/e.bpm*SampleRate))
}

// auditionTrack is the pseudo-track for one-shot previews from the Sounds page.
const auditionTrack = -1

// Audition plays one sound immediately, even while stopped.
func (e *Engine) Audition(word string, p synth.Params, gate int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seedCtr++
	if p.Gain == 0 {
		p.Gain = 1
	}
	if gate <= 0 {
		gate = SampleRate / 4
	}
	v, err := synth.New(word, SampleRate, 0, gate, p, e.seedCtr)
	if err != nil {
		return err
	}
	v.Track = auditionTrack
	e.voices = append(e.voices, v)
	return nil
}

// Now is the engine clock in seconds: everything before it has been rendered.
func (e *Engine) Now() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return float64(e.samples) / SampleRate
}

// NextBar is the engine time, in seconds, of the next bar line that is at
// least minAhead seconds after Now. A stopped transport has not started a
// bar yet (Play puts one at Now), so NextBar returns Now.
func (e *Engine) NextBar(minAhead float64) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := float64(e.samples) / SampleRate
	if !e.playing || e.bpm <= 0 {
		return now
	}
	if minAhead < 0 {
		minAhead = 0
	}
	beat := e.beatAt(e.samples)
	bar := float64(BeatsPerCycle)
	n := math.Floor(beat/bar) + 1
	for step := 0; step < 8; step++ {
		t := float64(e.sampleAtBeat((n+float64(step))*bar)) / SampleRate
		if t >= now+minAhead {
			return t
		}
	}
	return now + bar*60/e.bpm
}

// Playing reports whether the transport is running.
func (e *Engine) Playing() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.playing
}

// Play starts the transport: the bar counter begins at the next rendered sample.
func (e *Engine) Play() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.playing {
		return
	}
	e.playing = true
	e.hushed = false
	e.startPending = true
	e.anchorS, e.anchorBeat = e.samples, 0
	e.lastClock = -1
}

// Stop halts the transport, drops scheduled hits and fades out voices.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
}

func (e *Engine) stopLocked() {
	e.playing = false
	e.queue = nil
	e.tracks = map[string]*track{}
	e.roll = nil
	for _, v := range e.voices {
		v.Kill()
	}
	if len(e.outputs) > 0 {
		e.emit(Event{Kind: KindStop, At: time.Now()})
		e.emit(Event{Kind: KindPanic, At: time.Now()})
	}
}

// Hush is Stop that also shows HUSH on the transport bar.
func (e *Engine) Hush() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
	e.hushed = true
}

// SetBPM changes the display tempo (bars, MIDI clock), keeping the bar
// counter continuous. Note timing comes from the code, not from this.
func (e *Engine) SetBPM(b float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b = math.Max(10, math.Min(600, b))
	beat := e.beatAt(e.samples)
	e.anchorS, e.anchorBeat = e.samples, beat
	e.bpm = b
}

// BPM returns the display tempo.
func (e *Engine) BPM() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bpm
}

// SetDelay sets the echo bus time and feedback (from with_fx :echo).
func (e *Engine) SetDelay(seconds, feedback float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.delaySeconds = math.Max(0.001, math.Min(seconds, 3.9))
	e.delayFB = math.Max(0, math.Min(feedback, 0.95))
}

// SetRoomSize sets the reverb size 0..1 (from with_fx :reverb, room:).
func (e *Engine) SetRoomSize(size float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roomSize = math.Max(0, math.Min(size, 1))
}

// Schedule queues hits. Hits already in the past play at once.
func (e *Engine) Schedule(hs ...Hit) {
	if len(hs) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queue = append(e.queue, hs...)
	sort.SliceStable(e.queue, func(i, j int) bool { return e.queue[i].At < e.queue[j].At })
}

// track returns (creating) the track for a thread name. Tracks get a number
// (for Esc u / Esc k) only once they are worth showing: a live_loop, or a
// thread that played something. One-off runs that only define loops stay hidden.
func (e *Engine) track(name string) *track {
	t, ok := e.tracks[name]
	if !ok {
		t = &track{name: name, level: 1, lastHit: -1, ended: -1}
		e.tracks[name] = t
	}
	return t
}

func (e *Engine) number(t *track) {
	if t.id > 0 {
		return
	}
	used := map[int]bool{}
	for _, o := range e.tracks {
		used[o.id] = true
	}
	id := 1
	for used[id] {
		id++
	}
	t.id = id
}

// ThreadStarted records that a live_loop or thread with this name is running.
func (e *Engine) ThreadStarted(name string, loop bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.track(name)
	t.refs++
	t.loop = t.loop || loop
	if t.loop {
		e.number(t)
	}
}

// ThreadEnded records that one thread with this name finished.
func (e *Engine) ThreadEnded(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.tracks[name]; ok {
		if t.refs--; t.refs <= 0 {
			t.refs = 0
			t.ended = e.samples
			if t.loop { // a stopped loop's queued notes go too
				q := e.queue[:0]
				for _, h := range e.queue {
					if h.Track != name {
						q = append(q, h)
					}
				}
				e.queue = q
			}
		}
	}
}

func (e *Engine) trackByID(id int) *track {
	if id <= 0 {
		return nil
	}
	for _, t := range e.tracks {
		if t.id == id {
			return t
		}
	}
	return nil
}

// ToggleMute flips mute on a track; reports whether the track exists.
func (e *Engine) ToggleMute(id int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.trackByID(id)
	if t != nil {
		t.mute = !t.mute
	}
	return t != nil
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
	t := e.trackByID(id)
	if t != nil {
		t.solo = !t.solo
	}
	return t != nil
}

// TrackName is the thread name shown at a track number.
func (e *Engine) TrackName(id int) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.trackByID(id)
	if t == nil {
		return "", false
	}
	return t.name, true
}

// SilenceTrack drops a track's queued hits and fades notes it is already playing.
func (e *Engine) SilenceTrack(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tracks[name]
	if !ok {
		return false
	}
	q := e.queue[:0]
	for _, h := range e.queue {
		if h.Track != name {
			q = append(q, h)
		}
	}
	e.queue = q
	if t.id > 0 {
		for _, v := range e.voices {
			if v.Track == t.id {
				v.Kill()
			}
		}
	}
	return true
}

// AdjustLevel nudges a track's level by delta (clamped 0..1.5).
func (e *Engine) AdjustLevel(id int, delta float64) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.trackByID(id)
	if t == nil {
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

// Render produces n stereo frames (interleaved int16).
func (e *Engine) Render(n int) []int16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	mix := make([]float32, n*2)
	e.dsend, e.rsend = make([]float32, n*2), make([]float32, n*2)
	e.startHits(n)
	e.clock(n)
	e.mixVoices(mix, n)
	ds := int(e.delaySeconds * SampleRate)
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
	e.prune()
	return out
}

func (e *Engine) anySolo() bool {
	for _, t := range e.tracks {
		if t.solo {
			return true
		}
	}
	return false
}

func audible(t *track, anySolo bool) bool { return !t.mute && (!anySolo || t.solo) }

// startHits turns queued hits due in this block into voices.
func (e *Engine) startHits(n int) {
	end := e.samples + int64(n)
	k := 0
	for k < len(e.queue) && e.queue[k].At < end {
		k++
	}
	if k == 0 {
		return
	}
	due := e.queue[:k]
	e.queue = append([]Hit(nil), e.queue[k:]...)
	solo := e.anySolo()
	for _, h := range due {
		t := e.track(h.Track)
		e.number(t)
		t.hist = append(t.hist, stamp{h.At, h.Label})
		t.lastHit = h.At
		t.src = h.Word
		e.roll = append(e.roll, rollNote(h, t))
		if !audible(t, solo) {
			continue
		}
		off := h.At - e.samples
		if off < 0 {
			off = 0
		}
		if len(e.outputs) > 0 {
			if at := e.wallAt(e.samples + off); !at.IsZero() {
				e.emit(Event{Kind: KindNote, At: at, Track: t.id, Word: h.Word, Params: h.Params,
					Dur: time.Duration(h.Gate) * time.Second / SampleRate, BPM: e.bpm})
			}
		}
		if !e.internal {
			continue
		}
		e.seedCtr++
		v, err := synth.New(h.Word, SampleRate, int(off), h.Gate, h.Params, e.seedCtr)
		if err != nil {
			continue
		}
		v.Track = t.id
		e.voices = append(e.voices, v)
	}
	if over := len(e.voices) - maxVoices; over > 0 {
		for _, v := range e.voices[:over] {
			v.Kill()
		}
	}
}

// clock sends MIDI clock (24 per beat) and a start on the first bar line.
func (e *Engine) clock(n int) {
	if !e.playing || len(e.outputs) == 0 || e.wallBegin.IsZero() {
		return
	}
	a, b := e.beatAt(e.samples)*24, e.beatAt(e.samples+int64(n))*24
	for k := int64(math.Ceil(a)); float64(k) < b; k++ {
		if k <= e.lastClock {
			continue
		}
		e.lastClock = k
		abs := e.sampleAtBeat(float64(k) / 24)
		if e.startPending && k%(24*BeatsPerCycle) == 0 {
			e.startPending = false
			e.emit(Event{Kind: KindStart, At: e.wallAt(abs)})
		}
		e.emit(Event{Kind: KindClock, At: e.wallAt(abs), BPM: e.bpm})
	}
}

func (e *Engine) mixVoices(mix []float32, n int) {
	solo := e.anySolo()
	gains := map[int]float32{auditionTrack: 1}
	for _, t := range e.tracks {
		if t.id > 0 && audible(t, solo) {
			gains[t.id] = float32(t.level)
		}
	}
	live := e.voices[:0]
	for _, v := range e.voices {
		g := gains[v.Track]
		for i := 0; i < n && !v.Done(); i++ {
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

// barSamples is one bar at the current tempo.
func (e *Engine) barSamples() int64 {
	return int64(float64(BeatsPerCycle) * 60 / e.bpm * SampleRate)
}

// prune drops old step history and finished tracks that have gone quiet.
func (e *Engine) prune() {
	keep := e.samples - 3*e.barSamples()
	if len(e.roll) > 0 {
		i := 0
		for i < len(e.roll) && (e.roll[i].End < e.samples-rollKeep*SampleRate || len(e.roll)-i > 4000) {
			i++
		}
		if i > 0 {
			e.roll = append(e.roll[:0:0], e.roll[i:]...)
		}
	}
	for name, t := range e.tracks {
		i := 0
		for i < len(t.hist) && t.hist[i].at < keep {
			i++
		}
		t.hist = t.hist[i:]
		if t.refs == 0 && t.ended >= 0 && e.samples-max(t.ended, t.lastHit) > e.barSamples() {
			delete(e.tracks, name)
		}
	}
}

// StepCell is one cell of the step display.
type StepCell struct {
	On    bool
	Label byte
}

// TrackView is what the UI shows for one track (a live_loop or thread).
type TrackView struct {
	ID    int
	Name  string
	Loop  bool
	Src   string // the last sound it played
	Mute  bool
	Solo  bool
	Level float64
	Steps [StepCells]StepCell
}

// View is a snapshot of the engine for the UI.
type View struct {
	Playing   bool
	BPM       float64
	Cycle     int64 // bar number from 0
	Beat      int   // 1..4
	Phase     float64
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
	v := View{Playing: e.playing, BPM: e.bpm, Hushed: e.hushed, Voices: len(e.voices), Underruns: e.underruns}
	if e.rec != nil {
		v.Recording = true
		v.RecTime = time.Duration(e.rec.Frames()) * time.Second / SampleRate
	}
	heard := e.samples - e.lead
	beat := e.beatAt(heard)
	if !e.playing || beat < 0 {
		beat = 0
	}
	v.Cycle = int64(beat / BeatsPerCycle)
	v.Phase = beat/BeatsPerCycle - float64(v.Cycle)
	v.Beat = int(v.Phase*BeatsPerCycle) + 1
	barStart := float64(v.Cycle) * BeatsPerCycle
	var list []*track
	for _, t := range e.tracks {
		recent := t.lastHit >= 0 && heard-t.lastHit < e.barSamples()
		if t.id > 0 && ((t.loop && t.refs > 0) || recent || (t.refs > 0 && len(t.hist) > 0)) {
			list = append(list, t)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
	for _, t := range list {
		tv := TrackView{ID: t.id, Name: t.name, Loop: t.loop, Src: t.src, Mute: t.mute, Solo: t.solo, Level: t.level}
		for _, s := range t.hist {
			cell := int((e.beatAt(s.at) - barStart) / BeatsPerCycle * StepCells)
			if cell >= 0 && cell < StepCells {
				tv.Steps[cell] = StepCell{On: true, Label: s.label}
			}
		}
		v.Tracks = append(v.Tracks, tv)
	}
	return v
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
			if e.playing { // a gap while nothing plays can't be heard
				e.underruns++
			}
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

// RenderOffline renders seconds of audio without a device. Before each block,
// feed is asked to schedule everything due up to the given time, so code can
// run in lockstep with rendering.
func (e *Engine) RenderOffline(seconds float64, feed func(until float64) error, write func([]int16) error) error {
	e.Play()
	total := int64(seconds * SampleRate)
	for done := int64(0); done < total; {
		n := int64(SampleRate / 100)
		if total-done < n {
			n = total - done
		}
		if feed != nil {
			if err := feed(float64(e.clockSamples()+n)/SampleRate + 0.05); err != nil {
				return err
			}
		}
		if err := write(e.Render(int(n))); err != nil {
			return err
		}
		done += n
	}
	return nil
}

func (e *Engine) clockSamples() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.samples
}

// ---- piano roll ----------------------------------------------------------------

// rollKeep is how many seconds of notes the piano roll remembers.
const rollKeep = 30

// RollNote is one note for the piano roll. Pitched notes have Note > 0;
// drums and samples have Drum set instead.
type RollNote struct {
	At, End int64 // samples
	Note    float64
	Drum    string
	Track   int // track number (for colour)
	Name    string
	Muted   bool
}

func rollNote(h Hit, t *track) RollNote {
	n := RollNote{At: h.At, Track: t.id, Name: t.name, Muted: t.mute}
	if h.Params.Note > 0 && h.Params.Wave != "" {
		n.Note = h.Params.Note
		n.End = h.At + int64(h.Gate) + int64(math.Max(h.Params.Release, 0.03)*SampleRate)
	} else {
		n.Drum = h.Word
		if i := strings.IndexByte(n.Drum, ':'); i > 0 && !strings.HasPrefix(n.Drum, "tom") {
			n.Drum = n.Drum[:i]
		}
		n.End = h.At + SampleRate/20
	}
	return n
}

// RollView is what the piano roll needs: notes around the audible moment and
// how to turn samples into beats.
type RollView struct {
	Now            int64   // the sample being heard
	Beat           float64 // beats since play at Now
	SamplesPerBeat float64
	Playing        bool
	Notes          []RollNote
}

// Roll returns notes from pastBeats before the audible moment to futureBeats
// after it (notes are scheduled slightly ahead, so a few are still to come).
func (e *Engine) Roll(pastBeats, futureBeats float64) RollView {
	e.mu.Lock()
	defer e.mu.Unlock()
	spb := 60 / e.bpm * SampleRate
	now := e.samples - e.lead
	v := RollView{Now: now, SamplesPerBeat: spb, Playing: e.playing}
	if e.playing {
		v.Beat = e.beatAt(now)
	}
	from, to := now-int64(pastBeats*spb), now+int64(futureBeats*spb)
	for _, n := range e.roll {
		if n.End >= from && n.At <= to {
			v.Notes = append(v.Notes, n)
		}
	}
	// notes Ruby has already scheduled but the engine hasn't started yet
	for _, h := range e.queue {
		if h.At > to {
			break
		}
		t := e.track(h.Track)
		if t.id > 0 {
			v.Notes = append(v.Notes, rollNote(h, t))
		}
	}
	return v
}
