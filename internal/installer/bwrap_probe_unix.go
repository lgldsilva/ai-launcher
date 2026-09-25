//go:build !windows

package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// StatBwrap is the host BwrapProbe: it resolves symlinks the way ai-jail's
// canonicalize does and returns the owner and raw mode of the target.
func StatBwrap(path string) (BwrapFile, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return BwrapFile{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return BwrapFile{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return BwrapFile{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return BwrapFile{}, fmt.Errorf("stat %s: no owner information", resolved)
	}
	return BwrapFile{
		Path:  resolved,
		UID:   stat.Uid,
		Mode:  uint32(stat.Mode) & 0o7777,
		IsDir: info.IsDir(),
	}, nil
}
