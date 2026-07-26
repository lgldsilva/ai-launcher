//go:build !windows

package launcher

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
)

// watchTTYResize mirrors host window size into the PTY on SIGWINCH.
func watchTTYResize(ttyIn, ptmx *os.File) (stop func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			_ = pty.InheritSize(ttyIn, ptmx)
		}
	}()
	return func() { signal.Stop(winch) }
}
