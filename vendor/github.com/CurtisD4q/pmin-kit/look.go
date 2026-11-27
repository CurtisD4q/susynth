package mint

// Looks: the shapes Mint draws with, chosen the way a theme chooses the
// colours. A look sets the frame around the window, the dividing lines,
// the mark beside the selected row, the mark before a section's name and
// the scrollbar. It changes no layout: every look draws one cell where
// another does, so screens line up the same in all of them.
//
// The look comes from, in order: --look NAME for one run, the look saved
// in theme.conf ("look = square"), the app's own App.Look, then "rounded",
// which is how Mint has always looked.

import (
	"strings"

	"github.com/CurtisD4q/pmin-kit/theme"
)

// Look is a set of glyphs. Each field is one terminal cell wide.
type Look struct {
	Name string
	Desc string // a few words for the Theme screen

	// The frame around the window: its four corners, its top and bottom
	// edge, and its sides.
	TopLeft, TopRight, BottomLeft, BottomRight string
	Edge, Side                                 string

	Rule     string // dividing lines: under the title, between sections
	PaneLine string // the line between panes side by side
	Cursor   string // beside the selected row, and a focused pane's name
	Section  string // before a section's and an unfocused pane's name

	ScrollTrack, ScrollThumb string
}

// Looks lists the built-in looks, the default first.
var Looks = []Look{
	{Name: "rounded", Desc: "soft rounded corners (Mint's own)",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯", Edge: "─", Side: "│",
		Rule: "─", PaneLine: "│", Cursor: "▸", Section: "┄", ScrollTrack: "│", ScrollThumb: "█"},
	{Name: "square", Desc: "crisp square corners",
		TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘", Edge: "─", Side: "│",
		Rule: "─", PaneLine: "│", Cursor: "›", Section: "▪", ScrollTrack: "│", ScrollThumb: "┃"},
	{Name: "double", Desc: "a double-lined frame, like a classic panel",
		TopLeft: "╔", TopRight: "╗", BottomLeft: "╚", BottomRight: "╝", Edge: "═", Side: "║",
		Rule: "─", PaneLine: "│", Cursor: "▶", Section: "◆", ScrollTrack: "│", ScrollThumb: "█"},
	{Name: "heavy", Desc: "bold, thick lines",
		TopLeft: "┏", TopRight: "┓", BottomLeft: "┗", BottomRight: "┛", Edge: "━", Side: "┃",
		Rule: "━", PaneLine: "┃", Cursor: "▶", Section: "■", ScrollTrack: "┃", ScrollThumb: "█"},
	{Name: "dashed", Desc: "light dashed lines",
		TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘", Edge: "╌", Side: "╎",
		Rule: "╌", PaneLine: "╎", Cursor: "›", Section: "·", ScrollTrack: "╎", ScrollThumb: "┃"},
	{Name: "minimal", Desc: "no frame, only quiet dividers",
		TopLeft: " ", TopRight: " ", BottomLeft: " ", BottomRight: " ", Edge: " ", Side: " ",
		Rule: "─", PaneLine: "│", Cursor: "›", Section: "·", ScrollTrack: " ", ScrollThumb: "▐"},
	{Name: "ascii", Desc: "plain ASCII, for any terminal or font",
		TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+", Edge: "-", Side: "|",
		Rule: "-", PaneLine: "|", Cursor: ">", Section: "#", ScrollTrack: "|", ScrollThumb: "#"},
}

// LookNames lists the built-in looks' names.
func LookNames() []string {
	out := make([]string, len(Looks))
	for i, l := range Looks {
		out[i] = l.Name
	}
	return out
}

// LookByName finds a built-in look; case and spaces do not matter.
func LookByName(name string) (Look, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, l := range Looks {
		if l.Name == name {
			return l, true
		}
	}
	return Look{}, false
}

// look is the look being drawn.
var look = Looks[0]

// shownLook is the look on screen when it is not the saved one (set by
// --look or a preview on the Theme screen), or "".
var shownLook string

// CurrentLook returns the look being drawn.
func CurrentLook() Look { return look }

// SetLook draws with l from the next frame. A field left empty keeps the
// default look's glyph, so an app can change only what it wants. A field
// wider than one cell is refused, since it would push the layout out of
// line, and the default glyph is used instead.
func SetLook(l Look) {
	base := Looks[0]
	pick := func(v, d string) string {
		if v == "" || VLen(v) != 1 {
			return d
		}
		return v
	}
	l.TopLeft = pick(l.TopLeft, base.TopLeft)
	l.TopRight = pick(l.TopRight, base.TopRight)
	l.BottomLeft = pick(l.BottomLeft, base.BottomLeft)
	l.BottomRight = pick(l.BottomRight, base.BottomRight)
	l.Edge = pick(l.Edge, base.Edge)
	l.Side = pick(l.Side, base.Side)
	l.Rule = pick(l.Rule, base.Rule)
	l.PaneLine = pick(l.PaneLine, base.PaneLine)
	l.Cursor = pick(l.Cursor, base.Cursor)
	l.Section = pick(l.Section, base.Section)
	l.ScrollTrack = pick(l.ScrollTrack, base.ScrollTrack)
	l.ScrollThumb = pick(l.ScrollThumb, base.ScrollThumb)
	look = l
}

// applyLook shows the named look now, without saving it.
func applyLook(name string) bool {
	l, ok := LookByName(name)
	if !ok {
		return false
	}
	SetLook(l)
	shownLook = l.Name
	return true
}

// savedLook is the look theme.conf selects, when it names a built-in one.
func savedLook() string {
	if _, ok := LookByName(theme.LookName()); ok {
		return theme.LookName()
	}
	return ""
}

// startLook sets the look an app starts with: the saved one, else the
// app's own (a built-in name), else the default.
func startLook(appLook string) {
	switch {
	case savedLook() != "":
		applyLook(savedLook())
	case appLook != "":
		if !applyLook(appLook) {
			SetLook(Looks[0])
		}
	default:
		SetLook(Looks[0])
	}
	shownLook = ""
}

// lookFlag reads --look NAME or --look=NAME at the start of args, like
// themeFlag.
func lookFlag(args []string) (name string, rest []string, ok bool) {
	if len(args) == 0 {
		return "", args, false
	}
	switch arg := args[0]; {
	case arg == "--look":
		if len(args) < 2 {
			return "", nil, true
		}
		return args[1], args[2:], true
	case strings.HasPrefix(arg, "--look="):
		return strings.TrimPrefix(arg, "--look="), args[1:], true
	}
	return "", args, false
}

// ruleChar is the character a Divider row draws with: the look's rule for
// the default "─", or the row's own character.
func ruleChar(ch string) string {
	if ch == "" || ch == "─" {
		return look.Rule
	}
	return ch
}

// cursorMark is the selected-row mark and a space, as drawn before a label.
func cursorMark() string { return look.Cursor + " " }
