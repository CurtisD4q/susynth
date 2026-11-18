//go:build !linux && !darwin && !freebsd

package ui

import "errors"

// The interactive editor needs a Unix terminal. `susynth render`, `check`
// and `midi` work everywhere.
func makeRaw(fd int) (func(), error) {
	return nil, errors.New("the live editor is not supported on this OS yet")
}
func termSize(fd int) (int, int, error) { return 0, 0, errors.New("unsupported") }
