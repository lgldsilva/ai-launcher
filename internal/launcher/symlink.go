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
func HomeSymlinkMounts(home string) ([]config.Mount, []RefusedMount) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, nil
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		canonicalHome = filepath.Clean(home)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, nil
	}
	targets := make([]string, 0, len(entries))
	refused := make([]RefusedMount, 0)
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
		if reason, denied := deniedAutoMount(target); denied {
			refused = append(refused, RefusedMount{Link: filepath.Join(home, name), Target: target, Reason: reason})
			continue
		}
		targets = append(targets, target)
	}
	sort.Slice(refused, func(i, j int) bool { return refused[i].Link < refused[j].Link })
	return nestedFreeMounts(targets), refused
}

// RefusedMount is an auto-mount candidate the denylist rejected. It is reported
// rather than dropped: a hidden symlink silently granting or silently losing
// access are both surprises.
type RefusedMount struct {
	Link   string
	Target string
	Reason string
}

// autoMountDenylist are the trees a single hidden symlink must never expose
// read-write to a sandboxed agent. The whole point of the jail is that these
// stay outside it, and the auto-mount path is not an operator decision — it
// triggers on whatever the home directory happens to contain.
// Entries are compared against the *resolved* target, so the macOS forms are
// listed too: there, /etc and /var are themselves symlinks into /private.
var autoMountDenylist = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc",
	"/sys", "/var", "/tmp", "/opt", "/root", "/System", "/Library", "/Applications",
	"/private", "/private/etc", "/private/var", "/private/tmp", "/Volumes",
}

// deniedAutoMount reports whether target is a filesystem root or one of the
// system trees on the denylist. A path *beneath* a denied tree is allowed: the
// concern is handing over the tree itself.
func deniedAutoMount(target string) (string, bool) {
	clean := filepath.Clean(target)
	if clean == string(filepath.Separator) {
		return "the filesystem root", true
	}
	for _, denied := range autoMountDenylist {
		if clean == denied {
			return "the system directory " + denied, true
		}
	}
	return "", false
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
// of any mount in the list. A leading "~" in a configured mount is expanded
// against the process home first, so "~/.config/gh" dedups against the
// absolute auto-mounts instead of appearing twice in the argv.
func coveredByMounts(path string, mounts []config.Mount) bool {
	return coveredByMountsWhere(path, mounts, nil)
}

// coveredByWritableMounts reports whether path is covered by a mount that
// grants read-write access. Coverage by a read-only mount satisfies the path
// check but not a permission that needs to write (for example gh, whose CLI
// writes to ~/.config/gh).
func coveredByWritableMounts(path string, mounts []config.Mount) bool {
	return coveredByMountsWhere(path, mounts, mountWritable)
}

// mountWritable reports whether the mount grants write access; anything that
// is not an explicit read-only mode is read-write, matching argv emission.
func mountWritable(mount config.Mount) bool {
	mode := strings.ToLower(strings.TrimSpace(mount.Mode))
	return mode != "ro" && mode != "read-only"
}

func coveredByMountsWhere(path string, mounts []config.Mount, accept func(config.Mount) bool) bool {
	path = filepath.Clean(path)
	for _, mount := range mounts {
		if accept != nil && !accept(mount) {
			continue
		}
		base := filepath.Clean(expandTilde(strings.TrimSpace(mount.Path)))
		if base == "." {
			continue
		}
		if path == base || strings.HasPrefix(path, base+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// expandTilde resolves a leading "~" (or "~/") against the process home
// directory. Unexpandable forms (no home, or "~user" syntax) are returned
// unchanged.
func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
