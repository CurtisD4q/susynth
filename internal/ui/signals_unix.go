//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"syscall"
)

func watchResize(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGWINCH) }
