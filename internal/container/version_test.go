package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lgldsilva/ai-launcher/internal/config"
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

// When no state-path override is passed, AgentVersion reads the default
// location under home; a state recorded there must answer for the agent.
func TestAgentVersionDefaultStatePath(t *testing.T) {
	home := t.TempDir()
	writeInstallState(t, home, map[string]installStateEntry{
		"/home/u/.local/bin/claude": {Tag: "3.0.0", Path: "/home/u/.local/bin/claude"},
	})
	if got := AgentVersion(home, "claude", ""); got != "3.0.0" {
		t.Fatalf("AgentVersion() with the default state path = %q; want 3.0.0", got)
	}
}

// Entries whose recorded path belongs to another tool, or whose tag is blank,
// must never answer for this agent.
func TestAgentVersionSkipsUnrelatedAndBlankEntries(t *testing.T) {
	home := t.TempDir()
	path := writeInstallState(t, home, map[string]installStateEntry{
		"/home/u/.local/bin/codex":  {Tag: "9.9.9", Path: "/home/u/.local/bin/codex"},
		"/home/u/.local/bin/claude": {Tag: "  ", Path: "/home/u/.local/bin/claude"},
	})
	if got := AgentVersion(home, "claude", path); got != "" {
		t.Fatalf("AgentVersion() = %q; want empty", got)
	}
}

func TestAgentVersionMalformedState(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "install-state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := AgentVersion(home, "claude", path); got != "" {
		t.Fatalf("AgentVersion() with malformed state = %q; want empty", got)
	}
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

func TestSemverGreater(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"v2.10.0", "v2.9.0", true},
		{"v2.9.0", "v2.10.0", false},
		{"v2.10.0", "v2.10.0", false},
		{"2.1.0", "", true},
		{"", "2.1.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v2.0.0-rc.1", "v2.0.0-beta.2", false}, // 2.0.0-rc > 2.0.0-beta numerically after prefix
		{"v2.1.0", "v2.1", true},                // equal prefix, more parts wins
		{"v2.1", "v2.1.0", false},
		{"v2a1", "v3", false},        // digits stop at the first non-digit: 2 < 3
		{"v2.9.0", "v2.8.0", true},   // the digit 9 decides on the winning side
		{"v1.19.0", "v1.9.0", true},  // 19 > 9 numerically, not lexically
		{"v0.9.9", "v0.10.0", false}, // 9 < 10 on the losing side too
	}
	for _, tt := range tests {
		if got := semverGreater(tt.a, tt.b); got != tt.want {
			t.Errorf("semverGreater(%q, %q) = %v; want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseVersionPart(t *testing.T) {
	for _, tt := range []struct {
		part string
		want int
	}{
		{"2a1", 2}, // consumes leading digits, stops at the first non-digit
		{"rc", 0},
		{"007", 7},
		{"", 0},
		{"9", 9},   // the boundary digit itself must parse
		{"19", 19}, // multi-digit with 9 in the tail
		{"99", 99},
	} {
		if got := parseVersionPart(tt.part); got != tt.want {
			t.Errorf("parseVersionPart(%q) = %d; want %d", tt.part, got, tt.want)
		}
	}
}

// PlanTool carries the catalog release recipe and the pinned host version into
// the selection so the image tag reflects the real tool build.
func TestPlanTool(t *testing.T) {
	release := &config.GitHubRelease{Repository: "lgldsilva/semidx", Binary: "semidx"}
	tool := config.Tool{Command: "semidx", Release: release}
	got := PlanTool(tool, "0.44.9")
	want := ToolInstall{Command: "semidx", Version: "0.44.9", Kind: InstallRelease, Release: release}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanTool() = %#v; want %#v", got, want)
	}
}

func TestPlanInstallMarksNodePrerequisites(t *testing.T) {
	pi := PlanInstall(config.Agent{Command: "pi", SourceURL: "https://pi.dev/install.sh", NeedsNode: true}, "", "")
	if !pi.NeedsNode || pi.Kind != InstallScript {
		t.Fatalf("Pi install = %#v; want script install with Node prerequisite", pi)
	}
	npm := PlanInstall(config.Agent{Command: "gemini", NpmPackage: "@google/gemini-cli"}, "", "")
	if !npm.NeedsNode || npm.Kind != InstallNpm {
		t.Fatalf("npm install = %#v; want npm install with Node prerequisite", npm)
	}
	release := PlanInstall(config.Agent{Command: "claude", Release: &config.GitHubRelease{}}, "1.0.0", "")
	if release.NeedsNode {
		t.Fatalf("release install unexpectedly requires Node: %#v", release)
	}
}

// An agent with no release, script, or npm recipe falls back to a host-binary
// mount with the resolved host path (item 13).
func TestPlanInstallFallsBackToHostBinary(t *testing.T) {
	got := PlanInstall(config.Agent{Command: "kiro-cli"}, "", "  /opt/kiro/bin  ")
	if got.Kind != InstallHostBinary || got.HostPath != "/opt/kiro/bin" {
		t.Fatalf("PlanInstall() = %#v; want a host-binary install with the trimmed host path", got)
	}
}
