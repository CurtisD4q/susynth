package ui

import (
	"os"
	"time"
	"unicode/utf8"
)

type keyKind int

const (
	kRune keyKind = iota
	kEnter
	kBackspace
	kDelete
	kUp
	kDown
	kLeft
	kRight
	kHome
	kEnd
	kPgUp
	kPgDn
	kTab
	kEsc
	kFn   // r holds the function-key number 1..12
	kCtrl // r holds the lower-case letter
)

type key struct {
	kind keyKind
	r    rune
	alt  bool
}

// readKeys decodes stdin into keys until the process exits.
func readKeys(out chan<- key) {
	bytes := make(chan byte, 4096)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := os.Stdin.Read(buf)
			for _, b := range buf[:n] {
				bytes <- b
			}
			if err != nil {
				close(bytes)
				return
			}
		}
	}()
	next := func(wait time.Duration) (byte, bool) {
		if wait == 0 {
			b, ok := <-bytes
			return b, ok
		}
		select {
		case b, ok := <-bytes:
			return b, ok
		case <-time.After(wait):
			return 0, false
		}
	}
	for {
		b, ok := next(0)
		if !ok {
			return
		}
		switch {
		case b == 0x1b:
			c, ok := next(25 * time.Millisecond)
			if !ok {
				out <- key{kind: kEsc}
				continue
			}
			switch c {
			case '[':
				out <- parseCSI(next)
			case 'O':
				d, _ := next(25 * time.Millisecond)
				if d >= 'P' && d <= 'S' { // SS3 P..S = F1..F4
					out <- key{kind: kFn, r: rune(d-'P') + 1}
				} else {
					out <- csiFinal(d, 0)
				}
			case '\r', '\n':
				out <- key{kind: kEnter, alt: true}
			default:
				out <- key{kind: kRune, r: rune(c), alt: true}
			}
		case b == '\r' || b == '\n':
			out <- key{kind: kEnter}
		case b == 0x7f || b == 0x08:
			out <- key{kind: kBackspace}
		case b == '\t':
			out <- key{kind: kTab}
		case b >= 1 && b <= 26:
			out <- key{kind: kCtrl, r: rune('a' + b - 1)}
		case b < 0x20:
		case b < 0x80:
			out <- key{kind: kRune, r: rune(b)}
		default:
			buf := []byte{b}
			need := 1
			switch {
			case b >= 0xf0:
				need = 3
			case b >= 0xe0:
				need = 2
			}
			for i := 0; i < need; i++ {
				c, ok := next(25 * time.Millisecond)
				if !ok {
					break
				}
				buf = append(buf, c)
			}
			if r, _ := utf8.DecodeRune(buf); r != utf8.RuneError {
				out <- key{kind: kRune, r: r}
			}
		}
	}
}

func parseCSI(next func(time.Duration) (byte, bool)) key {
	num := 0
	for {
		c, ok := next(25 * time.Millisecond)
		if !ok {
			return key{kind: kEsc}
		}
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
			continue
		}
		if c == ';' {
			num = 0 // ignore modifiers: keep the final key
			continue
		}
		return csiFinal(c, num)
	}
}

func csiFinal(c byte, n int) key {
	switch c {
	case 'A':
		return key{kind: kUp}
	case 'B':
		return key{kind: kDown}
	case 'C':
		return key{kind: kRight}
	case 'D':
		return key{kind: kLeft}
	case 'H':
		return key{kind: kHome}
	case 'F':
		return key{kind: kEnd}
	case '~':
		switch n {
		case 1, 7:
			return key{kind: kHome}
		case 4, 8:
			return key{kind: kEnd}
		case 3:
			return key{kind: kDelete}
		case 11, 12, 13, 14:
			return key{kind: kFn, r: rune(n - 10)}
		case 15:
			return key{kind: kFn, r: 5}
		case 17, 18, 19, 20, 21:
			return key{kind: kFn, r: rune(n - 11)}
		case 5:
			return key{kind: kPgUp}
		case 6:
			return key{kind: kPgDn}
		}
	}
	return key{kind: kEsc, r: 0, alt: true} // unknown: ignored by the app
}
