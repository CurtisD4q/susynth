package mint

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/CurtisD4q/pmin-base/util"
)

// Section is a heading followed by rows. An empty Name draws no heading.
//
// A Collapsible section draws its heading as ▾ Name, can be selected, and
// folds away its rows with Enter, Space, ← or a click on the arrow
// (▸ Name, "3 hidden"). Collapsed sets its starting state; after that the
// screen remembers what the user chose, even across Refresh.
type Section struct {
	Name        string
	Rows        []Row
	Collapsible bool
	Collapsed   bool
}

// Screen is one page of rows. Returning a *Screen from a home item's Open
// (or passing one to ctx.Push) shows it; b pops it and q quits.
type Screen struct {
	Icon       string
	Title      string
	Breadcrumb string // defaults to the app's Crumb
	// Context, optional, is a short line about what the screen is working
	// on, shown after the trail at the top ("case default · scope
	// 10.0.0.0/24"). Without one, the app's Context is used.
	Context func() string

	trail      []string   // titles of the screens under this one (App.Run)
	rail       []railItem // the app's pages, for the page rail (App.Run)
	railCur    int        // the page this screen belongs to, or -1
	railOn     bool       // the rail has the keys (m)
	railSel    int        // the page selected in the rail
	railW      int        // columns the rail took, as last drawn
	railOpenW  int        // columns the open pop-up reaches (its right edge), as last drawn
	railPopW   int        // the pop-up's own width
	railTop    int        // row of the rail's first page, as last drawn
	railOff    int        // pages scrolled off the rail's top
	appContext func() string
	Banner     string // amber line under the title
	Hints      string // footer override
	Sections   []Section

	// Weight is the top pane's share of the content height when Panes is
	// set and any pane sets a weight above zero. With every weight left at
	// zero, the top pane takes half the content rows and the other panes
	// split what remains.
	Weight float64

	// TopWidth is the top pane's share of the width when the pane after it
	// has Beside set (0 shares equally).
	TopWidth float64

	// TopTitle names the top band when it is a pane of its own, such as
	// Basic Options. Empty keeps the screen title as that band's name and
	// draws no divider while the band is open.
	TopTitle string

	// Panes are stacked under Sections, top to bottom. A screen with none
	// is one column and draws exactly as before. There is no side-by-side
	// layout: every extra pane sits under the one above it, with a one-line
	// divider that shows its title.
	Panes []*Pane

	// Dock is drawn under the panes and does not scroll. The run command
	// bar uses it so the command stays pinned under Output, above the footer.
	Dock []Section

	// SectionSpacing puts that many blank lines before every named section
	// except the first. 0 uses the app's setting; negative means none.
	SectionSpacing int

	// Status returns the badges shown on the left of the footer, such as
	// {"running", BadgeOK}. It overrides the app's Status. It is called
	// about ten times a second so the footer can update; keep it cheap.
	Status func() []Badge

	// Refresh runs before each draw and may replace Sections. A panic sets
	// Banner to the error.
	Refresh func(*Screen)
	// Poll runs on idle (about ten times a second) and returns true when the
	// next frame must be painted. Keep it non-blocking.
	Poll func(*Screen) bool
	// Enter runs once each time the screen is shown. If it takes a while,
	// the footer shows a loading spinner.
	Enter func(*Screen)

	// Boxed draws every pane in a box of its own, with its title and its
	// Info on the line above the box. The focused pane's box is drawn in the accent colour,
	// and 1-9 move focus straight to a pane (unless the screen's Keys use
	// that digit). It only changes a screen that has Panes.
	Boxed bool
	// TopInfo returns short live facts shown after the top pane's title in
	// a boxed screen, such as "12 jobs · newest". It is called on every
	// draw; keep it cheap.
	TopInfo func() string

	// Draw, optional, draws the screen's own content cell by cell instead
	// of rows: a code editor, a piano roll, a live meter. On a screen with
	// Panes it draws the first pane (index 0, titled TopTitle or Title);
	// without Panes it draws the whole content area. focused is whether
	// that pane has the focus. p.SetCursor shows the terminal's cursor.
	Draw func(p *Paint, focused bool)

	// Grab, optional, sees every key and click before Mint does, while the
	// page pop-up is shut. Returning true means the screen used it: Mint
	// does nothing more with it and draws the screen again. A screen you
	// type into uses it to keep its own keys (q, b, m and the rest are
	// then text); c can Push, Pop, Quit, Page and OpenPages.
	Grab func(c *Ctx, k *Key) bool

	// Every, when set, draws the screen again that often, calling Poll each
	// time, for a screen that moves on its own (a playhead, a level meter).
	// Only the rows that change are sent, so it does not flicker. Without
	// it the screen is drawn when a key is pressed or Poll says so, about
	// ten times a second at most.
	Every time.Duration

	// Keys are keys this screen answers itself, such as "d" to open a day
	// view. Each is one printable character. Mint's own keys always win:
	// b, q, y, space, H, L, [, ] and |, and in the vim style j, k, h, l, g
	// and G, are never handed to a screen. A key is ignored while a text
	// field is being edited.
	Keys []ScreenKey

	appStatus func() []Badge
	ui        *uiState
	customRun func() screenResult

	// onLeave runs when the screen is closed with back.
	onLeave func()

	// Split-view state. Zero values keep a screen with no Panes on the
	// single-column path.
	focus     int
	bias      map[int]int
	topHidden bool
	bands     []paneBand
	viewTop   int
	viewAvail int
	viewWidth int
	selRow    Row
	selOK     bool
	sel       Selection // the row under the cursor in the pane last moved through
	selHave   bool

	dockUI            *uiState
	dockTop, dockRows int

	cursor string // where the terminal's cursor goes, from a Draw's SetCursor

	redraw   func(int, int)
	onKey    func(*Key, *int, *int, *int) (*screenResult, bool)
	grabKey  func(*Key) (*screenResult, bool)
	frameBuf *strings.Builder
	// ptrSeen is the pointer as of the last frame (hover and pressed).
	ptrSeen pointerWatch

	// viewerLines is the text a full-screen log viewer shows. ViewLog sets
	// it; every other screen leaves it nil.
	viewerLines func() []LogLine
}

// ScreenKey is one key a screen answers itself.
type ScreenKey struct {
	Key string // one printable character
	// Label says what the key does, for the key list ? shows, such as
	// "open the menu". Without one, the list uses the words after the key
	// in the screen's Hints.
	Label string
	// Do runs when the key is pressed, with the screen's Ctx, so it can
	// Push, Pop, Message or Toast as a row's OnActivate does.
	Do func(*Ctx)
}

// Pane is one band stacked under a screen's own sections. Each pane has its
// own sections, cursor, scroll and collapsed state, and a weight for how
// much of the height it takes when any pane sets a weight above zero.
// With every weight left at zero, the top pane takes half the content
// rows and the other panes share what remains.
type Pane struct {
	Title    string
	Sections []Section
	Weight   float64
	Refresh  func(*Pane)

	// Beside draws this pane to the right of the pane before it, in the
	// same band of rows, instead of under it. Width is its share of the
	// band's width among the panes beside each other (0 shares equally).
	// On a terminal too narrow for them, the panes are stacked as usual.
	Beside bool
	Width  float64

	// Info returns short live facts shown after the title in a boxed
	// screen, such as "20 · newest". It is called on every draw; keep it
	// cheap.
	Info func() string

	// Draw, optional, draws the pane's own content cell by cell instead of
	// its Sections (see Screen.Draw).
	Draw func(p *Paint, focused bool)

	ui     *uiState
	hidden bool
}

type editState struct {
	idx     int
	chars   []string
	buf     *lineBuffer
	numeric bool
	commit  func(string)
}

type hitEntry struct {
	idx      int
	slider   bool
	sx0, sw  int
	stepper  bool
	minusCol int
	plusCol  int
	mark     bool // check / radio: clicking the box or circle acts at once
	header   bool
}

type scrollInfo struct {
	col, top, rows, maxoff, vis int
}

type uiState struct {
	row, scroll int
	lastGood    map[string]any
	editing     *editState
	hit         map[int]hitEntry
	sb          *scrollInfo
	items       []Row
	collapsed   map[string]bool
	footSig     string
	page        int
	x0          int    // columns to the left of this pane's window (0 unless split)
	cardKey     string // the card last shown here: a new one starts at the top
	filter      string // rows shown must contain this (see filter.go)
	filtering   bool   // the filter is being typed
}

// Ctx is handed to row callbacks. It can open dialogs, show toasts, run
// slow work behind a spinner, and change screens.
type Ctx struct {
	nav *navReq
}

type navReq struct {
	op     string
	screen *Screen
	page   int
}

// Page opens page i of the app (0 is the first item of the launch menu),
// as the page pop-up does.
func (c *Ctx) Page(i int) { c.nav.op, c.nav.page = "page", i }

// OpenPages opens the page pop-up, as m does.
func (c *Ctx) OpenPages() { c.nav.op = "pages" }

// Confirm asks a Yes/No question (No is preselected).
func (c *Ctx) Confirm(title, body string) bool { return Confirm(title, body) }

// Message shows text until it is dismissed.
func (c *Ctx) Message(title, body string) { Message(title, body) }

// Prompt asks for a line of text. ok is false when cancelled.
func (c *Ctx) Prompt(title, subtitle, initial string) (string, bool) {
	return Prompt(title, subtitle, initial)
}

// Choose offers a filterable list. ok is false when cancelled.
func (c *Ctx) Choose(title string, items []string, subtitle string) (string, bool) {
	return Choose(title, items, subtitle)
}

// ChooseItem is Choose with separate labels and values.
func (c *Ctx) ChooseItem(title string, items []Choice, subtitle string) (Choice, bool) {
	return ChooseItem(title, items, subtitle)
}

// Password asks for masked text. ok is false when cancelled.
func (c *Ctx) Password(title, subtitle string) (string, bool) { return Password(title, subtitle) }

// TypeToConfirm requires the exact phrase to be typed.
func (c *Ctx) TypeToConfirm(title, body, phrase string) bool {
	return TypeToConfirm(title, body, phrase)
}

// Toast flashes "✓ msg" in the footer for a couple of seconds.
func (c *Ctx) Toast(msg string) { Toast(msg) }

// ToastWarn flashes "! msg" in amber.
func (c *Ctx) ToastWarn(msg string) { ToastWarn(msg) }

// ToastError flashes "✕ msg" in red.
func (c *Ctx) ToastError(msg string) { ToastError(msg) }

// Busy runs slow work (saving, applying) with a spinner and msg in the
// footer if it takes more than a moment, e.g. ctx.Busy("Saving…", save).
// fn runs on another goroutine and must not open dialogs. A panic inside it
// becomes an error toast and Busy returns false.
func (c *Ctx) Busy(msg string, fn func()) bool { return Busy(msg, fn) }

// Busy is ctx.Busy for code without a ctx, such as a row's Set. Call it
// from the interface's own callbacks, not from another goroutine.
func Busy(msg string, fn func()) bool {
	if p := withLoading(msg, fn); p != nil {
		ToastError(fmt.Sprint(p))
		return false
	}
	return true
}

// Open builds a screen with "Loading <title>…" in the footer while it
// works, then shows it. fn runs on another goroutine and must not open
// dialogs. A nil screen stays put.
func (c *Ctx) Open(title string, fn func() *Screen) {
	var s *Screen
	if p := withLoading("Loading "+title+"…", func() { s = fn() }); p != nil {
		ToastError(fmt.Sprint(p))
		return
	}
	if s != nil {
		c.Push(s)
	}
}

// Push shows another screen on top of this one.
func (c *Ctx) Push(s *Screen) { c.nav.op, c.nav.screen = "push", s }

// Pop returns to the previous screen.
func (c *Ctx) Pop() { c.nav.op = "pop" }

// Quit leaves the app.
func (c *Ctx) Quit() { c.nav.op = "quit" }

type screenResult struct {
	op     string // "back", "quit", "push", "page"
	screen *Screen
	page   int // for "page": the home item (0-based) the rail chose
}

// expand turns one Row into the lines it occupies at this width.
func expand(r Row, width int) []Row {
	if r.fit != nil && (r.wtype == wtNote || r.wtype == wtLabel) {
		// The columns a note has: eight go to its margins and the scroll bar.
		protect(func() { r.label = r.fit(max(10, width-8)) })
	}
	switch r.wtype {
	case wtRadioGroup:
		var out []Row
		if r.label != "" {
			caption := Label(StyleCaption, r.label)
			caption.indent = 2
			out = append(out, expand(caption, width-2)...)
		}
		for _, opt := range r.options {
			o := r
			o.wtype, o.radioOpt, o.label, o.options = wtRadio, opt, opt, nil
			out = append(out, o)
		}
		return out
	case wtLog:
		return expandLog(r)
	case wtTable:
		return expandTable(r, width)
	case wtCard:
		return expandCard(r, width)
	case wtNote:
		// A note is drawn eight cells in from the edge; it wraps there
		// rather than being cut with an ellipsis.
		// Lines after the first keep the note's own indent.
		text := strings.TrimLeft(r.label, " ")
		indent := r.label[:len(r.label)-len(text)]
		lines := wrapLines(text, max(10, width-8-len(indent)))
		if len(lines) <= 1 {
			return []Row{r}
		}
		out := make([]Row, len(lines))
		for i, l := range lines {
			out[i] = r
			out[i].label = indent + l
		}
		return out
	case wtLabel:
		lines := wrapLines(r.label, max(10, width-6))
		if len(lines) == 0 {
			lines = []string{""}
		}
		out := make([]Row, len(lines))
		for i, l := range lines {
			out[i] = r
			out[i].label = l
		}
		return out
	}
	return []Row{r}
}

func (st *uiState) expandRow(r Row, width int) []Row {
	r = r.normalized()
	if r.wtype == wtCollapsible {
		key := r.id
		if key == "" {
			key = "col:" + r.name
		}
		var collapsed bool
		if r.openPtr != nil {
			collapsed = !*r.openPtr
			st.collapsed[key] = collapsed
		} else {
			var seen bool
			collapsed, seen = st.collapsed[key]
			if !seen {
				collapsed = r.collapsed
				st.collapsed[key] = collapsed
			}
		}
		childRows, badge := buildCollapsibleChildren(r)
		var expanded []Row
		for _, cr := range childRows {
			expanded = append(expanded, st.expandRow(cr, width)...)
		}
		hidden := 0
		for i := range expanded {
			if w := expanded[i].wtype; w != wtSpacer && w != wtDivider && w != wtLabel && w != wtLogLine && w != wtNote {
				hidden++
			}
		}
		if hidden == 0 {
			hidden = len(expanded)
		}
		hdr := Row{
			kind:        "section",
			id:          key,
			name:        r.name,
			badge:       badge,
			collapsible: true,
			collapsed:   collapsed,
			hidden:      hidden,
			openPtr:     r.openPtr,
		}
		if collapsed {
			return []Row{hdr}
		}
		return append([]Row{hdr}, expanded...)
	}
	return expand(r, width)
}

func (st *uiState) flatten(sections []Section, width, spacing int) []Row {
	var items []Row
	for _, sec := range sections {
		var rows []Row
		for _, r := range sec.Rows {
			rows = append(rows, st.expandRow(r, width)...)
		}
		if sec.Name != "" {
			if spacing > 0 && len(items) > 0 {
				// Gaps between sections stay blank. The dim rule is only
				// drawn between stacked panes, not inside one.
				for i := 0; i < spacing; i++ {
					items = append(items, Spacer())
				}
			}
			if sec.Collapsible {
				collapsed, seen := st.collapsed[sec.Name]
				if !seen {
					collapsed = sec.Collapsed
					st.collapsed[sec.Name] = collapsed
				}
				hidden := 0
				for i := range rows {
					if w := rows[i].wtype; w != wtSpacer && w != wtDivider && w != wtLabel && w != wtLogLine {
						hidden++
					}
				}
				items = append(items, Row{kind: "section", name: sec.Name,
					collapsible: true, collapsed: collapsed, hidden: hidden})
				if collapsed {
					continue
				}
			} else {
				items = append(items, Row{kind: "section", name: sec.Name})
			}
		}
		items = append(items, rows...)
	}
	return items
}

// selectable returns the 1-based indexes of rows that can be selected.
func selectable(items []Row) []int {
	var idx []int
	for i := range items {
		if items[i].isSelectable() {
			idx = append(idx, i+1)
		}
	}
	return idx
}

func floorf(x float64) int { return int(math.Floor(x)) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// dividerLine repeats ch to exactly n cells.
func dividerLine(ch string, n int) string {
	cells := util.Chars(ch)
	if len(cells) == 0 {
		cells = []string{"─"}
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(cells[i%len(cells)])
	}
	return b.String()
}

// alertPrefix is red and bold. On a monochrome terminal Err is bold only;
// the row's label text still states the warning.
func alertPrefix(selected bool) string {
	col := C.Amber + C.Bold
	if C.Err != "" {
		col = C.Err + C.Bold
	}
	if selected {
		return C.SelBg + col
	}
	return col
}

func paintAlert(styled string, selected bool) string {
	return alertPrefix(selected) + sgrSeq.ReplaceAllString(styled, "") + Reset
}

func labelStyle(s Style) string {
	switch s {
	case StyleTitle:
		return C.Accent + C.Bold
	case StyleHeading:
		return C.Bright + C.Bold
	case StyleCaption:
		return C.Muted
	case StyleItalic:
		return C.Text + esc + "3m"
	case StyleUnderline:
		return C.Text + esc + "4m"
	}
	return C.Text
}

// contentLine is one row of the content viewport, in the order it is drawn.
type contentLine struct {
	index int // 0-based index into the flattened items
	row   int // terminal row
}

// layoutContent places items into the content viewport. scroll is how many
// items are above it. A row's line is index - scroll + contentTop, the place
// the screen has always used, except a row marked pin is left out of that
// scroll: while its table is still on screen the pin stays at the top of the
// table's visible area and the body keeps its own line underneath.
func layoutContent(items []Row, scroll, contentTop, avail int) []contentLine {
	if avail < 1 {
		return nil
	}
	contentBottom := contentTop + avail - 1
	natural := func(index int) int { return index - scroll + contentTop }
	inView := func(r int) bool { return r >= contentTop && r <= contentBottom }

	at := make([]int, contentBottom+1)
	for i := range at {
		at[i] = -1
	}
	// Pins drawn at the sticky line, not at the line scroll would give them.
	held := map[int]bool{}

	for i := 0; i < len(items); {
		if !items[i].pin {
			i++
			continue
		}
		start := i
		for i < len(items) && items[i].pin {
			i++
		}
		end := i
		if natural(start) >= contentTop {
			continue // still on its own line; the pass below draws it
		}
		// What scrolls under the pin: the body, or the empty-table note.
		body := end
		for body < len(items) && (items[body].wtype == wtTableRow || items[body].cardBody) {
			body++
		}
		if body == end && body < len(items) && items[body].wtype == wtNote {
			body++
		}
		first, last := -1, -1
		for j := start; j < body; j++ {
			if r := natural(j); inView(r) {
				if first < 0 {
					first = r
				}
				last = r
			}
		}
		if first < 0 {
			continue // the table has scrolled off; the pin goes with it
		}
		for k := start; k < end; k++ {
			r := first + (k - start)
			if !inView(r) || r > last {
				break
			}
			at[r] = k
			held[k] = true
		}
	}

	for i := range items {
		if held[i] {
			continue
		}
		r := natural(i)
		if !inView(r) {
			continue
		}
		if at[r] >= 0 {
			// A pin already occupies this line. The row stays on the line
			// scroll gave it, underneath the pin, so it is not drawn.
			continue
		}
		at[r] = i
	}

	var out []contentLine
	for r := contentTop; r <= contentBottom; r++ {
		if at[r] >= 0 {
			out = append(out, contentLine{index: at[r], row: r})
		}
	}
	return out
}

func (screen *Screen) run() screenResult {
	if screen.customRun != nil {
		return screen.customRun()
	}
	screen.bind()
	prev := activeScreen
	activeScreen = screen
	defer func() { activeScreen = prev }()
	if screen.Every > 0 {
		old := idleWait
		idleWait = max(10*time.Millisecond, min(screen.Every, 100*time.Millisecond))
		defer func() { idleWait = old }()
	}
	h, w := TermSize()
	screen.redraw(h, w)
	sc := 0
	for {
		editing := false
		if u := screen.uiAt(screen.focus); u != nil {
			editing = u.editing != nil
		}
		k := ReadKey()
		if screen.Grab == nil || k == nil {
			k = navKey(k, editing)
		}
		if k != nil && screen.Grab != nil && !screen.railOn {
			res, used := screen.grabKey(k)
			if res != nil {
				return *res
			}
			if used {
				screen.redraw(h, w)
				continue
			}
		}
		k, rres, handled := screen.railKey(k)
		if rres != nil {
			return *rres
		}
		if handled {
			screen.redraw(h, w)
			continue
		}
		res, dirty := screen.onKey(k, &h, &w, &sc)
		if res != nil {
			return *res
		}
		if dirty {
			screen.redraw(h, w)
		}
	}
}

// screenSession holds the pane, navigation and click state that bind's
// closures used to capture. Drawing, keys and widget helpers are methods
// on it.
type screenSession struct {
	screen        *Screen
	st            *uiState
	nav           *navReq
	ctx           *Ctx
	lastClickIdx  int
	lastClickTime time.Time
	lastClickPane *uiState // rows are numbered per pane, so a double click is on one pane
	lastDraw      time.Time
}

func (screen *Screen) bind() {
	if screen.redraw != nil {
		return
	}
	if screen.ui == nil {
		screen.ui = &uiState{row: 1, lastGood: map[string]any{}}
	}
	st := screen.ui
	if st.collapsed == nil {
		st.collapsed = map[string]bool{}
	}
	st.editing = nil
	st.hit = map[int]hitEntry{}
	st.sb = nil
	if screen.Enter != nil {
		title := screen.Title
		if title == "" {
			title = "Screen"
		}
		if p := withLoading("Loading "+title+"…", func() { screen.Enter(screen) }); p != nil {
			ToastError(fmt.Sprint(p))
		}
	}

	nav := &navReq{}
	ctx := &Ctx{nav: nav}

	ss := &screenSession{
		screen: screen,
		st:     st,
		nav:    nav,
		ctx:    ctx,
	}
	screen.redraw = ss.draw
	screen.onKey = ss.onKey
	screen.grabKey = ss.grab
}

func (screen *Screen) renderFrame(h, w int) string {
	screen.bind()
	var b strings.Builder
	screen.frameBuf = &b
	screen.redraw(h, w)
	screen.frameBuf = nil
	return b.String()
}

// FrameText draws the screen at h rows by w columns, including colour
// sequences, without a real terminal. Screenshots and tests use it.
func (s *Screen) FrameText(h, w int) string {
	if s == nil {
		return ""
	}
	return s.renderFrame(h, w)
}
