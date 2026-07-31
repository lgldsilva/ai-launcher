//go:build !windows

package launcher

import (
	"os"
	"os/exec"
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

// configureProcessGroup is a no-op on Unix when using creack/pty: pty.Start
// already sets Setsid, so the child is session and process-group leader.
// Setting Setpgid alongside Setsid fails with EPERM on macOS.
func configureProcessGroup(_ *exec.Cmd) {}

// killProcessGroup sends SIGKILL to the child's process group (negative PID).
// creack/pty's Setsid makes the child the group leader, so descendants that
// stay in the group die with it. ESRCH when already reaped is ignored.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
