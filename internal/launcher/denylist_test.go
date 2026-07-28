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

// deniedAutoMount is checked directly because the interesting targets are not
// all present on every platform, and a filesystem-driven test would silently
// skip exactly the entries that matter (`/home` on macOS, `/snap` on a Mac,
// `/System/Volumes/Data` on Linux).
//
// The auto-mount path is not an operator decision — it fires on whatever the
// home directory happens to contain — so every tree that holds other people's
// data has to be refused. `/home` and `/Users` were both missing: one hidden
// symlink to either handed the sandboxed agent read-write access to every user
// account on the machine.
func TestDeniedAutoMountCoversTreesHoldingOtherUsersData(t *testing.T) {
	denied := []string{
		"/home", "/Users",
		// macOS resolves /home through a firmlink, and EvalSymlinks runs before
		// the denylist check, so the resolved spelling has to be listed too.
		"/System/Volumes/Data", "/System/Volumes/Data/home", "/System/Volumes/Data/Users",
		"/run", "/srv", "/media", "/mnt", "/nix", "/snap",
	}
	for _, target := range denied {
		t.Run(target, func(t *testing.T) {
			reason, refused := deniedAutoMount(target)
			if !refused {
				t.Fatalf("deniedAutoMount(%q) = not refused; this tree holds data the sandbox must not get", target)
			}
			if reason == "" {
				t.Error("reason = \"\"; a refusal has to be reportable to the operator")
			}
		})
	}
}

// The denylist covers the trees themselves, not what lives beneath them: the
// user's own project volume and home are legitimate auto-mount targets, and
// refusing those would break the feature this exists to protect.
func TestDeniedAutoMountAllowsPathsBeneathDeniedTrees(t *testing.T) {
	for _, target := range []string{
		"/Volumes/MSD512", "/home/lgldsilva", "/Users/luizg/Projects",
		"/storage/cache", "/mnt/data", "/opt/homebrew",
	} {
		t.Run(target, func(t *testing.T) {
			if _, refused := deniedAutoMount(target); refused {
				t.Fatalf("deniedAutoMount(%q) = refused; only the tree itself is denied", target)
			}
		})
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
