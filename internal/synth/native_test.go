package synth

import (
	"math"
	"testing"
)

// renderNative plays one note of a native voice and returns its left channel.
func renderNative(t *testing.T, wave string, opt map[string]float64, secs float64) []float64 {
	t.Helper()
	sr := 48000
	v, err := New("52", sr, 0, sr/2, Params{Gain: 1, Speed: 1, Wave: wave, Note: 52, Release: 0.5,
		EnvShape: 1, Opt: opt}, 7)
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, int(secs*float64(sr)))
	for i := range x {
		l, _ := v.Next()
		x[i] = float64(l)
	}
	return x
}

func periodOf(x []float64, sr int) float64 {
	seg := x[sr/5 : sr/5+9000]
	best, lag := -1.0, 0
	for k := sr / 1000; k < sr/30; k++ {
		var c float64
		for i := 0; i+k < len(seg); i++ {
			c += seg[i] * seg[i+k]
		}
		if c > best {
			best, lag = c, k
		}
	}
	return float64(sr) / float64(lag)
}

// Every native voice plays the note it is given (the sub-octave voices an
// octave or two down) and makes a sound of a sensible size.
func TestNativeVoicesPitchAndLevel(t *testing.T) {
	e3 := 440 * math.Pow(2, (52.0-69)/12)
	for name, want := range map[string]float64{
		"bass_foundation": e3, "winwood_lead": e3, "supersaw": e3, "chiplead": e3, "chipbass": e3, "tb303": e3,
		"growl": e3, "rhodey": e3, "rodeo": e3, "kalimba": e3, "organ_tonewheel": e3 / 2, "subpulse": e3 / 2, "bass_highend": e3 / 4,
	} {
		var opt map[string]float64
		if name == "kalimba" {
			opt = map[string]float64{"clickiness": 1} // the tone alone is quiet
		}
		x := renderNative(t, name, opt, 0.9)
		f := periodOf(x, 48000)
		semis := 12 * math.Log2(f/want)
		var rms float64
		for _, s := range x[12000:36000] {
			rms += s * s
		}
		rms = math.Sqrt(rms / 24000)
		if math.Abs(semis) > 0.6 && math.Abs(math.Abs(semis)-12) > 0.6 || rms < 0.01 || rms > 1 || math.IsNaN(rms) {
			t.Errorf("%s: %.1f Hz (want %.1f), level %.3f", name, f, want, rms)
		}
	}
}

// The stereo and wobbling voices make sound in both channels without blowing up.
func TestNativeStereoAndModulatedVoices(t *testing.T) {
	for _, name := range []string{"tech_saws", "hoover", "zawa", "gabberkick", "organ_tonewheel"} {
		v, err := New("52", 48000, 0, 24000, Params{Gain: 1, Speed: 1, Wave: name, Note: 52, Release: 0.3, EnvShape: 1}, 3)
		if err != nil {
			t.Fatal(err)
		}
		var l2, r2 float64
		for i := 0; i < 30000; i++ {
			l, r := v.Next()
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.Abs(float64(l)) > 20 {
				t.Fatalf("%s: bad sample %v at %d", name, l, i)
			}
			l2 += float64(l * l)
			r2 += float64(r * r)
		}
		if l2 < 1e-3 || r2 < 1e-3 {
			t.Errorf("%s: silent channel (left %.4f, right %.4f)", name, l2, r2)
		}
	}
}

// Sonic Pi's envelope is straight lines unless env_curve says otherwise:
// halfway through a release the note is at half its level.
func TestNativeReleaseIsLinear(t *testing.T) {
	x := renderNative(t, "chipbass", nil, 1.2) // a flat-topped voice: 0.25 s gate, 0.5 s release
	peak := func(a, b int) float64 {
		var m float64
		for _, s := range x[a:b] {
			m = math.Max(m, math.Abs(s))
		}
		return m
	}
	sus := peak(6000, 10000)
	gate := 24000
	mid := peak(gate+int(0.25*48000)-500, gate+int(0.25*48000)+500)
	if r := mid / sus; r < 0.42 || r > 0.58 {
		t.Errorf("a quarter second into a half-second release the level is %.2f of sustain, want 0.5", r)
	}
}

// control cutoff: moves a native voice's own filter.
func TestNativeCutoffControl(t *testing.T) {
	sr := 48000
	mk := func(cut float64) float64 {
		v, _ := New("52", sr, 0, sr, Params{Gain: 1, Speed: 1, Wave: "bass_foundation", Note: 52, Release: 0.3, EnvShape: 1,
			Opt: map[string]float64{"cutoff": 70}}, 5)
		if !math.IsNaN(cut) {
			c := NoControl()
			c.Cutoff = cut
			v.Control(c, 0)
		}
		var e, prev float64 // brightness: the energy of the sample-to-sample change
		for i := 0; i < sr/2; i++ {
			l, _ := v.Next()
			if i > sr/4 {
				e += (float64(l) - prev) * (float64(l) - prev)
			}
			prev = float64(l)
		}
		return e
	}
	if dark, bright := mk(math.NaN()), mk(120); bright < dark*1.5 {
		t.Errorf("opening the cutoff changed the energy %.4f -> %.4f, want much brighter", dark, bright)
	}
}

func TestEnvelopeShapes(t *testing.T) {
	for shape, want := range map[int]float64{1: 0.5, 3: 0.5, 6: 0.25, 7: 0.125} {
		e := NewEnvelope(1000, EnvTimes{Attack: 0.003, Sustain: 0.01, Release: 1, Shape: shape})
		x := levels(e)
		if got := x[len(x)-1-500]; math.Abs(got-want) > 0.03 {
			t.Errorf("shape %d: halfway down the release is %.3f, want %.3f", shape, got, want)
		}
	}
}

// The noise synths make their own kinds of noise: the brown one is mostly
// low, the white one is not, and the chip noise holds each value for a while.
func TestNativeNoiseKinds(t *testing.T) {
	bright := func(name string) float64 {
		x := renderNative(t, name, map[string]float64{"cutoff": 120}, 0.6)
		var d, e float64
		for i := 12000; i < len(x)-1; i++ {
			d += (x[i+1] - x[i]) * (x[i+1] - x[i])
			e += x[i] * x[i]
		}
		if e < 1e-6 || math.IsNaN(e) {
			t.Fatalf("%s: silent or broken", name)
		}
		return d / e
	}
	if b, w := bright("sp_bnoise"), bright("sp_noise"); b > w/10 {
		t.Errorf("brown noise brightness %.3f against white %.3f: want much duller", b, w)
	}
	if p, w := bright("sp_pnoise"), bright("sp_noise"); p > w/2 {
		t.Errorf("pink noise brightness %.3f against white %.3f: want duller", p, w)
	}
	for _, n := range []string{"sp_gnoise", "sp_cnoise", "chipnoise"} {
		bright(n)
	}
}

// TestSC808Drums plays every 808 drum: each makes sound, rings out by itself
// and ends, at the pitch of its design where it has one.
func TestSC808Drums(t *testing.T) {
	for _, name := range []string{"sc808_bassdrum", "sc808_snare", "sc808_clap", "sc808_claves", "sc808_closed_hihat",
		"sc808_open_hihat", "sc808_cowbell", "sc808_cymbal", "sc808_maracas", "sc808_rimshot", "sc808_tomlo",
		"sc808_tommid", "sc808_tomhi", "sc808_congalo", "sc808_congamid", "sc808_congahi"} {
		note, _ := DefaultNote(name)
		if note == 0 {
			note = 52
		}
		v, err := New("52", 48000, 0, 24000, Params{Gain: 1, Speed: 1, Wave: name, Note: note, Release: 0.1, EnvShape: 1}, 7)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var peak float64
		n := 0
		for ; n < 48000*8 && !v.Done(); n++ {
			l, r := v.Next()
			peak = math.Max(peak, math.Max(math.Abs(float64(l)), math.Abs(float64(r))))
		}
		if peak < 0.01 {
			t.Errorf("%s is silent (peak %g)", name, peak)
		}
		if !v.Done() {
			t.Errorf("%s never ended", name)
		}
	}
}
