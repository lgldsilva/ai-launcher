package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
