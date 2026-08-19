package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
	"github.com/lgldsilva/ai-launcher/internal/container"
	"github.com/lgldsilva/ai-launcher/internal/launcher"
)

func TestFinalizeDockerWorktreePermissionDiscoversExternalWorktree(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "external checkout")
	runGitTestCommand(t, repo, "init", "-q")
	runGitTestCommand(t, repo, "switch", "-c", "fixture")
	runGitTestCommand(t, repo, "config", "user.email", "test@example.com")
	runGitTestCommand(t, repo, "config", "user.name", "ai-launcher test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, repo, "add", "README.md")
	runGitTestCommand(t, repo, "commit", "-qm", "test: initial")
	runGitTestCommand(t, repo, "worktree", "add", "--detach", worktree)

	var errOut bytes.Buffer
	cfg := launcher.LaunchConfig{
		UseDocker:  true,
		ProjectDir: repo,
		// An ai-memory scope name is not a search root: discovery asks Git
		// about the project directory and the cwd, never about a scope.
		Project:     "billing",
		Permissions: map[string]bool{config.PermissionWorktree: true},
		Docker:      container.RunConfig{ProjectDir: repo},
	}
	got := finalizeLaunchConfig(cfg, config.DefaultGlobal(), t.TempDir(), &errOut)
	canonical := func(path string) string {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolved
		}
		return path
	}
	repo = canonical(repo)
	worktree = canonical(worktree)

	contains := func(path string) bool {
		for _, candidate := range got.Docker.WorktreeMounts {
			if candidate == path {
				return true
			}
		}
		return false
	}
	if !contains(repo) || !contains(worktree) {
		t.Fatalf("discovered mounts = %#v; want repository %q and external worktree %q", got.Docker.WorktreeMounts, repo, worktree)
	}
	if !strings.Contains(errOut.String(), worktree) {
		t.Fatalf("discovery output = %q; want external worktree path", errOut.String())
	}
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- test uses a fixed executable and controlled arguments.
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range launcher.GitProbeEnv(os.Environ()) {
		if !strings.HasPrefix(entry, "GIT_CONFIG_GLOBAL=") {
			env = append(env, entry)
		}
	}
	// The fixture must not depend on the maintainer's global signing key or
	// hook template; those are outside the worktree behavior under test. The
	// repository-pointing Git variables are scrubbed by GitProbeEnv: under a
	// Git hook (e.g. the ai-standards pre-push) they would otherwise redirect
	// every fixture command to the real repository.
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
