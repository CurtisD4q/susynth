package engine

import (
	"math"
	"testing"
)

func runMixer(set MixerSet, l, r float32) (float32, float32) {
	s := newMixerState()
	s.set = s.set.Apply(set)
	buf := make([]float32, 2*4800)
	for i := 0; i < len(buf); i += 2 {
		buf[i], buf[i+1] = l, r
	}
	s.process(buf)
	n := len(buf) - 2
	return buf[n], buf[n+1]
}

func TestMixerDefaultIsTransparent(t *testing.T) {
	l, r := runMixer(nil, 0.3, -0.2)
	if math.Abs(float64(l)-0.3) > 1e-4 || math.Abs(float64(r)+0.2) > 1e-4 {
		t.Fatalf("default mixer changed the sound: %v %v", l, r)
	}
}

func TestMixerVolumeDriveMonoInvert(t *testing.T) {
	if l, _ := runMixer(MixerSet{"amp": 0.5}, 0.4, 0); math.Abs(float64(l)-0.2) > 1e-3 {
		t.Errorf("volume 0.5 of 0.4 = %v", l)
	}
	if l, _ := runMixer(MixerSet{"drive": 1}, 0.1, 0); math.Abs(float64(l)-0.4) > 1e-3 {
		t.Errorf("drive 1 should quadruple 0.1, got %v", l)
	}
	if l, r := runMixer(MixerSet{"force_mono": 1}, 0.4, 0); math.Abs(float64(l)-0.2) > 1e-4 || l != r {
		t.Errorf("mono: %v %v", l, r)
	}
	if l, r := runMixer(MixerSet{"invert_stereo": 1}, 0.4, 0.1); l < 0.09 || l > 0.11 || r < 0.39 {
		t.Errorf("inverted: %v %v", l, r)
	}
}

func TestMixerLimiterHoldsLoudSound(t *testing.T) {
	l, _ := runMixer(MixerSet{"drive": 1}, 3, 0)
	if l > limitCeil || l < limitKnee {
		t.Fatalf("limited level %v, want between %v and %v", l, limitKnee, limitCeil)
	}
}

func TestMixerFiltersAndReset(t *testing.T) {
	// a constant (0 Hz) signal is removed by a high-pass and kept by a low-pass
	if l, _ := runMixer(MixerSet{"hpf": 60}, 0.3, 0); math.Abs(float64(l)) > 0.01 {
		t.Errorf("hpf let DC through: %v", l)
	}
	if l, _ := runMixer(MixerSet{"lpf": 100}, 0.3, 0); math.Abs(float64(l)-0.3) > 0.01 {
		t.Errorf("lpf removed DC: %v", l)
	}
	m := DefaultMixer.Apply(MixerSet{"amp": 0.2, "drive": 1}).Apply(MixerSet{"reset": 1})
	if m != DefaultMixer {
		t.Errorf("reset left %+v", m)
	}
	if d := PreAmpToDrive(DriveToPreAmp(0.3)); math.Abs(d-0.3) > 1e-9 {
		t.Errorf("drive round trip %v", d)
	}
}
