//go:build unix

package launcher

import (
	"os"
	"syscall"
)

// requestFileIdentity reads the owner and link count ai-jail checks before
// it accepts --request-file. ok is false when the platform stat has no inode
// identity, in which case those two checks are left to ai-jail.
func requestFileIdentity(info os.FileInfo) (uid int, nlink uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, 1, false
	}
	return int(stat.Uid), uint64(stat.Nlink), true
}
