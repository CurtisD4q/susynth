package mint

// Panes a program draws itself (Screen.Draw, Pane.Draw).

// drawAt is the Draw for pane index (0 is the screen's own first pane), or
// nil when the pane has rows.
func (s *Screen) drawAt(index int) func(*Paint, bool) {
	if s == nil {
		return nil
	}
	if index <= 0 {
		return s.Draw
	}
	if p := s.paneAt(index); p != nil {
		return p.Draw
	}
	return nil
}

// live reports whether the screen is sent as changed rows rather than whole
// frames: it redraws on its own (Every) or draws a pane itself.
func (s *Screen) live() bool {
	if s == nil {
		return false
	}
	if s.Every > 0 || s.Draw != nil {
		return true
	}
	for _, p := range s.Panes {
		if p != nil && p.Draw != nil {
			return true
		}
	}
	return false
}

// paintCustom runs f on a Paint of rows × width cells and copies it into cv
// from row top, column 1. A focused drawing's cursor is kept for the frame.
func (s *Screen) paintCustom(cv *canvas, f func(*Paint, bool), top, rows, width int, focused bool) {
	if rows < 1 || width < 1 {
		return
	}
	p := NewPaint(width, rows)
	protect(func() { f(p, focused) })
	for y := 0; y < rows; y++ {
		cv.put(top+y, 1, p.row(y))
	}
	if focused && p.cursor {
		s.cursor = move(cv.absRow(top+p.cy), cv.absCol(1+p.cx))
	}
}
