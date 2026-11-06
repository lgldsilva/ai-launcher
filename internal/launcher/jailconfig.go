package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// jailConfigFile is the name ai-jail reads both as the project config (in the
// working directory) and as the global config (in $HOME).
const jailConfigFile = ".ai-jail"

// jailConfigSymlinkIssues predicts ai-jail's own refusal to read a symlinked
// config, so it surfaces as a pre-flight error instead of an ai-jail exit
// inside the PTY that the TUI can only answer with "press r to retry".
//
// ai-jail (every release since 1.18, so the whole supported range) applies two
// rules, both fail-closed:
//
//   - the project .ai-jail is untrusted and is never followed through a
//     symlink ("Refusing to read .ai-jail: path is a symlink");
//   - the global ~/.ai-jail may be a symlink (GNU stow and other dotfile
//     managers), but only when the resolved target lies outside the project
//     directory, which the sandbox can write.
//
// Launching from $HOME is where both collide: ~/.ai-jail is then the global
// config and the project config at once, so a symlinked one is refused as the
// project file and, under --clean, again as a global config inside the
// project. That case gets its own code and message because the fix is not to
// edit the file but to launch from somewhere else.
//
// A nil getwd or home skips the check, as the other cwd-dependent checks do.
func jailConfigSymlinkIssues(cfg LaunchConfig, getwd, home func() (string, error)) []Issue {
	if !cfg.UseJail || getwd == nil || home == nil {
		return nil
	}
	cwd, err := getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return nil
	}
	homeDir, err := home()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return nil
	}
	project := filepath.Join(cwd, jailConfigFile)
	global := filepath.Join(homeDir, jailConfigFile)
	if isSymlink(project) {
		if samePath(project, global) {
			return []Issue{{
				Code: "jail-home-as-project",
				Message: fmt.Sprintf(
					"launching from %s makes %s both the global and the project ai-jail config, and ai-jail never reads a project config through a symlink; "+
						"launch from a project directory instead, or launch with --no-jail",
					cwd, global),
			}}
		}
		return []Issue{{
			Code: "jail-project-config-symlink",
			Message: fmt.Sprintf(
				"%s is a symlink, and ai-jail never reads a project config through one; "+
					"replace it with a regular file, remove it, or launch with --no-jail",
				project),
		}}
	}
	if !isSymlink(global) {
		return nil
	}
	target, err := filepath.EvalSymlinks(global)
	if err != nil {
		// A dangling global symlink is ai-jail's to report; nothing here would
		// name the problem better.
		return nil
	}
	if !pathInside(cwd, target) {
		return nil
	}
	return []Issue{{
		Code: "jail-global-config-inside-project",
		Message: fmt.Sprintf(
			"%s is a symlink to %s, which is inside the project directory %s; ai-jail refuses a global config the sandbox can write — "+
				"launch from a directory that does not contain it, or launch with --no-jail",
			global, target, cwd),
	}}
}

// jailStoreIssues predicts ai-jail 3's refusal to keep its trusted store
// inside the project or to reach it through a symlink. 2.x has no such
// store, so the check stays quiet below 3.0.0 and when the version probe
// did not run. A nil getwd or home skips it, as the legacy symlink check does.
// xdgConfigHome is the raw $XDG_CONFIG_HOME value; empty means unset.
func jailStoreIssues(cfg LaunchConfig, getwd, home func() (string, error), xdgConfigHome string) []Issue {
	if !cfg.UseJail || !jailUsesExternalStore(cfg) || getwd == nil || home == nil {
		return nil
	}
	cwd, err := getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return nil
	}
	homeDir, err := home()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return nil
	}
	if base := strings.TrimSpace(xdgConfigHome); base != "" && !filepath.IsAbs(base) {
		return []Issue{{
			Code: "jail-xdg-store-invalid",
			Message: fmt.Sprintf(
				"XDG_CONFIG_HOME %q is not an absolute path; ai-jail 3 refuses it and will not open the trusted store",
				base),
		}}
	}
	store, ok := JailStoreDir(homeDir, xdgConfigHome)
	if !ok {
		return nil
	}
	if pathInside(cwd, store) || samePath(filepath.Clean(cwd), store) {
		return []Issue{{
			Code: "jail-xdg-store-inside-project",
			Message: fmt.Sprintf(
				"ai-jail keeps trusted policy in %s, which is inside the project directory %s; "+
					"launch from a directory that does not contain the store, or launch with --no-jail",
				store, cwd),
		}}
	}
	if component, linked := symlinkComponent(store); linked {
		return []Issue{{
			Code: "jail-xdg-store-symlink",
			Message: fmt.Sprintf(
				"ai-jail's trusted store %s is reached through the symlink %s; ai-jail 3 refuses a store path with a symlink component — "+
					"point XDG_CONFIG_HOME at a path with no symlinks, or launch with --no-jail",
				store, component),
		}}
	}
	return nil
}

// symlinkComponent reports the first existing symlink on path. A missing
// component stops the walk: ai-jail does the same, and a store that does
// not exist yet is not a symlink.
func symlinkComponent(path string) (string, bool) {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" || cleaned == "." {
		return "", false
	}
	current := ""
	if filepath.IsAbs(cleaned) {
		current = string(filepath.Separator)
	}
	rest := cleaned
	if filepath.IsAbs(cleaned) {
		rest = strings.TrimPrefix(cleaned, string(filepath.Separator))
	}
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, true
		}
	}
	return "", false
}

// requestFileIssues refuses jail_flags.request_file when this ai-jail cannot
// accept it, and when the path would fail ai-jail's own checks. The file is
// host input: a checkout path, a symlink, or a group-writable file must not
// reach the wrapper.
func requestFileIssues(cfg LaunchConfig, getwd func() (string, error)) []Issue {
	path := strings.TrimSpace(cfg.JailFlags.RequestFile)
	if !cfg.UseJail || path == "" {
		return nil
	}
	if !launchSupportsRequestFile(cfg) {
		seen := strings.TrimSpace(cfg.JailVersion)
		if seen == "" {
			seen = "unknown"
		}
		if cfg.JailContract.Probed {
			return []Issue{{
				Code: "request-file-unsupported",
				Message: fmt.Sprintf(
					"jail_flags.request_file is set, but this ai-jail does not advertise --request-file (installed: %s); remove request_file or upgrade to ai-jail >= %s",
					seen, config.MinJailContractVersion),
			}}
		}
		return []Issue{{
			Code: "request-file-requires-ai-jail-3-0-1",
			Message: fmt.Sprintf(
				"jail_flags.request_file needs ai-jail >= %s (installed: %s); upgrade ai-jail, or remove request_file",
				config.MinJailContractVersion, seen),
		}}
	}
	project := ""
	if getwd != nil {
		if cwd, err := getwd(); err == nil {
			project = cwd
		}
	}
	if problem := inspectRequestFile(path, project); problem != "" {
		return []Issue{{
			Code:    "request-file-rejected",
			Message: fmt.Sprintf("jail_flags.request_file %s %s", path, problem),
		}}
	}
	return nil
}

// inspectRequestFile reports why path would not pass ai-jail's --request-file
// checks, or "" when it would. project is the working directory; empty skips
// the outside-the-checkout check.
func inspectRequestFile(path, project string) string {
	if !filepath.IsAbs(path) {
		return "must be an absolute path outside the project checkout"
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "does not exist, or cannot be inspected"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "cannot be a symlink"
	}
	if !info.Mode().IsRegular() {
		return "must be a regular file"
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "must not be group- or world-writable"
	}
	if problem := requestFileOwnershipProblem(info); problem != "" {
		return problem
	}
	if strings.TrimSpace(project) == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	if pathInside(project, resolved) {
		return "must live outside the project checkout"
	}
	return ""
}

// requestFileOwnershipProblem applies the owner and hard-link rules when the
// platform can see them. current is split out so the mismatch branch is
// testable without a second uid.
func requestFileOwnershipProblem(info os.FileInfo) string {
	uid, nlink, ok := requestFileIdentity(info)
	return requestFileOwnership(uid, nlink, os.Getuid(), ok)
}

func requestFileOwnership(uid int, nlink uint64, current int, known bool) string {
	if !known {
		return ""
	}
	if nlink > 1 {
		return "must not be a hard link"
	}
	if uid != current {
		return "must be owned by the current user"
	}
	return ""
}

// isSymlink reports whether path exists and is itself a symbolic link.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// samePath compares two paths after resolving the symlinks in their parent
// directories, so /tmp and /private/tmp on macOS count as one. The final
// component is not resolved: both paths name the link itself.
func samePath(a, b string) bool {
	return resolveDir(a) == resolveDir(b)
}

// resolveDir resolves the directory part of path and re-joins the final
// component, falling back to the cleaned path when the directory is unreadable.
func resolveDir(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, filepath.Base(path))
}

// pathInside reports whether target, an already resolved path, is dir itself or
// lies beneath it once dir's own symlinks are resolved.
func pathInside(dir, target string) bool {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = filepath.Clean(dir)
	}
	rel, err := filepath.Rel(resolved, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
