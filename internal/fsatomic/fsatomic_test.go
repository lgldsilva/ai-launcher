package fsatomic

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCreateTemp forces the create step to fail. A chmod-based read-only
// directory does not fail under root or on Windows, so the seam is the only
// portable way to reach this branch.
func stubCreateTemp(t *testing.T, err error) {
	t.Helper()
	original := CreateTemp
	CreateTemp = func(string, string) (*os.File, error) { return nil, err }
	t.Cleanup(func() { CreateTemp = original })
}

func TestWriteFileReplacesTheContentsAndAppliesTheMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("new"), 0o600, ".test-*"); err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("contents = %q; want the new data", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The mode is applied to the temporary before the rename, so the file is
	// never briefly readable at whatever mode CreateTemp chose — one caller
	// writes a config that may carry an auth token.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v; want 0600", perm)
	}
}

// The whole point of writing through a temporary: a failure leaves the
// previous file exactly as it was, rather than truncated or gone.
func TestWriteFileLeavesThePreviousFileIntactOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubCreateTemp(t, errors.New("no space left on device"))

	if err := WriteFile(path, []byte("new"), 0o600, ".test-*"); err == nil {
		t.Fatal("WriteFile() = nil; the create step failed")
	}

	data, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Errorf("contents = %q; a failed write must not replace the previous file", data)
	}
}

// Callers wrap the error with their own domain label, so the stage is what
// turns "permission denied" into something the reader can act on.
func TestWriteErrorNamesTheStageAndUnwraps(t *testing.T) {
	cause := errors.New("permission denied")
	stubCreateTemp(t, cause)

	err := WriteFile(filepath.Join(t.TempDir(), "config.yaml"), nil, 0o600, ".test-*")
	var failure *WriteError
	if !errors.As(err, &failure) {
		t.Fatalf("WriteFile() = %v; want a *WriteError", err)
	}
	if failure.Stage != "create temporary" {
		t.Errorf("Stage = %q; want the failing stage", failure.Stage)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(err, cause) = false; the cause must stay reachable")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("Error() = %q; want the cause in the message", err.Error())
	}
}

// A failed write must not litter: the temporary lives beside the destination,
// which is a directory the operator reads.
func TestWriteTempRemovesTheTemporaryOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")

	// A closed file fails at the write stage, which is past the point where
	// the temporary already exists on disk.
	original := CreateTemp
	CreateTemp = func(d, pattern string) (*os.File, error) {
		file, err := original(d, pattern)
		if err != nil {
			return nil, err
		}
		_ = file.Close()
		return file, nil
	}
	t.Cleanup(func() { CreateTemp = original })

	if _, err := WriteTemp(path, []byte("data"), 0o600, ".test-*"); err == nil {
		t.Fatal("WriteTemp() = nil; writing to a closed file must fail")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("directory = %v; a failed write must leave no temporary behind", entries)
	}
}

// Windows cannot rename over a running executable, so the self-updater takes
// the two halves separately and owns the temporary until it renames it.
func TestWriteTempLeavesTheRenameToTheCaller(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ai-launcher")

	name, err := WriteTemp(path, []byte("binary"), 0o700, ".test-*")
	if err != nil {
		t.Fatalf("WriteTemp() = %v", err)
	}
	defer func() { _ = os.Remove(name) }()

	if filepath.Dir(name) != dir {
		t.Errorf("temporary = %q; a rename is only atomic within one filesystem", name)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(path) err = %v; WriteTemp must not rename", err)
	}
	data, err := os.ReadFile(name) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary" {
		t.Errorf("temporary contents = %q; want the written data", data)
	}
}

// The rename is the only step that can fail after a complete temporary exists,
// and it is the one the caller most needs named: "replace" says the new
// contents were written and only the swap failed.
func TestWriteFileReportsAFailedRename(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory cannot be replaced by a rename.
	path := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o750); err != nil {
		t.Fatal(err)
	}

	err := WriteFile(path, []byte("data"), 0o600, ".test-*")
	var failure *WriteError
	if !errors.As(err, &failure) {
		t.Fatalf("WriteFile() = %v; want a *WriteError", err)
	}
	if failure.Stage != "replace" {
		t.Errorf("Stage = %q; want \"replace\"", failure.Stage)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory = %v; the temporary must be cleaned up after a failed rename", entries)
	}
}
