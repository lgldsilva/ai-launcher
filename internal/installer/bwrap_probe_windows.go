//go:build windows

package installer

import "errors"

// StatBwrap has nothing to find on Windows: ai-jail does not run there, and
// ResolveBubblewrap returns before any probe for a goos other than linux.
func StatBwrap(string) (BwrapFile, error) {
	return BwrapFile{}, errors.New("bwrap is Linux-only")
}
