//go:build !windows

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatBwrapResolvesSymlinksAndReadsTheOwner(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bwrap-real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o751); err != nil { // #nosec G302 -- the probe must report an executable mode; the file is in t.TempDir
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bwrap")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file, err := StatBwrap(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != want || int(file.UID) != os.Getuid() || file.Mode != 0o751 || file.IsDir {
		t.Fatalf("file = %#v; want %s owned by uid %d with mode 0751", file, want, os.Getuid())
	}
	if _, err := StatBwrap(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing path must be an error")
	}
}
