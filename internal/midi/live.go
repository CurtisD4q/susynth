package midi

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/susynth/susynth/internal/engine"
)

// Devices lists raw MIDI device nodes found on this machine (Linux ALSA
// rawmidi, OSS). Without cgo there is no sequencer API, so susynth writes MIDI
// bytes straight to the device file.
func Devices() []string {
	var out []string
	for _, pat := range []string{"/dev/snd/midiC*D*", "/dev/midi*", "/dev/umidi*"} {
		m, _ := filepath.Glob(pat)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

type timed struct {
	at   time.Time
	data []byte
}

// Out sends engine events to a MIDI device or file, on time, with clock.
type Out struct {
	f     *os.File
	clock bool
	in    chan engine.Event
	quit  chan struct{}
	done  chan struct{}
	once  sync.Once
}

// Open opens path for writing. With clock, MIDI clock (24 ppqn), start and
// stop messages are sent so external gear follows the tempo.
func Open(path string, clock bool) (*Out, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	o := &Out{f: f, clock: clock, in: make(chan engine.Event, 4096), quit: make(chan struct{}), done: make(chan struct{})}
	go o.loop()
	return o, nil
}

// Emit queues an event; it never blocks (a full queue drops the event).
func (o *Out) Emit(ev engine.Event) {
	select {
	case o.in <- ev:
	default:
	}
}

// Close sends all-notes-off and closes the device.
func (o *Out) Close() error {
	o.once.Do(func() { close(o.quit) })
	<-o.done
	return nil
}

func (o *Out) loop() {
	defer close(o.done)
	var queue []timed
	var held []byte // notes currently on, as channel<<8|note pairs
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	add := func(at time.Time, data ...byte) {
		i := sort.Search(len(queue), func(i int) bool { return queue[i].at.After(at) })
		queue = append(queue, timed{})
		copy(queue[i+1:], queue[i:])
		queue[i] = timed{at, data}
	}
	panic := func() {
		queue = queue[:0]
		for c := byte(0); c < 16; c++ {
			o.f.Write([]byte{0xB0 | c, 123, 0}) // all notes off
		}
		held = held[:0]
	}
	for {
		now := time.Now()
		for len(queue) > 0 && !queue[0].at.After(now) {
			m := queue[0]
			queue = queue[1:]
			o.f.Write(m.data)
			if len(m.data) == 3 && m.data[0]&0xF0 == 0x90 {
				held = append(held, m.data[0]&15, m.data[1])
			}
			if len(m.data) == 3 && m.data[0]&0xF0 == 0x80 {
				for i := 0; i+1 < len(held); i += 2 {
					if held[i] == m.data[0]&15 && held[i+1] == m.data[1] {
						held = append(held[:i], held[i+2:]...)
						break
					}
				}
			}
		}
		wait := time.Hour
		if len(queue) > 0 {
			wait = time.Until(queue[0].at)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-o.quit:
			panic()
			o.f.Close()
			return
		case <-timer.C:
		case ev := <-o.in:
			switch ev.Kind {
			case engine.KindNote:
				note, ch, ok := NoteFor(ev.Word, ev.Track)
				if !ok {
					continue
				}
				vel := byte(Velocity(ev.Params.Gain))
				// a short gap keeps repeated notes from merging into one
				off := ev.At.Add(ev.Dur - 2*time.Millisecond)
				if off.Before(ev.At.Add(time.Millisecond)) {
					off = ev.At.Add(time.Millisecond)
				}
				add(ev.At, 0x90|byte(ch), byte(note), vel)
				add(off, 0x80|byte(ch), byte(note), 0)
			case engine.KindClock:
				if o.clock {
					add(ev.At, 0xF8)
				}
			case engine.KindStart:
				if o.clock {
					add(ev.At, 0xFA)
				}
			case engine.KindStop:
				if o.clock {
					o.f.Write([]byte{0xFC})
				}
			case engine.KindPanic:
				panic()
			}
		}
	}
}
