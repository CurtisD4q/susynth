package main

import "errors"

// runEditor: the live editor needs a Unix terminal. `susynth render`,
// `check` and `midi` work everywhere.
func runEditor(file, sink, theme string) error {
	return errors.New("the live editor is not supported on Windows yet; try susynth render, check or midi")
}
