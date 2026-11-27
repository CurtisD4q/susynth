package mint

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CurtisD4q/pmin-base/clean"
	"github.com/CurtisD4q/pmin-base/util"
)

// LogLine is one line of output from a command. Err is true when the line
// came from standard error.
type LogLine struct {
	Text      string
	Err       bool
	Overwrite bool
	// Colours are the program's own colours for Text, read safely from its
	// output (see CleanLineColours). Empty for most lines.
	Colours []ColourRun
	// panicLine is set when Text stands in for a callback that panicked.
	// The line is drawn in the error colour, not as program output.
	panicLine bool
}

// panicLogLine is the line drawn when a log callback panics.
// It stays in the log: a dialog or toast during draw would interrupt the frame.
func panicLogLine(p any) LogLine {
	return LogLine{Text: "error: " + CleanLine(fmt.Sprint(p)), panicLine: true}
}

var ansiEsc = clean.ANSI

// CleanLine strips terminal escape sequences and control characters, expands
// tabs to 8-column stops, and resolves backspace overstrikes (see
// clean.Line, which it is).
func CleanLine(raw string) string { return clean.Line(raw) }

// CleanLineVisible is CleanLine for views that compare or audit text (diffs,
// findings, file previews, rule lists): zero-width and other invisible
// characters are shown as markers too, so nothing in the line is hidden.
func CleanLineVisible(raw string) string { return clean.LineVisible(raw) }

func isBidiControl(r rune) bool { return clean.IsBidiControl(r) }

// RuneMarker is how a hidden or reordering character is shown: <U+202E>.
func RuneMarker(r rune) string { return clean.Marker(r) }

func cleanLineFrom(raw string, visible bool, col int) (string, int) {
	return clean.From(raw, visible, col)
}

// LogOpts configures an inline Log box inside a Section.
type LogOpts struct {
	ID     string
	Label  string
	Height int
	Lines  func() []LogLine
}

// Log creates an inline log box showing the last Height lines returned by Lines.
func Log(opts LogOpts) Row {
	h := opts.Height
	if h <= 0 {
		h = 6
	}
	return Row{
		kind:      "row",
		wtype:     wtLog,
		id:        opts.ID,
		label:     opts.Label,
		logHeight: h,
		logLines:  opts.Lines,
	}
}

func expandLog(r Row) []Row {
	h := r.logHeight
	if h <= 0 {
		h = 6
	}
	var lines []LogLine
	var panicked any
	if r.logLines != nil {
		func() {
			defer func() { panicked = recover() }()
			lines = r.logLines()
		}()
	}
	// A panic used to leave the box empty. Draw the panic in the box;
	// a toast here would open UI in the middle of a draw.
	if panicked != nil {
		lines = []LogLine{panicLogLine(panicked)}
	}
	var out []Row
	if r.label != "" {
		lbl := r.label
		if panicked == nil && len(lines) > 0 {
			lbl = fmt.Sprintf("%s (%d lines)", r.label, len(lines))
		}
		out = append(out, Label(StyleCaption, lbl))
	}
	start := 0
	if len(lines) > h {
		start = len(lines) - h
	}
	tail := lines[start:]
	for i := 0; i < h; i++ {
		if i < len(tail) {
			out = append(out, Row{
				kind:    "row",
				wtype:   wtLogLine,
				logLine: cleanedLogLine(tail[i]),
			})
		} else if i == 0 && len(tail) == 0 {
			out = append(out, Row{
				kind:     "row",
				wtype:    wtLogLine,
				logEmpty: true,
				logLine:  LogLine{Text: "(no output)"},
			})
		} else {
			out = append(out, Row{
				kind:  "row",
				wtype: wtLogLine,
			})
		}
	}
	return out
}

func (c *canvas) logLine(r, w int, it *Row) {
	room := max(4, w-8)
	prefix := C.Dim + "│ " + Reset
	txt := util.Truncate(it.logLine.Text, room)
	link := func(s string) string { return s }
	if !it.logEmpty && linksOn() {
		if spans := linkSpans(it.logLine.Text); len(spans) > 0 {
			src := truncSrc(it.logLine.Text, txt)
			link = func(s string) string { return withLinks(s, spans, src) }
		}
	}
	switch {
	case it.logLine.panicLine:
		c.put(r, 3, prefix+roleStyle(RoleError)+txt+Reset)
	case it.logEmpty:
		c.put(r, 3, prefix+C.Dim+txt+Reset)
	case it.logLine.Err:
		c.put(r, 3, prefix+link(C.Amber+txt+Reset))
	case len(it.logLine.Colours) > 0 && coloursOn():
		c.put(r, 3, prefix+link(renderColoured(it.logLine.Text, it.logLine.Colours, room, C.Text)))
	default:
		c.put(r, 3, prefix+link(C.Text+txt+Reset))
	}
}

// ViewLogOpts configures the full-screen ViewLog viewer.
type ViewLogOpts struct {
	Title      string
	Breadcrumb string
	Lines      func() []LogLine
	Poll       func() bool
	Status     func() []Badge
	SaveDir    string
	SaveName   string
	Follow     bool
	// SavePrivate makes a file saved with s readable only by its owner
	// (0600) from the moment it is created.
	SavePrivate bool
	// OnSave is called with the full path after s saves a file.
	OnSave func(path string)
	// NoSave turns the s key off, for text that must not reach the disk.
	NoSave bool
	// NoCopy turns the y key off, for text that must stay off the
	// clipboard.
	NoCopy bool
}

// SaveLogLines writes log lines to path, creating parent directories if needed.
func SaveLogLines(path string, lines []LogLine) error {
	return SaveLogLinesMode(path, lines, 0o644)
}

// SaveLogLinesPrivate is SaveLogLines for sensitive output: the file is
// readable only by its owner (0600) from the moment it is created, and an
// existing file is made private before anything is written to it.
func SaveLogLinesPrivate(path string, lines []LogLine) error {
	return SaveLogLinesMode(path, lines, 0o600)
}

// SaveLogLinesMode writes log lines to path with the given permissions.
// A private mode (no group or other bits) also applies to an existing file.
func SaveLogLinesMode(path string, lines []LogLine, mode os.FileMode) error {
	path = util.ExpandUser(strings.TrimSpace(path))
	if path == "" {
		return fmt.Errorf("empty file path")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := util.Mkdir(dir); err != nil {
			return err
		}
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(CleanLine(l.Text))
		b.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if mode&0o077 == 0 {
		if err := f.Chmod(mode); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ViewerLines returns the text a full-screen log viewer is showing.
// ok is false for every other screen.
func (s *Screen) ViewerLines() ([]LogLine, bool) {
	if s == nil || s.viewerLines == nil {
		return nil, false
	}
	return s.viewerLines(), true
}

// ViewLog returns a full-screen log viewer Screen with vertical scrolling,
// horizontal panning (←/→/0), case-insensitive search (/, n, N), and
// save-to-file (s).
func ViewLog(title string, lines func() []LogLine) *Screen {
	return ViewLogWith(ViewLogOpts{
		Title: title,
		Lines: lines,
	})
}

// ViewLogWith returns a full-screen log viewer Screen configured by opts.
func ViewLogWith(opts ViewLogOpts) *Screen {
	title := opts.Title
	if title == "" {
		title = "Output"
	}
	scr := &Screen{
		Title:       title,
		Breadcrumb:  opts.Breadcrumb,
		Status:      opts.Status,
		viewerLines: opts.Lines,
	}
	scr.customRun = func() screenResult {
		s := scr
		return runViewLog(s, opts)
	}
	return scr
}

func runViewLog(screen *Screen, opts ViewLogOpts) screenResult {
	scroll := 0
	hscroll := 0
	follow := opts.Follow
	query := ""
	var matches []int
	matchIdx := -1
	lastCount := -1
	var sb *scrollInfo
	footSig := ""

	getLines := func() []LogLine {
		if opts.Lines == nil {
			return nil
		}
		var out []LogLine
		var panicked any
		func() {
			defer func() { panicked = recover() }()
			out = opts.Lines()
		}()
		// Show the panic instead of the callback's lines. A panic used to
		// look like a command that printed nothing ("(no output)"), and
		// a non-empty partial result would have hidden it.
		if panicked != nil {
			return []LogLine{panicLogLine(panicked)}
		}
		return out
	}

	recomputeMatches := func(lines []LogLine) {
		matches = matches[:0]
		if query == "" {
			matchIdx = -1
			return
		}
		for i, l := range lines {
			if containsFold(CleanLine(l.Text), query) {
				matches = append(matches, i)
			}
		}
		if len(matches) == 0 {
			matchIdx = -1
		} else if matchIdx < 0 || matchIdx >= len(matches) {
			matchIdx = 0
		}
	}

	jumpToMatch := func(avail int) {
		if len(matches) == 0 || matchIdx < 0 || matchIdx >= len(matches) {
			return
		}
		target := matches[matchIdx]
		follow = false
		if target < scroll || target >= scroll+avail {
			scroll = max(0, target-avail/3)
		}
	}

	statusFn := func() []Badge {
		if screen.Status != nil {
			return callStatus(screen.Status)
		}
		return callStatus(screen.appStatus)
	}

	const viewHints = " ↑↓ scroll   ←→ pan   / find   n/N next   s save   y copy   b close "

	draw := func(h, w int) (int, []LogLine) {
		lines := getLines()
		total := len(lines)
		if total != lastCount {
			recomputeMatches(lines)
			lastCount = total
		}
		cv := begin(h, w)
		if w < 48 || h < 12 {
			cv.frame(h, w)
			cv.put(2, 3, C.Amber+"Terminal too small"+Reset)
			cv.put(3, 3, C.Muted+fmt.Sprintf("need 48x12, have %dx%d", w, h)+Reset)
			cv.paint()
			return 1, lines
		}
		cv.frame(h, w)

		contentTop := 4
		contentBottom := h - 2
		avail := max(1, contentBottom-contentTop+1)
		maxScroll := max(0, total-avail)
		if follow {
			scroll = maxScroll
		}
		scroll = clampInt(scroll, 0, maxScroll)

		// Build right-side breadcrumb info (lines, col offset, search matches).
		var crumbs []string
		if total == 0 {
			crumbs = append(crumbs, "0 lines")
		} else {
			end := min(total, scroll+avail)
			crumbs = append(crumbs, fmt.Sprintf("%d–%d of %d", scroll+1, end, total))
		}
		if hscroll > 0 {
			crumbs = append(crumbs, fmt.Sprintf("col +%d", hscroll))
		}
		if query != "" {
			if len(matches) == 0 {
				crumbs = append(crumbs, fmt.Sprintf("/%s (0)", query))
			} else {
				crumbs = append(crumbs, fmt.Sprintf("/%s (%d/%d)", query, matchIdx+1, len(matches)))
			}
		}
		crumb := strings.Join(crumbs, " · ")
		cv.chromeTitle(w, screen.Title, crumb)
		cv.titleRule(w)

		lineWidth := max(4, w-6)
		qLower := strings.ToLower(query)
		activeLine := -1
		if len(matches) > 0 && matchIdx >= 0 && matchIdx < len(matches) {
			activeLine = matches[matchIdx]
		}

		if total == 0 {
			cv.put(contentTop, 3, C.Dim+"(no output)"+Reset)
		} else {
			for row := 0; row < avail; row++ {
				idx := scroll + row
				if idx >= total {
					break
				}
				y := contentTop + row
				line := cleanedLogLine(lines[idx])
				raw := line.Text
				shown, runs := sliceColoured(raw, line.Colours, hscroll, lineWidth)
				isMatch := qLower != "" && containsFold(raw, query)
				link := func(s string) string { return s }
				if linksOn() {
					if spans := linkSpans(raw); len(spans) > 0 {
						_, from, to := util.SliceColsSpan(raw, hscroll, lineWidth)
						src := sliceSrc(from, to, from > 0 && lineWidth > 1)
						link = func(s string) string { return withLinks(s, spans, src) }
					}
				}
				// The program's own colours, redrawn safely. The selected
				// line and search matches keep Mint's highlight so they
				// stay easy to see.
				coloured := len(runs) > 0 && coloursOn() && idx != activeLine && !isMatch
				switch {
				case line.panicLine:
					cv.put(y, 3, roleStyle(RoleError)+shown+Reset)
				case coloured && lines[idx].Err:
					cv.put(y, 3, link(renderColoured(shown, runs, lineWidth, C.Amber)))
				case coloured:
					cv.put(y, 3, link(renderColoured(shown, runs, lineWidth, C.Text)))
				case idx == activeLine:
					cv.selBar(y, w)
					cv.put(y, 3, link(C.SelBg+C.Accent+C.Bold+shown+Reset))
				case isMatch:
					cv.put(y, 3, link(C.Bright+C.Bold+shown+Reset))
				case lines[idx].Err:
					cv.put(y, 3, link(C.Amber+shown+Reset))
				default:
					cv.put(y, 3, link(C.Text+shown+Reset))
				}
			}
		}

		sb = nil
		if total > avail {
			sbx := w - 1
			thumb := max(1, floorf(float64(avail*avail)/float64(total)+0.5))
			maxtop := avail - thumb
			tpos := 0
			if maxScroll > 0 {
				tpos = floorf(float64(scroll)/float64(maxScroll)*float64(maxtop) + 0.5)
			}
			sb = &scrollInfo{col: sbx, top: contentTop, rows: avail, maxoff: maxScroll, vis: avail}
			cv.scrollbar(sbx, contentTop, avail, thumb, tpos)
		}

		badges := statusFn()
		cv.footer(h, w, viewHints, badges)
		footSig = footerSig(badges)
		cv.paint()
		return avail, lines
	}

	h, w := TermSize()
	avail, lines := draw(h, w)
	sc := 0
	for {
		k := readNav(false)
		dirty := false
		maxScroll := max(0, len(lines)-avail)
		switch {
		case k == nil:
			sc++
			if rt.consumeResize() || sc >= 2 {
				sc = 0
				if nh, nw := TermSize(); nh != h || nw != w {
					h, w = nh, nw
					dirty = true
				}
			}
			if opts.Poll != nil {
				changed := false
				protect(func() { changed = opts.Poll() })
				if changed {
					dirty = true
				}
			}
			if cur := getLines(); len(cur) != len(lines) {
				dirty = true
			}
			if footerSig(statusFn()) != footSig {
				dirty = true
			}
		case k.Mouse != nil && (k.Mouse.WheelUp || k.Mouse.WheelDown):
			follow = false
			if k.Mouse.WheelUp {
				scroll = max(0, scroll-3)
			} else {
				scroll = min(maxScroll, scroll+3)
			}
			dirty = true
		case k.Mouse != nil && k.Mouse.Click:
			m := k.Mouse
			if sb != nil && m.Col == sb.col && m.Row >= sb.top && m.Row <= sb.top+sb.rows-1 {
				follow = false
				frac := float64(m.Row-sb.top) / float64(max(1, sb.rows-1))
				scroll = clampInt(int(math.Floor(frac*float64(sb.maxoff)+0.5)), 0, sb.maxoff)
				dirty = true
			}
		case k.Name == "up":
			follow = false
			scroll = max(0, scroll-1)
			dirty = true
		case k.Name == "down":
			follow = false
			scroll = min(maxScroll, scroll+1)
			dirty = true
		case k.Name == "pgup":
			follow = false
			scroll = max(0, scroll-max(1, avail-1))
			dirty = true
		case k.Name == "pgdn" || k.Name == " ":
			follow = false
			scroll = min(maxScroll, scroll+max(1, avail-1))
			dirty = true
		case k.Name == "home" || k.Name == "g":
			follow = false
			scroll = 0
			dirty = true
		case k.Name == "end" || k.Name == "G":
			scroll = maxScroll
			follow = opts.Follow
			dirty = true
		case k.Name == "right":
			hscroll += 8
			dirty = true
		case k.Name == "left":
			hscroll = max(0, hscroll-8)
			dirty = true
		case k.Name == "0":
			hscroll = 0
			dirty = true
		case k.Name == "/":
			if q, ok := Prompt("Search output", "Case-insensitive substring (empty clears search)", query); ok {
				query = strings.TrimSpace(q)
				recomputeMatches(lines)
				if query != "" {
					if len(matches) == 0 {
						ToastWarn("No matches for " + query)
					} else {
						matchIdx = 0
						jumpToMatch(avail)
					}
				}
			}
			dirty = true
		case k.Name == "n":
			if len(matches) > 0 {
				matchIdx = (matchIdx + 1) % len(matches)
				jumpToMatch(avail)
				dirty = true
			} else if query != "" {
				ToastWarn("No matches for " + query)
				dirty = true
			}
		case k.Name == "N":
			if len(matches) > 0 {
				matchIdx = (matchIdx - 1 + len(matches)) % len(matches)
				jumpToMatch(avail)
				dirty = true
			} else if query != "" {
				ToastWarn("No matches for " + query)
				dirty = true
			}
		case k.Name == "y" && opts.NoCopy:
			ToastWarn("This text is not copied")
			dirty = true
		case k.Name == "y":
			// Everything in the viewer, as the program printed it, without
			// its colours.
			texts := make([]string, len(lines))
			for i, l := range lines {
				texts[i] = l.Text
			}
			copied(copyOutput(strings.Join(texts, "\n")), linesWord(len(lines)))
			dirty = true
		case k.Name == "s" && opts.NoSave:
			ToastWarn("This text is not saved to disk")
			dirty = true
		case k.Name == "s":
			defDir := opts.SaveDir
			if defDir == "" {
				defDir = "~"
			}
			defName := opts.SaveName
			if defName == "" {
				defName = fmt.Sprintf("mint-output-%s.txt", time.Now().Format("20060102-150405"))
			}
			initial := filepath.Join(defDir, defName)
			if dest, ok := Prompt("Save output to file", "Directory will be created if needed", initial); ok && strings.TrimSpace(dest) != "" {
				save := SaveLogLines
				if opts.SavePrivate {
					save = SaveLogLinesPrivate
				}
				if err := save(dest, lines); err != nil {
					ToastError("Save failed: " + err.Error())
				} else {
					full := util.ExpandUser(strings.TrimSpace(dest))
					if opts.OnSave != nil {
						opts.OnSave(full)
					}
					Toast("Saved " + full)
				}
			}
			dirty = true
		case k.Name == "b" || k.Name == "esc":
			return screenResult{op: "back"}
		case k.Name == "q" || k.Name == "\x03":
			return screenResult{op: "quit"}
		}
		if dirty {
			avail, lines = draw(h, w)
		}
	}
}
