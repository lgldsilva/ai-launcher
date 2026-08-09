package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestParseGitWorktreeListPreservesPathsAndBareRecords(t *testing.T) {
	data := []byte("worktree /repo/main\nHEAD abc\nbranch refs/heads/main\n\nworktree /Volumes/Other Repo/feature\nHEAD def\ndetached\n\nworktree /repo/bare\nbare\n")
	want := []GitWorktree{
		{Path: "/repo/main"},
		{Path: "/Volumes/Other Repo/feature"},
		{Path: "/repo/bare", Bare: true},
	}
	if got := ParseGitWorktreeList(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseGitWorktreeList() = %#v; want %#v", got, want)
	}
}

func TestDiscoverGitWorktreeMountsUsesRegisteredExistingDirectories(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external checkout")
	if err := os.MkdirAll(external, 0o750); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "removed")
	original := gitWorktreeList
	gitWorktreeList = func(gotRoot string) ([]byte, error) {
		if gotRoot != root {
			t.Fatalf("gitWorktreeList root = %q; want %q", gotRoot, root)
		}
		return []byte("worktree " + external + "\nHEAD abc\n\n" +
			"worktree " + root + "\nHEAD def\n\n" +
			"worktree " + missing + "\nHEAD ghi\n\n" +
			"worktree " + filepath.Join(t.TempDir(), "bare") + "\nbare\n"), nil
	}
	t.Cleanup(func() { gitWorktreeList = original })

	got, err := DiscoverGitWorktreeMounts(root)
	if err != nil {
		t.Fatalf("DiscoverGitWorktreeMounts() error = %v", err)
	}
	want := []config.Mount{
		{Path: external, Mode: config.MountReadWrite},
		{Path: root, Mode: config.MountReadWrite},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverGitWorktreeMounts() = %#v; want %#v", got, want)
	}
}

func TestDiscoverGitWorktreeMountsReportsNonGitRoot(t *testing.T) {
	_, err := DiscoverGitWorktreeMounts(t.TempDir())
	if err == nil {
		t.Fatal("DiscoverGitWorktreeMounts() error = nil; want non-Git error")
	}
}

func TestDiscoverGitWorktreeMountsIgnoresAmbientGitEnvironment(t *testing.T) {
	// Git hooks (e.g. the ai-standards pre-push running this suite) export
	// GIT_DIR for their own repository. Without scrubbing, `git -C <root>`
	// resolves the ambient GIT_DIR instead of root and a non-Git root looks
	// valid. Point GIT_DIR at this repository to reproduce the hook
	// environment.
	gitDir, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output() // #nosec G204 -- fixed read-only git query
	if err != nil {
		t.Fatalf("resolve repository git dir: %v", err)
	}
	t.Setenv("GIT_DIR", strings.TrimSpace(string(gitDir)))
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	if _, err := DiscoverGitWorktreeMounts(t.TempDir()); err == nil {
		t.Fatal("DiscoverGitWorktreeMounts() error = nil with ambient GIT_DIR; want non-Git error")
	}
}

func TestGitProbeEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"GIT_DIR=/other/repo/.git",
		"GIT_WORK_TREE=/other/repo",
		"GIT_QUARANTINE_PATH=/quarantine",
		"HOME=/home/tester",
	}
	got := GitProbeEnv(env)
	want := []string{"PATH=/usr/bin", "HOME=/home/tester"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GitProbeEnv() = %#v; want %#v", got, want)
	}
}
