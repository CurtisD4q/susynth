package mint

import "strings"

// Pop-up boxes.
//
// A dialog is drawn as a box in the middle of the screen, over a faint
// copy of the screen it was opened from, so it is plain what it belongs
// to. Its title is set into the top border and its keys into the bottom
// border, the way a boxed pane carries its title:
//
//	╭─ Delete the job? ───────────────────╮
//	│                                     │
//	│ This removes its history too.       │
//	│                                     │
//	│  Yes    No                          │
//	╰────────── ←→ choose · Esc cancel ─╯
//
// Inside the box, row 1 and column 1 are the border and the contents start
// at column 3, as on a full screen, so a dialog draws its rows the same way.

// activeScreen is the screen whose loop is running. A dialog opened from it
// draws it, faint, behind the box.
var activeScreen *Screen

// drawingBackdrop is set while the screen behind a dialog is being drawn.
var drawingBackdrop bool

// backdrop is the faint screen behind a dialog. It is drawn once, and again
// only when the terminal size changes, so typing into a dialog does not
// redraw the screen underneath on every key.
type backdrop struct {
	h, w  int
	frame string
}

func (b *backdrop) at(h, w int) string {
	if b.frame != "" && b.h == h && b.w == w {
		return b.frame
	}
	frame := ""
	// A dialog opened while the screen is being drawn (from its Refresh,
	// say) must not draw that screen again: it would open the dialog again.
	if s := activeScreen; s != nil && s.redraw != nil && !drawingBackdrop {
		drawingBackdrop = true
		protect(func() { frame = s.renderFrame(h, w) })
		drawingBackdrop = false
	}
	if frame == "" {
		lastFrame.Lock()
		frame = lastFrame.s
		lastFrame.Unlock()
	}
	if frame != "" {
		frame = dimFrame(frame) + Reset
	}
	b.h, b.w, b.frame = h, w, frame
	return frame
}

// popupSize fits a box wanting wantW by wantH cells into an h by w
// terminal, leaving a margin of the screen around it where there is room.
func popupSize(h, w, wantW, wantH int) (bh, bw int) {
	bw = min(wantW, w-4)
	if bw < 24 {
		bw = min(24, w)
	}
	bh = min(wantH, h-2)
	if bh < 3 {
		bh = min(3, h)
	}
	return bh, bw
}

// popup draws the screen behind, faint, and a box of bh by bw cells in the
// middle of it with title in the top border and keys in the bottom border.
// It returns the canvas to paint and a canvas for the inside of the box.
func popup(bd *backdrop, h, w, bh, bw int, title, keys string) (root, box *canvas) {
	root = &canvas{w: w}
	if bg := bd.at(h, w); bg != "" {
		root.b.WriteString(bg)
	} else {
		root.b.WriteString(esc + "2J")
	}
	top := (h-bh)/2 + 1
	left := (w-bw)/2 + 1
	right := left + bw - 1
	bottom := top + bh - 1
	side := C.Accent + look.Side + Reset
	blank := Reset + strings.Repeat(" ", max(0, bw-2))
	for r := top + 1; r < bottom; r++ {
		root.put(r, left, side+blank+side)
	}
	paintBoxEdge(root, top, left, right, boxLabel{title: CleanLine(title)}, true, true)
	paintKeysEdge(root, bottom, left, right, keys)
	return root, root.area(top, left, bw)
}

// paintKeysEdge is a box's bottom border with the keys set into it at the
// right:
//
//	╰──────── ↑↓ move · Enter select ─╯
func paintKeysEdge(cv *canvas, row, left, right int, keys string) {
	inner := right - left - 1
	if inner < 0 {
		return
	}
	keys = keysText(keys)
	label := ""
	if room := inner - 4; room >= 6 && keys != "" {
		label = " " + Truncate(keys, room) + " "
	}
	fill := inner - 1 - VLen(label)
	if label == "" {
		fill = inner
	}
	line := C.Accent
	var b strings.Builder
	b.WriteString(line + look.BottomLeft + strings.Repeat(look.Edge, max(0, fill)) + Reset)
	if label != "" {
		b.WriteString(C.Muted + label + Reset + line + look.Edge + Reset)
	}
	b.WriteString(line + look.BottomRight + Reset)
	cv.put(row, left, b.String())
}

// inputField draws a text field: a shaded band of width cells at row, col
// with text (already styled for the band) in it.
func inputField(cv *canvas, row, col, width int, text string) {
	if width < 1 {
		return
	}
	pad := max(0, width-VLen(text))
	cv.put(row, col, C.SelBg+C.Bright+text+C.SelBg+strings.Repeat(" ", pad)+Reset)
}

// widest is the widest of lines, in cells.
func widest(lines ...string) int {
	n := 0
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			n = max(n, VLen(part))
		}
	}
	return n
}

// keysText is a footer's keys as a bottom border shows them, joined by dots.
func keysText(keys string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(keys, "   ", " · ")), " ")
}

// keysWidth is how wide a box must be for keys to fit in its bottom border.
func keysWidth(keys string) int { return VLen(keysText(keys)) + 6 }
