package ui

// Theme holds every colour the UI uses, so a projector theme is one struct.
type Theme struct {
	Name string

	Base      style // background for the whole screen
	Frame     style // the rounded border around the window
	Rule      style // dividing lines inside the frame
	Title     style
	Divider   style
	Text      style
	Muted     style
	Pane      style // pane header
	CursorRow style
	Select    style // selected text in the editor
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
	Lit       style // code whose sound has just started

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
	// Mint's look: a teal frame, pale-cyan titles, a slate selection bar.
	accent, bright, slate := int16(110), int16(116), int16(237)
	return &Theme{
		Name:      "default",
		Base:      st(253, -1),
		Frame:     st(109, -1),
		Rule:      st(109, -1),
		Title:     style{fg: accent, bg: -1, bold: true},
		Divider:   st(109, -1),
		Text:      st(253, -1),
		Muted:     st(245, -1),
		Pane:      style{fg: accent, bg: -1, bold: true},
		CursorRow: st(255, slate),
		Select:    st(255, 24),
		Gutter:    st(245, -1),
		GutterOK:  st(114, -1),
		GutterErr: st(203, -1),
		Comment:   st(245, -1),
		Keyword:   style{fg: accent, bg: -1, bold: true},
		String:    st(114, -1),
		Modifier:  st(214, -1),
		ErrRow:    st(203, -1),
		Toast:     st(253, -1),
		ToastErr:  style{fg: 203, bg: -1, bold: true},
		Bar:       st(253, -1),
		BarKey:    st(245, -1),
		Rec:       style{fg: 203, bg: -1, bold: true},
		Hush:      style{fg: 214, bg: -1, bold: true},
		Pending:   style{fg: 214, bg: -1, bold: true},
		StepOn:    style{fg: bright, bg: -1, bold: true},
		StepOff:   st(240, -1),
		Playhead:  style{fg: 231, bg: -1, bold: true},
		MuteTag:   style{fg: 203, bg: -1, bold: true},
		SoloTag:   style{fg: 220, bg: -1, bold: true},
		LevelOn:   st(accent, -1),
		LevelOff:  st(240, -1),
		Lit:       style{fg: 16, bg: 180, bold: true},
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
	t.Frame = style{fg: 15, bg: 0, bold: true}
	t.Rule = st(15, 0)
	t.Title = style{fg: 11, bg: 0, bold: true}
	t.Divider = st(15, 0)
	t.Text = style{fg: 15, bg: 0, bold: true}
	t.Muted = st(250, 0)
	t.Pane = style{fg: 11, bg: 0, bold: true}
	t.CursorRow = style{fg: 15, bg: 19, bold: true}
	t.Select = style{fg: 0, bg: 14, bold: true}
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
	t.Lit = style{fg: 0, bg: 11, bold: true}
	return t
}
