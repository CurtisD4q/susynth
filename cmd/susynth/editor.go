//go:build !windows

package main

import "github.com/susynth/susynth/internal/ui"

// runEditor opens the live editor.
func runEditor(file, sink, theme string) error {
	return ui.Run(ui.Options{File: file, Sink: sink, Theme: theme})
}
