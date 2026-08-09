package container

import (
	"os"
	"reflect"
	"testing"
)

func TestAgentMounts(t *testing.T) {
	home := "/home/lgldsilva"
	commands := []string{"claude", "codex", "opencode", "unknown-agent", " claude "}

	t.Run("all exist", func(t *testing.T) {
		got := AgentMountsFor(home, commands, func(path string) bool { return true }, "linux")
		// claude: ~/.claude (credential ro) + ~/.claude.json (credential ro) + ~/.claude/projects (rw history)
		// codex: ~/.codex ro
		// opencode: config + data + state under XDG
		want := []AgentMount{
			{Agent: "claude", HostPath: "/home/lgldsilva/.claude"},
			{Agent: "claude", HostPath: "/home/lgldsilva/.claude.json"},
			{Agent: "claude", HostPath: "/home/lgldsilva/.claude/projects"},
			{Agent: "codex", HostPath: "/home/lgldsilva/.codex"},
			{Agent: "opencode", HostPath: "/home/lgldsilva/.config/opencode"},
			{Agent: "opencode", HostPath: "/home/lgldsilva/.local/share/opencode"},
			{Agent: "opencode", HostPath: "/home/lgldsilva/.local/state/opencode"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("AgentMounts() = %#v\nwant %#v", got, want)
		}
	})

	t.Run("skips missing paths", func(t *testing.T) {
		existing := map[string]bool{"/home/lgldsilva/.claude": true}
		got := AgentMountsFor(home, commands, func(path string) bool { return existing[path] }, "linux")
		if len(got) != 1 || got[0].HostPath != "/home/lgldsilva/.claude" {
			t.Fatalf("AgentMounts() with missing paths = %#v; want only .claude", got)
		}
	})

	t.Run("empty home skips everything", func(t *testing.T) {
		got := AgentMountsFor("", commands, nil, "linux")
		if len(got) != 0 {
			t.Fatalf("AgentMounts() with empty home = %#v; want none", got)
		}
	})

	t.Run("nil existingFiles mounts everything", func(t *testing.T) {
		got := AgentMountsFor(home, []string{"codex"}, nil, "linux")
		if len(got) != 1 || got[0].HostPath != "/home/lgldsilva/.codex" {
			t.Fatalf("AgentMounts() with nil probe = %#v", got)
		}
	})

	t.Run("unknown agent contributes nothing", func(t *testing.T) {
		got := AgentMountsFor(home, []string{"totally-unknown"}, nil, "linux")
		if len(got) != 0 {
			t.Fatalf("unknown agent should contribute no mounts, got %#v", got)
		}
	})
}

func TestAgentMountsIncludesOnlySemidxOwnedDirectories(t *testing.T) {
	home := "/home/alice"
	got := AgentMounts(home, []string{"semidx"}, func(path string) bool {
		return path == "/home/alice/.config/semidx" || path == "/home/alice/.cache/semidx"
	})
	if len(got) != 2 {
		t.Fatalf("AgentMounts(semidx) = %#v; want two tool-owned directories", got)
	}
	for _, mount := range got {
		if mount.Agent != "semidx" {
			t.Fatalf("semidx mount has agent %q", mount.Agent)
		}
		if mount.HostPath == home || mount.HostPath == "/home/alice/.config" {
			t.Fatalf("semidx mount exposes too much host state: %#v", mount)
		}
	}
}

// Platform-aware: the resolver must evaluate platform-qualified entries while
// retaining the same user-home paths for tools that are cross-platform.
func TestAgentMountsPlatformVariants(t *testing.T) {
	home := "/home/u"
	exists := func(string) bool { return true }

	t.Run("linux qwen uses home root", func(t *testing.T) {
		got := AgentMountsFor(home, []string{"qwen"}, exists, "linux")
		found := map[string]bool{}
		for _, m := range got {
			found[m.HostPath] = true
		}
		if !found[home+"/.qwen"] {
			t.Fatalf("linux qwen missing ~/.qwen: %#v", got)
		}
	})

	t.Run("darwin qwen uses home root", func(t *testing.T) {
		got := AgentMountsFor(home, []string{"qwen"}, exists, "darwin")
		found := map[string]bool{}
		for _, m := range got {
			found[m.HostPath] = true
		}
		if !found[home+"/.qwen"] {
			t.Fatalf("darwin qwen missing ~/.qwen: %#v", got)
		}
	})

	t.Run("windows claude uses ~/.claude (Docker Desktop path)", func(t *testing.T) {
		got := AgentMountsFor(home, []string{"claude"}, exists, "windows")
		found := map[string]bool{}
		for _, m := range got {
			found[m.HostPath] = true
		}
		if !found[home+"/.claude"] {
			t.Fatalf("windows claude missing ~/.claude: %#v", got)
		}
	})
}

func TestAgentMountsAllCatalogAgents(t *testing.T) {
	// Every catalog agent with a config mapping must resolve at least one
	// mount (no silent gaps).
	home := "/home/u"
	exists := func(string) bool { return true }
	for command := range agentConfigDirs {
		got := AgentMountsFor(home, []string{command}, exists, "linux")
		if len(got) == 0 {
			t.Errorf("agent %q has config dirs but none resolved for linux", command)
		}
	}
}

func TestPlatformApplies(t *testing.T) {
	if !platformApplies(nil, "linux") {
		t.Fatal("empty platforms must apply everywhere")
	}
	if !platformApplies([]string{"linux", "darwin"}, "darwin") {
		t.Fatal("matching platform must apply")
	}
	if platformApplies([]string{"darwin"}, "linux") {
		t.Fatal("non-matching platform must not apply")
	}
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		raw, home string
		want      string
	}{
		{"~/.claude", "/home/u", "/home/u/.claude"},
		{"~", "/home/u", "/home/u"},
		{"/abs/path", "/home/u", "/abs/path"},
		{"", "/home/u", ""},
		{"~/.claude", "", ""},
		{"~/a/../b", "/home/u", "/home/u/b"},
		{".nvm", "/home/u", "/home/u/.nvm"},
	}
	for _, tt := range tests {
		if got := ExpandHome(tt.raw, tt.home); got != tt.want {
			t.Errorf("ExpandHome(%q, %q) = %q; want %q", tt.raw, tt.home, got, tt.want)
		}
	}
}

func TestExistsOnHost(t *testing.T) {
	dir := t.TempDir()
	if !ExistsOnHost(dir) {
		t.Fatalf("ExistsOnHost(%q) = false; want true", dir)
	}
	if ExistsOnHost(dir + "/definitely-missing") {
		t.Fatal("ExistsOnHost on a missing path = true; want false")
	}
}

func TestStackCacheMounts(t *testing.T) {
	home := t.TempDir()
	dirs := map[string]bool{
		home + "/.nvm":    true,
		home + "/.cargo":  true,
		home + "/go":      true,
		home + "/.sdkman": true,
	}
	exists := func(path string) bool { return dirs[path] }

	t.Run("node stack", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"node"}, exists)
		if len(got) != 1 || got[0] != home+"/.nvm" {
			t.Fatalf("StackCacheMounts(node) = %v; want [%s/.nvm]", got, home)
		}
	})
	t.Run("java stack", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"java"}, exists)
		if len(got) != 1 || got[0] != home+"/.sdkman" {
			t.Fatalf("StackCacheMounts(java) = %v; want [%s/.sdkman]", got, home)
		}
	})
	t.Run("multiple dedup", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"go", "node", "go"}, exists)
		if len(got) != 2 {
			t.Fatalf("StackCacheMounts(go,node,go) = %v; want 2 deduped", got)
		}
	})
	t.Run("missing dirs skipped", func(t *testing.T) {
		got := StackCacheMounts(home, []string{"rust"}, func(string) bool { return false })
		if len(got) != 0 {
			t.Fatalf("StackCacheMounts with no existing dirs = %v; want none", got)
		}
	})
	t.Run("empty home", func(t *testing.T) {
		if got := StackCacheMounts("", []string{"node"}, nil); len(got) != 0 {
			t.Fatalf("StackCacheMounts('') = %v; want none", got)
		}
	})
	t.Run("unknown stack contributes nothing", func(t *testing.T) {
		if got := StackCacheMounts(home, []string{"cobol"}, exists); len(got) != 0 {
			t.Fatalf("StackCacheMounts(cobol) = %v; want none", got)
		}
	})
	// A nil probe falls back to the real filesystem check (ExistsOnHost).
	t.Run("nil probe uses the filesystem", func(t *testing.T) {
		if err := os.MkdirAll(home+"/.nvm", 0o700); err != nil {
			t.Fatal(err)
		}
		got := StackCacheMounts(home, []string{"node"}, nil)
		if len(got) != 1 || got[0] != home+"/.nvm" {
			t.Fatalf("StackCacheMounts(node, nil probe) = %v; want [%s/.nvm]", got, home)
		}
	})
}

var _ = os.Stat // keep os import for future filesystem tests

// AgentMounts (no explicit platform) resolves platform-qualified entries
// against the host GOOS: ~/.claude/projects only applies on linux/darwin.
func TestAgentMountsResolvesHostPlatform(t *testing.T) {
	original := hostGOOS
	t.Cleanup(func() { hostGOOS = original })
	exists := func(string) bool { return true }
	home := "/home/u"

	hostGOOS = func() string { return "darwin" }
	got := AgentMounts(home, []string{"claude"}, exists)
	found := false
	for _, mount := range got {
		if mount.HostPath == home+"/.claude/projects" {
			found = true
		}
	}
	if !found {
		t.Fatalf("darwin claude mounts missing ~/.claude/projects: %#v", got)
	}

	hostGOOS = func() string { return "windows" }
	got = AgentMounts(home, []string{"claude"}, exists)
	for _, mount := range got {
		if mount.HostPath == home+"/.claude/projects" {
			t.Fatalf("windows claude must not mount the linux/darwin-only projects dir: %#v", got)
		}
	}
}
