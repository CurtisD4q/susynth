// Package text holds the small wording and string helpers that programs in
// the family kept writing for themselves: plurals, clipping to a width,
// sizes in bytes, first lines, and quoting for a shell.
package text

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// S is "s" unless n is 1: "3 file" + text.S(3).
func S(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Pick is one when n is 1 and many otherwise: text.Pick(n, "is", "are").
func Pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Count says n of word: "1 file", "3 files".
func Count(n int, word string) string {
	return strconv.Itoa(n) + " " + word + S(n)
}

// Clip shortens s to at most n characters (runes), ending with "…" when
// anything was cut. n below 1 gives "".
func Clip(s string, n int) string {
	if n < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Bytes says a size the way a person reads it: "512 B", "1.5 KB",
// "120 MB". Units are powers of 1024; 100 and over drop the decimal.
func Bytes(n int64) string {
	const k = 1024
	if n < 0 {
		return "-" + Bytes(-n)
	}
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	f, u := float64(n)/k, "KB"
	for _, next := range []string{"MB", "GB", "TB", "PB"} {
		if f < k {
			break
		}
		f /= k
		u = next
	}
	if f >= 100 {
		return fmt.Sprintf("%.0f %s", f, u)
	}
	return fmt.Sprintf("%.1f %s", f, u)
}

// FirstLine is the first line of s, without surrounding space.
func FirstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimRight(s, "\r")
}

// FirstLines is at most the first n lines of s, without surrounding space.
func FirstLines(s string, n int) string {
	if n < 1 {
		return ""
	}
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, "\n")
}

// Lines splits s into lines. A final line ending does not make an empty
// last line, and "\r\n" endings lose their "\r".
func Lines(s string) []string {
	if s == "" {
		return nil
	}
	ls := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range ls {
		ls[i] = strings.TrimSuffix(l, "\r")
	}
	return ls
}

// ShellQuote quotes s for a POSIX shell, so it is read as one word with
// nothing in it expanded.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SortedKeys are m's keys in order.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
