//go:build windows

package ui

import "os"

func watchResize(ch chan<- os.Signal) {}
