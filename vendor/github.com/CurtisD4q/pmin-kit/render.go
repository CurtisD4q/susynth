package mint

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/CurtisD4q/pmin-base/util"
	"github.com/CurtisD4q/pmin-kit/theme"
)

// C is the active palette. It starts as the theme selected in
// ~/.config/mint-interfaces/theme.conf (the default purple if none),
// quantised to the terminal's probed colour depth when necessary.
//
// The quantiser is not used here: at package initialisation rt.profile has
// not been filled in yet, so quantising would read a colour depth of 0 and
// strip the colour from every role. ScreenEnter re-quantises once the
// profile is known.
var C = basePalette

// basePalette is the palette in use before it is fitted to the terminal:
// the theme file's, or the one SetPalette (a --theme flag, the Theme
// screen, an app's own default) chose. ScreenEnter fits it to the
// terminal; it does not read the theme file again, so a theme chosen
// before the screen opened is kept.
var basePalette = loadPalette()

// paletteWarn is a problem loading the theme or MINT_PALETTE. ScreenEnter
// shows it once. A bad spec used to be ignored, so a launcher's colours
// could fail and the screen would look like the file theme with no hint.
var paletteWarn string

// loadPalette picks the theme from ~/.config/mint-interfaces/theme.conf,
// then applies any colours given in the MINT_PALETTE environment variable
// (see theme.FromSpec), which is how launchers such as Emacs pass their
// own colours in. A spec that does not parse leaves the file theme in
// place and records paletteWarn. A panic falls back to the default theme
// and records the same warning, instead of aborting process startup.
func loadPalette() (p theme.Palette) {
	warn := ""
	defer func() {
		if rec := recover(); rec != nil {
			warn = CleanLine(fmt.Sprint(rec))
			p = theme.Load("default")
		} else if !p.Complete() {
			p = theme.Load("default")
		}
		paletteWarn = warn
	}()
	p = theme.Load("")
	if spec := os.Getenv("MINT_PALETTE"); spec != "" {
		q, err := theme.FromSpec(p, spec)
		if err != nil {
			warn = CleanLine(err.Error())
		} else {
			p = q
		}
	}
	return p
}

// SetPalette applies a palette immediately. It is ignored, and false is
// returned, unless every role is set.
func SetPalette(p theme.Palette) bool {
	if !p.Complete() {
		return false
	}
	basePalette = p
	if screenActive {
		C = rt.quantizePalette(p)
	} else {
		// The terminal is not known yet; ScreenEnter fits it.
		C = p
	}
	return true
}

// VLen and Truncate are the width helpers the layout uses.
var (
	VLen     = util.VLen
	Truncate = util.Truncate
)

// Default footers.
var (
	HomeHints   = " ↑↓ move   ⏎ open   q quit "
	ScreenHints = " ↑↓ move   ←→/Space change   b home   q quit "
	EditHints   = " type   Enter save   Esc cancel "
)

// canvas collects one frame: a clear followed by positioned strings,
// written to the terminal in a single write.
type canvas struct {
	b strings.Builder
	w int

	// A view draws into parent, shifted dx columns right and dy rows down
	// (see view and area).
	parent *canvas
	dx, dy int
}

func begin(h, w int) *canvas {
	c := &canvas{w: w}
	c.b.WriteString(esc + "2J")
	return c
}

func (c *canvas) put(r, col int, s string) {
	if r < 1 || col < 1 || col > c.w {
		return
	}
	// Text is valid UTF-8, so it is measured as the terminal draws it (a
	// stray byte shows as one replacement character), and nothing is drawn
	// past the right edge: the terminal would wrap it onto the next line,
	// over whatever is there.
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	s = clipCells(s, c.w-col+1)
	if c.parent != nil {
		c.parent.put(r+c.dy, col+c.dx, s)
		return
	}
	c.b.WriteString(move(r, col))
	c.b.WriteString(s)
}

// paint writes the frame and parks the (hidden) cursor at the top left.
// Terminals embedded in editors scroll their window to keep the cursor in
// view; leaving it at the bottom right made Neovim's :terminal stay
// scrolled sideways after the window shrank.
func (c *canvas) paint() { showFrame(c.b.String() + esc + "H") }

// frame used to draw a thin border round the whole screen. Screens have
// no border now: only panes (in boxed screens) and pop-ups have one, so it
// draws nothing. It stays so every screen's drawing reads the same.
func (c *canvas) frame(h, w int) {}

// titleRule is the plain divider directly under the title bar. It uses the
// same dim rule as the gaps between sections.
func (c *canvas) titleRule(w int) { paintRule(c, 3, w) }

// ruleStyle is how every dividing line is drawn: the line under the
// title, the lines between panes and above the run bar and footer, and
// Divider rows. It is the theme's accent colour, dimmed, so the lines
// follow the theme without competing with the frame. With no colour
// (NO_COLOR, a monochrome terminal) it is plain.
func ruleStyle() string { return C.Accent + C.Dim }

// paintRule draws a full-width line of the look's rule across the content width (inside
// the frame, stopping before the scrollbar column), in ruleStyle.
func paintRule(c *canvas, row, w int) {
	if c == nil || w < 4 || row < 1 {
		return
	}
	n := w - 3
	if n < 1 {
		return
	}
	c.put(row, 2, ruleStyle()+dividerLine(look.Rule, n)+Reset)
}

func (c *canvas) chromeTitle(w int, title, crumb string) {
	room := w - 6 - VLen(crumb)
	if room < 4 {
		room = 4
	}
	title, crumb = CleanLine(title), CleanLine(crumb)
	c.put(2, 3, C.Accent+C.Bold+Truncate(title, room)+Reset)
	if crumb != "" && w-2-VLen(crumb) > 3 {
		c.put(2, w-2-VLen(crumb), C.Text+crumb+Reset)
	}
}

func (c *canvas) chromeFooter(h, w int, keys string) {
	width := w - 2
	if width < 0 {
		width = 0
	}
	c.put(h-1, 2, C.Dim+strings.Repeat(" ", width)+Reset)
	keys = fitHints(keys, w-3)
	col := w - 1 - VLen(keys)
	if col < 2 {
		col = 2
	}
	c.put(h-1, col, C.Muted+keys+Reset)
}

// fitHints shortens a footer's key hints ("  ↑↓ move   Enter open  ") to
// at most room cells, so they never run past the frame. Whole hints are
// dropped from the left, where the general ones (moving, scrolling) are;
// the screen's own keys and "close" are at the right.
func fitHints(keys string, room int) string {
	if room <= 0 {
		return ""
	}
	if VLen(keys) <= room {
		return keys
	}
	var hints []string
	for _, h := range strings.Split(keys, "  ") {
		if h = strings.TrimSpace(h); h != "" {
			hints = append(hints, h)
		}
	}
	for len(hints) > 1 {
		hints = hints[1:]
		if s := " " + strings.Join(hints, "   ") + " "; VLen(s) <= room {
			return s
		}
	}
	if len(hints) == 1 {
		return Truncate(" "+hints[0]+" ", room)
	}
	return Truncate(keys, room)
}

func (c *canvas) scrollbar(col, top, rows, thumb, tpos int) {
	for k := 0; k < rows; k++ {
		glyph, colour := look.ScrollTrack, C.Dim
		if k >= tpos && k < tpos+thumb {
			glyph, colour = look.ScrollThumb, C.Accent
		}
		c.put(top+k, col, colour+glyph+Reset)
	}
}

func (c *canvas) selBar(r, w int) {
	n := w - 2
	if n < 0 {
		n = 0
	}
	c.put(r, 2, C.SelBg+strings.Repeat(" ", n)+Reset)
}

// sizeWatch notices terminal resizes on idle ticks.
type sizeWatch struct{ h, w int }

func watchSize() *sizeWatch {
	h, w := TermSize()
	return &sizeWatch{h, w}
}

// changed reports whether the size differs from the last call.
func (s *sizeWatch) changed() bool {
	sigResized := rt.consumeResize()
	h, w := TermSize()
	if sigResized || h != s.h || w != s.w {
		s.h, s.w = h, w
		return true
	}
	return false
}

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plainLen(s string) int { return VLen(sgrSeq.ReplaceAllString(s, "")) }

// wrapHead splits the first line of s at a space within width cells.
// It returns the line, the remaining text, and whether a space was used.
// Width is counted in terminal cells, so a line of wide characters (CJK,
// emoji) is cut where it fills the width, not where it has width characters.
func wrapHead(s string, width int) (string, string) {
	chars := util.Chars(s)
	n, used := 0, 0
	for n < len(chars) {
		cw := VLen(chars[n])
		if used+cw > width {
			break
		}
		used += cw
		n++
	}
	if n == 0 && len(chars) > 0 {
		n = 1 // a single character wider than the width still moves on
	}
	head := chars[:n]
	lastSpace := 0
	for i, ch := range head {
		if util.IsSpace(ch) {
			lastSpace = i + 1
		}
	}
	cut, skip := n, n
	if lastSpace > 1 && n < len(chars) {
		cut, skip = lastSpace-1, lastSpace
	}
	if cut > len(head) {
		cut = len(head)
	}
	if skip > len(chars) {
		skip = len(chars)
	}
	return util.TrimRight(strings.Join(head[:cut], "")), util.TrimLeft(strings.Join(chars[skip:], ""))
}

// wrap2 fits s into at most two lines of width cells; the second is
// truncated with "…" if it still does not fit.
func wrap2(s string, width int) [2]string {
	if s == "" {
		return [2]string{"", ""}
	}
	if VLen(s) <= width {
		return [2]string{s, ""}
	}
	l1, l2 := wrapHead(s, width)
	if VLen(l2) > width {
		l2 = Truncate(l2, width)
	}
	return [2]string{l1, l2}
}

// wrapLines word-wraps every paragraph of text to width.
func wrapLines(text string, width int) []string {
	var lines []string
	if text == "" {
		return lines
	}
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		rest := para
		for rest != "" {
			if VLen(rest) <= width {
				lines = append(lines, rest)
				break
			}
			var line string
			line, rest = wrapHead(rest, width)
			lines = append(lines, line)
		}
	}
	return lines
}
