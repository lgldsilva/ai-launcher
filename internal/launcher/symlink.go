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
	targets, refused := symlinkTargetsOutsideHome(home, canonicalHome, entries)
	return nestedFreeMounts(targets), refused
}

func symlinkTargetsOutsideHome(home, canonicalHome string, entries []os.DirEntry) ([]string, []RefusedMount) {
	targets := make([]string, 0, len(entries))
	refused := make([]RefusedMount, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") {
			continue
		}
		target, reason, ok := symlinkMountTarget(home, canonicalHome, name)
		switch {
		case reason != "":
			refused = append(refused, RefusedMount{Link: filepath.Join(home, name), Target: target, Reason: reason})
		case ok:
			targets = append(targets, target)
		}
	}
	sort.Slice(refused, func(i, j int) bool { return refused[i].Link < refused[j].Link })
	return targets, refused
}

// symlinkMountTarget classifies one hidden home entry: it returns the
// resolved mount target when the entry is a symlink escaping the home tree,
// or the denylist reason when that target is a denied tree. ok is false for
// everything else (non-symlinks, broken links, links resolving back inside
// the home tree).
func symlinkMountTarget(home, canonicalHome, name string) (target, reason string, ok bool) {
	link := filepath.Join(home, name)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", "", false
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || !filepath.IsAbs(resolved) {
		// Broken or relative-only symlink: nothing mountable.
		return "", "", false
	}
	if resolved == canonicalHome || strings.HasPrefix(resolved, canonicalHome+string(os.PathSeparator)) {
		return "", "", false
	}
	if reason, denied := deniedAutoMount(resolved); denied {
		return resolved, reason, false
	}
	return resolved, "", true
}

// RefusedMount is an auto-mount candidate the denylist rejected. It is reported
// rather than dropped: a hidden symlink silently granting or silently losing
// access are both surprises.
type RefusedMount struct {
	Link   string
	Target string
	Reason string
}

// autoMountHighRiskTrees are refused at the root and for every descendant.
// These hold host authority (credentials, system binaries, device nodes). The
// auto-mount path is not an operator decision, so a single hidden symlink must
// never expose them. Broader trees like /var and /opt stay exact-only so
// benign cache targets (and macOS /private/var/folders temps) still work.
var autoMountHighRiskTrees = []struct {
	reason string
	paths  []string
}{
	{
		reason: "a system directory",
		paths: []string{
			"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev",
			"/proc", "/sys", "/root", "/System", "/Library", "/Applications",
			"/private/etc", "/nix", "/snap",
		},
	},
}

// autoMountExactOnlyTrees are refused only as the root itself. Descendants are
// legitimate data mounts (project volumes, single-user homes, package caches).
var autoMountExactOnlyTrees = []struct {
	reason string
	paths  []string
}{
	{
		reason: "a system directory",
		paths: []string{
			"/var", "/tmp", "/opt", "/private", "/private/var", "/private/tmp",
		},
	},
	{
		reason: "the home directory of every user on this machine",
		paths: []string{
			"/home", "/Users",
			"/System/Volumes/Data", "/System/Volumes/Data/home",
			"/System/Volumes/Data/Users",
		},
	},
	{
		reason: "a mount-point root covering every attached volume",
		paths:  []string{"/Volumes", "/media", "/mnt", "/run", "/srv"},
	},
}

// deniedAutoMount reports whether an inferred home-symlink target must not be
// auto-mounted. Container-control sockets and high-risk system descendants are
// refused; exact-only roots keep benign data paths (e.g. /Volumes/MSD512,
// /var/folders/…, /opt/homebrew) available.
func deniedAutoMount(target string) (string, bool) {
	clean := filepath.Clean(strings.TrimSpace(target))
	if clean == "" || clean == "." {
		return "", false
	}
	if clean == string(filepath.Separator) {
		return "the filesystem root", true
	}
	base := filepath.Base(clean)
	if base == "docker.sock" || base == "podman.sock" {
		return "a container-control socket (" + base + ")", true
	}
	for _, group := range autoMountHighRiskTrees {
		for _, denied := range group.paths {
			d := filepath.Clean(denied)
			if clean == d {
				return group.reason, true
			}
			if strings.HasPrefix(clean, d+string(filepath.Separator)) {
				return "under " + group.reason, true
			}
		}
	}
	for _, group := range autoMountExactOnlyTrees {
		for _, denied := range group.paths {
			if clean == filepath.Clean(denied) {
				return group.reason, true
			}
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
		mounts = append(mounts, config.Mount{Path: target, Mode: config.MountReadWrite})
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
	return mode != config.MountReadOnly && mode != config.MountReadOnlyLabel
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
