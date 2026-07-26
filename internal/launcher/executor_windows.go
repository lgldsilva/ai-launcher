//go:build windows

package launcher

import "os"

// watchTTYResize is a no-op on Windows (no SIGWINCH).
func watchTTYResize(_, _ *os.File) (stop func()) {
	return func() {}
}
