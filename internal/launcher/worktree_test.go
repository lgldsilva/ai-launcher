package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
