package synth

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/susynth/susynth/internal/flac"
)

// Sample is a decoded WAV file. Mono files (and stereo files whose two
// channels are the same) have Right nil; stereo files keep both channels.
// Files with more than two channels are mixed to mono.
//
// Loaded samples are resampled to PlayRate, so at normal speed they play
// sample for sample with no interpolation.
type Sample struct {
	Name  string
	Rate  int
	Data  []float32 // the left channel, or the only one
	Right []float32 // the right channel of a stereo sample, else nil

	// oct[k] is the sample band-limited and halved k+1 times, for playing
	// it fast without aliasing (see octaveFor).
	oct []octave

	// A sample from the samples folder is only read when it is first
	// played: until then path is set, Data is nil and frames/fileRate
	// come from the file's header.
	path     string
	frames   int64
	fileRate int
	loadErr  error
	used     int64 // when it was last loaded or played, for freeing memory
}

// Seconds is the sample's length at normal speed.
func (s *Sample) Seconds() float64 {
	if s.Data != nil && s.Rate > 0 {
		return float64(len(s.Data)) / float64(s.Rate)
	}
	if s.fileRate > 0 {
		return float64(s.frames) / float64(s.fileRate)
	}
	return 0
}

type octave struct {
	rate int
	l, r []float32
}

// Stereo reports whether the sample has a separate right channel.
func (s *Sample) Stereo() bool { return s.Right != nil }

// PlayRate is the rate loaded samples are converted to: the engine's.
const PlayRate = 48000

// octaves is how many half-rate copies a loaded sample keeps: enough for
// speed up to about 2^octaves * 1.2 (~9.6x) without aliasing.
const octaves = 3

// floats is how many float32 values s holds, octave copies included.
func (s *Sample) floats() int {
	n := len(s.Data) + len(s.Right)
	for _, o := range s.oct {
		n += len(o.l) + len(o.r)
	}
	return n
}

// prepared estimates floats() after prepare, before doing the work.
func (s *Sample) prepared() int {
	n := float64(len(s.Data)) * PlayRate / float64(s.Rate)
	if s.Stereo() {
		n *= 2
	}
	return int(n * 1.875) // the octave copies add 1/2 + 1/4 + 1/8
}

// prepare converts a freshly parsed sample to PlayRate and builds its
// octave copies. It runs once, when the sample is loaded.
func (s *Sample) prepare() {
	if s.Rate != PlayRate {
		s.Data = resample(s.Data, s.Rate, PlayRate)
		if s.Right != nil {
			s.Right = resample(s.Right, s.Rate, PlayRate)
		}
		s.Rate = PlayRate
	}
	l, r, rate := s.Data, s.Right, s.Rate
	s.oct = nil
	for k := 0; k < octaves && len(l) > 64; k++ {
		l = resample(l, rate, rate/2)
		if r != nil {
			r = resample(r, rate, rate/2)
		}
		rate /= 2
		s.oct = append(s.oct, octave{rate, l, r})
	}
}

// Bank is a named group of samples (a folder). "kick:2" picks the 3rd file.
type Bank struct {
	Name    string
	Samples []*Sample
}

// Seconds is the total length of the bank.
func (b Bank) Seconds() float64 {
	var s float64
	for _, x := range b.Samples {
		s += x.Seconds()
	}
	return s
}

const (
	maxSampleFile  = 32 << 20 // bytes per file
	maxSampleTotal = 64 << 20 // float32 values loaded at once, copies included (~256 MB)
	maxSampleRate  = 384000
	maxSampleFiles = 50000 // files in the samples folder
)

var (
	reg struct {
		sync.RWMutex
		banks map[string]*Bank
	}
	bankName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)
)

// LoadSamples replaces the sample registry with the WAV files under dir:
// every sub-folder is a bank, and loose .wav files become one-file banks.
// Bad files are skipped; the first problem is returned alongside the counts.
func LoadSamples(dir string) (banks, files int, err error) {
	next := map[string]*Bank{}
	var firstErr error
	note := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}
	// Only each file's header is read now; the sound is read when it is
	// first played, so a big library costs nothing until it is used.
	load := func(bank, path string) {
		info, e := os.Stat(path)
		if e != nil || info.IsDir() {
			return
		}
		if info.Size() > maxSampleFile {
			note(fmt.Errorf("%s: larger than %d MB, skipped", filepath.Base(path), maxSampleFile>>20))
			return
		}
		if files >= maxSampleFiles {
			note(fmt.Errorf("more than %d sample files, the rest skipped", maxSampleFiles))
			return
		}
		rate, frames, e := audioHeader(path)
		if e != nil {
			note(fmt.Errorf("%s: %w", filepath.Base(path), e))
			return
		}
		s := &Sample{Name: filepath.Base(path), path: path, fileRate: rate, frames: frames}
		if next[bank] == nil {
			next[bank] = &Bank{Name: bank}
		}
		next[bank].Samples = append(next[bank].Samples, s)
		files++
	}
	isAudio := func(n string) bool { return strings.HasSuffix(n, ".wav") || strings.HasSuffix(n, ".flac") }
	ents, rerr := os.ReadDir(dir)
	if rerr != nil && !os.IsNotExist(rerr) {
		note(rerr)
	}
	for _, e := range ents {
		name := strings.ToLower(e.Name())
		full := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if !bankName.MatchString(name) {
				note(fmt.Errorf("folder %q: bank names use a-z 0-9 _ - only", e.Name()))
				continue
			}
			sub, _ := os.ReadDir(full)
			sort.Slice(sub, func(i, j int) bool { return sub[i].Name() < sub[j].Name() })
			for _, f := range sub {
				if isAudio(strings.ToLower(f.Name())) {
					load(name, filepath.Join(full, f.Name()))
				}
			}
		case isAudio(name):
			bn := strings.TrimSuffix(strings.TrimSuffix(name, ".wav"), ".flac")
			if bankName.MatchString(bn) {
				load(bn, full)
			}
		}
	}
	reg.Lock()
	reg.banks = next
	reg.Unlock()
	fileCache.Lock()
	fileCache.keys = nil
	fileCache.Unlock()
	builtin.Lock()
	builtin.loaded = nil
	builtin.Unlock()
	return len(next), files, firstErr
}

// Banks lists loaded sample banks, sorted by name.
func Banks() []Bank {
	reg.RLock()
	defer reg.RUnlock()
	out := make([]Bank, 0, len(reg.banks))
	for _, b := range reg.banks {
		if !strings.HasPrefix(b.Name, "@") {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupSample finds a sample by bank name and index, reading it from disk
// if it hasn't been yet. It returns nil for no such bank, or a file that
// can't be read.
func lookupSample(name string, idx int) *Sample {
	s := findSample(name, idx)
	if s == nil || s.ensure() != nil {
		return nil
	}
	return s
}

// findSample finds a sample without reading it.
func findSample(name string, idx int) *Sample {
	reg.RLock()
	defer reg.RUnlock()
	b := reg.banks[strings.ToLower(name)]
	if b == nil || len(b.Samples) == 0 {
		return nil
	}
	if idx < 0 {
		idx = 0
	}
	return b.Samples[idx%len(b.Samples)]
}

// maxLoaded is how many float32 values the samples folder may hold in
// memory at once (a variable so tests can make it small).
var maxLoaded = maxSampleTotal

var loaded struct {
	sync.Mutex
	floats int
	clock  int64
	list   []*Sample
}

// ensure reads a lazily listed sample from disk the first time it is
// needed. When too much is loaded, the samples played longest ago are let
// go (voices still playing them keep their copy).
func (s *Sample) ensure() error {
	loaded.Lock()
	defer loaded.Unlock()
	loaded.clock++
	s.used = loaded.clock
	if s.path == "" || s.Data != nil {
		return nil
	}
	if s.loadErr != nil {
		return s.loadErr
	}
	d, err := readAudio(s.path)
	if err != nil {
		s.loadErr = err
		return err
	}
	need := d.floats()
	for loaded.floats+need > maxLoaded && len(loaded.list) > 0 {
		old := 0
		for i, x := range loaded.list {
			if x.used < loaded.list[old].used {
				old = i
			}
		}
		x := loaded.list[old]
		loaded.floats -= x.floats()
		x.Data, x.Right, x.oct, x.Rate = nil, nil, nil, 0
		loaded.list = append(loaded.list[:old], loaded.list[old+1:]...)
	}
	s.Data, s.Right, s.oct, s.Rate = d.Data, d.Right, d.oct, d.Rate
	loaded.floats += need
	loaded.list = append(loaded.list, s)
	return nil
}

// readAudio reads and prepares a .wav or .flac file.
func readAudio(path string) (*Sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".flac") {
		return sampleFromFLAC(b)
	}
	s, err := ParseWAV(b)
	if err != nil {
		return nil, err
	}
	if s.prepared() > maxSampleTotal {
		return nil, errors.New("too long to load")
	}
	s.prepare()
	return s, nil
}

// audioHeader reads a file's sample rate and length without decoding it.
func audioHeader(path string) (rate int, frames int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	head := make([]byte, 64<<10)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if strings.HasSuffix(strings.ToLower(path), ".flac") {
		in, err := flac.ReadInfo(head)
		if err != nil {
			return 0, 0, err
		}
		return in.Rate, in.Frames, nil
	}
	if len(head) < 12 || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0, 0, errors.New("not a WAV file")
	}
	var ch, bits int
	for p := 12; p+8 <= len(head); {
		id := string(head[p : p+4])
		sz := int64(binary.LittleEndian.Uint32(head[p+4:]))
		body := p + 8
		switch id {
		case "fmt ":
			if body+16 > len(head) {
				return 0, 0, errors.New("short fmt chunk")
			}
			ch = int(binary.LittleEndian.Uint16(head[body+2:]))
			rate = int(binary.LittleEndian.Uint32(head[body+4:]))
			bits = int(binary.LittleEndian.Uint16(head[body+14:]))
		case "data":
			if ch < 1 || bits < 8 || rate < 1000 || rate > maxSampleRate {
				return 0, 0, errors.New("unsupported or incomplete WAV")
			}
			if st, err := f.Stat(); err == nil && int64(body)+sz > st.Size() {
				sz = st.Size() - int64(body) // a truncated file plays what is there
			}
			return rate, sz / int64(ch*bits/8), nil
		}
		next := int64(body) + sz + sz&1
		if next > int64(len(head)) {
			break
		}
		p = int(next)
	}
	return 0, 0, errors.New("unsupported or incomplete WAV")
}

// ParseWAV decodes PCM (8/16/24/32-bit) and 32-bit float WAV data at the
// file's own rate, keeping stereo (see Sample). The input is untrusted:
// every length is bounds-checked.
func ParseWAV(b []byte) (*Sample, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var format, ch, bits, rate int
	var data []byte
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		n := int(binary.LittleEndian.Uint32(b[p+4:]))
		body := p + 8
		end := body + n
		if n < 0 || end > len(b) || end < body {
			end = len(b) // truncated final chunk: use what is there
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return nil, errors.New("short fmt chunk")
			}
			format = int(binary.LittleEndian.Uint16(b[body:]))
			ch = int(binary.LittleEndian.Uint16(b[body+2:]))
			rate = int(binary.LittleEndian.Uint32(b[body+4:]))
			bits = int(binary.LittleEndian.Uint16(b[body+14:]))
			if format == 0xFFFE && end-body >= 26 { // extensible: real format in the GUID
				format = int(binary.LittleEndian.Uint16(b[body+24:]))
			}
		case "data":
			data = b[body:end]
		}
		p = end + end&1
	}
	if ch < 1 || ch > 8 || rate < 1000 || rate > maxSampleRate || data == nil {
		return nil, errors.New("unsupported or incomplete WAV")
	}
	if !(format == 1 && (bits == 8 || bits == 16 || bits == 24 || bits == 32)) && !(format == 3 && bits == 32) {
		return nil, fmt.Errorf("unsupported format (%d-bit, type %d)", bits, format)
	}
	bps := bits / 8
	frames := len(data) / (bps * ch)
	read := func(o int) float32 {
		switch {
		case format == 3:
			return math.Float32frombits(binary.LittleEndian.Uint32(data[o:]))
		case bits == 8:
			return (float32(data[o]) - 128) / 128
		case bits == 16:
			return float32(int16(binary.LittleEndian.Uint16(data[o:]))) / 32768
		case bits == 24:
			x := int32(data[o]) | int32(data[o+1])<<8 | int32(data[o+2])<<16
			if x&0x800000 != 0 {
				x |= ^0xFFFFFF
			}
			return float32(x) / 8388608
		}
		return float32(int32(binary.LittleEndian.Uint32(data[o:]))) / 2147483648
	}
	if ch == 2 {
		l, r := make([]float32, frames), make([]float32, frames)
		same := true
		for i := 0; i < frames; i++ {
			o := i * 2 * bps
			l[i], r[i] = read(o), read(o+bps)
			same = same && l[i] == r[i]
		}
		if same { // a mono recording saved as stereo
			r = nil
		}
		return &Sample{Rate: rate, Data: l, Right: r}, nil
	}
	out := make([]float32, frames)
	for i := 0; i < frames; i++ {
		var sum float32
		for c := 0; c < ch; c++ {
			sum += read((i*ch + c) * bps)
		}
		out[i] = sum / float32(ch)
	}
	return &Sample{Rate: rate, Data: out}, nil
}
