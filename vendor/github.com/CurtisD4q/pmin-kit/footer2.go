package mint

import (
	"strings"
	"sync"
	"time"
)

// The two-row footer of a boxed screen.
//
// A boxed screen uses the two rows under its boxes, the screen's last two,
// as podmutt does:
//
//	⠹ refreshing 3 of 12…                      ● case default
//	↑↓ move   ⏎ open   / filter   ? keys   q quit
//
// The upper row is for what is happening: work in progress, a toast, a
// filter being typed, then the status badges. The lower row is the keys
// for the focused pane, each key in the accent colour and its action
// muted. Screens that are not boxed keep their one-row footer.

var activity struct {
	sync.Mutex
	msg   string
	start time.Time
}

// SetActivity shows msg with a spinner on the footer while work goes on in
// the background, such as "refreshing 3 of 12…". Call it again to change
// the words, and with "" when the work is done. Safe from any goroutine.
func SetActivity(msg string) {
	msg = CleanLine(msg)
	activity.Lock()
	if msg != "" && activity.msg == "" {
		activity.start = time.Now()
	}
	activity.msg = msg
	activity.Unlock()
}

// currentActivity is the work in progress and its spinner frame.
func currentActivity() (string, string, bool) {
	activity.Lock()
	defer activity.Unlock()
	if activity.msg == "" {
		return "", "", false
	}
	n := int(time.Since(activity.start)/(100*time.Millisecond)) % len(spinnerFrames)
	return activity.msg, spinnerFrames[n], true
}

// footer2 draws a boxed screen's two footer rows: line (a filter being
// typed), a toast or the work in progress, or the badges, on row h-1, and
// the keys on row h, the screen's last.
func (c *canvas) footer2(h, w int, hints string, status []Badge, line string) {
	room := max(0, w-4)
	c.put(h-1, 2, strings.Repeat(" ", max(0, w-2)))
	c.put(h, 2, strings.Repeat(" ", max(0, w-2)))
	left := ""
	switch msg, kind, hasToast := activeToast(); {
	case line != "":
		left = line
	case hasToast:
		gc, g := toastParts(msg, kind)
		left = gc + g + Reset + " " + C.Text + Truncate(msg, max(1, room-2)) + Reset
	default:
		if msg, spin, ok := currentActivity(); ok {
			left = C.Accent + spin + Reset + " " + C.Muted + Truncate(msg, max(1, room-2)) + Reset
		}
	}
	// The badges go on the right of the upper row, or on its left when
	// nothing else is there.
	var b strings.Builder
	used := 0
	for i, s := range status {
		add := s.width()
		if i > 0 {
			add += len(badgeGap)
		}
		if used+add > room-VLen(left)-3 {
			break
		}
		if i > 0 {
			b.WriteString(badgeGap)
		}
		b.WriteString(s.render())
		used += add
	}
	if left != "" {
		c.put(h-1, 3, left)
		if used > 0 {
			c.put(h-1, w-1-used, b.String())
		}
	} else if used > 0 {
		c.put(h-1, 3, b.String())
	}
	c.put(h, 3, accentHints(fitHints(hints, room)))
}

// accentHints draws "↑↓ move   ⏎ open   [ ] size" with each key in the
// accent colour and each action muted. Hints are separated by three spaces;
// a hint's key is its first word, "[ ]", or "Esc" and the word after it.
func accentHints(hints string) string {
	var out []string
	for _, h := range strings.Split(hints, "   ") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		key, action := h, ""
		if strings.HasPrefix(h, "[ ]") {
			key, action = "[ ]", strings.TrimSpace(h[len("[ ]"):])
		} else if f := strings.Fields(h); len(f) > 2 && f[0] == "Esc" {
			// "Esc m pages": a key pressed after Esc
			key, action = f[0]+" "+f[1], strings.Join(f[2:], " ")
		} else if i := strings.IndexByte(h, ' '); i > 0 {
			key, action = h[:i], strings.TrimSpace(h[i+1:])
		}
		s := C.Accent + C.Bold + key + Reset
		if action != "" {
			s += " " + C.Muted + action + Reset
		}
		out = append(out, s)
	}
	return strings.Join(out, "   ")
}
