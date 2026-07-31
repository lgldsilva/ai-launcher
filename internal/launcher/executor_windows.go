//go:build windows

package launcher

import (
	"os"
	"os/exec"
)

// watchTTYResize is a no-op on Windows (no SIGWINCH).
func watchTTYResize(_, _ *os.File) (stop func()) {
	return func() {
		// Intentionally empty: no resize watcher is started on Windows,
		// so there is nothing to stop.
	}
}

func configureProcessGroup(_ *exec.Cmd) {
	// Windows has no Unix process-group model for this path.
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
