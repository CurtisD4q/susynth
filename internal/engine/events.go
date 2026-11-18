package engine

import (
	"time"

	"github.com/susynth/susynth/internal/synth"
)

// Kind says what an Event is.
type Kind int

const (
	KindNote  Kind = iota // a note/hit at At lasting Dur
	KindClock             // one MIDI clock tick (24 per beat) at At
	KindStart             // transport started at At
	KindStop              // transport stopped at At
	KindPanic             // silence everything now (hush)
)

// Event is what external outputs (OSC, MIDI) receive. Times are wall-clock,
// shortly in the future, derived from the audio sample counter, so outputs
// stay locked to the beat even while the audio is buffered.
type Event struct {
	Kind   Kind
	At     time.Time
	Track  int
	Word   string
	Params synth.Params
	Dur    time.Duration
	BPM    float64
}

// Output receives events. Emit is called with the engine lock held, so it
// must return immediately (queue the event and send from another goroutine).
type Output interface {
	Emit(Event)
	Close() error
}

// SetOutputs replaces the external outputs. The caller closes the old ones.
func (e *Engine) SetOutputs(o []Output) {
	e.mu.Lock()
	e.outputs = o
	e.startPending = e.playing && len(o) > 0 // a newly attached device gets a start on the next bar
	e.mu.Unlock()
}

// SetInternal turns the built-in synth on or off (outputs keep receiving events).
func (e *Engine) SetInternal(on bool) {
	e.mu.Lock()
	e.internal = on
	e.mu.Unlock()
}

// SetOutputDelay shifts external events later by d. Use the audio player's
// latency when the internal synth and an external device play together.
func (e *Engine) SetOutputDelay(d time.Duration) {
	e.mu.Lock()
	e.outputDelay = d
	e.mu.Unlock()
}

func (e *Engine) emit(ev Event) {
	for _, o := range e.outputs {
		o.Emit(ev)
	}
}

// wallAt maps an absolute sample index to wall-clock time (zero when the
// engine is not running against a real clock, e.g. offline rendering).
func (e *Engine) wallAt(abs int64) time.Time {
	if e.wallBegin.IsZero() {
		return time.Time{}
	}
	return e.wallBegin.Add(time.Duration(abs-e.samplesAtBegin)*time.Second/SampleRate + e.outputDelay)
}
