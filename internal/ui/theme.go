package ui

// Theme holds every colour the UI uses, so a projector theme is one struct.
type Theme struct {
	Name string

	Base      style // background for the whole screen
	Title     style
	Divider   style
	Text      style
	Muted     style
	Pane      style // pane header
	CursorRow style
	Gutter    style
	GutterOK  style
	GutterErr style
	Comment   style
	Keyword   style
	String    style
	Modifier  style
	ErrRow    style
	Toast     style
	ToastErr  style
	Bar       style // transport bar
	BarKey    style
	Rec       style
	Hush      style
	Pending   style
	StepOn    style
	StepOff   style
	Playhead  style
	MuteTag   style
	SoloTag   style
	LevelOn   style
	LevelOff  style

	CellW int // width of one step cell
	Bold  bool
}

func st(fg, bg int16) style { return style{fg: fg, bg: bg} }

var themes = []*Theme{themeDefault(), themeLarge(), themeContrast()}

func themeByName(n string) *Theme {
	for _, t := range themes {
		if t.Name == n {
			return t
		}
	}
	return themes[0]
}

func nextTheme(cur *Theme) *Theme {
	for i, t := range themes {
		if t == cur {
			return themes[(i+1)%len(themes)]
		}
	}
	return themes[0]
}

func themeDefault() *Theme {
	mint := int16(79)
	return &Theme{
		Name:      "default",
		Base:      st(252, -1),
		Title:     style{fg: mint, bg: -1, bold: true},
		Divider:   st(238, -1),
		Text:      st(252, -1),
		Muted:     st(244, -1),
		Pane:      style{fg: 244, bg: -1, bold: true},
		CursorRow: st(255, 236),
		Gutter:    st(240, -1),
		GutterOK:  st(mint, -1),
		GutterErr: st(203, -1),
		Comment:   st(243, -1),
		Keyword:   style{fg: mint, bg: -1, bold: true},
		String:    st(150, -1),
		Modifier:  st(179, -1),
		ErrRow:    st(255, 124),
		Toast:     st(255, 238),
		ToastErr:  style{fg: 255, bg: 124, bold: true},
		Bar:       st(252, 236),
		BarKey:    st(244, 236),
		Rec:       style{fg: 255, bg: 160, bold: true},
		Hush:      style{fg: 16, bg: 214, bold: true},
		Pending:   style{fg: 16, bg: 179, bold: true},
		StepOn:    style{fg: mint, bg: -1, bold: true},
		StepOff:   st(238, -1),
		Playhead:  style{fg: 16, bg: mint, bold: true},
		MuteTag:   style{fg: 255, bg: 124, bold: true},
		SoloTag:   style{fg: 16, bg: 220, bold: true},
		LevelOn:   st(mint, -1),
		LevelOff:  st(238, -1),
		CellW:     2,
	}
}

// themeLarge widens every step and bolds all text: readable from the back of a room.
func themeLarge() *Theme {
	t := themeDefault()
	t.Name = "large"
	t.CellW = 3
	t.Bold = true
	t.Muted = style{fg: 248, bg: -1}
	t.Text = style{fg: 255, bg: -1, bold: true}
	t.Base = style{fg: 255, bg: -1, bold: true}
	return t
}

// themeContrast is black/white/yellow for washed-out projectors.
func themeContrast() *Theme {
	t := themeDefault()
	t.Name = "contrast"
	t.CellW = 3
	t.Bold = true
	t.Base = style{fg: 15, bg: 0, bold: true}
	t.Title = style{fg: 11, bg: 0, bold: true}
	t.Divider = st(15, 0)
	t.Text = style{fg: 15, bg: 0, bold: true}
	t.Muted = st(250, 0)
	t.Pane = style{fg: 11, bg: 0, bold: true}
	t.CursorRow = style{fg: 15, bg: 19, bold: true}
	t.Gutter = st(250, 0)
	t.GutterOK = style{fg: 10, bg: 0, bold: true}
	t.GutterErr = style{fg: 9, bg: 0, bold: true}
	t.Comment = st(250, 0)
	t.Keyword = style{fg: 11, bg: 0, bold: true}
	t.String = style{fg: 14, bg: 0, bold: true}
	t.Modifier = style{fg: 13, bg: 0, bold: true}
	t.ErrRow = style{fg: 15, bg: 1, bold: true}
	t.Toast = style{fg: 0, bg: 15, bold: true}
	t.ToastErr = style{fg: 15, bg: 1, bold: true}
	t.Bar = style{fg: 0, bg: 15, bold: true}
	t.BarKey = style{fg: 0, bg: 15}
	t.Rec = style{fg: 15, bg: 1, bold: true}
	t.Hush = style{fg: 0, bg: 11, bold: true}
	t.Pending = style{fg: 0, bg: 11, bold: true}
	t.StepOn = style{fg: 10, bg: 0, bold: true}
	t.StepOff = st(244, 0)
	t.Playhead = style{fg: 0, bg: 11, bold: true}
	t.MuteTag = style{fg: 15, bg: 1, bold: true}
	t.SoloTag = style{fg: 0, bg: 11, bold: true}
	t.LevelOn = style{fg: 10, bg: 0, bold: true}
	t.LevelOff = st(244, 0)
	return t
}
