package synth

import "math"

// Equalisers.

func init() {
	// MidEQ: the sound plus (gain-1) times a band-pass of unity gain at the centre.
	defFX("band_eq", 1, map[string]float64{"freq": 100, "res": 0.6, "db": 0.6},
		func(float64) any { return &[2]biq{} },
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*[2]biq)
			w := 2 * math.Pi * math.Min(midicps(f.g("freq")), f.sr*0.49) / f.sr
			al := math.Sin(w) * math.Max(linlin(f.g("res"), 1, 0, 0, 1), 0.001) / 2
			var q biq
			q.set(al, 0, -al, 1+al, -2*math.Cos(w), 1-al)
			for i := range st {
				z1, z2 := st[i].z1, st[i].z2
				st[i] = q
				st[i].z1, st[i].z2 = z1, z2
			}
			g := math.Pow(10, f.g("db")/20) - 1
			stereo(out, in, func(ch int, x float64) float64 { return x + g*st[ch].run(x) })
		})

	type eqSt struct{ b [2][5]biq }
	defFX("eq", 1, map[string]float64{
		"low_shelf": 0, "low_shelf_note": 43.349957, "low_shelf_slope": 1,
		"low": 0, "low_note": 59.2130948, "low_q": 0.6,
		"mid": 0, "mid_note": 83.2130948, "mid_q": 0.6,
		"high": 0, "high_note": 104.9013539, "high_q": 0.6,
		"high_shelf": 0, "high_shelf_note": 114.2326448, "high_shelf_slope": 1},
		func(float64) any { return &eqSt{} },
		func(f *simpleFX, out, in []float32) {
			st := f.st.(*eqSt)
			hq := 1 / math.Max(0.001, f.g("high_q"))
			lq := 1 / math.Max(0.001, f.g("low_q"))
			sec := [5]biq{
				lowShelf(midicps(f.g("low_shelf_note")), f.g("low_shelf_slope"), f.g("low_shelf")*15, f.sr),
				peakEQ(midicps(f.g("low_note")), lq, f.g("low")*15, f.sr),
				peakEQ(midicps(f.g("mid_note")), hq, f.g("mid")*15, f.sr), // Sonic Pi gives the mid band the high band's q
				peakEQ(midicps(f.g("high_note")), hq, f.g("high")*15, f.sr),
				hiShelf(midicps(f.g("high_shelf_note")), f.g("high_shelf_slope"), f.g("high_shelf")*15, f.sr),
			}
			for ch := range st.b {
				for k := range sec {
					z1, z2 := st.b[ch][k].z1, st.b[ch][k].z2
					st.b[ch][k] = sec[k]
					st.b[ch][k].z1, st.b[ch][k].z2 = z1, z2
				}
			}
			stereo(out, in, func(ch int, x float64) float64 {
				for k := range st.b[ch] {
					x = st.b[ch][k].run(x)
				}
				return x
			})
		})
}
