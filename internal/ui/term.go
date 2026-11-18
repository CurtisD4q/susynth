package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
)

// Term owns the terminal while the editor runs: raw mode, the alternate
// screen and buffered output. It uses ioctls directly, so there is no
// dependency on the stty program.
type Term struct {
	restore func()
	out     *bufio.Writer
}

func openTerm() (*Term, error) {
	restore, err := makeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, errors.New("susynth needs an interactive terminal (for headless use try: susynth render set.syn -o out.wav)")
	}
	t := &Term{restore: restore, out: bufio.NewWriterSize(os.Stdout, 64<<10)}
	t.out.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	t.out.Flush()
	return t, nil
}

// Close restores the terminal.
func (t *Term) Close() {
	t.out.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l")
	t.out.Flush()
	t.restore()
}

func (t *Term) size() (w, h int) {
	for _, f := range []*os.File{os.Stdout, os.Stdin, os.Stderr} {
		if w, h, err := termSize(int(f.Fd())); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}

func (t *Term) write(s string) { t.out.WriteString(s) }
func (t *Term) flush()         { t.out.Flush() }

func moveTo(row, col int) string { return fmt.Sprintf("\x1b[%d;%dH", row+1, col+1) }
