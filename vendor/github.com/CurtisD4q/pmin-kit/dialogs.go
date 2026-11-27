package mint

import (
	"fmt"
	"strings"
	"time"
)

// Dialogs are full-screen modals drawn in the same frame as the rest of the
// UI. They need the screen to be active (call them from row callbacks, or
// between ScreenEnter and ScreenLeave).

// Message shows body until Enter, Space, Esc, q or a click.
func Message(title, body string) {
	if title == "" {
		title = "Notice"
	}
	bd := &backdrop{}
	draw := func() {
		h, w := TermSize()
		_, bw := popupSize(h, w, max(44, widest(body)+4, VLen(title)+8, keysWidth(" Enter dismiss ")), h)
		lines := wrapLines(body, max(10, bw-4))
		// border, blank, the text, blank, OK, blank, border
		bh, bw := popupSize(h, w, bw, len(lines)+6)
		root, cv := popup(bd, h, w, bh, bw, title, " Enter dismiss ")
		row := 3
		for _, line := range lines {
			if row > bh-4 {
				break
			}
			cv.put(row, 3, C.Text+line+Reset)
			row++
		}
		cv.put(max(2, bh-2), 3, C.SelBg+C.Bright+" OK "+Reset)
		root.paint()
	}
	draw()
	sz := watchSize()
	for {
		k := readNav(false)
		if k == nil {
			if sz.changed() {
				draw()
			}
			continue
		}
		switch {
		case k.Name == "esc" || k.Name == "\r" || k.Name == "\n" || k.Name == " " || k.Name == "q":
			return
		case k.Mouse != nil && k.Mouse.Click:
			return
		}
	}
}

const confirmKeys = " ←→ choose   Enter confirm   Esc cancel   y/n "

// Confirm asks Yes/No with No preselected. y/n answer directly.
func Confirm(title, warning string) bool {
	if title == "" {
		title = "Are you sure?"
	}
	sel := 2
	type hitBox struct{ row, col0, col1, sel int }
	var hits []hitBox
	bd := &backdrop{}
	draw := func() {
		h, w := TermSize()
		_, bw := popupSize(h, w, max(44, widest(warning)+4, VLen(title)+8, keysWidth(confirmKeys)), h)
		lines := wrapLines(warning, max(10, bw-4))
		bh, bw := popupSize(h, w, bw, len(lines)+6)
		root, cv := popup(bd, h, w, bh, bw, title, confirmKeys)
		hits = hits[:0]
		row := 3
		for _, line := range lines {
			if row > bh-4 {
				break
			}
			if line != "" {
				cv.put(row, 3, C.Text+line+Reset)
			}
			row++
		}
		yes := C.Muted + " Yes " + Reset
		if sel == 1 {
			yes = C.SelBg + C.Bright + " Yes " + Reset
		}
		no := C.Muted + " No " + Reset
		if sel == 2 {
			no = C.SelBg + C.Bright + " No " + Reset
		}
		row = max(2, bh-2)
		cv.put(row, 3, yes+"   "+no)
		ar, ac := cv.absRow(row), cv.dx
		hits = append(hits, hitBox{ar, ac + 3, ac + 7, 1}, hitBox{ar, ac + 11, ac + 14, 2})
		root.paint()
	}
	draw()
	sz := watchSize()
	for {
		k := readNav(false)
		if k == nil {
			if sz.changed() {
				draw()
			}
			continue
		}
		switch n := k.Name; {
		case n == "esc" || n == "q" || n == "n" || n == "N" || n == "\x03":
			return false
		case n == "y" || n == "Y":
			return true
		case n == "left":
			sel = 1
			draw()
		case n == "right":
			sel = 2
			draw()
		case n == "\r" || n == "\n" || n == " ":
			return sel == 1
		case k.Mouse != nil && k.Mouse.Click:
			for _, hb := range hits {
				if k.Mouse.Row == hb.row && k.Mouse.Col >= hb.col0 && k.Mouse.Col <= hb.col1 {
					sel = hb.sel
					draw()
					return sel == 1
				}
			}
		}
	}
}

// Choice is a list entry with a label shown and a value returned.
// markOn and markOff show whether a list entry is marked.
const (
	markOn  = "[●] "
	markOff = "[ ] "
)

type Choice struct {
	Label string
	Value string
}

// Choose offers a filterable list of strings.
func Choose(title string, items []string, subtitle string) (string, bool) {
	all := make([]Choice, len(items))
	for i, s := range items {
		all[i] = Choice{Label: s, Value: s}
	}
	c, ok := ChooseItem(title, all, subtitle)
	return c.Value, ok
}

// ChooseItem offers a filterable list. Powered by baseRuntime's virtualList
// and lineBuffer engines, it scales to 100,000+ entries with zero-alloc
// Unicode case-insensitive incremental narrowing, cursor movement + paste
// in the filter bar, and Space multi-toggle (returning comma-joined values
// in Choice.Value when multiple items are marked).
func ChooseItem(title string, items []Choice, subtitle string) (Choice, bool) {
	if title == "" {
		title = "Select"
	}
	vl := newVirtualList(items, nil)
	filterBuf := newLineBuffer("")
	lastFilter := ""
	sel, top := 1, 1
	hit := map[int]int{}
	lastIdx, lastTime := 0, time.Time{}

	syncFilter := func() {
		q := filterBuf.String()
		if q != lastFilter {
			vl.filter(q)
			lastFilter = q
			sel, top = 1, 1
		}
	}

	page := 1
	bd := &backdrop{}
	longest := 0
	for _, it := range items {
		if l := VLen(it.Label); l > longest {
			longest = l
		}
		if longest > 200 {
			break
		}
	}
	draw := func() {
		syncFilter()
		h, w := TermSize()
		total := vl.count()
		if sel > total {
			sel = max(1, total)
		}
		if sel < 1 {
			sel = 1
		}
		// Inside the box: an optional subtitle, the filter, a blank row,
		// then the list. The box is sized for every item, not the filtered
		// ones, so it does not jump about while typing.
		listTop := 4
		if subtitle != "" {
			listTop = 5
		}
		boxTitle := title
		if len(items) >= 50 {
			boxTitle = fmt.Sprintf("%s · %d/%d", title, total, len(items))
		}
		footer := " ↑↓ move   Space mark   Enter select   Esc cancel "
		if vl.hasSelected() {
			footer = fmt.Sprintf(" Space toggle (%d)   Enter confirm   Esc cancel ", len(vl.selectedSet))
		}
		// One blank row under the list keeps it off the bottom border.
		bh, bw := popupSize(h, w, max(48, longest+12, VLen(subtitle)+6, VLen(boxTitle)+8, keysWidth(footer)), listTop+max(1, len(items))+1)
		listH := max(1, bh-listTop-1)
		page = listH
		if sel < top {
			top = sel
		}
		if sel > top+listH-1 {
			top = sel - listH + 1
		}
		if top < 1 {
			top = 1
		}
		root, cv := popup(bd, h, w, bh, bw, boxTitle, footer)
		filterRow := 2
		if subtitle != "" {
			cv.put(2, 3, C.Muted+Truncate(subtitle, bw-4)+Reset)
			filterRow = 3
		}
		filter := filterBuf.String()
		if filter != "" {
			cv.put(filterRow, 3, C.Bright+"filter: "+filterBuf.renderViewport(max(8, bw-14), "")+Reset)
		} else {
			cv.put(filterRow, 3, C.Dim+"type to filter"+Reset)
		}
		hit = map[int]int{}
		if total == 0 {
			cv.put(listTop, 3, C.Muted+"no matches"+Reset)
		} else {
			for i := 0; i < listH; i++ {
				idx := top + i
				if idx > total {
					break
				}
				row := listTop + i
				it := vl.itemAt(idx - 1)
				valKey := it.Value
				if valKey == "" {
					valKey = it.Label
				}
				mark := ""
				if vl.hasSelected() {
					if vl.selectedSet[valKey] {
						mark = C.On + markOn + Reset
					} else {
						mark = C.Muted + markOff + Reset
					}
				}
				label := Truncate(it.Label, max(4, bw-10))
				if pressedAt(cv, row) {
					cv.paintPressed(row, bw, label)
				} else if idx == sel {
					cv.selBar(row, bw)
					if vl.hasSelected() {
						box := markOff
						if vl.selectedSet[valKey] {
							box = markOn
						}
						cv.put(row, 3, C.SelBg+C.Accent+C.Bold+cursorMark()+box+C.Bright+label+Reset)
					} else {
						cv.put(row, 3, C.SelBg+C.Accent+C.Bold+cursorMark()+C.Bright+label+Reset)
					}
				} else if hoverAt(cv, row) {
					cv.put(row, 3, C.Muted+hoverMark+Reset+mark+C.Bright+label+Reset)
				} else {
					cv.put(row, 3, "  "+mark+C.Text+label+Reset)
				}
				hit[cv.absRow(row)] = idx
			}
		}
		root.paint()
	}

	draw()
	sz := watchSize()
	var ptrSeen pointerWatch
	for {
		syncFilter()
		total := vl.count()
		k := readNav(true)
		if k == nil {
			if sz.changed() || ptrSeen.changed() {
				draw()
			}
			continue
		}
		n := k.Name
		switch {
		case n == "esc" || n == "\x03":
			if filterBuf.String() != "" && n == "esc" {
				filterBuf.setString("")
				draw()
				continue
			}
			return Choice{}, false
		case n == "up":
			if sel > 1 {
				sel--
				draw()
			}
		case n == "down":
			if sel < total {
				sel++
				draw()
			}
		case n == "pgup" || n == "pgdn":
			if n == "pgup" {
				sel = max(1, sel-page)
			} else {
				sel = min(max(1, total), sel+page)
			}
			draw()
		case (n == "home" || n == "end") && filterBuf.String() == "":
			if n == "home" {
				sel = 1
			} else {
				sel = max(1, total)
			}
			draw()
		case n == " " && filterBuf.String() == "":
			if sel >= 1 && sel <= total {
				vl.toggleAt(sel - 1)
				draw()
			}
		case k.Mouse != nil && (k.Mouse.WheelUp || k.Mouse.WheelDown):
			if k.Mouse.WheelUp && sel > 1 {
				sel--
				draw()
			} else if k.Mouse.WheelDown && sel < total {
				sel++
				draw()
			}
		case k.Mouse != nil && k.Mouse.Click:
			if idx, ok := hit[k.Mouse.Row]; ok {
				now := time.Now()
				if idx == sel && lastIdx == idx && now.Sub(lastTime) <= 450*time.Millisecond {
					if idx <= total {
						return vl.itemAt(idx - 1), true
					}
					return Choice{}, false
				}
				sel = idx
				lastIdx, lastTime = idx, now
				draw()
			}
		case n == "\r" || n == "\n":
			if vl.hasSelected() {
				vals := vl.selectedValues()
				joined := strings.Join(vals, ",")
				return Choice{Label: joined, Value: joined}, true
			}
			if sel >= 1 && sel <= total {
				return vl.itemAt(sel - 1), true
			}
		default:
			if filterBuf.handleKey(k) {
				draw()
			}
		}
	}
}

type editOpts struct {
	title, subtitle, initial, placeholder string
	mask                                  bool
	history                               []string // oldest first; ↑ and ↓ step through it
}

// historyNav steps through earlier entries with ↑ and ↓, keeping what was
// being typed as the newest position.
type historyNav struct {
	items []string
	pos   int // len(items) is the line being typed
	draft string
}

func newHistoryNav(items []string) *historyNav { return &historyNav{items: items, pos: len(items)} }

// step moves by d (-1 older, +1 newer) from current, and returns the
// text to show and whether it moved.
func (h *historyNav) step(d int, current string) (string, bool) {
	next := h.pos + d
	if next < 0 || next > len(h.items) {
		return current, false
	}
	if h.pos == len(h.items) {
		h.draft = current
	}
	h.pos = next
	if next == len(h.items) {
		return h.draft, true
	}
	return h.items[next], true
}

func editLoop(o editOpts) (string, bool) {
	buf := newLineBuffer(o.initial)
	hist := newHistoryNav(o.history)
	reveal := false
	value := func() string { return buf.String() }
	bd := &backdrop{}
	draw := func() {
		h, w := TermSize()
		hint := " ←→ move   Enter confirm   Esc cancel "
		if len(o.history) > 0 {
			hint = " ↑↓ earlier   Enter confirm   Esc cancel "
		}
		if o.mask {
			word := "reveal"
			if reveal {
				word = "hide"
			}
			hint = " ←→ move   Enter confirm   Esc cancel   Tab " + word + " "
		}
		// border, [subtitle, blank,] the field, blank, border
		want := 4
		if o.subtitle != "" {
			want = 6
		}
		bh, bw := popupSize(h, w, max(60, VLen(o.subtitle)+6, VLen(o.title)+8, keysWidth(hint)), want+1)
		root, cv := popup(bd, h, w, bh, bw, o.title, hint)
		fieldRow := 3
		if o.subtitle != "" {
			cv.put(3, 3, C.Muted+Truncate(o.subtitle, bw-4)+Reset)
			fieldRow = 5
		}
		fieldRow = min(fieldRow, bh-1)
		fieldW := bw - 4
		shown := value()
		maskChar := ""
		if o.mask && !reveal {
			maskChar = "*"
		}
		if shown == "" {
			inputField(cv, fieldRow, 3, fieldW, C.Accent+"▏"+C.Dim+o.placeholder)
		} else {
			inputField(cv, fieldRow, 3, fieldW, buf.renderViewport(max(6, fieldW-1), maskChar))
		}
		root.paint()
	}
	draw()
	sz := watchSize()
	for {
		k := readNav(true)
		if k == nil {
			if sz.changed() {
				draw()
			}
			continue
		}
		n := k.Name
		switch {
		case n == "esc":
			return "", false
		case n == "\r" || n == "\n":
			return value(), true
		case o.mask && n == "\t":
			reveal = !reveal
			draw()
		case len(o.history) > 0 && (n == "up" || n == "down"):
			d := -1
			if n == "down" {
				d = 1
			}
			if text, ok := hist.step(d, value()); ok {
				buf.setString(text)
				draw()
			}
		default:
			if buf.handleKey(k) {
				draw()
			}
		}
	}
}

// Prompt asks for a line of text, starting from initial.
func Prompt(title, subtitle, initial string) (string, bool) {
	if title == "" {
		title = "Enter value"
	}
	return editLoop(editOpts{title: title, subtitle: subtitle, initial: initial, placeholder: "(type a value)"})
}

// PromptHistory is Prompt with earlier entries (oldest first) that ↑ and
// ↓ bring back, as in a shell.
func PromptHistory(title, subtitle, initial string, history []string) (string, bool) {
	if title == "" {
		title = "Enter value"
	}
	return editLoop(editOpts{title: title, subtitle: subtitle, initial: initial, placeholder: "(type a command)", history: history})
}

// Password asks for masked text. Tab reveals it.
func Password(title, subtitle string) (string, bool) {
	if title == "" {
		title = "Password"
	}
	return editLoop(editOpts{title: title, subtitle: subtitle, mask: true, placeholder: "(type password)"})
}

// TypeToConfirm only returns true once phrase has been typed exactly.
func TypeToConfirm(title, warning, phrase string) bool {
	if title == "" {
		title = "Confirm"
	}
	buf := newLineBuffer("")
	bd := &backdrop{}
	draw := func() {
		typed := buf.String()
		h, w := TermSize()
		match := typed == phrase
		keys := " type the phrase   Esc cancel "
		if match {
			keys = " Enter confirm   Esc cancel "
		}
		_, bw := popupSize(h, w, max(52, widest(warning)+4, VLen(phrase)+24, VLen(title)+8, keysWidth(keys)), h)
		lines := wrapLines(warning, max(10, bw-4))
		// border, blank, warning, blank, the ask, the field, blank, border
		bh, bw := popupSize(h, w, bw, len(lines)+7)
		root, cv := popup(bd, h, w, bh, bw, title, keys)
		row := 3
		for _, line := range lines {
			if row > bh-5 {
				break
			}
			cv.put(row, 3, C.Text+line+Reset)
			row++
		}
		cv.put(bh-3, 3, C.Dim+"Type "+Reset+C.Bright+phrase+Reset+C.Dim+" to confirm:"+Reset)
		fieldW := bw - 4
		if match {
			inputField(cv, bh-2, 3, fieldW, C.On+typed)
		} else {
			inputField(cv, bh-2, 3, fieldW, buf.renderViewport(max(6, fieldW-1), ""))
		}
		root.paint()
	}
	draw()
	sz := watchSize()
	for {
		k := readNav(true)
		if k == nil {
			if sz.changed() {
				draw()
			}
			continue
		}
		n := k.Name
		switch {
		case n == "esc":
			return false
		case n == "\r" || n == "\n":
			if buf.String() == phrase {
				return true
			}
		default:
			if buf.handleKey(k) {
				draw()
			}
		}
	}
}
