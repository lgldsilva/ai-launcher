//go:build darwin || linux

package container

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// SocketGroupID returns the numeric group owner of a Unix control socket.
// Docker runs the container with the host UID/GID, so this group is added as
// a supplemental group when the socket permission is explicitly enabled.
func SocketGroupID(path string) (int, error) {
	if path == "" {
		return 0, fmt.Errorf("socket path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unsupported stat metadata for socket %q", path)
	}
	// Docker Desktop exposes the host socket through its Linux VM. A bind
	// mount appears there as root:root even when the macOS source is owned by
	// the host user's staff group, so group 0 is the usable supplemental group
	// inside the container.
	if runtime.GOOS == "darwin" {
		return 0, nil
	}
	return int(stat.Gid), nil
}
