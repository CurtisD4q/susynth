package synth

// Effects that route sound rather than change it.

func init() {
	// scope_out, sound_out and sound_out_stereo send a copy of the sound to a
	// scope or an audio output susynth doesn't have; the sound itself goes on
	// to the mix as it is.
	for _, n := range []string{"scope_out", "sound_out", "sound_out_stereo"} {
		defFX(n, 0, nil, nil, func(f *simpleFX, out, in []float32) { copy(out, in) })
	}

	// record writes what passes through into a buffer, once from the start
	// (RecordBuf with loop 0), and lets the sound go on.
	type rec struct {
		buf *Sample
		pos int
	}
	defFX("record", 0, map[string]float64{"buffer": 0, "seconds": 8},
		func(float64) any { return &rec{} },
		func(f *simpleFX, out, in []float32) {
			s := f.st.(*rec)
			if s.buf == nil {
				s.buf = RegisterBuffer(int(f.g("buffer")), f.g("seconds"))
			}
			for i := 0; i+1 < len(in); i += 2 {
				if s.pos < len(s.buf.Data) {
					s.buf.Data[s.pos], s.buf.Right[s.pos] = in[i], in[i+1]
					s.pos++
				}
			}
			copy(out, in)
		})
}
