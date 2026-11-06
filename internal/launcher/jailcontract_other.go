//go:build !unix

package launcher

import "os"

// requestFileIdentity has nothing to read off Windows, where ai-jail does
// not run. The portable checks (absolute path, symlink, mode) still apply.
func requestFileIdentity(_ os.FileInfo) (uid int, nlink uint64, ok bool) {
	return 0, 1, false
}
