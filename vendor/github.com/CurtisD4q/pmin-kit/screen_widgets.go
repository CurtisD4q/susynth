package mint

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/CurtisD4q/pmin-base/util"
)

func (ss *screenSession) clamp() {
	n := len(selectable(ss.st.items))
	if n < 1 {
		ss.st.row = 1
		return
	}
	ss.st.row = clampInt(ss.st.row, 1, n)
}

func (ss *screenSession) readValue(it *Row, key string) (any, string, bool) {
	if !it.hasGet() {
		if it.hasValue {
			return it.value, "", false
		}
		return nil, "", false
	}
	val, msg, failed := it.callGet()
	if val == nil {
		if lg, ok := ss.st.lastGood[key]; ok && lg != nil {
			val = lg
		}
	} else {
		ss.st.lastGood[key] = val
	}
	return val, msg, failed
}

func (ss *screenSession) keyOf(it *Row, idx int) string {
	k := it.id
	if k == "" {
		k = string(it.wtype)
	}
	return fmt.Sprintf("%s:%d", k, idx)
}

// marked reports whether a checkbox or radio option is on.
// hooks are the session's widget and marked functions, for the painters.
func (ss *screenSession) hooks() rowHooks { return rowHooks{ss.widget, ss.marked} }

func (ss *screenSession) marked(it *Row, idx int) bool {
	val, _, _ := ss.readValue(it, ss.keyOf(it, idx))
	if it.wtype == wtRadio {
		return val != nil && tostr(val) == it.radioOpt
	}
	return truthy(val)
}

func (ss *screenSession) widget(it *Row, idx int) string {
	editingHere := ss.st.editing != nil && ss.st.editing.idx == idx
	if it.wtype == wtText && editingHere {
		if ss.st.editing.buf == nil {
			ss.st.editing.buf = newLineBuffer(strings.Join(ss.st.editing.chars, ""))
		}
		s := ss.st.editing.buf.String()
		if s == "" {
			ph := it.placeholder
			if ph == "" {
				ph = "…"
			}
			return C.Dim + ph + Reset + C.Accent + "▏" + Reset
		}
		return C.Bright + ss.st.editing.buf.renderViewport(28, "") + Reset
	}
	if it.wtype == wtStepper && editingHere {
		return C.Dim + "− " + Reset + C.Bright + strings.Join(ss.st.editing.chars, "") +
			C.Accent + "_" + Reset + C.Dim + " +" + Reset
	}
	if it.wtype == wtCheck || it.wtype == wtRadio || it.wtype == wtTableRow {
		return ""
	}
	val, msg, failed := ss.readValue(it, ss.keyOf(it, idx))
	if s, ok := val.(string); ok {
		val = CleanLine(s) // a value can come from anywhere: a settings file, a paste
	}
	if val == nil && it.wtype != wtAction && it.wtype != wtText {
		switch {
		case it.wtype == wtToggle:
			val = false
		case it.wtype == wtSlider || it.wtype == wtProgress:
			val = it.min
		case failed:
			return C.Amber + "  " + Truncate(msg, 18) + Reset
		case it.wtype == wtStepper:
			val = 0
		}
	}
	switch it.wtype {
	case wtToggle:
		if truthy(val) {
			return C.On + "● ON " + Reset
		}
		return C.Muted + "  OFF" + Reset
	case wtSlider:
		lo, hi := float64(it.min), float64(it.max)
		v := math.Max(lo, math.Min(hi, toNumber(val)))
		const bar = 12
		fill := floorf((v - lo) / (hi - lo) * bar)
		var num string
		switch {
		case it.format != nil || it.unit != "":
			num = " " + it.numberText(floorf(v))
		case it.min == 0 && it.max == 100:
			num = fmt.Sprintf(" %d%%", floorf(v))
		default:
			num = " " + strconv.Itoa(floorf(v))
		}
		return C.Accent + rt.profile.repeatTo("━", fill) + "●" +
			C.Dim + rt.profile.repeatTo("─", bar-fill) + Reset +
			C.Muted + num + Reset
	case wtProgress:
		v := math.Max(0, math.Min(100, toNumber(val)))
		const bar = 13
		fill := floorf(v / 100 * bar)
		num := fmt.Sprintf(" %d%%", floorf(v))
		if it.format != nil {
			num = " " + it.numberText(floorf(v))
		}
		return C.Accent + rt.profile.repeatTo("━", fill) +
			C.Dim + rt.profile.repeatTo("─", bar-fill) + Reset +
			C.Muted + num + Reset
	case wtStepper:
		v := int(toNumber(val))
		minus, plus := C.Accent+"−"+Reset, C.Accent+"+"+Reset
		if it.limited() && v <= it.min {
			minus = C.Dim + "−" + Reset
		}
		if it.limited() && v >= it.max {
			plus = C.Dim + "+" + Reset
		}
		return minus + " " + C.Text + Truncate(it.numberText(v), 20) + Reset + " " + plus
	case wtPicker:
		shown := "—"
		if val != nil && tostr(val) != "" {
			shown = tostr(val)
		}
		return C.Text + "‹ " + Truncate(shown, 24) + " ›" + Reset
	case wtAction:
		right := it.hint
		if right == "" && val != nil {
			right = tostr(val)
		}
		if right == "" {
			right = "open"
		}
		return C.Muted + Truncate(right, 22) + Reset
	case wtText:
		if val == nil || tostr(val) == "" {
			ph := it.placeholder
			if ph == "" {
				ph = "…"
			}
			return C.Dim + ph + Reset
		}
		return C.Text + Truncate(tostr(val), 28) + Reset
	}
	if val == nil || tostr(val) == "" {
		return C.Muted + "—" + Reset
	}
	return C.Muted + Truncate(tostr(val), 28) + Reset
}

func (ss *screenSession) current() (*Row, int) {
	sel := selectable(ss.st.items)
	if ss.st.row < 1 || ss.st.row > len(sel) {
		return nil, 0
	}
	idx := sel[ss.st.row-1]
	return &ss.st.items[idx-1], idx
}

func (ss *screenSession) cyclePicker(it *Row, dir int) {
	opts := it.resolveOptions()
	if len(opts) == 0 {
		if it.step != nil {
			protect(func() { it.step(dir) })
		}
		return
	}
	var cur any
	if it.getStr != nil {
		if v, _, failed := it.callGet(); !failed {
			cur = v
		}
	}
	idx := 1
	for i, v := range opts {
		if cur != nil && v == cur.(string) {
			idx = i + 1
			break
		}
	}
	step := dir
	if dir == 0 {
		step = 1
	}
	n := len(opts)
	next := ((idx-1+step)%n+n)%n + 1
	if it.setStr != nil {
		protect(func() { it.setStr(opts[next-1]) })
	}
}

func (ss *screenSession) setToggle(it *Row, dir int) {
	cur := false
	if it.getBool != nil {
		if v, _, failed := it.callGet(); !failed {
			cur = truthy(v)
		}
	}
	newv := dir > 0
	if dir == 0 {
		newv = !cur
	}
	if it.setBool != nil {
		protect(func() { it.setBool(newv) })
	}
}

func (ss *screenSession) chooseRadio(it *Row) {
	if it.setStr != nil {
		opt := it.radioOpt
		protect(func() { it.setStr(opt) })
	}
}

func (ss *screenSession) numberValue(it *Row, idx int) float64 {
	val, _, _ := ss.readValue(it, ss.keyOf(it, idx))
	return toNumber(val)
}

func (ss *screenSession) setSlider(it *Row, v float64) {
	n := clampInt(floorf(v+0.5), it.min, it.max)
	if it.setInt != nil {
		protect(func() { it.setInt(n) })
	}
}

func (ss *screenSession) setStepper(it *Row, n int) {
	if it.limited() {
		n = clampInt(n, it.min, it.max)
	}
	if it.setInt != nil {
		protect(func() { it.setInt(n) })
	}
}

func (ss *screenSession) toggleSection(it *Row, dir int) {
	c := !it.collapsed
	if dir > 0 {
		c = false
	} else if dir < 0 {
		c = true
	}
	key := it.id
	if key == "" {
		key = it.name
	}
	ss.st.collapsed[key] = c
	if it.openPtr != nil {
		*it.openPtr = !c
	}
}

func (ss *screenSession) startEdit(it *Row, idx int) {
	if it.wtype == wtStepper {
		cur := strconv.Itoa(int(ss.numberValue(it, idx)))
		ss.st.editing = &editState{idx: idx, chars: util.Chars(cur), numeric: true, commit: func(s string) {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				ToastWarn("Not a whole number: " + s)
				return
			}
			ss.setStepper(it, n)
		}}
		return
	}
	cur := ""
	if it.getStr != nil {
		if v, _, failed := it.callGet(); !failed && v != nil {
			cur = tostr(v)
		}
	} else if it.hasValue {
		cur = it.value
	}
	set := it.setStr
	ss.st.editing = &editState{idx: idx, chars: util.Chars(cur), buf: newLineBuffer(cur), commit: func(s string) {
		if set != nil {
			set(s)
		}
	}}
}

func (ss *screenSession) commitEdit() {
	ed := ss.st.editing
	if ed == nil {
		return
	}
	ss.st.editing = nil
	val := strings.Join(ed.chars, "")
	if ed.buf != nil && !ed.numeric {
		val = ed.buf.String()
	}
	protect(func() { ed.commit(val) })
}

func (ss *screenSession) selIndexOf(itemIdx int) int {
	n := 0
	for i := 1; i <= itemIdx && i <= len(ss.st.items); i++ {
		if ss.st.items[i-1].isSelectable() {
			n++
		}
	}
	return n
}
