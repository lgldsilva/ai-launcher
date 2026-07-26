package launcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// HomeSymlinkMounts detects hidden entries of the user's home directory that
// are symlinks resolving OUTSIDE the home tree (a common homelab setup, for
// example ~/.cache -> /storage/cache) and returns one read-write mount per
// resolved target.
//
// ai-jail rebuilds $HOME as a tmpfs and recreates dotfile symlinks inside the
// sandbox without mounting their targets, so every such symlink dangles and
// any tool that writes through it (ai-memory's native-runner cache, harness
// state, package caches) fails. Mounting the resolved targets makes the
// recreated symlinks resolve again. Targets are read-write because these are
// data directories by construction; read-only would defeat their purpose.
//
// Symlinks that resolve back inside the home tree are skipped: ai-jail
// already passes the relevant dotdirs through, so they keep resolving.
func HomeSymlinkMounts(home string) []config.Mount {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		canonicalHome = filepath.Clean(home)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	targets := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") {
			continue
		}
		info, err := os.Lstat(filepath.Join(home, name))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(filepath.Join(home, name))
		if err != nil || !filepath.IsAbs(target) {
			// Broken or relative-only symlink: nothing mountable.
			continue
		}
		if target == canonicalHome || strings.HasPrefix(target, canonicalHome+string(os.PathSeparator)) {
			continue
		}
		targets = append(targets, target)
	}
	return nestedFreeMounts(targets)
}

// nestedFreeMounts sorts the targets and drops duplicates and targets already
// covered by another target (mounting /storage makes /storage/cache
// redundant), returning the surviving paths as read-write mounts.
func nestedFreeMounts(targets []string) []config.Mount {
	sort.Strings(targets)
	mounts := make([]config.Mount, 0, len(targets))
	for _, target := range targets {
		if coveredByMounts(target, mounts) {
			continue
		}
		mounts = append(mounts, config.Mount{Path: target, Mode: "rw"})
	}
	return mounts
}

// MergeAutoMounts appends auto-detected mounts to the configured list,
// skipping any target already covered (equal to or beneath) a configured or
// previously accepted mount path. Configured mounts keep their mode.
func MergeAutoMounts(configured, auto []config.Mount) []config.Mount {
	merged := append([]config.Mount(nil), configured...)
	for _, mount := range auto {
		path := strings.TrimSpace(mount.Path)
		if path == "" || coveredByMounts(path, merged) {
			continue
		}
		merged = append(merged, mount)
	}
	return merged
}

// coveredByMounts reports whether path is equal to or nested under the path
// of any mount in the list.
func coveredByMounts(path string, mounts []config.Mount) bool {
	path = filepath.Clean(path)
	for _, mount := range mounts {
		base := filepath.Clean(strings.TrimSpace(mount.Path))
		if base == "." {
			continue
		}
		if path == base || strings.HasPrefix(path, base+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
