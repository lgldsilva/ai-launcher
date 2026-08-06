package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeInstallState(t *testing.T, home string, entries map[string]installStateEntry) string {
	t.Helper()
	path := filepath.Join(home, ".config", "ai-launch", "install-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	state := installState{Installs: entries}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentVersion(t *testing.T) {
	t.Run("reads verified tag", func(t *testing.T) {
		home := t.TempDir()
		path := writeInstallState(t, home, map[string]installStateEntry{
			"/home/u/.local/bin/claude": {Repository: "acme/claude", Tag: "2.1.0", Path: "/home/u/.local/bin/claude"},
		})
		if got := AgentVersion(home, "claude", path); got != "2.1.0" {
			t.Fatalf("AgentVersion() = %q; want 2.1.0", got)
		}
	})

	t.Run("prefers newest", func(t *testing.T) {
		home := t.TempDir()
		path := writeInstallState(t, home, map[string]installStateEntry{
			"/a/claude": {Tag: "1.0.0", Path: "/a/claude"},
			"/b/claude": {Tag: "2.0.0", Path: "/b/claude"},
		})
		if got := AgentVersion(home, "claude", path); got != "2.0.0" {
			t.Fatalf("AgentVersion() = %q; want 2.0.0", got)
		}
	})

	t.Run("ignores latest", func(t *testing.T) {
		home := t.TempDir()
		path := writeInstallState(t, home, map[string]installStateEntry{
			"/a/claude": {Tag: "latest", Path: "/a/claude"},
		})
		if got := AgentVersion(home, "claude", path); got != "" {
			t.Fatalf("AgentVersion() = %q; want empty for latest", got)
		}
	})

	t.Run("missing state", func(t *testing.T) {
		if got := AgentVersion(t.TempDir(), "claude", ""); got != "" {
			t.Fatalf("AgentVersion() = %q; want empty", got)
		}
	})

	t.Run("missing command", func(t *testing.T) {
		if got := AgentVersion("/home/u", "", ""); got != "" {
			t.Fatalf("AgentVersion() with empty command = %q; want empty", got)
		}
	})

	t.Run("empty home", func(t *testing.T) {
		if got := AgentVersion("", "claude", ""); got != "" {
			t.Fatalf("AgentVersion() with empty home = %q; want empty", got)
		}
	})
}

func TestSortedAssetKeys(t *testing.T) {
	assets := map[string]string{
		"darwin-arm64": "a",
		"linux-amd64":  "b",
		"windows":      "c",
	}
	want := []string{"darwin-arm64", "linux-amd64", "windows"}
	if got := SortedAssetKeys(assets); !reflect.DeepEqual(got, want) {
		t.Fatalf("SortedAssetKeys() = %v; want %v", got, want)
	}
	if got := SortedAssetKeys(nil); len(got) != 0 {
		t.Fatalf("SortedAssetKeys(nil) = %v; want empty", got)
	}
}
