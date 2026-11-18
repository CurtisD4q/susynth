package synth

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Sample is a decoded WAV file, mixed to mono.
type Sample struct {
	Name string
	Rate int
	Data []float32
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
		s += float64(len(x.Data)) / float64(x.Rate)
	}
	return s
}

const (
	maxSampleFile  = 32 << 20 // bytes per file
	maxSampleTotal = 64 << 20 // decoded float32 frames overall (~256 MB)
	maxSampleRate  = 384000
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
	total := 0
	load := func(bank, path string) {
		info, e := os.Stat(path)
		if e != nil || info.IsDir() {
			return
		}
		if info.Size() > maxSampleFile {
			note(fmt.Errorf("%s: larger than %d MB, skipped", filepath.Base(path), maxSampleFile>>20))
			return
		}
		b, e := os.ReadFile(path)
		if e != nil {
			note(e)
			return
		}
		s, e := ParseWAV(b)
		if e != nil {
			note(fmt.Errorf("%s: %w", filepath.Base(path), e))
			return
		}
		if total+len(s.Data) > maxSampleTotal {
			note(errors.New("sample memory limit reached, remaining files skipped"))
			return
		}
		total += len(s.Data)
		s.Name = filepath.Base(path)
		if next[bank] == nil {
			next[bank] = &Bank{Name: bank}
		}
		next[bank].Samples = append(next[bank].Samples, s)
		files++
	}
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
				if strings.HasSuffix(strings.ToLower(f.Name()), ".wav") {
					load(name, filepath.Join(full, f.Name()))
				}
			}
		case strings.HasSuffix(name, ".wav"):
			bn := strings.TrimSuffix(name, ".wav")
			if bankName.MatchString(bn) {
				load(bn, full)
			}
		}
	}
	reg.Lock()
	reg.banks = next
	reg.Unlock()
	return len(next), files, firstErr
}

// Banks lists loaded sample banks, sorted by name.
func Banks() []Bank {
	reg.RLock()
	defer reg.RUnlock()
	out := make([]Bank, 0, len(reg.banks))
	for _, b := range reg.banks {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func lookupSample(name string, idx int) *Sample {
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

// ParseWAV decodes PCM (8/16/24/32-bit) and 32-bit float WAV data to mono.
// The input is untrusted: every length is bounds-checked.
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
	out := make([]float32, frames)
	for i := 0; i < frames; i++ {
		var sum float32
		for c := 0; c < ch; c++ {
			o := (i*ch + c) * bps
			var v float32
			switch {
			case format == 3:
				v = math.Float32frombits(binary.LittleEndian.Uint32(data[o:]))
			case bits == 8:
				v = (float32(data[o]) - 128) / 128
			case bits == 16:
				v = float32(int16(binary.LittleEndian.Uint16(data[o:]))) / 32768
			case bits == 24:
				x := int32(data[o]) | int32(data[o+1])<<8 | int32(data[o+2])<<16
				if x&0x800000 != 0 {
					x |= ^0xFFFFFF
				}
				v = float32(x) / 8388608
			default:
				v = float32(int32(binary.LittleEndian.Uint32(data[o:]))) / 2147483648
			}
			sum += v
		}
		out[i] = sum / float32(ch)
	}
	return &Sample{Rate: rate, Data: out}, nil
}
