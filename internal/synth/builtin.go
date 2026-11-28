package synth

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/susynth/susynth/internal/flac"
)

// Sonic Pi's own samples (CC0, see sonicpi_samples/README.md), so code that
// uses :loop_amen or :ambi_choir plays the real sound. They are decoded the
// first time they are played.
//
//go:embed sonicpi_samples/*.flac
var builtinFS embed.FS

var builtin struct {
	sync.Mutex
	info   map[string]flac.Info // name -> stream info, read once
	loaded map[string]string    // name -> registry key, cleared by LoadSamples
	failed map[string]error
}

func builtinInfo() map[string]flac.Info {
	builtin.Lock()
	defer builtin.Unlock()
	if builtin.info != nil {
		return builtin.info
	}
	builtin.info = map[string]flac.Info{}
	ents, _ := fs.ReadDir(builtinFS, "sonicpi_samples")
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".flac")
		b, err := builtinFS.ReadFile(path.Join("sonicpi_samples", e.Name()))
		if err != nil {
			continue
		}
		if in, err := flac.ReadInfo(b); err == nil {
			builtin.info[name] = in
		}
	}
	return builtin.info
}

// BuiltinSamples lists the Sonic Pi sample names susynth carries.
func BuiltinSamples() []string {
	in := builtinInfo()
	out := make([]string, 0, len(in))
	for n := range in {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// builtinKey decodes a built-in sample (once) and returns its registry key,
// or "" if there is no such sample.
func builtinKey(name string) (string, error) {
	if _, ok := builtinInfo()[name]; !ok {
		return "", nil
	}
	builtin.Lock()
	defer builtin.Unlock()
	if k, ok := builtin.loaded[name]; ok {
		return k, nil
	}
	if err := builtin.failed[name]; err != nil {
		return "", err
	}
	s, err := decodeBuiltin(name)
	if err != nil {
		if builtin.failed == nil {
			builtin.failed = map[string]error{}
		}
		builtin.failed[name] = err
		return "", err
	}
	key := "@sp." + name
	reg.Lock()
	if reg.banks == nil {
		reg.banks = map[string]*Bank{}
	}
	reg.banks[key] = &Bank{Name: key, Samples: []*Sample{s}}
	reg.Unlock()
	if builtin.loaded == nil {
		builtin.loaded = map[string]string{}
	}
	builtin.loaded[name] = key
	return key, nil
}

func decodeBuiltin(name string) (*Sample, error) {
	b, err := builtinFS.ReadFile(path.Join("sonicpi_samples", name+".flac"))
	if err != nil {
		return nil, err
	}
	s, err := sampleFromFLAC(b)
	if err != nil {
		return nil, fmt.Errorf("sample :%s: %w", name, err)
	}
	s.Name = name
	return s, nil
}

// sampleFromFLAC decodes FLAC data into a prepared Sample, keeping stereo
// the way ParseWAV does.
func sampleFromFLAC(b []byte) (*Sample, error) {
	st, err := flac.Decode(b)
	if err != nil {
		return nil, err
	}
	if st.Rate < 1000 || st.Rate > maxSampleRate || len(st.Samples) == 0 {
		return nil, fmt.Errorf("unsupported FLAC (%d Hz)", st.Rate)
	}
	scale := 1 / float32(int64(1)<<(st.Bits-1))
	conv := func(c []int32) []float32 {
		out := make([]float32, len(c))
		for i, x := range c {
			out[i] = float32(x) * scale
		}
		return out
	}
	s := &Sample{Rate: st.Rate}
	switch len(st.Samples) {
	case 1:
		s.Data = conv(st.Samples[0])
	case 2:
		l, r := st.Samples[0], st.Samples[1]
		same := len(l) == len(r)
		for i := 0; same && i < len(l); i++ {
			same = l[i] == r[i]
		}
		s.Data = conv(l)
		if !same {
			s.Right = conv(r)
		}
	default:
		n := len(st.Samples[0])
		s.Data = make([]float32, n)
		for _, c := range st.Samples {
			for i := 0; i < n && i < len(c); i++ {
				s.Data[i] += float32(c[i]) * scale / float32(len(st.Samples))
			}
		}
	}
	if s.prepared() > maxSampleTotal {
		return nil, fmt.Errorf("too long to load")
	}
	s.prepare()
	return s, nil
}

// SampleSeconds is how long a sample name plays at rate 1, if it is known
// without loading it: a built-in Sonic Pi sample or a loaded bank.
func SampleSeconds(name string) (float64, bool) {
	if s := findSample(name, 0); s != nil && s.Seconds() > 0 {
		return s.Seconds(), true
	}
	if in, ok := builtinInfo()[name]; ok && in.Frames > 0 {
		return in.Seconds(), true
	}
	return 0, false
}

// SampleTable is the sample list Ruby gets: every name, with its length in
// seconds where known (name=seconds).
func SampleTable() []string {
	names := SampleNames()
	out := make([]string, len(names))
	for i, n := range names {
		if s, ok := SampleSeconds(n); ok {
			out[i] = fmt.Sprintf("%s=%.5f", n, s)
		} else {
			out[i] = n
		}
	}
	return out
}
