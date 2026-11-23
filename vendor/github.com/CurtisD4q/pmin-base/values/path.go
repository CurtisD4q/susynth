package values

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

// Path checks a file or folder path a person typed or a program printed,
// and returns it cleaned. It refuses an empty path, one that starts with
// "-" (it would be read as an option), and one with control characters or
// a NUL. It does not look at the disk.
func Path(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "", errors.New("the path is empty")
	}
	if strings.HasPrefix(s, "-") {
		return "", errors.New("a path cannot start with '-'; write ./" + s + " for a file named like that")
	}
	for _, r := range s {
		if r == 0 || unicode.IsControl(r) {
			return "", errors.New("the path has a control character in it")
		}
	}
	return filepath.Clean(s), nil
}

// Arg checks one free-text argument (a search pattern, a name) before it
// goes on a command line: not empty, not starting with "-", no control
// characters. Commands should still put it after "--" where they can.
func Arg(s string) error {
	if s == "" {
		return errors.New("it is empty")
	}
	if strings.HasPrefix(s, "-") {
		return errors.New("it cannot start with '-'")
	}
	for _, r := range s {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return errors.New("it has a control character in it")
		}
	}
	return nil
}

// Within reports whether path is inside root (or is root), after both
// are cleaned and made absolute. It does not follow links; callers that
// care about links check with filepath.EvalSymlinks first.
func Within(root, path string) bool {
	r, err1 := filepath.Abs(root)
	p, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
