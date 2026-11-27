package mint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CurtisD4q/pmin-base/util"
)

// The key list (?) and the quick start.
//
// ? on a screen shows every key that works there, in groups, in a box in
// the middle of the screen: moving, the rows, the screen's own keys, and
// the keys that work everywhere. A screen whose own Keys use ? keeps it.
//
// App.QuickStart is a few lines shown in the same kind of box the first
// time a program is run, before anything else.

// keyLine is one line of a key list: a group heading (key empty) or a key
// and what it does.
type keyLine struct{ key, does string }

// keyList is every key for the screen ss is showing.
func (ss *screenSession) keyList() []keyLine {
	s := ss.screen
	var l []keyLine
	group := func(name string) {
		if len(l) > 0 {
			l = append(l, keyLine{}) // a blank line between groups
		}
		l = append(l, keyLine{does: name})
	}
	add := func(key, does string) { l = append(l, keyLine{key, does}) }

	group("Moving")
	add("↑ ↓", "move up and down")
	add("PgUp PgDn", "a page at a time")
	add("Home End", "the first or last row")
	if s.focusCount() > 1 {
		add("Tab S-Tab", "the next or previous pane")
		if s.boxed() {
			add(fmt.Sprintf("1 – %d", min(9, s.paneCount())), "go straight to a pane")
		}
		if len(s.Panes) > 0 {
			add("[ ]", "make the pane smaller or larger")
			add("|", "hide or show the pane")
		}
	}

	group("Rows")
	add("Enter", "open the row, or change it")
	add("Space", "tick or change the row")
	add("y", "copy the row")
	if ss.ownKey("/") == nil {
		add("/", "filter the list; Esc clears the filter")
	}

	if len(s.Keys) > 0 {
		group("This screen")
		for _, k := range s.Keys {
			if k.Do == nil || reservedKeys[k.Key] {
				continue
			}
			does := k.Label
			if does == "" {
				does = hintFor(s.Hints, k.Key)
			}
			if does == "" {
				does = "(the screen's own key)"
			}
			add(k.Key, does)
		}
	}

	group("Everywhere")
	add("b  Esc", "back")
	add("q", "quit")
	add("?", "this list")
	return l
}

// ownKey is the screen's own key for name, or nil.
func (ss *screenSession) ownKey(name string) *ScreenKey {
	for i := range ss.screen.Keys {
		if k := &ss.screen.Keys[i]; k.Key == name && k.Do != nil {
			return k
		}
	}
	return nil
}

// hintFor is the words after key in hints ("m menu   ? help"), or "".
func hintFor(hints, key string) string {
	for _, h := range strings.Split(hints, "   ") {
		h = strings.TrimSpace(h)
		if rest, ok := strings.CutPrefix(h, key+" "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// showKeys shows the key list for the screen until a key is pressed.
func (ss *screenSession) showKeys() {
	showKeyBox("Keys", ss.keyList(), " ↑↓ scroll   any other key closes ")
}

// showKeyBox shows lines in a box in the middle of the screen. ↑↓ and
// PgUp/PgDn scroll a list longer than the screen; any other key closes it.
func showKeyBox(title string, lines []keyLine, keys string) {
	bd := &backdrop{}
	keyW := 0
	textW := 0
	for _, l := range lines {
		if l.key != "" {
			keyW = max(keyW, VLen(l.key))
			textW = max(textW, VLen(l.does))
		} else {
			textW = max(textW, VLen(l.does)-keyW-2)
		}
	}
	top := 0
	draw := func() {
		h, w := TermSize()
		// border, blank, the lines, blank, border
		bh, bw := popupSize(h, w, max(44, keyW+textW+8, VLen(title)+8, keysWidth(keys)), len(lines)+4)
		room := max(1, bh-4)
		top = max(0, min(top, len(lines)-room))
		root, cv := popup(bd, h, w, bh, bw, title, keys)
		for i := 0; i < room && top+i < len(lines); i++ {
			l := lines[top+i]
			row := 3 + i
			if l.key == "" {
				if l.does != "" {
					cv.put(row, 3, C.Accent+C.Bold+Truncate(l.does, bw-4)+Reset)
				}
				continue
			}
			k := l.key + strings.Repeat(" ", max(0, keyW-VLen(l.key)))
			cv.put(row, 3, C.Bright+C.Bold+k+Reset+"  "+C.Text+Truncate(l.does, max(1, bw-6-keyW))+Reset)
		}
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
		switch k.Name {
		case "up":
			top--
		case "down":
			top++
		case "pgup":
			top -= 10
		case "pgdn":
			top += 10
		default:
			if k.Mouse == nil || k.Mouse.Click {
				return
			}
			continue
		}
		top = max(0, top)
		draw()
	}
}

// quickStartSeen is the file that records that a program's quick start
// has been shown.
func quickStartSeen() string {
	name := filepath.Base(os.Args[0])
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < ' ' {
			return '-'
		}
		return r
	}, name)
	return filepath.Join(util.ConfigDir(), "quick-start", name+".seen")
}

// showQuickStart shows the app's quick start, the first time only.
func (a *App) showQuickStart() {
	if len(a.QuickStart) == 0 {
		return
	}
	seen := quickStartSeen()
	if _, err := os.Stat(seen); err == nil {
		return
	}
	lines := make([]keyLine, 0, len(a.QuickStart))
	for _, s := range a.QuickStart {
		lines = append(lines, keyLine{key: " ", does: CleanLine(s)})
	}
	showKeyBox("Quick start", lines, " any key to begin ")
	// Shown once: a program that cannot write its settings folder shows
	// it again next time, which does no harm.
	if os.MkdirAll(filepath.Dir(seen), 0o700) == nil {
		_ = os.WriteFile(seen, nil, 0o600)
	}
}
