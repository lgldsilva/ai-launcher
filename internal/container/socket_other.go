//go:build !darwin && !linux

package container

import "fmt"

// SocketGroupID is unavailable on platforms without Unix stat group metadata.
func SocketGroupID(path string) (int, error) {
	return 0, fmt.Errorf("socket group lookup is unsupported on this platform for %q", path)
}
