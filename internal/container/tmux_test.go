package container

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

func TestResolveTmuxMountsUsesExplicitReadOnlyPaths(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, "custom.tmux.conf")
	localFile := filepath.Join(home, "custom.tmux.local")
	ohMyDir := filepath.Join(home, "oh-my-tmux")
	for _, path := range []string{configFile, localFile} {
		if err := os.WriteFile(path, []byte("# test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(ohMyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	additional := filepath.Join(home, "tmux", "plugins")
	if err := os.MkdirAll(additional, 0o750); err != nil {
		t.Fatal(err)
	}

	got := ResolveTmuxMounts(home, config.TmuxSettings{
		Enabled:     true,
		Config:      configFile,
		LocalConfig: localFile,
		OhMyTmuxDir: ohMyDir,
		AdditionalPaths: []string{
			additional,
			"~/does-not-exist",
			additional,
		},
	}, osPathExists)
	want := []string{configFile, localFile, ohMyDir, additional}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveTmuxMounts() = %#v; want %#v", got, want)
	}
}

func TestResolveTmuxMountsFindsConventionalFiles(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, ".tmux.conf")
	localFile := filepath.Join(home, ".tmux.conf.local")
	ohMyDir := filepath.Join(home, ".tmux")
	if err := os.WriteFile(configFile, []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localFile, []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ohMyDir, 0o750); err != nil {
		t.Fatal(err)
	}

	got := ResolveTmuxMounts(home, config.TmuxSettings{Enabled: true}, osPathExists)
	want := []string{configFile, localFile, ohMyDir}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conventional tmux mounts = %#v; want %#v", got, want)
	}
}

func TestResolveTmuxMountsDisabledOrMissingIsEmpty(t *testing.T) {
	home := t.TempDir()
	if got := ResolveTmuxMounts(home, config.TmuxSettings{}, osPathExists); got != nil {
		t.Fatalf("disabled tmux mounts = %#v; want nil", got)
	}
	if got := ResolveTmuxMounts(home, config.TmuxSettings{Enabled: true}, osPathExists); got != nil {
		t.Fatalf("missing tmux mounts = %#v; want nil", got)
	}
}

func TestTmuxCommandKeepsAgentAsFirstWindowCommand(t *testing.T) {
	got := TmuxCommand([]string{"ai-memory", "run", "pi", "--model", "fixture"})
	want := []string{"tmux", "new-session", "-A", "-s", "ai-launcher", "ai-memory", "run", "pi", "--model", "fixture"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TmuxCommand() = %#v; want %#v", got, want)
	}
}

func osPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
