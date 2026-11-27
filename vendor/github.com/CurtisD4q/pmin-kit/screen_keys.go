package mint

import (
	"math"
	"strings"
	"time"
)

func (ss *screenSession) takeNav() *screenResult {
	if ss.nav.op == "" {
		return nil
	}
	op, pushed := ss.nav.op, ss.nav.screen
	ss.nav.op, ss.nav.screen = "", nil
	switch op {
	case "push":
		return &screenResult{op: "push", screen: pushed}
	case "pop":
		return &screenResult{op: "back"}
	case "page":
		return &screenResult{op: "page", page: ss.nav.page}
	case "pages":
		if len(ss.screen.rail) > 0 {
			ss.screen.railOn = true
			ss.screen.railSel = max(0, ss.screen.railCur)
		}
		return nil
	}
	return &screenResult{op: "quit"}
}

func (ss *screenSession) editKey(k *Key) bool {
	ed := ss.st.editing
	if !ed.numeric && ed.buf == nil {
		ed.buf = newLineBuffer(strings.Join(ed.chars, ""))
	}
	switch {
	case k.Mouse != nil:
		ss.commitEdit()
		return false
	case k.Name == "esc":
		ss.st.editing = nil
	case k.Name == "\r" || k.Name == "\n":
		ss.commitEdit()
	case k.Name == "up":
		ss.commitEdit()
		ss.st.row--
		ss.clamp()
	case k.Name == "down":
		ss.commitEdit()
		ss.st.row++
		ss.clamp()
	case ed.numeric:
		if k.Name == "\x7f" || k.Name == "\b" {
			if len(ed.chars) > 0 {
				ed.chars = ed.chars[:len(ed.chars)-1]
			}
			break
		}
		if k.Special {
			break
		}
		if (k.Name >= "0" && k.Name <= "9" && len(k.Name) == 1) || (k.Name == "-" && len(ed.chars) == 0) {
			ed.chars = append(ed.chars, k.Name)
		}
	default:
		if ed.buf.handleKey(k) {
			ed.chars = ed.buf.clusters
		}
	}
	return true
}

func (ss *screenSession) change(dir int) {
	it, idx := ss.current()
	if it == nil {
		return
	}
	switch {
	case it.kind == "section":
		ss.toggleSection(it, dir)
	case it.wtype == wtToggle || it.wtype == wtCheck:
		ss.setToggle(it, dir)
	case it.wtype == wtRadio:
		if dir >= 0 {
			ss.chooseRadio(it)
		}
	case it.wtype == wtSlider:
		if dir != 0 {
			lo, hi := float64(it.min), float64(it.max)
			cur := math.Max(lo, math.Min(hi, ss.numberValue(it, idx)))
			ss.setSlider(it, cur+float64(dir)*float64(it.stepBy))
		}
	case it.wtype == wtStepper:
		if dir == 0 {
			ss.startEdit(it, idx)
		} else {
			ss.setStepper(it, addStep(intFromNumber(ss.numberValue(it, idx)), stepDelta(dir, it.stepBy)))
		}
	case it.wtype == wtPicker:
		ss.cyclePicker(it, dir)
	case it.wtype == wtText:
		if dir == 0 {
			ss.startEdit(it, idx)
		}
	case it.wtype == wtTableRow && it.table != nil && it.table.viewer && dir != 0:
		// A viewer scrolls sideways: ← → move through the columns.
		scrollTable(it.table, dir*tableScrollStep, 0)
	case dir == 0 && it.onActivate != nil:
		protect(func() { it.onActivate(ss.ctx) })
	}
}

// copyRow puts the selected row on the clipboard (y): a table row's
// cells (tab-separated), a value as shown, or else the row's label.
func (ss *screenSession) copyRow() {
	it, idx := ss.current()
	if it == nil {
		ToastWarn("Nothing to copy here")
		return
	}
	switch {
	case it.wtype == wtTableRow && len(it.cells) > 0:
		copied(copyOutput(strings.Join(it.cells, "\t")), "the row")
		return
	case it.wtype == wtLogLine:
		copied(copyOutput(it.logLine.Text), "the line")
		return
	case it.kind == "section":
		copied(Copy(it.name), "the heading")
		return
	}
	switch it.wtype {
	case wtValue, wtText, wtPicker, wtStepper, wtSlider:
		if val, _, failed := ss.readValue(it, ss.keyOf(it, idx)); !failed && tostr(val) != "" {
			copied(Copy(tostr(val)), "the value")
			return
		}
	}
	copied(Copy(it.label), "the label")
}

// grab gives k to the screen's Grab, and says what it asked for and
// whether it used the key.
func (ss *screenSession) grab(k *Key) (*screenResult, bool) {
	used := false
	protect(func() { used = ss.screen.Grab(ss.ctx, k) })
	if res := ss.takeNav(); res != nil {
		return res, true
	}
	return nil, used || ss.screen.railOn
}

func (ss *screenSession) onKey(k *Key, h, w, sc *int) (*screenResult, bool) {
	dirty := false
	consumed := false
	if ss.screen.focusCount() > 1 {
		ss.st = ss.screen.uiAt(ss.screen.focus)
	}
	if k != nil && k.Mouse != nil && (len(ss.screen.Panes) > 0 || len(ss.screen.Dock) > 0) {
		switch {
		case ss.screen.dockRows > 0 && k.Mouse.Row >= ss.screen.dockTop && k.Mouse.Row < ss.screen.dockTop+ss.screen.dockRows:
			ss.screen.focus = ss.screen.paneCount()
			ss.st = ss.screen.uiAt(ss.screen.focus)
			dirty = true
		default:
			if idx, ok := ss.screen.bandIndexAt(k.Mouse.Row, k.Mouse.Col); ok {
				ss.screen.focus = idx
				ss.st = ss.screen.uiAt(idx)
				dirty = true
			}
		}
	}
	if ss.screen.focusCount() > 1 && k != nil && (k.Name == "\t" || k.Name == "btab") {
		if ss.st.editing != nil {
			ss.commitEdit()
		}
		dir := 1
		if k.Name == "btab" {
			dir = -1
		}
		ss.screen.cycleFocus(dir)
		ss.st = ss.screen.uiAt(ss.screen.focus)
		return nil, true
	}
	if ss.filterKey(k) {
		return nil, true
	}
	if ss.st.editing == nil && ss.screen.paneDigit(k) {
		ss.st = ss.screen.uiAt(ss.screen.focus)
		return nil, true
	}
	if ss.st.editing != nil && k != nil {
		dirty = true
		consumed = ss.editKey(k)
	}
	switch {
	case k == nil:
		*sc = *sc + 1
		if rt.consumeResize() || *sc >= 2 {
			*sc = 0
			if nh, nw := TermSize(); nh != *h || nw != *w {
				*h, *w = nh, nw
				dirty = true
			}
		}
		if ss.screen.Poll != nil {
			changed := false
			protect(func() { changed = ss.screen.Poll(ss.screen) })
			if changed {
				dirty = true
			}
		}
		if ss.screen.Every > 0 && time.Since(ss.lastDraw) >= ss.screen.Every {
			dirty = true
		}
		if footerSig(ss.status()) != ss.st.footSig {
			dirty = true
		}
		if ss.screen.ptrSeen.changed() {
			dirty = true
		}
	case consumed:
	case k.Mouse != nil && (k.Mouse.WheelUp || k.Mouse.WheelDown):
		n := len(selectable(ss.st.items))
		if k.Mouse.WheelUp && ss.st.row > 1 {
			ss.st.row--
			dirty = true
		}
		if k.Mouse.WheelDown && ss.st.row < n {
			ss.st.row++
			dirty = true
		}
	case k.Mouse != nil && k.Mouse.Click:
		// A pane's hit columns are relative to its own window.
		local := *k.Mouse
		local.Col -= ss.st.x0
		m := &local
		if sb := ss.st.sb; sb != nil && m.Col == sb.col && m.Row >= sb.top && m.Row <= sb.top+sb.rows-1 {
			frac := float64(m.Row-sb.top) / float64(max(1, sb.rows-1))
			ss.st.scroll = clampInt(floorf(frac*float64(sb.maxoff)+0.5), 0, sb.maxoff)
			sel := selectable(ss.st.items)
			firstVis, lastVis := ss.st.scroll+1, ss.st.scroll+sb.rows
			if ss.st.row >= 1 && ss.st.row <= len(sel) {
				if cur := sel[ss.st.row-1]; cur < firstVis || cur > lastVis {
					for si, ii := range sel {
						if ii >= firstVis && ii <= lastVis {
							ss.st.row = si + 1
							break
						}
					}
				}
			}
			dirty = true
		} else if entry, ok := ss.st.hit[m.Row]; ok {
			if si := ss.selIndexOf(entry.idx); si >= 1 {
				now := time.Now()
				double := si == ss.st.row && ss.lastClickIdx == entry.idx && ss.lastClickPane == ss.st && now.Sub(ss.lastClickTime) <= 450*time.Millisecond
				ss.st.row = si
				ss.lastClickIdx, ss.lastClickTime, ss.lastClickPane = entry.idx, now, ss.st
				dirty = true
				it := &ss.st.items[entry.idx-1]
				onMark := m.Col == 5 || m.Col == 6
				switch {
				case entry.header:
					if double || m.Col == 3 || m.Col == 4 {
						ss.toggleSection(it, 0)
						ss.lastClickIdx = 0
					}
				case entry.slider:
					rel := clampInt(m.Col-entry.sx0, 0, entry.sw-1)
					lo, hi := float64(it.min), float64(it.max)
					v := float64(rel)/float64(max(1, entry.sw-1))*(hi-lo) + lo
					if it.snap {
						s := float64(it.stepBy)
						v = lo + math.Floor((v-lo)/s+0.5)*s
					}
					ss.setSlider(it, math.Floor(v+0.5))
				case entry.stepper && m.Col == entry.minusCol:
					ss.setStepper(it, addStep(intFromNumber(ss.numberValue(it, entry.idx)), negStep(it.stepBy)))
					ss.lastClickIdx = 0
				case entry.stepper && m.Col == entry.plusCol:
					ss.setStepper(it, addStep(intFromNumber(ss.numberValue(it, entry.idx)), it.stepBy))
					ss.lastClickIdx = 0
				case entry.mark && (onMark || double):
					if it.wtype == wtRadio {
						ss.chooseRadio(it)
					} else {
						ss.setToggle(it, 0)
					}
					ss.lastClickIdx = 0
				case double:
					switch {
					case it.wtype == wtText || it.wtype == wtStepper:
						ss.startEdit(it, entry.idx)
					case it.wtype == wtToggle:
						ss.setToggle(it, 0)
					case it.wtype == wtPicker:
						ss.cyclePicker(it, 0)
					case it.onActivate != nil:
						protect(func() { it.onActivate(ss.ctx) })
					}
				}
			}
		}
	case len(selectable(ss.st.items)) == 0 && scrollKey(k.Name) != 0:
		// Nothing here to select, such as a details card: the keys scroll
		// it instead.
		switch k.Name {
		case "home":
			ss.st.scroll = 0
		case "end":
			ss.st.scroll = len(ss.st.items)
		case "pgup", "pgdn":
			ss.st.scroll += scrollKey(k.Name) * max(1, ss.st.page-1)
		default:
			ss.st.scroll += scrollKey(k.Name)
		}
		ss.st.scroll = max(0, min(ss.st.scroll, len(ss.st.items)-max(1, ss.st.page)))
		dirty = true
	case k.Name == "home" || k.Name == "end" || k.Name == "pgup" || k.Name == "pgdn":
		n := len(selectable(ss.st.items))
		switch k.Name {
		case "home":
			ss.st.row = 1
		case "end":
			ss.st.row = n
		case "pgup":
			ss.st.row -= max(1, ss.st.page-1)
		case "pgdn":
			ss.st.row += max(1, ss.st.page-1)
		}
		ss.clamp()
		dirty = true
	case k.Name == "up":
		ss.st.row--
		ss.clamp()
		dirty = true
	case k.Name == "down":
		ss.st.row++
		ss.clamp()
		dirty = true
	case k.Name == "right":
		ss.change(1)
		dirty = true
	case k.Name == "left":
		ss.change(-1)
		dirty = true
	case k.Name == "H" || k.Name == "L":
		// Resize the table column under the selection, if there is one.
		if it, _ := ss.current(); it != nil && it.table != nil && it.table.viewer {
			d := 1
			if k.Name == "H" {
				d = -1
			}
			resizeTableColumn(it.table, it.table.hscroll, d)
			dirty = true
		}
	case k.Name == " " || k.Name == "\r" || k.Name == "\n":
		ss.change(0)
		dirty = true
	case ss.screenKey(k) != nil:
		key := ss.screenKey(k)
		protect(func() { key.Do(ss.ctx) })
		dirty = true
	case k.Name == "?" && !k.Special && ss.st.editing == nil:
		ss.showKeys()
		dirty = true
	case k.Name == "b" || k.Name == "esc":
		return &screenResult{op: "back"}, false
	case k.Name == "q" || k.Name == "\x03":
		return &screenResult{op: "quit"}, false
	case k.Name == "y" && !k.Special && ss.st.editing == nil:
		ss.copyRow()
		dirty = true
	case len(ss.screen.Panes) > 0 && ss.screen.focus < ss.screen.paneCount() && ss.st.editing == nil && (k.Name == "[" || k.Name == "]" || k.Name == "|"):
		switch k.Name {
		case "[":
			ss.screen.nudgeFocus(-1)
		case "]":
			ss.screen.nudgeFocus(1)
		default:
			ss.screen.toggleFocused()
		}
		dirty = true
	}
	if res := ss.takeNav(); res != nil {
		return res, false
	}
	return nil, dirty
}

// intFromNumber converts a clamped number value to int. Values outside
// the int range become MaxInt or MinInt instead of an implementation-
// defined conversion.
func intFromNumber(v float64) int {
	if v >= float64(math.MaxInt) {
		return math.MaxInt
	}
	if v <= float64(math.MinInt) {
		return math.MinInt
	}
	return int(v)
}

// stepDelta is dir*step without overflowing. Keyboard dir is -1, 0 or 1.
func stepDelta(dir, step int) int {
	if dir > 0 {
		return step
	}
	if dir < 0 {
		return negStep(step)
	}
	return 0
}

// negStep is -step. Negating MinInt does not fit in int; MaxInt is the
// closest value that does.
func negStep(step int) int {
	if step == math.MinInt {
		return math.MaxInt
	}
	return -step
}

// addStep is cur+delta clamped to the int range. A wrapping add at
// MaxInt or MinInt used to land on the other end of the stepper.
func addStep(cur, delta int) int {
	if delta > 0 {
		if cur > math.MaxInt-delta {
			return math.MaxInt
		}
		return cur + delta
	}
	if delta < 0 {
		if delta == math.MinInt {
			if cur < 0 {
				return math.MinInt
			}
			return cur - math.MaxInt - 1
		}
		if cur < math.MinInt-delta {
			return math.MinInt
		}
		return cur + delta
	}
	return cur
}

// reservedKeys are Mint's own, and the vim style's, which a screen's Keys
// never take.
var reservedKeys = map[string]bool{
	"b": true, "q": true, "y": true, " ": true, "H": true, "L": true, "[": true, "]": true, "|": true,
	"j": true, "k": true, "h": true, "l": true, "g": true, "G": true,
}

// screenKey is the screen's own key for k, or nil.
func (ss *screenSession) screenKey(k *Key) *ScreenKey {
	if k == nil || k.Special || k.Mouse != nil || ss.st.editing != nil || reservedKeys[k.Name] {
		return nil
	}
	for i := range ss.screen.Keys {
		if sk := &ss.screen.Keys[i]; sk.Key == k.Name && sk.Do != nil {
			return sk
		}
	}
	return nil
}

// scrollKey is the direction a key scrolls a pane that has nothing to
// select: -1 up, 1 down, 0 for a key that does not scroll.
func scrollKey(name string) int {
	switch name {
	case "up", "pgup", "home":
		return -1
	case "down", "pgdn", "end":
		return 1
	}
	return 0
}
