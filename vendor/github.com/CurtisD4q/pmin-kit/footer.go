package mint

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── status badges ────────────────────────────────────────────────────────────

// BadgeKind sets the colour of a status badge's dot.
type BadgeKind int

const (
	BadgeInfo  BadgeKind = iota // accent ●
	BadgeOK                     // green ●
	BadgeWarn                   // amber ●
	BadgeError                  // red ●
	BadgeIdle                   // muted ○
)

// Badge is one item in the footer's status area, e.g. {"running", BadgeOK}.
type Badge struct {
	Text string
	Kind BadgeKind
}

func errColour() string {
	if C.Err != "" {
		return C.Err
	}
	return C.Amber
}

func (b Badge) render() string {
	dot, col := "●", C.Accent
	switch b.Kind {
	case BadgeOK:
		col = C.On
	case BadgeWarn:
		col = C.Amber
	case BadgeError:
		col = errColour()
	case BadgeIdle:
		dot, col = "○", C.Muted
	}
	return col + dot + Reset + " " + C.Muted + b.Text + Reset
}

func (b Badge) width() int { return 2 + VLen(b.Text) }

const badgeGap = "   "

func callStatus(fn func() []Badge) (out []Badge) {
	if fn == nil {
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			// A status callback runs while the footer is drawn. Returning
			// an error badge keeps the frame intact and shows the failure;
			// swallowing it used to look like "no status".
			out = []Badge{{
				Text: Truncate(CleanLine(fmt.Sprint(p)), 48),
				Kind: BadgeError,
			}}
		}
	}()
	return fn()
}

// ── toasts ───────────────────────────────────────────────────────────────────

type toastKind int

const (
	toastOK toastKind = iota
	toastWarn
	toastError
)

// ToastDuration is how long a toast stays in the footer.
var ToastDuration = 2 * time.Second

var toast struct {
	sync.Mutex
	msg   string
	kind  toastKind
	until time.Time
}

func showToast(kind toastKind, msg string) {
	// Panic values and other caller text can carry escapes or newlines.
	// The footer is one row; CleanLine keeps it that way.
	msg = CleanLine(msg)
	toast.Lock()
	toast.msg, toast.kind, toast.until = msg, kind, time.Now().Add(ToastDuration)
	toast.Unlock()
}

// Toast flashes "✓ msg" in the footer. Safe to call from any goroutine.
func Toast(msg string) { showToast(toastOK, msg) }

// ToastWarn flashes "! msg" in amber.
func ToastWarn(msg string) { showToast(toastWarn, msg) }

// ToastError flashes "✕ msg" in red.
func ToastError(msg string) { showToast(toastError, msg) }

func activeToast() (string, toastKind, bool) {
	toast.Lock()
	defer toast.Unlock()
	if toast.msg == "" || time.Now().After(toast.until) {
		return "", 0, false
	}
	return toast.msg, toast.kind, true
}

// toastActivity is work in progress shown in a one-row footer; its glyph
// is the spinner frame in activitySpin.
const toastActivity toastKind = 99

var activitySpin string

func toastParts(msg string, kind toastKind) (glyphCol, glyph string) {
	switch kind {
	case toastActivity:
		return C.Accent, activitySpin
	case toastWarn:
		return C.Amber, "!"
	case toastError:
		return errColour(), "✕"
	}
	return C.On, "✓"
}

// ── footer ───────────────────────────────────────────────────────────────────

// footerSig identifies what the footer's left side would show now, so the
// idle loop can repaint only when a toast appears or expires or a badge
// changes.
func footerSig(status []Badge) string {
	var b strings.Builder
	if msg, kind, ok := activeToast(); ok {
		fmt.Fprintf(&b, "t%d:%s|", kind, msg)
	}
	// Work in progress animates: each spinner frame is a new footer.
	if msg, spin, ok := currentActivity(); ok {
		fmt.Fprintf(&b, "a%s:%s|", spin, msg)
	}
	for _, s := range status {
		fmt.Fprintf(&b, "%d:%s|", s.Kind, s.Text)
	}
	return b.String()
}

// footerHints is the key line the footer draws. While a text field is being
// edited, that line replaces everything else. Otherwise a screen's own hints
// win, and a focused viewer table adds ←→ (scroll sideways) and H/L (narrow
// or widen the column). Any other row adds nothing.
func footerHints(editing bool, custom string, focused *Row) string {
	if editing {
		return EditHints
	}
	hints := ScreenHints
	if custom != "" {
		hints = custom
	}
	if focused != nil && focused.table != nil && focused.table.viewer {
		hints = addHint(hints, "←→ scroll sideways   H/L narrow or widen")
	}
	// y copies a table row or a value; the hint shows where it helps most.
	if focused != nil && (focused.wtype == wtTableRow || focused.wtype == wtValue) && !strings.Contains(hints, "y copy") {
		hints = addHint(hints, "y copy")
	}
	return hints
}

// addHint puts extra at the end of a footer's hints.
func addHint(hints, extra string) string {
	hints = strings.TrimRight(hints, " ")
	if hints == "" {
		return " " + extra + " "
	}
	if !strings.HasPrefix(hints, " ") {
		hints = " " + hints
	}
	return hints + "   " + extra + " "
}

// footer draws the key hints on the right and, on the left, a toast if one
// is showing, otherwise the status badges. With neither, it is exactly the
// original footer.
func (c *canvas) footer(h, w int, hints string, status []Badge) {
	msg, kind, hasToast := activeToast()
	if !hasToast {
		if amsg, spin, ok := currentActivity(); ok {
			// Work in progress shows as a toast does, with a spinner.
			msg, hasToast = amsg, true
			kind = toastActivity
			activitySpin = spin
		}
	}
	if !hasToast && len(status) == 0 {
		c.chromeFooter(h, w, hints)
		return
	}
	width := max(0, w-2)
	c.put(h-1, 2, C.Dim+strings.Repeat(" ", width)+Reset)
	hints = fitHints(hints, w-3)
	hcol := max(2, w-1-VLen(hints))
	room := hcol - 1 - 3 // cells from column 3 up to one gap before the hints
	showHints := true
	if hasToast {
		gc, g := toastParts(msg, kind)
		need := 2 + VLen(msg)
		if need > room {
			showHints = false
			room = w - 4
		}
		c.put(h-1, 3, gc+g+Reset+" "+C.Text+Truncate(msg, max(1, room-2))+Reset)
	} else {
		var b strings.Builder
		used := 0
		for i, s := range status {
			add := s.width()
			if i > 0 {
				add += len(badgeGap)
			}
			if used+add > room {
				break
			}
			if i > 0 {
				b.WriteString(badgeGap)
			}
			b.WriteString(s.render())
			used += add
		}
		if used > 0 {
			c.put(h-1, 3, b.String())
		}
	}
	if showHints {
		c.put(h-1, hcol, C.Muted+hints+Reset)
	}
}

// ── loading ──────────────────────────────────────────────────────────────────

// LoadingDelay is how long work may take before the footer spinner shows,
// so fast page changes do not flicker.
var LoadingDelay = 150 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func paintSpinner(frame int, msg string) {
	h, w := TermSize()
	if w < 4 || h < 4 {
		return
	}
	var b strings.Builder
	b.WriteString(move(h-1, 2) + C.Dim + strings.Repeat(" ", w-2) + Reset)
	b.WriteString(move(h-1, 3) + C.Accent + spinnerFrames[frame%len(spinnerFrames)] + Reset +
		" " + C.Muted + Truncate(msg, max(1, w-6)) + Reset)
	out(b.String() + esc + "H") // park the cursor, as paint does
}

// withLoading runs fn in the background. If it takes longer than
// LoadingDelay, a spinner and msg animate in the footer of whatever is on
// screen until it finishes. Keys pressed meanwhile are discarded. It
// returns the value of any panic inside fn.
func withLoading(msg string, fn func()) any {
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case p := <-done:
		return p
	case <-time.After(LoadingDelay):
	}
	start := time.Now()
	last := -1
	for {
		frame := int(time.Since(start) / (100 * time.Millisecond))
		if frame != last {
			paintSpinner(frame, msg)
			last = frame
		}
		select {
		case p := <-done:
			return p
		default:
		}
		ReadKey() // waits up to 100 ms; input during loading is dropped
	}
}

// footerLine is footer with line (a filter) on the left in place of a
// toast or the badges. With no line it is footer.
func (c *canvas) footerLine(h, w int, hints string, status []Badge, line string) {
	if line == "" {
		c.footer(h, w, hints, status)
		return
	}
	c.put(h-1, 2, C.Dim+strings.Repeat(" ", max(0, w-2))+Reset)
	hints = fitHints(hints, max(0, w-3-VLen(line)-3))
	c.put(h-1, 3, line)
	if hints != "" {
		c.put(h-1, max(2, w-1-VLen(hints)), C.Muted+hints+Reset)
	}
}
