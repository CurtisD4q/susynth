package mint

import (
	"fmt"
	"strings"
)

// Filtering a list with /.
//
// / starts a filter on the focused pane. What is typed narrows the pane to
// the rows that contain it, ignoring case: the rows that can be chosen,
// and a table's headings, which stay. Enter keeps the filter and goes back
// to moving; Esc clears it. While a filter is set it shows beside the
// pane's title on a boxed screen and in the footer on others, and Esc
// clears it before Esc goes back.

// filterRows narrows items to the rows that match f.
func filterRows(items []Row, f string) []Row {
	if f == "" {
		return items
	}
	q := strings.ToLower(f)
	var out []Row
	found := false
	for _, it := range items {
		switch {
		case it.pin || it.tableHead:
			out = append(out, it)
		case it.isSelectable() && strings.Contains(strings.ToLower(rowText(it)), q):
			out = append(out, it)
			found = true
		}
	}
	if !found {
		out = append(out, Note("Nothing here matches “"+CleanLine(f)+"”. Esc clears the filter."))
	}
	return out
}

// rowText is the text a filter looks in: a row's label and, for a table
// row, its cells.
func rowText(it Row) string {
	if len(it.cells) > 0 {
		return it.label + " " + strings.Join(it.cells, " ")
	}
	return it.label
}

// filterKey handles / and the keys typed into a filter. It reports
// whether it used k.
func (ss *screenSession) filterKey(k *Key) bool {
	if k == nil || k.Mouse != nil || ss.st.editing != nil {
		return false
	}
	st := ss.st
	if st.filtering {
		switch {
		case k.Name == "esc":
			st.filter, st.filtering = "", false
		case k.Name == "\r" || k.Name == "\n":
			st.filtering = false
		case k.Name == "\x7f" || k.Name == "\b":
			r := []rune(st.filter)
			if len(r) > 0 {
				st.filter = string(r[:len(r)-1])
			}
		case k.IsText():
			st.filter += k.Name
		default:
			return false // arrows still move through what is left
		}
		st.row, st.scroll = 1, 0
		return true
	}
	if k.Name == "esc" && st.filter != "" {
		st.filter = ""
		st.row, st.scroll = 1, 0
		return true
	}
	if k.Name == "/" && !k.Special && ss.screenKey(k) == nil && len(selectable(st.items)) > 0 {
		st.filtering = true
		return true
	}
	return false
}

// filterLine is what the footer shows for the focused pane's filter: the
// text being typed, with a cursor, or the filter that is set.
func (ss *screenSession) filterLine() string {
	st := ss.st
	if !st.filtering && st.filter == "" {
		return ""
	}
	n := 0
	if st.filter != "" {
		n = len(selectable(st.items))
	}
	count := ""
	if st.filter != "" {
		count = C.Muted + "  " + plural(n, "match") + Reset
	}
	if st.filtering {
		return C.Accent + C.Bold + "/ " + Reset + C.Bright + CleanLine(st.filter) + C.Accent + "▏" + Reset + count
	}
	return C.Muted + "filter: " + Reset + C.Bright + CleanLine(st.filter) + Reset + count + C.Muted + "  (Esc clears)" + Reset
}

// filterHints are the footer's keys while a filter is being typed.
const filterHints = " ↑↓ move   ⏎ keep   Esc clear "

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "ch") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
