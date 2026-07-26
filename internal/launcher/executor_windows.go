//go:build windows

package launcher

import "os"

// watchTTYResize is a no-op on Windows (no SIGWINCH).
func watchTTYResize(_, _ *os.File) (stop func()) {
	return func() {
		// Intentionally empty: no resize watcher is started on Windows,
		// so there is nothing to stop.
	}
}
