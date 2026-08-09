package launcher

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// GitWorktree describes one entry returned by `git worktree list --porcelain`.
// Bare entries are kept while parsing so the discovery step can deliberately
// exclude them from the mount list.
type GitWorktree struct {
	Path string
	Bare bool
}

// ParseGitWorktreeList parses Git's machine-readable worktree listing. The
// path is everything after the `worktree ` prefix, so spaces in a directory
// name remain part of the path. Unknown record fields are ignored so newer Git
// versions can add metadata without breaking discovery.
func ParseGitWorktreeList(data []byte) []GitWorktree {
	var result []GitWorktree
	var current *GitWorktree
	flush := func() {
		if current == nil || strings.TrimSpace(current.Path) == "" {
			current = nil
			return
		}
		result = append(result, *current)
		current = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &GitWorktree{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
		case strings.TrimSpace(line) == "bare":
			if current != nil {
				current.Bare = true
			}
		}
	}
	flush()
	return result
}

// DiscoverGitWorktreeMounts returns the existing, non-bare worktree roots
// registered by the repository containing root. It intentionally follows Git
// metadata only; it never scans parent directories or the rest of the host.
// A stale worktree registration is ignored because Git can retain one after a
// directory was removed, while a non-Git root is reported to the caller.
func DiscoverGitWorktreeMounts(root string) ([]config.Mount, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root %q: %w", root, err)
	}
	data, err := gitWorktreeList(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("list Git worktrees from %q: %w", absoluteRoot, err)
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, worktree := range ParseGitWorktreeList(data) {
		if worktree.Bare {
			continue
		}
		path := strings.TrimSpace(worktree.Path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(absoluteRoot, path)
		}
		path = filepath.Clean(path)
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	mounts := make([]config.Mount, 0, len(paths))
	for _, path := range paths {
		mounts = append(mounts, config.Mount{Path: path, Mode: config.MountReadWrite})
	}
	return mounts, nil
}

// gitWorktreeList is a seam for deterministic parser/discovery tests. The
// production command is read-only and receives a fixed subcommand; root is
// passed as data to Git's -C option rather than interpolated into a shell.
var gitWorktreeList = func(root string) ([]byte, error) {
	gitCLI, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git not found in PATH: %w", err)
	}
	cmd := exec.Command(gitCLI, "-C", root, "worktree", "list", "--porcelain") // #nosec G204 -- fixed arguments, absolute path from LookPath
	cmd.Env = GitProbeEnv(os.Environ())
	return cmd.Output()
}

// gitEnvOverride names the variables that point Git at a repository other
// than the one -C selects. Hooks and scripts export them for their own
// repository (git sets GIT_DIR for every hook), and Git resolves them before
// -C — which would make discovery list the ambient repository and report a
// non-Git root as valid.
var gitEnvOverride = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_COMMON_DIR":                   true,
	"GIT_NAMESPACE":                    true,
	"GIT_PREFIX":                       true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_QUARANTINE_PATH":              true,
	"GIT_GRAFT_FILE":                   true,
}

// GitProbeEnv returns env without the repository-pointing Git variables. It is
// exported so test helpers spawning `git -C <fixture>` share the same
// protection: under a Git hook the ambient variables would redirect every
// fixture command to the real repository.
func GitProbeEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if gitEnvOverride[key] {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
