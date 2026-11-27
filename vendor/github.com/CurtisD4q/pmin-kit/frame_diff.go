package mint

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/CurtisD4q/pmin-base/util"
)

// Drawing only what changed.
//
// A screen that redraws many times a second (Every) would flicker if each
// frame cleared the terminal and drew everything again. Its frames are laid
// out on a grid of cells instead, compared row by row with the last frame
// sent, and only the rows that changed are written, each one whole, so
// nothing needs clearing first.

type diffCell struct {
	r   rune // 0: the second half of a wide character
	sgr string
}

var lastSent struct {
	sync.Mutex
	rows   []string
	h, w   int
	dimmed bool
	seq    uint64 // frameSeq when the rows were sent
}

// frameSeq counts the frames written in full (showFrame), so a diff knows
// when something else, such as a dialog, has drawn over the screen since.
var frameSeq uint64

// frameRows lays a frame (cursor moves, colour sequences and text, as a
// canvas writes them) out on an h × w grid and returns each row as text,
// each cell with its colours set in full.
func frameRows(frame string, h, w int) []string {
	grid := make([][]diffCell, h)
	for i := range grid {
		grid[i] = make([]diffCell, w)
		for j := range grid[i] {
			grid[i][j] = diffCell{r: ' '}
		}
	}
	row, col := 0, 0
	sgr := ""
	for i := 0; i < len(frame); {
		c := frame[i]
		if c == 0x1b && i+1 < len(frame) {
			switch frame[i+1] {
			case '[':
				j := i + 2
				for j < len(frame) && (frame[j] < 0x40 || frame[j] > 0x7e) {
					j++
				}
				if j >= len(frame) {
					return render(grid)
				}
				params, final := frame[i+2:j], frame[j]
				switch final {
				case 'm':
					if params == "" || params == "0" {
						sgr = ""
					} else {
						sgr += frame[i : j+1]
					}
				case 'H':
					r, cc, _ := strings.Cut(params, ";")
					row, col = atoiOr(r, 1)-1, atoiOr(cc, 1)-1
				case 'J':
					for y := range grid {
						for x := range grid[y] {
							grid[y][x] = diffCell{r: ' '}
						}
					}
				}
				i = j + 1
				continue
			case ']':
				// An OSC sequence (a link): left out.
				j := strings.Index(frame[i:], "\x1b\\")
				k := strings.IndexByte(frame[i:], 0x07)
				switch {
				case j >= 0 && (k < 0 || j < k):
					i += j + 2
				case k >= 0:
					i += k + 1
				default:
					return render(grid)
				}
				continue
			}
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(frame[i:])
		i += n
		if r < ' ' {
			continue
		}
		cw := util.RuneWidth(r)
		if cw <= 0 {
			continue
		}
		if row >= 0 && row < h && col >= 0 && col+cw <= w {
			grid[row][col] = diffCell{r: r, sgr: sgr}
			if cw == 2 {
				grid[row][col+1] = diffCell{r: 0, sgr: sgr}
			}
		}
		col += cw
	}
	return render(grid)
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func render(grid [][]diffCell) []string {
	out := make([]string, len(grid))
	for y, cells := range grid {
		var b strings.Builder
		cur := "\x00"
		for _, c := range cells {
			if c.r == 0 {
				continue
			}
			if c.sgr != cur {
				b.WriteString(Reset + c.sgr)
				cur = c.sgr
			}
			b.WriteRune(c.r)
		}
		b.WriteString(Reset)
		out[y] = b.String()
	}
	return out
}

// showDiff writes frame, an h × w screen, sending only the rows that
// differ from the last frame sent this way. tail goes after it (the
// cursor).
func showDiff(frame string, h, w int, tail string) {
	lastFrame.Lock()
	lastFrame.s = frame + esc + "H"
	lastFrame.Unlock()
	dim := !rt.focused.Load() && dimWanted()
	rows := frameRows(frame, h, w)
	lastSent.Lock()
	full := lastSent.h != h || lastSent.w != w || lastSent.dimmed != dim || lastSent.seq != frameSeq || lastSent.rows == nil
	var b strings.Builder
	if full {
		b.WriteString(esc + "2J")
	}
	for y, r := range rows {
		if !full && y < len(lastSent.rows) && lastSent.rows[y] == r {
			continue
		}
		b.WriteString(move(y+1, 1) + r)
	}
	lastSent.rows, lastSent.h, lastSent.w, lastSent.dimmed, lastSent.seq = rows, h, w, dim, frameSeq
	lastSent.Unlock()
	out := b.String()
	if dim {
		out = dimFrame(out)
	}
	if tail == "" {
		tail = esc + "H"
	}
	writeFrame(rt.wrapFrame(out + tail))
}
