package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestHomeSymlinkMountsDetectsOutsideTargets(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	cache := filepath.Join(outside, "cache")
	npm := filepath.Join(outside, "npm")
	for _, dir := range []string{cache, npm, filepath.Join(home, ".config")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		".cache":  cache,
		".npm":    npm,
		".broken": filepath.Join(outside, "gone"), // dangling: skipped
		".inside": filepath.Join(home, ".config"), // inside home: skipped
		"visible": cache,                          // not hidden: skipped
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(home, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A real directory is not a symlink and must be ignored.
	if err := os.MkdirAll(filepath.Join(home, ".real"), 0o750); err != nil {
		t.Fatal(err)
	}

	// macOS resolves /var to /private/var via EvalSymlinks; canonicalize the
	// expected paths so the assertion is portable.
	var err error
	cache, err = filepath.EvalSymlinks(cache)
	if err != nil {
		t.Fatal(err)
	}
	npm, err = filepath.EvalSymlinks(npm)
	if err != nil {
		t.Fatal(err)
	}

	mounts := HomeSymlinkMounts(home)
	if len(mounts) != 2 {
		t.Fatalf("mounts = %#v; want exactly .cache and .npm targets", mounts)
	}
	for _, mount := range mounts {
		if mount.Mode != "rw" {
			t.Fatalf("mount %s mode = %q; want rw", mount.Path, mount.Mode)
		}
	}
	if mounts[0].Path != cache || mounts[1].Path != npm {
		t.Fatalf("mounts = %#v; want sorted targets %s, %s", mounts, cache, npm)
	}
}

func TestHomeSymlinkMountsDropsTargetsCoveredByAnother(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	cache := filepath.Join(outside, "cache")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	// .cache -> <outside>/cache and .data -> <outside>: the first is covered
	// by the second once sorted (<outside> < <outside>/cache).
	if err := os.Symlink(cache, filepath.Join(home, ".cache")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".data")); err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	mounts := HomeSymlinkMounts(home)
	if len(mounts) != 1 || mounts[0].Path != outside {
		t.Fatalf("mounts = %#v; want only the covering target %s", mounts, outside)
	}
}

func TestHomeSymlinkMountsHandlesMissingHome(t *testing.T) {
	if mounts := HomeSymlinkMounts(""); mounts != nil {
		t.Fatalf("empty home = %#v; want nil", mounts)
	}
	if mounts := HomeSymlinkMounts(filepath.Join(t.TempDir(), "missing")); mounts != nil {
		t.Fatalf("missing home = %#v; want nil", mounts)
	}
}

func TestMergeAutoMountsSkipsCoveredAndEmpty(t *testing.T) {
	configured := []config.Mount{{Path: "/storage", Mode: "ro"}}
	auto := []config.Mount{
		{Path: "/storage/cache", Mode: "rw"}, // covered by /storage
		{Path: "/storage", Mode: "rw"},       // exact duplicate
		{Path: "", Mode: "rw"},               // empty
		{Path: "/opt/extra", Mode: "rw"},     // new
	}
	merged := MergeAutoMounts(configured, auto)
	if len(merged) != 2 {
		t.Fatalf("merged = %#v; want configured plus /opt/extra", merged)
	}
	if merged[0] != configured[0] {
		t.Fatalf("configured mount mutated: %#v", merged[0])
	}
	if merged[1].Path != "/opt/extra" || merged[1].Mode != "rw" {
		t.Fatalf("merged[1] = %#v; want /opt/extra rw", merged[1])
	}
}
