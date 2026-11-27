package mint

import (
	"fmt"
	"strings"

	"github.com/CurtisD4q/pmin-base/util"
)

// Paint is a grid of cells to draw on: text, fills and box lines that join
// where they meet. A canvas view hands one to its Draw function each frame.
// Text is cleaned before it is drawn, so text from a program or a file name
// cannot change the terminal.
type Paint struct {
	w, h  int
	ch    []rune // 0: empty; contWide: the second half of a wide character
	role  []Role
	lines []uint8 // box-line directions (lineUp, lineDown...)
	lrole []Role
	ink   []Ink // a cell's own colours, where inked is set (made on first use)
	inked []bool

	cx, cy int // where the terminal's cursor goes, when cursor is set
	cursor bool
}

// Colour is an xterm 256-colour number for Ink, made with Xterm. The zero
// Colour is the terminal's own colour.
type Colour int16

// Xterm is colour n (0-255) of the xterm 256-colour palette.
func Xterm(n int) Colour { return Colour(max(0, min(255, n)) + 1) }

// Ink is a cell's own colours and weight, for a drawing that needs more
// than the roles: code with its syntax colours, a piano roll with a colour
// per track. On a 16-colour terminal the colours are the nearest of the 16;
// with NO_COLOR they are left out and only the weight is kept.
type Ink struct {
	Fg, Bg                        Colour
	Bold, Dim, Reverse, Underline bool
}

func (k Ink) sgr() string {
	var b strings.Builder
	b.WriteString(Reset)
	if k.Bold {
		b.WriteString(esc + "1m")
	}
	if k.Dim {
		b.WriteString(esc + "2m")
	}
	if k.Underline {
		b.WriteString(esc + "4m")
	}
	if k.Reverse {
		b.WriteString(esc + "7m")
	}
	if C.Text == "" {
		return b.String() // no colour
	}
	full := strings.Contains(C.Text, ";5;") || strings.Contains(C.Text, ";2;")
	colour := func(c Colour, bg bool) {
		if c <= 0 {
			return
		}
		n := int(c) - 1
		switch {
		case full && bg:
			fmt.Fprintf(&b, esc+"48;5;%dm", n)
		case full:
			fmt.Fprintf(&b, esc+"38;5;%dm", n)
		default:
			b.WriteString(ansi16From256(n, bg))
		}
	}
	colour(k.Fg, false)
	colour(k.Bg, true)
	return b.String()
}

// SetInk puts one character at x, y in its own colours.
func (p *Paint) SetInk(x, y int, r rune, k Ink) {
	if !p.in(x, y) {
		return
	}
	p.Set(x, y, r, RoleText)
	p.setInk(y*p.w+x, k)
}

func (p *Paint) setInk(i int, k Ink) {
	if p.ink == nil {
		p.ink, p.inked = make([]Ink, len(p.ch)), make([]bool, len(p.ch))
	}
	p.ink[i], p.inked[i] = k, true
}

// TextInk writes s from x, y in at most width cells in its own colours,
// ending it with "…" when it is cut, and returns the cells it took.
func (p *Paint) TextInk(x, y, width int, s string, k Ink) int {
	n := p.TextIn(x, y, width, s, RoleText)
	for c := max(0, x); c < min(p.w, x+n); c++ {
		if p.in(c, y) {
			p.setInk(y*p.w+c, k)
		}
	}
	return n
}

// FillInk fills the cells from x0, y0 up to but not including x1, y1 with
// spaces in k: a background band, such as the line the cursor is on.
func (p *Paint) FillInk(x0, y0, x1, y1 int, k Ink) {
	for y := max(0, y0); y < min(p.h, y1); y++ {
		for x := max(0, x0); x < min(p.w, x1); x++ {
			p.SetInk(x, y, ' ', k)
		}
	}
}

// SetCursor shows the terminal's cursor at x, y, for a drawing you can
// type into. Without it the cursor is hidden.
func (p *Paint) SetCursor(x, y int) {
	if p.in(x, y) {
		p.cx, p.cy, p.cursor = x, y, true
	}
}

const contWide = -1

const (
	lineUp uint8 = 1 << iota
	lineDown
	lineLeft
	lineRight
)

// NewPaint returns an empty grid of w × h cells.
func NewPaint(w, h int) *Paint {
	w, h = max(0, w), max(0, h)
	n := w * h
	return &Paint{w: w, h: h, ch: make([]rune, n), role: make([]Role, n), lines: make([]uint8, n), lrole: make([]Role, n)}
}

// Size is the grid's width and height in cells.
func (p *Paint) Size() (w, h int) { return p.w, p.h }

func (p *Paint) in(x, y int) bool { return x >= 0 && y >= 0 && x < p.w && y < p.h }

// Set puts one character at x, y. Control characters become spaces.
func (p *Paint) Set(x, y int, r rune, role Role) {
	if !p.in(x, y) {
		return
	}
	if r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) {
		r = ' '
	}
	i := y*p.w + x
	p.ch[i], p.role[i] = r, role
	if p.inked != nil {
		p.inked[i] = false
	}
}

// Text writes s from x, y, stopping at the right edge, and returns the
// cells it took. Wide characters take two cells.
func (p *Paint) Text(x, y int, s string, role Role) int {
	return p.TextIn(x, y, p.w-x, s, role)
}

// TextIn writes s from x, y in at most width cells, ending it with "…" when
// it is cut, and returns the cells it took.
func (p *Paint) TextIn(x, y, width int, s string, role Role) int {
	if y < 0 || y >= p.h || width <= 0 {
		return 0
	}
	t := CleanLineVisible(s)
	if VLen(t) > width {
		t = util.Truncate(t, width)
	}
	used := 0
	for _, r := range t {
		cw := util.RuneWidth(r)
		if cw <= 0 {
			continue
		}
		if used+cw > width {
			break
		}
		if p.in(x+used, y) {
			p.Set(x+used, y, r, role)
			if cw == 2 && p.in(x+used+1, y) {
				p.ch[y*p.w+x+used+1] = contWide
			} else if cw == 2 {
				p.ch[y*p.w+x+used] = ' ' // no room for its second half
			}
		}
		used += cw
	}
	return used
}

// Fill sets every cell from x0, y0 up to but not including x1, y1.
func (p *Paint) Fill(x0, y0, x1, y1 int, r rune, role Role) {
	for y := max(0, y0); y < min(p.h, y1); y++ {
		for x := max(0, x0); x < min(p.w, x1); x++ {
			p.Set(x, y, r, role)
		}
	}
}

// Shade changes the role of the cells from x0, y0 up to but not including
// x1, y1, keeping what is drawn there: to highlight a selection.
func (p *Paint) Shade(x0, y0, x1, y1 int, role Role) {
	for y := max(0, y0); y < min(p.h, y1); y++ {
		for x := max(0, x0); x < min(p.w, x1); x++ {
			i := y*p.w + x
			p.role[i], p.lrole[i] = role, role
		}
	}
}

// Box draws lines along the cells from x0, y0 to x1, y1, both included.
// Boxes that share an edge share its line, and where lines meet they join
// (┬ ├ ┼), so tiles drawn side by side make one clean grid. Text drawn in a
// cell is shown over its line.
func (p *Paint) Box(x0, y0, x1, y1 int, role Role) {
	if x1 < x0 || y1 < y0 {
		return
	}
	p.hline(x0, x1, y0, role)
	p.hline(x0, x1, y1, role)
	p.vline(x0, y0, y1, role)
	p.vline(x1, y0, y1, role)
}

func (p *Paint) mark(x, y int, bits uint8, role Role) {
	if p.in(x, y) {
		i := y*p.w + x
		p.lines[i] |= bits
		p.lrole[i] = role
	}
}

func (p *Paint) hline(x0, x1, y int, role Role) {
	for x := x0; x < x1; x++ {
		p.mark(x, y, lineRight, role)
		p.mark(x+1, y, lineLeft, role)
	}
}

func (p *Paint) vline(x, y0, y1 int, role Role) {
	for y := y0; y < y1; y++ {
		p.mark(x, y, lineDown, role)
		p.mark(x, y+1, lineUp, role)
	}
}

var lineGlyphs = [16]rune{
	0:                                        ' ',
	lineUp:                                   '│',
	lineDown:                                 '│',
	lineUp | lineDown:                        '│',
	lineLeft:                                 '─',
	lineRight:                                '─',
	lineLeft | lineRight:                     '─',
	lineDown | lineRight:                     '┌',
	lineDown | lineLeft:                      '┐',
	lineUp | lineRight:                       '└',
	lineUp | lineLeft:                        '┘',
	lineUp | lineDown | lineRight:            '├',
	lineUp | lineDown | lineLeft:             '┤',
	lineLeft | lineRight | lineDown:          '┬',
	lineLeft | lineRight | lineUp:            '┴',
	lineUp | lineDown | lineLeft | lineRight: '┼',
}

// Cell returns what is shown at x, y and its role: for tests, and for
// programs that check their own drawing.
func (p *Paint) Cell(x, y int) (rune, Role) {
	if !p.in(x, y) {
		return ' ', RoleText
	}
	i := y*p.w + x
	switch {
	case p.ch[i] == contWide:
		return 0, p.role[i]
	case p.ch[i] != 0:
		return p.ch[i], p.role[i]
	case p.lines[i] != 0:
		return lineGlyphs[p.lines[i]], p.lrole[i]
	}
	return ' ', RoleText
}

// Plain returns the grid as text, one line per row, without styling.
func (p *Paint) Plain() string {
	var b strings.Builder
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			if r, _ := p.Cell(x, y); r != 0 {
				b.WriteRune(r)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// row renders one row with the palette's styles, in runs of one role.
func (p *Paint) row(y int) string {
	var b strings.Builder
	var run strings.Builder
	cur, curInk, inked := RoleText, Ink{}, false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		switch {
		case inked:
			b.WriteString(curInk.sgr() + run.String() + Reset)
		case cur == RoleText:
			b.WriteString(C.Text + run.String() + Reset)
		default:
			b.WriteString(roleStyle(cur) + run.String() + Reset)
		}
		run.Reset()
	}
	for x := 0; x < p.w; x++ {
		r, role := p.Cell(x, y)
		if r == 0 {
			continue
		}
		i := y*p.w + x
		hasInk := p.inked != nil && p.inked[i] && p.ch[i] != 0
		var k Ink
		if hasInk {
			k = p.ink[i]
		}
		if role != cur || hasInk != inked || k != curInk {
			flush()
			cur, inked, curInk = role, hasInk, k
		}
		run.WriteRune(r)
	}
	flush()
	return b.String()
}

// CanvasKey is a key a canvas view handles.
type CanvasKey struct {
	Key  string // as reported by the terminal: "up", "enter", "\r", "o"…
	Hint string // shown in the footer, such as "o open"; empty to hide it
	// Do runs when the key is pressed. It may open dialogs and change
	// screens through c. Returning true redraws.
	Do func(c *Ctx) bool
}

// CanvasOpts configures ViewCanvas.
type CanvasOpts struct {
	Title string
	// Crumb, if set, is shown at the top right.
	Crumb func() string
	// Draw paints the view into p, which is the space inside the frame. It
	// is called for every frame, after a resize, and after a key or click
	// that returns true.
	Draw func(p *Paint)
	Keys []CanvasKey
	// Click is told about a click inside the drawing, in its cells; it
	// returns true to redraw.
	Click func(c *Ctx, x, y int) bool
	// Poll runs about ten times a second and returns true to redraw, for a
	// view of something still changing. Keep it quick.
	Poll   func() bool
	Status func() []Badge
}

// ViewCanvas returns a full-screen view drawn cell by cell, for what is not
// lines of text: maps, charts, boards. b or Esc closes it and q quits;
// every other key is the program's.
func ViewCanvas(opts CanvasOpts) *Screen {
	scr := &Screen{Title: opts.Title, Status: opts.Status}
	scr.customRun = func() screenResult { return runViewCanvas(scr, opts) }
	return scr
}

// canvasArea is where a canvas view's drawing goes on a terminal of h × w:
// inside the frame and margins, between the title rule and the footer.
func canvasArea(h, w int) (top, left, rows, cols int) {
	return 4, 3, max(0, h-5), max(0, w-4)
}

// canvasKey handles one key or click, and says whether to redraw and
// whether the screen is left.
func canvasKey(opts CanvasOpts, k *Key, h, w int) (dirty bool, res *screenResult) {
	c := &Ctx{nav: &navReq{}}
	switch {
	case k.Mouse != nil:
		if k.Mouse.Click && opts.Click != nil {
			top, left, rows, cols := canvasArea(h, w)
			x, y := k.Mouse.Col-left, k.Mouse.Row-top
			if x >= 0 && y >= 0 && x < cols && y < rows {
				protect(func() { dirty = opts.Click(c, x, y) })
			}
		}
	default:
		handled := false
		for _, ck := range opts.Keys {
			if k.Name == ck.Key && ck.Do != nil {
				protect(func() { dirty = ck.Do(c) })
				handled = true
				break
			}
		}
		if !handled {
			switch k.Name {
			case "b", "esc":
				return false, &screenResult{op: "back"}
			case "q", "\x03":
				return false, &screenResult{op: "quit"}
			}
		}
	}
	switch c.nav.op {
	case "push":
		return dirty, &screenResult{op: "push", screen: c.nav.screen}
	case "pop":
		return dirty, &screenResult{op: "back"}
	case "quit":
		return dirty, &screenResult{op: "quit"}
	}
	return dirty, nil
}

func canvasHints(opts CanvasOpts) string {
	var b strings.Builder
	for _, k := range opts.Keys {
		if k.Hint != "" {
			b.WriteString(" " + k.Hint + "  ")
		}
	}
	b.WriteString(" b close ")
	return b.String()
}

func runViewCanvas(screen *Screen, opts CanvasOpts) screenResult {
	footSig := ""
	statusFn := func() []Badge {
		if screen.Status != nil {
			return callStatus(screen.Status)
		}
		return callStatus(screen.appStatus)
	}
	hints := canvasHints(opts)

	draw := func(h, w int) {
		cv := begin(h, w)
		if w < 48 || h < 12 {
			cv.frame(h, w)
			cv.put(2, 3, C.Amber+"Terminal too small"+Reset)
			cv.put(3, 3, C.Muted+fmt.Sprintf("need 48x12, have %dx%d", w, h)+Reset)
			cv.paint()
			return
		}
		cv.frame(h, w)
		crumb := ""
		if opts.Crumb != nil {
			protect(func() { crumb = opts.Crumb() })
		}
		cv.chromeTitle(w, screen.Title, crumb)
		cv.titleRule(w)
		top, left, rows, cols := canvasArea(h, w)
		p := NewPaint(cols, rows)
		if opts.Draw != nil {
			protect(func() { opts.Draw(p) })
		}
		for y := 0; y < rows; y++ {
			cv.put(top+y, left, p.row(y))
		}
		badges := statusFn()
		cv.footer(h, w, hints, badges)
		footSig = footerSig(badges)
		cv.paint()
	}

	h, w := TermSize()
	draw(h, w)
	sc := 0
	for {
		k := readNav(false)
		dirty := false
		if k == nil {
			if opts.Poll != nil {
				changed := false
				protect(func() { changed = opts.Poll() })
				dirty = changed
			}
			sc++
			if rt.consumeResize() || sc >= 2 {
				sc = 0
				if nh, nw := TermSize(); nh != h || nw != w {
					h, w = nh, nw
					dirty = true
				}
			}
			if footerSig(statusFn()) != footSig {
				dirty = true
			}
		} else {
			var res *screenResult
			dirty, res = canvasKey(opts, k, h, w)
			if res != nil {
				return *res
			}
		}
		if dirty {
			draw(h, w)
		}
	}
}
