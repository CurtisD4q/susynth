package mint

import (
	"fmt"
	"strings"
)

// Details that follow the selection.
//
// A details pane shows, in full, the item under the cursor in another
// pane: a job in a list of jobs, a tool in a list of tools. Its Refresh
// reads Selected and builds a Card. The card's heading stays in place
// while its body scrolls with ↑↓ and PgUp/PgDn when the pane has focus, and
// a different item's card starts at its top.

// CardOpts is a details card.
type CardOpts struct {
	// Key names what the card is about, such as a job's name. When it
	// changes the card starts again at the top.
	Key string
	// Title is the first line, in bold.
	Title string
	// Facts are short lines under the title, in the muted colour, such as
	// "daily at 02:00 · ran 3 hours ago". They stay in place with the title.
	Facts []string
	// Body is the text under the heading. It wraps to the pane; blank
	// lines separate paragraphs.
	Body string
}

const cardIDPrefix = "card:"

// Card is a details card row. Put it in a pane on its own.
func Card(o CardOpts) Row {
	cp := o
	return Row{kind: "row", wtype: wtCard, card: &cp}
}

// expandCard lays a card out at width: the pinned heading (title, facts and
// a rule), then the body's wrapped lines.
func expandCard(r Row, width int) []Row {
	o := r.card
	if o == nil {
		return nil
	}
	id := cardIDPrefix + o.Key
	textW := max(10, width-6)
	var out []Row
	head := func(row Row) {
		row.pin = true
		row.id = id
		out = append(out, row)
	}
	if t := CleanLine(o.Title); t != "" {
		for _, l := range wrapLines(t, textW) {
			head(Row{kind: "row", wtype: wtLabel, style: StyleHeading, label: l})
		}
	}
	for _, f := range o.Facts {
		if f = CleanLine(f); f != "" {
			head(Row{kind: "row", wtype: wtLabel, style: StyleCaption, label: Truncate(f, textW)})
		}
	}
	if len(out) > 0 {
		head(Row{kind: "row", wtype: wtLabel, style: StyleCaption, label: ""})
	}
	body := strings.ReplaceAll(o.Body, "\r\n", "\n")
	for _, para := range strings.Split(body, "\n") {
		para = CleanLine(para)
		lines := []string{""}
		if strings.TrimSpace(para) != "" {
			lines = wrapLines(para, textW)
		}
		for _, l := range lines {
			out = append(out, Row{kind: "row", wtype: wtLabel, style: StyleBody, label: l, cardBody: true, id: id})
		}
	}
	// Trailing blank lines would only add scrolling.
	for len(out) > 0 && out[len(out)-1].cardBody && strings.TrimSpace(out[len(out)-1].label) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// Selection is the row under the cursor in the pane being moved through.
type Selection struct {
	Pane  int    // 0 for the screen's own sections, 1 and up for Panes
	ID    string // the row's ID, if it has one
	Label string // the row's label, or a folding group's name
	// For a table row: its cells, the table's headers, and its index.
	Cells   []string
	Headers []string
	Index   int
}

// Selected reports the row under the cursor in the pane the reader last
// moved through. Moving focus to a pane with nothing to select, such as a
// details card, keeps the selection where it was, so the card does not
// change. A pane's Refresh can call it to follow the cursor. ok is false
// before anything has been selected.
func (s *Screen) Selected() (Selection, bool) {
	if s == nil || !s.selHave {
		return Selection{}, false
	}
	return s.sel, true
}

// MoveCursor moves the cursor of the pane in focus by d of the rows that
// can be selected, stopping at either end. A key that reorders a list calls
// it so the cursor stays on the row it moved. It does nothing before the
// screen has been drawn.
func (s *Screen) MoveCursor(d int) {
	if s == nil || s.focus < 0 || s.focus >= s.paneCount() {
		return
	}
	st := s.uiAt(s.focus)
	if st == nil {
		return
	}
	if n := len(selectable(st.items)); n > 0 {
		st.row = clampInt(st.row+d, 1, n)
		s.noteFocusSelection()
	}
}

// focusSig tells one selection from another.
func (s *Screen) focusSig() string {
	if !s.selHave {
		return ""
	}
	return fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s", s.sel.Pane, s.sel.ID, s.sel.Label,
		s.sel.Index, strings.Join(s.sel.Cells, "\x00"))
}

// noteFocusSelection records the row under the cursor in the focused pane,
// if that pane has anything to select.
func (s *Screen) noteFocusSelection() {
	if s == nil || s.focus < 0 || s.focus >= s.paneCount() {
		return
	}
	st := s.uiAt(s.focus)
	if st == nil {
		return
	}
	sel := selectable(st.items)
	if len(sel) == 0 {
		return
	}
	n := st.row
	if n < 1 || n > len(sel) {
		n = 1
	}
	row := st.items[sel[n-1]-1]
	label := row.label
	if row.kind == "section" {
		label = row.name // a folding group's heading is known by its name
	}
	s.sel = Selection{Pane: s.focus, ID: row.id, Label: CleanLine(label), Index: -1}
	if h, c, i, ok := row.TableCells(); ok {
		s.sel.Cells, s.sel.Headers, s.sel.Index = c, h, i
	}
	s.selHave = true
}
