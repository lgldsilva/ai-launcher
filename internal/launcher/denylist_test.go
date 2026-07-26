package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

// A single hidden symlink pointing at a system root used to grant the sandboxed
// agent read-write access to that whole tree, visible only in the final argv
// echo. A denylist refuses those targets outright.
func TestSystemRootsAreNeverAutoMounted(t *testing.T) {
	home := t.TempDir()
	for name, target := range map[string]string{
		".root":   string(filepath.Separator),
		".etc":    "/etc",
		".usr":    "/usr",
		".var":    "/var",
		".system": "/System",
	} {
		if _, err := os.Stat(target); err != nil {
			continue // Not present on this platform; nothing to link.
		}
		if err := os.Symlink(target, filepath.Join(home, name)); err != nil {
			t.Fatal(err)
		}
	}
	mounts, refused := HomeSymlinkMounts(home)
	if len(mounts) != 0 {
		t.Fatalf("mounts = %#v; system roots must never be auto-mounted", mounts)
	}
	if len(refused) == 0 {
		t.Fatal("refused = none; a refused auto-mount must be reported, not dropped in silence")
	}
	for _, entry := range refused {
		if entry.Link == "" || entry.Target == "" {
			t.Errorf("refused entry = %#v; want the link and its target named", entry)
		}
	}
}

// An ordinary data directory outside home is still auto-mounted: the feature
// exists because ai-jail recreates the symlink without its target.
func TestOrdinaryTargetsAreStillAutoMounted(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "cache")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".cache")); err != nil {
		t.Fatal(err)
	}
	mounts, refused := HomeSymlinkMounts(home)
	if len(mounts) != 1 {
		t.Fatalf("mounts = %#v; want the data directory auto-mounted", mounts)
	}
	if len(refused) != 0 {
		t.Fatalf("refused = %#v; want none", refused)
	}
}
