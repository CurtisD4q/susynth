package ui

import (
	"strings"
	"unicode/utf8"

	mint "github.com/CurtisD4q/pmin-kit"
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
	kCtrl  // r holds the lower-case letter
	kPaste // text holds what the terminal pasted (bracketed paste)
)

type key struct {
	kind  keyKind
	r     rune
	alt   bool
	shift bool   // Shift held with an arrow, Home/End or PgUp/PgDn
	ctrl  bool   // Ctrl held with an arrow
	text  string // kPaste only
}

// keyFrom turns a key as Mint reads it into susynth's own. ok is false
// for what susynth has no use for (a click, Shift+Tab).
func keyFrom(k *mint.Key) (key, bool) {
	if k == nil || k.Mouse != nil {
		return key{}, false
	}
	if p := k.Pasted(); p != "" {
		return key{kind: kPaste, text: p}, true
	}
	mods := func(kk key) (key, bool) {
		kk.shift, kk.alt, kk.ctrl = k.Shift, kk.alt || k.Alt, k.Ctrl
		return kk, true
	}
	name := k.Name
	if k.Special {
		if rest, ok := strings.CutPrefix(name, "M-"); ok {
			// Alt+key, or Esc and the key pressed together.
			if rest == "\r" || rest == "\n" {
				return key{kind: kEnter, alt: true}, true
			}
			r, _ := utf8.DecodeRuneInString(rest)
			return key{kind: kRune, r: r, alt: true}, true
		}
		switch name {
		case "esc":
			return key{kind: kEsc}, true
		case "up":
			return mods(key{kind: kUp})
		case "down":
			return mods(key{kind: kDown})
		case "left":
			return mods(key{kind: kLeft})
		case "right":
			return mods(key{kind: kRight})
		case "home":
			return mods(key{kind: kHome})
		case "end":
			return mods(key{kind: kEnd})
		case "pgup":
			return mods(key{kind: kPgUp})
		case "pgdn":
			return mods(key{kind: kPgDn})
		case "delete":
			return mods(key{kind: kDelete})
		}
		return key{}, false
	}
	switch name {
	case "\r", "\n":
		return key{kind: kEnter}, true
	case "\t":
		return key{kind: kTab}, true
	case "\x7f", "\x08":
		return key{kind: kBackspace}, true
	}
	r, _ := utf8.DecodeRuneInString(name)
	switch {
	case r >= 1 && r <= 26:
		return key{kind: kCtrl, r: 'a' + r - 1}, true
	case r < 0x20:
		return key{}, false
	}
	return key{kind: kRune, r: r}, true
}
