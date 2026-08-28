// Package fsatomic writes a file through a temporary file and a rename, so a
// reader never observes a half-written file and a failed write never destroys
// the previous contents.
//
// It exists because three packages had grown their own copy of this — the
// config store, the self-updater and the installer — and the copies had
// drifted where it matters. Only the installer's fsynced before renaming, so
// the other two could leave an empty file behind a crash or power loss; only
// the config store labelled which stage failed, so the other two reported
// "permission denied" with no way to tell a failed chmod from a failed rename;
// and only the config store had a seam for forcing the create to fail, so the
// other two could not test their own error paths. This package is the union:
// every caller now gets the fsync, the staged error and the seam.
package fsatomic

import (
	"os"
	"path/filepath"
)

// CreateTemp is an indirection over os.CreateTemp so tests can force the
// temporary-file creation step to fail deterministically. A chmod-based
// read-only directory does not fail under root or on Windows.
var CreateTemp = os.CreateTemp

// WriteError names the stage of the write that failed. Callers wrap it with
// their own domain label; the stage is what turns "permission denied" into
// something a reader can act on.
type WriteError struct {
	Stage string
	Err   error
}

func (e *WriteError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// WriteFile writes data to path atomically with the given mode. pattern is the
// os.CreateTemp pattern for the intermediate file, which lands in path's own
// directory — a rename is only atomic within one filesystem, so the temporary
// cannot live in TMPDIR.
//
// The caller creates the destination directory. This owns the write lifecycle.
func WriteFile(path string, data []byte, mode os.FileMode, pattern string) error {
	name, err := WriteTemp(path, data, mode, pattern)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(name) }()
	if err := os.Rename(name, path); err != nil {
		return &WriteError{Stage: "replace", Err: err}
	}
	return nil
}

// WriteTemp writes data to a closed temporary file beside path and returns its
// name, leaving the rename to the caller. Windows cannot rename over a running
// executable, so the self-updater needs the two halves separately.
//
// On any failure the temporary is removed and the name is not returned; on
// success the caller owns it, including removing it if the rename never
// happens.
func WriteTemp(path string, data []byte, mode os.FileMode, pattern string) (string, error) {
	temporary, err := CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return "", &WriteError{Stage: "create temporary", Err: err}
	}
	name := temporary.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(name)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", &WriteError{Stage: "write", Err: err}
	}
	// Chmod before the rename, not after: between a rename and a chmod the
	// file is readable at whatever mode CreateTemp chose, and one of these
	// callers is writing a config that may carry an auth token.
	if err := temporary.Chmod(mode); err != nil { // #nosec G302 -- mode is the caller's; one caller installs an executable
		_ = temporary.Close()
		return "", &WriteError{Stage: "protect", Err: err}
	}
	// Without the fsync the rename can be durable while the contents are not,
	// which turns a crash into an empty file where the old one used to be —
	// the exact outcome writing through a temporary is meant to rule out.
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", &WriteError{Stage: "flush", Err: err}
	}
	if err := temporary.Close(); err != nil {
		return "", &WriteError{Stage: "close", Err: err}
	}
	done = true
	return name, nil
}
